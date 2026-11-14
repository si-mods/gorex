package rexapi

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"gorex/internal/rex"
	"gorex/internal/tailnode"
)

var _ rex.TailnetDevice = (*tailnode.Node)(nil)

// desktop is the gateway the GoRex server runs in this process
// (Supervise), nil while none runs. The desktop's tailnet broker uses its
// built-in Tailscale node.
var desktop struct {
	sync.Mutex
	g *Gateway
}

func setDesktop(g *Gateway) {
	desktop.Lock()
	defer desktop.Unlock()
	desktop.g = g
}

// clearDesktop forgets g, unless another gateway took its place.
func clearDesktop(g *Gateway) {
	desktop.Lock()
	defer desktop.Unlock()
	if desktop.g == g {
		desktop.g = nil
	}
}

func desktopGateway() *Gateway {
	desktop.Lock()
	defer desktop.Unlock()
	return desktop.g
}

// DesktopNode is rex.TailnetNode: the built-in Tailscale node of the
// gateway in this process, or nil while it is off or no gateway runs
// here. A gorex rexapi of its own is another process: nil.
func DesktopNode() rex.TailnetDevice {
	g := desktopGateway()
	if g == nil || g.tailnet == nil {
		return nil
	}
	if n, ok := g.tailnet.running().(rex.TailnetDevice); ok && n != nil {
		return n
	}
	return nil
}

// EnableBuiltinTailscale is rex.EnableBuiltinTailscale: it turns built-in
// Tailscale on, as gorex rexapi tailscale on does, keeping the saved
// allow_tailnet_members. With the gateway in this process it starts the
// node at once; without one it turns it on in tailscale.json, and the
// server's gateway starts it (Supervise, Wanted).
func EnableBuiltinTailscale() error {
	return enableBuiltin(TailnetDir(), desktopGateway())
}

func enableBuiltin(dir string, g *Gateway) error {
	if g != nil && g.tailnet != nil {
		if g.tailnet.running() != nil {
			return nil
		}
		g.tailnet.mu.Lock()
		allow := g.tailnet.conf.AllowTailnetMembers
		g.tailnet.mu.Unlock()
		if _, aerr := g.tailnet.enable(enableOptions{allowMembers: allow}); aerr != nil {
			return errors.New(aerr.Message)
		}
		return nil
	}
	conf, err := LoadTailscale(dir)
	if err != nil {
		return err
	}
	if conf.Enabled {
		return nil
	}
	return setTailnetEnabled(dir, nil)
}

// TailscaleAuthURL is the sign-in link of the built-in node, as the
// gateway's local socket gives it: empty while the node needs none or has
// none yet. The link binds the device to whoever opens it first, so it is
// for this machine's user only.
func TailscaleAuthURL() (string, error) {
	return tailscaleCall("server.status")
}

// StartTailscaleLogin asks the built-in node for a new sign-in, and
// returns its link when the node has one already; otherwise
// TailscaleAuthURL gives it shortly.
func StartTailscaleLogin() (string, error) {
	return tailscaleCall("server.tailscale.login")
}

// tailscaleCall calls method on the gateway's local socket and returns
// the auth_url of the tailscale status it answers.
func tailscaleCall(method string) (string, error) {
	out, _, err := callGatewayWelcome(SocketPath(), 5*time.Second, gatewayCall{method: method})
	if err != nil {
		return "", err
	}
	var res struct {
		Tailscale *tailscaleStatus `json:"tailscale"`
	}
	if err := json.Unmarshal(out[0], &res); err != nil {
		return "", err
	}
	if res.Tailscale == nil {
		return "", errors.New("this gateway can't run built-in Tailscale")
	}
	return res.Tailscale.AuthURL, nil
}
