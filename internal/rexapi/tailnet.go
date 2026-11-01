package rexapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
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

// newNodeFunc makes the device that stores its state in dir.
type newNodeFunc func(dir, hostname, authKey string, tags []string) tailnetNode

// tsnetNode is a tailnetNode of tsnet: a copy of Tailscale in the
// process, joining the tailnet as a device of its own.
type tsnetNode struct {
	srv *tsnet.Server
	lc  *local.Client
}

func newTsnetNode(dir, hostname, authKey string, tags []string) tailnetNode {
	return &tsnetNode{srv: &tsnet.Server{
		Dir:           dir,
		Hostname:      hostname,
		AuthKey:       authKey,
		AdvertiseTags: tags,
		UserLogf:      logf,
		Logf:          func(string, ...any) {},
	}}
}

func (n *tsnetNode) Start() error {
	if err := n.srv.Start(); err != nil {
		return err
	}
	lc, err := n.srv.LocalClient()
	if err != nil {
		return err
	}
	n.lc = lc
	return nil
}

func (n *tsnetNode) Status(ctx context.Context) (*ipnstate.Status, error) {
	return n.lc.StatusWithoutPeers(ctx)
}

func (n *tsnetNode) Watch(ctx context.Context, changed func()) {
	for ctx.Err() == nil {
		w, err := n.lc.WatchIPNBus(ctx, ipn.NotifyInitialState)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
				continue
			}
		}
		for {
			if _, err := w.Next(); err != nil {
				break
			}
			changed()
		}
		w.Close()
	}
}

func (n *tsnetNode) ListenTLS() (net.Listener, error) { return n.srv.ListenTLS("tcp", ":443") }

func (n *tsnetNode) WhoIs(ctx context.Context, addr string) (*apitype.WhoIsResponse, error) {
	return n.lc.WhoIs(ctx, addr)
}

func (n *tsnetNode) StartLogin(ctx context.Context) error { return n.lc.StartLoginInteractive(ctx) }

func (n *tsnetNode) Logout(ctx context.Context) error { return n.lc.Logout(ctx) }

func (n *tsnetNode) SetHostname(ctx context.Context, name string) error {
	_, err := n.lc.EditPrefs(ctx, &ipn.MaskedPrefs{Prefs: ipn.Prefs{Hostname: name}, HostnameSet: true})
	return err
}

func (n *tsnetNode) Close() error { return n.srv.Close() }

// tailnetConf is what the gateway keeps of built-in Tailscale between
// runs: whether it is on, the device's name, and who may connect. The
// auth key is never kept.
type tailnetConf struct {
	Enabled             bool   `json:"enabled"`
	Hostname            string `json:"hostname,omitempty"`
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

	mu      sync.Mutex
	conf    tailnetConf
	node    tailnetNode
	ln      *trackedListener
	https   bool
	err     string
	cancel  context.CancelFunc
	last    string
	closing bool
}

func newTailnet(g *Gateway, dir string, newNode newNodeFunc) *tailnet {
	t := &tailnet{g: g, dir: dir, newNode: newNode}
	if b, err := os.ReadFile(t.confPath()); err == nil {
		json.Unmarshal(b, &t.conf)
	}
	return t
}

func (t *tailnet) confPath() string { return filepath.Join(t.dir, "tailscale.json") }

// saveLocked writes the configuration; t.mu is held.
func (t *tailnet) saveLocked() {
	if err := os.MkdirAll(t.dir, 0o700); err != nil {
		logf("tailscale: %v", err)
		return
	}
	b, _ := json.MarshalIndent(t.conf, "", "  ")
	if err := os.WriteFile(t.confPath(), append(b, '\n'), 0o600); err != nil {
		logf("tailscale: %v", err)
	}
}

// enableOptions are server.tailscale.enable's arguments.
type enableOptions struct {
	hostname     string
	allowMembers bool
	authKey      string
	tags         []string
}

var (
	hostnameRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	tagRE      = regexp.MustCompile(`^tag:[A-Za-z]([A-Za-z0-9-]*[A-Za-z0-9])?$`)
)

// validHostname tells a Tailscale device name: 1 to 63 ASCII letters,
// digits and hyphens, not starting or ending with a hyphen.
func validHostname(s string) bool { return hostnameRE.MatchString(s) }

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
		t.conf.AllowTailnetMembers = o.allowMembers
		t.saveLocked()
		t.mu.Unlock()
		t.changed()
		return t.status(), nil
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
	if !validHostname(name) {
		t.mu.Unlock()
		return tailscaleStatus{}, errBadRequest("%q is not a Tailscale device name", name)
	}
	if err := os.MkdirAll(filepath.Join(t.dir, "tsnet"), 0o700); err != nil {
		t.mu.Unlock()
		return tailscaleStatus{}, errInternal("%v", err)
	}
	node := t.newNode(filepath.Join(t.dir, "tsnet"), name, o.authKey, o.tags)
	if err := node.Start(); err != nil {
		t.mu.Unlock()
		node.Close()
		return tailscaleStatus{}, errInternal("starting Tailscale: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.node, t.cancel, t.err, t.https = node, cancel, "", false
	t.conf = tailnetConf{Enabled: true, Hostname: name, AllowTailnetMembers: o.allowMembers}
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
	if !validHostname(name) {
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
	node, https, errText, allow := t.node, t.https, t.err, t.conf.AllowTailnetMembers
	t.mu.Unlock()
	if node == nil {
		return tailscaleStatus{State: "disabled", Error: errText, AllowTailnetMembers: allow}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	st, err := node.Status(ctx)
	if err != nil {
		return tailscaleStatus{State: "starting", AllowTailnetMembers: allow, HTTPS: &httpsStatus{State: "pending"}}
	}
	return statusOf(st, allow, https)
}

// statusOf maps a Tailscale device's status to Rex's.
func statusOf(st *ipnstate.Status, allow, https bool) tailscaleStatus {
	out := tailscaleStatus{AllowTailnetMembers: allow, HTTPS: &httpsStatus{State: "pending"}}
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

// changed tells the gateway's connections of a new status, when it is.
func (t *tailnet) changed() {
	st := t.status()
	b, _ := json.Marshal(st)
	t.mu.Lock()
	same := string(b) == t.last
	t.last = string(b)
	t.mu.Unlock()
	if !same {
		t.g.emitServerEvent("tailscale_status_changed", st)
	}
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
