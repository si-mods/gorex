package rex

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
	"tailscale.com/client/local"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
)

type macTailscale struct{}

func systemTailscaleClient() systemTailscale { return macTailscale{} }

const tailscaleAppCLI = "/Applications/Tailscale.app/Contents/MacOS/Tailscale"

func macTailscaleCLI(ctx context.Context, out any, args ...string) error {
	cmd := exec.CommandContext(ctx, tailscaleAppCLI, args...)
	cmd.Env = append(os.Environ(), "TAILSCALE_BE_CLI=1")
	b, err := cmd.Output()
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func (macTailscale) Status(ctx context.Context) (*ipnstate.Status, error) {
	if _, err := os.Stat(tailscaleAppCLI); os.IsNotExist(err) {
		return (&local.Client{}).Status(ctx)
	}
	var st ipnstate.Status
	err := macTailscaleCLI(ctx, &st, "status", "--json")
	return &st, err
}

func (macTailscale) WhoIs(ctx context.Context, addr string) (*apitype.WhoIsResponse, error) {
	if _, err := os.Stat(tailscaleAppCLI); os.IsNotExist(err) {
		return (&local.Client{}).WhoIs(ctx, addr)
	}
	var who apitype.WhoIsResponse
	err := macTailscaleCLI(ctx, &who, "whois", "--json", addr)
	return &who, err
}

func tailnetSocketControl(dev net.Interface) func(string, string, syscall.RawConn) error {
	return func(_, _ string, raw syscall.RawConn) error {
		var optErr error
		if err := raw.Control(func(fd uintptr) {
			optErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF, dev.Index)
		}); err != nil {
			return err
		}
		return optErr
	}
}
