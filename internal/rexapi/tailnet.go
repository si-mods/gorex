package rexapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorex/internal/tailnode"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
)

// tailnetNode is the Tailscale device the gateway runs: tsnet's, or a
// test's fake.
type tailnetNode interface {
	// Start starts the device, which signs in when it can.
	Start() error
	Status(ctx context.Context) (*ipnstate.Status, error)
	// Watch calls changed each time the device's state may have
	// changed, until ctx ends.
	Watch(ctx context.Context, changed func())
	// ListenTLS listens for HTTPS on port 443 of the tailnet. It blocks
	// until the device runs.
	ListenTLS() (net.Listener, error)
	WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error)
	StartLogin(ctx context.Context) error
	Logout(ctx context.Context) error
	SetHostname(ctx context.Context, name string) error
	Close() error
}

// newNodeFunc makes the device that stores its state in dir, signing in
// to the control server at controlURL when it is not empty.
type newNodeFunc func(dir, hostname, authKey, controlURL string, tags []string) tailnetNode

// newTsnetNode makes the tailnetNode of tsnet.
func newTsnetNode(dir, hostname, authKey, controlURL string, tags []string) tailnetNode {
	return tailnode.New(tailnode.Options{Dir: dir, Hostname: hostname, AuthKey: authKey, ControlURL: controlURL, Tags: tags, Logf: logf})
}

var _ tailnetNode = (*tailnode.Node)(nil)

// TailscaleConfig is what the gateway keeps of built-in Tailscale between
// runs: whether it is on, the device's name, its control server, and who
// may connect. The auth key is never kept. The app reads and writes it
// through LoadTailscale and SaveTailscale.
type TailscaleConfig struct {
	Enabled             bool   `json:"enabled"`
	Hostname            string `json:"hostname,omitempty"`
	ControlURL          string `json:"control_url,omitempty"`
	AllowTailnetMembers bool   `json:"allow_tailnet_members"`
}

// tailnet is the gateway's built-in Tailscale, as Rex's server has: a
// device of its own on the user's tailnet that serves the API over
// HTTPS, letting in the user who signed it in, or every member the
// tailnet's access rules allow.
type tailnet struct {
	g       *Gateway
	dir     string
	newNode newNodeFunc
	// serve serves the API on the device's listener (Gateway.serveOn),
	// given as the gateway is made: the methods that reach this would
	// otherwise refer back to the table of methods.
	serve func(net.Listener, string) error

	mu     sync.Mutex
	conf   TailscaleConfig
	node   tailnetNode
	ln     *trackedListener
	https  bool
	err    string
	cancel context.CancelFunc
	// last and lastRemote are the status last sent to the local socket
	// and to the other transports.
	last, lastRemote string
	closing          bool
}

func newTailnet(g *Gateway, dir string, newNode newNodeFunc) *tailnet {
	t := &tailnet{g: g, dir: dir, newNode: newNode}
	if b, err := os.ReadFile(t.confPath()); err == nil {
		json.Unmarshal(b, &t.conf)
	}
	return t
}

func (t *tailnet) confPath() string { return tailscaleConfPath(t.dir) }

// tailscaleConfPath is where a gateway keeps its built-in Tailscale
// settings in dir.
func tailscaleConfPath(dir string) string { return filepath.Join(dir, "tailscale.json") }

// LoadTailscale reads the built-in Tailscale settings in dir, the zero
// value when there are none.
func LoadTailscale(dir string) (TailscaleConfig, error) {
	var c TailscaleConfig
	b, err := os.ReadFile(tailscaleConfPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", tailscaleConfPath(dir), err)
	}
	return c, nil
}

// SaveTailscale checks c and writes its control server in dir. The rest of
// the file is the gateway's, which changes it while it runs, so it is kept
// as it is. A gateway reads the control server when built-in Tailscale is
// next turned on, so a change takes effect then.
func SaveTailscale(dir string, c TailscaleConfig) error {
	if c.ControlURL != "" && !tailnode.ValidControlURL(c.ControlURL) {
		return errControlURL(c.ControlURL)
	}
	saved, err := LoadTailscale(dir)
	if err != nil {
		return err
	}
	saved.ControlURL = c.ControlURL
	return writeJSON(tailscaleConfPath(dir), saved)
}

// keepSavedControlURL takes the control server the app saved, which the
// gateway does not own: the app changes that file while the gateway runs.
// t.mu is held.
func (t *tailnet) keepSavedControlURL() {
	if saved, err := LoadTailscale(t.dir); err == nil {
		t.conf.ControlURL = saved.ControlURL
	}
}

// saveLocked writes the configuration; t.mu is held.
func (t *tailnet) saveLocked() {
	if err := os.MkdirAll(t.dir, 0o700); err != nil {
		logf("tailscale: %v", err)
		return
	}
	if err := writeJSON(t.confPath(), t.conf); err != nil {
		logf("tailscale: %v", err)
	}
}

// enableOptions are server.tailscale.enable's arguments.
type enableOptions struct {
	hostname   string
	controlURL string
	// controlSet tells whether the call named control_url, so that an
	// explicit empty one returns the device to Tailscale's own control
	// server instead of reusing the saved one.
	controlSet   bool
	allowMembers bool
	authKey      string
	tags         []string
}

var tagRE = regexp.MustCompile(`^tag:[A-Za-z]([A-Za-z0-9-]*[A-Za-z0-9])?$`)

// errControlURL is the error for a control server ValidControlURL refuses.
func errControlURL(s string) error {
	return fmt.Errorf("%q is not a Tailscale control server URL: use https, or http only to localhost or a private address", s)
}

// defaultHostname is the device name when none is given: this machine's
// name with "-gorex", so that it stands apart from a Rex server's.
func defaultHostname() string {
	name, _ := os.Hostname()
	name, _, _ = strings.Cut(name, ".")
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	base := strings.Trim(b.String(), "-")
	if len(base) > 56 {
		base = strings.Trim(base[:56], "-")
	}
	if base == "" {
		return "gorex"
	}
	return base + "-gorex"
}

// enable turns built-in Tailscale on, or, when it is on, only applies
// allowMembers.
func (t *tailnet) enable(o enableOptions) (tailscaleStatus, *apiError) {
	t.mu.Lock()
	if t.closing {
		t.mu.Unlock()
		return tailscaleStatus{}, errConflict("the server is shutting down")
	}
	if t.node != nil {
		t.keepSavedControlURL()
		t.conf.AllowTailnetMembers = o.allowMembers
		t.saveLocked()
		t.mu.Unlock()
		t.changed()
		return t.status(), nil
	}
	// The app changes the control server in the settings' files while
	// the gateway runs: take what it saved before the device starts, so
	// that turning Tailscale on uses it.
	if saved, err := LoadTailscale(t.dir); err == nil {
		t.conf.ControlURL = saved.ControlURL
		if saved.Hostname != "" {
			t.conf.Hostname = saved.Hostname
		}
	}
	for _, tag := range o.tags {
		if !tagRE.MatchString(tag) {
			t.mu.Unlock()
			return tailscaleStatus{}, errBadRequest("%q is not a Tailscale tag", tag)
		}
	}
	if len(o.tags) > 0 && !o.allowMembers {
		t.mu.Unlock()
		return tailscaleStatus{}, errBadRequest("a tagged device has no owner, so it needs allow_tailnet_members")
	}
	name := o.hostname
	if name == "" {
		name = t.conf.Hostname
	}
	if name == "" {
		name = defaultHostname()
	}
	if !tailnode.ValidHostname(name) {
		t.mu.Unlock()
		return tailscaleStatus{}, errBadRequest("%q is not a Tailscale device name", name)
	}
	control := t.conf.ControlURL
	if o.controlSet {
		control = o.controlURL
	}
	if control != "" && !tailnode.ValidControlURL(control) {
		t.mu.Unlock()
		return tailscaleStatus{}, errBadRequest("%v", errControlURL(control))
	}
	if err := os.MkdirAll(filepath.Join(t.dir, "tsnet"), 0o700); err != nil {
		t.mu.Unlock()
		return tailscaleStatus{}, errInternal("%v", err)
	}
	node := t.newNode(filepath.Join(t.dir, "tsnet"), name, o.authKey, control, o.tags)
	if err := node.Start(); err != nil {
		t.mu.Unlock()
		node.Close()
		return tailscaleStatus{}, errInternal("starting Tailscale: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.node, t.cancel, t.err, t.https = node, cancel, "", false
	t.conf = TailscaleConfig{Enabled: true, Hostname: name, ControlURL: control, AllowTailnetMembers: o.allowMembers}
	t.saveLocked()
	t.mu.Unlock()
	go node.Watch(ctx, t.changed)
	go t.listen(ctx, node)
	t.changed()
	return t.status(), nil
}

// listen serves the API on the device's HTTPS port once it runs. If the
// listener fails, built-in Tailscale turns off with the error.
func (t *tailnet) listen(ctx context.Context, node tailnetNode) {
	ln, err := node.ListenTLS()
	if ctx.Err() != nil {
		if ln != nil {
			ln.Close()
		}
		return
	}
	if err != nil {
		logf("tailscale: HTTPS: %v", err)
		t.stop(err.Error(), true)
		return
	}
	tl := newTrackedListener(ln)
	t.mu.Lock()
	if t.node != node {
		t.mu.Unlock()
		tl.Close()
		return
	}
	t.ln, t.https = tl, true
	t.mu.Unlock()
	t.changed()
	if st := t.status(); st.HTTPS != nil && st.HTTPS.URL != "" {
		logf("tailscale: serving on %s", st.HTTPS.URL)
	}
	t.serve(tl, transportTailscale)
}

// stop turns built-in Tailscale off: it closes the device, its listener
// and every connection that came in over the tailnet. The device keeps
// its sign-in. keep tells whether it stays enabled for the next run.
func (t *tailnet) stop(reason string, disable bool) {
	t.mu.Lock()
	node, ln, cancel := t.node, t.ln, t.cancel
	t.node, t.ln, t.cancel, t.https = nil, nil, nil, false
	t.err = reason
	if disable {
		t.keepSavedControlURL()
		t.conf.Enabled = false
		t.saveLocked()
	}
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if ln != nil {
		ln.Close()
	}
	if node != nil {
		node.Close()
	}
	t.changed()
}

// resume turns the device back on when it was on as the gateway last
// stopped, with its sign-in and settings. When it cannot, the device
// stays off and enabled, and the status tells why.
func (t *tailnet) resume() {
	if !t.conf.Enabled {
		return
	}
	if _, aerr := t.enable(enableOptions{allowMembers: t.conf.AllowTailnetMembers}); aerr != nil {
		logf("tailscale: %s", aerr.Message)
		t.stop(aerr.Message, false)
	}
}

func (t *tailnet) disable() tailscaleStatus {
	t.mu.Lock()
	on := t.node != nil
	t.mu.Unlock()
	if on {
		t.stop("", true)
	}
	return t.status()
}

// close stops the device as the gateway stops, leaving it enabled for
// the next run.
func (t *tailnet) close() {
	t.mu.Lock()
	t.closing = true
	t.mu.Unlock()
	t.stop("", false)
}

func (t *tailnet) running() tailnetNode {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.node
}

func (t *tailnet) login() (tailscaleStatus, *apiError) {
	node := t.running()
	if node == nil {
		return tailscaleStatus{}, errConflict("built-in Tailscale is off: turn it on with server.tailscale.enable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := node.StartLogin(ctx); err != nil {
		return tailscaleStatus{}, errInternal("asking Tailscale to sign in: %v", err)
	}
	t.changed()
	return t.status(), nil
}

func (t *tailnet) logout() (tailscaleStatus, *apiError) {
	node := t.running()
	if node == nil {
		return t.status(), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := node.Logout(ctx); err != nil {
		return tailscaleStatus{}, errInternal("signing out of Tailscale: %v", err)
	}
	t.stop("", true)
	return t.status(), nil
}

func (t *tailnet) rename(name string) (tailscaleStatus, *apiError) {
	if !tailnode.ValidHostname(name) {
		return tailscaleStatus{}, errBadRequest("%q is not a Tailscale device name", name)
	}
	node := t.running()
	if node == nil {
		return tailscaleStatus{}, errConflict("built-in Tailscale is off: pass hostname to server.tailscale.enable instead")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := node.SetHostname(ctx, name); err != nil {
		return tailscaleStatus{}, errInternal("renaming the Tailscale device: %v", err)
	}
	t.mu.Lock()
	t.keepSavedControlURL()
	t.conf.Hostname = name
	t.saveLocked()
	t.mu.Unlock()
	t.changed()
	return t.status(), nil
}

// tailscaleStatus is the tailscale object of server.status and of the
// server.tailscale.* results and events.
type tailscaleStatus struct {
	Error               string         `json:"error,omitempty"`
	State               string         `json:"state"`
	ControlURL          string         `json:"control_url,omitempty"`
	DNSName             string         `json:"dns_name,omitempty"`
	AuthURL             string         `json:"auth_url,omitempty"`
	Account             *accountStatus `json:"account,omitempty"`
	TailnetName         string         `json:"tailnet_name,omitempty"`
	AllowTailnetMembers bool           `json:"allow_tailnet_members"`
	Tags                []string       `json:"tags,omitempty"`
	HTTPS               *httpsStatus   `json:"https,omitempty"`
}

type accountStatus struct {
	LoginName string `json:"login_name"`
}

type httpsStatus struct {
	State string `json:"state"`
	URL   string `json:"url,omitempty"`
}

// tailscaleResult wraps a status as the server.tailscale.* methods
// return it.
type tailscaleResult struct {
	Tailscale tailscaleStatus `json:"tailscale"`
}

func (t *tailnet) status() tailscaleStatus {
	t.mu.Lock()
	node, https, errText, allow, control := t.node, t.https, t.err, t.conf.AllowTailnetMembers, t.conf.ControlURL
	t.mu.Unlock()
	if node == nil {
		return tailscaleStatus{State: "disabled", Error: errText, AllowTailnetMembers: allow}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	st, err := node.Status(ctx)
	if err != nil {
		return tailscaleStatus{State: "starting", ControlURL: control, AllowTailnetMembers: allow, HTTPS: &httpsStatus{State: "pending"}}
	}
	return statusOf(st, control, allow, https)
}

// over is a status as a connection over transport sees it. The sign-in
// link binds the device to whoever opens it first, so, like the
// server.tailscale methods, it is for the local socket only.
func (st tailscaleStatus) over(transport string) tailscaleStatus {
	if transport != transportUnix {
		st.AuthURL = ""
	}
	return st
}

// statusOf maps a Tailscale device's status to Rex's, with the control
// server the device uses when it is not Tailscale's own.
func statusOf(st *ipnstate.Status, control string, allow, https bool) tailscaleStatus {
	out := tailscaleStatus{ControlURL: control, AllowTailnetMembers: allow, HTTPS: &httpsStatus{State: "pending"}}
	switch st.BackendState {
	case ipn.NeedsLogin.String():
		out.State = "needs_login"
		out.AuthURL = st.AuthURL
	case ipn.NeedsMachineAuth.String():
		out.State = "needs_machine_approval"
	case ipn.Running.String():
		out.State = "running"
	default:
		out.State = "starting"
	}
	if st.CurrentTailnet != nil {
		out.TailnetName = st.CurrentTailnet.Name
	}
	if self := st.Self; self != nil {
		out.DNSName = strings.TrimSuffix(self.DNSName, ".")
		if self.Tags != nil && self.Tags.Len() > 0 {
			out.Tags = self.Tags.AsSlice()
		} else if u, ok := st.User[self.UserID]; ok && u.LoginName != "" {
			out.Account = &accountStatus{LoginName: u.LoginName}
		}
	}
	if https && out.State == "running" {
		out.HTTPS.State = "available"
		if out.DNSName != "" {
			out.HTTPS.URL = "https://" + out.DNSName
		}
	}
	return out
}

// changed tells the gateway's connections of a new status, when it is
// new as their transport sees it: a new sign-in link alone tells only the
// local socket.
func (t *tailnet) changed() {
	local := t.status()
	remote := local.over(transportTCP)
	lb, _ := json.Marshal(local)
	rb, _ := json.Marshal(remote)
	t.mu.Lock()
	sameLocal, sameRemote := string(lb) == t.last, string(rb) == t.lastRemote
	t.last, t.lastRemote = string(lb), string(rb)
	t.mu.Unlock()
	t.g.emitServerEventOver("tailscale_status_changed", func(transport string) any {
		switch {
		case transport == transportUnix && !sameLocal:
			return local
		case transport != transportUnix && !sameRemote:
			return remote
		}
		return nil
	})
}

var errNotAllowed = errors.New("this server lets in only the Tailscale user who signed it in")

// identify tells who a caller over the tailnet is, and refuses one the
// device does not let in: the user who signed the device in, or, with
// allow_tailnet_members, everyone the tailnet's access rules let reach it.
func (t *tailnet) identify(ctx context.Context, remoteAddr string) (map[string]any, error) {
	t.mu.Lock()
	node, allow := t.node, t.conf.AllowTailnetMembers
	t.mu.Unlock()
	if node == nil {
		return nil, errors.New("built-in Tailscale is off")
	}
	who, err := node.WhoIs(ctx, remoteAddr)
	if err != nil || who == nil || who.Node == nil || who.UserProfile == nil {
		return nil, errors.New("this caller is not on the tailnet")
	}
	if !allow {
		st, err := node.Status(ctx)
		if err != nil || st.Self == nil || (st.Self.Tags != nil && st.Self.Tags.Len() > 0) {
			return nil, errNotAllowed
		}
		if who.Node.IsTagged() || who.UserProfile.ID != st.Self.UserID {
			return nil, errNotAllowed
		}
	}
	p := map[string]any{
		"kind":       "tailscale",
		"transport":  "tailscale",
		"display":    who.UserProfile.LoginName,
		"user_id":    strconv.FormatInt(int64(who.UserProfile.ID), 10),
		"login_name": who.UserProfile.LoginName,
		"node":       strings.TrimSuffix(who.Node.ComputedName, "."),
		"path":       []string{"tailscale"},
	}
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		p["remote_addr"] = host
	}
	if who.Node.IsTagged() {
		p["tags"] = who.Node.Tags
		p["display"] = p["node"]
	}
	return p, nil
}
