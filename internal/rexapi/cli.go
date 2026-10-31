package rexapi

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"rsc.io/qr"
)

// callGateway calls one method of the gateway serving the socket at
// path, as a Rex client does, and returns its payload.
func callGateway(path, method string, payload any) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	httpc := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
	}}
	ws, _, err := websocket.Dial(ctx, "ws://localhost/api/control", &websocket.DialOptions{
		HTTPClient: httpc, Subprotocols: []string{controlProtocol},
	})
	if err != nil {
		return nil, fmt.Errorf("no Rex API gateway on %s (run gorex rexapi): %w", path, err)
	}
	defer ws.CloseNow()
	ws.SetReadLimit(maxMessage)
	hello, _ := json.Marshal(map[string]any{
		"type": "hello", "api_revision": apiRevision,
		"client": map[string]any{"kind": "cli", "name": "gorex", "platform": platform()},
	})
	if err := ws.Write(ctx, websocket.MessageText, hello); err != nil {
		return nil, err
	}
	var welcome struct {
		Type string `json:"type"`
	}
	if _, b, err := ws.Read(ctx); err != nil {
		return nil, err
	} else if json.Unmarshal(b, &welcome); welcome.Type != "welcome" {
		return nil, fmt.Errorf("the gateway answered %s", b)
	}
	req := map[string]any{"type": "request", "id": "cli-1", "method": method}
	if payload != nil {
		req["payload"] = payload
	}
	b, _ := json.Marshal(req)
	if err := ws.Write(ctx, websocket.MessageText, b); err != nil {
		return nil, err
	}
	for {
		_, b, err := ws.Read(ctx)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Type    string          `json:"type"`
			ID      string          `json:"id"`
			Payload json.RawMessage `json:"payload"`
			Error   *apiError       `json:"error"`
		}
		if json.Unmarshal(b, &resp) != nil || resp.Type != "response" || resp.ID != "cli-1" {
			continue
		}
		ws.Close(websocket.StatusNormalClosure, "")
		if resp.Error != nil {
			return nil, fmt.Errorf("%s: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Payload, nil
	}
}

// callMain is "gorex rexapi call METHOD [JSON]": it calls a method of
// the gateway that runs and prints its result.
func callMain(args []string) int {
	if len(args) < 1 || len(args) > 2 {
		fmt.Fprintln(os.Stderr, "usage: gorex rexapi call METHOD [JSON]")
		return 2
	}
	var payload any
	if len(args) == 2 {
		if err := json.Unmarshal([]byte(args[1]), &payload); err != nil {
			fmt.Fprintf(os.Stderr, "gorex rexapi call: the arguments are not JSON: %v\n", err)
			return 2
		}
	}
	out, err := callGateway(SocketPath(), args[0], payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gorex rexapi call: %v\n", err)
		return 1
	}
	fmt.Println(string(out))
	return 0
}

// tailscaleMain is "gorex rexapi tailscale on|off|login|logout|rename
// NAME|status": built-in Tailscale of the gateway that runs.
func tailscaleMain(args []string) int {
	usage := "usage: gorex rexapi tailscale on [--hostname NAME] [--allow-tailnet-members] [--auth-key-file FILE] | off | login | logout | rename NAME | status"
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	var method string
	var payload any
	switch args[0] {
	case "on":
		fs := flag.NewFlagSet("tailscale on", flag.ContinueOnError)
		hostname := fs.String("hostname", "", "the device's name on the tailnet (default: this machine's, with -gorex)")
		allow := fs.Bool("allow-tailnet-members", false, "let in everyone the tailnet's access rules allow, not only you")
		keyFile := fs.String("auth-key-file", "", "sign in with the Tailscale auth key in FILE ('-' for stdin)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		p := map[string]any{"allow_tailnet_members": *allow}
		if *hostname != "" {
			p["hostname"] = *hostname
		}
		if *keyFile != "" {
			key, err := readKey(*keyFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "gorex rexapi tailscale: %v\n", err)
				return 1
			}
			p["auth_key"] = key
		}
		method, payload = "server.tailscale.enable", p
	case "off":
		method = "server.tailscale.disable"
	case "login":
		method = "server.tailscale.login"
	case "logout":
		method = "server.tailscale.logout"
	case "rename":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, usage)
			return 2
		}
		method, payload = "server.tailscale.rename", map[string]any{"hostname": args[1]}
	case "status":
		method = "server.status"
	default:
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	out, err := callGateway(SocketPath(), method, payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gorex rexapi tailscale: %v\n", err)
		return 1
	}
	var res struct {
		Tailscale *tailscaleStatus `json:"tailscale"`
	}
	json.Unmarshal(out, &res)
	if res.Tailscale == nil {
		fmt.Println("tailscale: not supported by this gateway")
		return 1
	}
	printTailscale(os.Stdout, *res.Tailscale)
	return 0
}

func readKey(path string) (string, error) {
	var b []byte
	var err error
	if path == "-" {
		b, err = io.ReadAll(os.Stdin)
	} else {
		b, err = os.ReadFile(path)
	}
	return strings.TrimSpace(string(b)), err
}

func printTailscale(w io.Writer, st tailscaleStatus) {
	fmt.Fprintf(w, "tailscale: %s\n", st.State)
	if st.DNSName != "" {
		fmt.Fprintf(w, "  device: %s\n", st.DNSName)
	}
	if st.Account != nil {
		fmt.Fprintf(w, "  account: %s\n", st.Account.LoginName)
	}
	if st.AuthURL != "" {
		fmt.Fprintf(w, "  sign in: %s\n", st.AuthURL)
	}
	if st.HTTPS != nil {
		fmt.Fprintf(w, "  https: %s %s\n", st.HTTPS.State, st.HTTPS.URL)
	}
	fmt.Fprintf(w, "  allow tailnet members: %s\n", strconv.FormatBool(st.AllowTailnetMembers))
	if st.Error != "" {
		fmt.Fprintf(w, "  error: %s\n", st.Error)
	}
}

// pairMain is "gorex rexapi pair": it shows the gateway's tailnet
// address as a QR code to scan with Rex on another device, as "rex
// server tailscale pair" does. The code holds only the address.
func pairMain(args []string) int {
	fs := flag.NewFlagSet("pair", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print the address as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	out, err := callGateway(SocketPath(), "server.status", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gorex rexapi pair: %v\n", err)
		return 1
	}
	var res struct {
		Tailscale *tailscaleStatus `json:"tailscale"`
	}
	json.Unmarshal(out, &res)
	url, err := pairURL(res.Tailscale)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gorex rexapi pair: %v\n", err)
		return 1
	}
	if *asJSON {
		b, _ := json.Marshal(map[string]string{"url": url})
		fmt.Println(string(b))
		return 0
	}
	code, err := qr.Encode(url, qr.M)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gorex rexapi pair: %v\n", err)
		return 1
	}
	fmt.Print(renderQR(code))
	fmt.Printf("\nScan with Rex on a device on the same tailnet: %s\n", url)
	return 0
}

// pairURL is the address a status lets another device pair with, or why
// there is none yet.
func pairURL(st *tailscaleStatus) (string, error) {
	switch {
	case st == nil:
		return "", errors.New("this gateway can't run built-in Tailscale")
	case st.State == "disabled":
		return "", errors.New("built-in Tailscale is off: run gorex rexapi tailscale on")
	case st.State == "needs_login":
		if st.AuthURL != "" {
			return "", fmt.Errorf("sign the device in first: %s", st.AuthURL)
		}
		return "", errors.New("sign the device in first: run gorex rexapi tailscale login")
	case st.HTTPS == nil || st.HTTPS.State != "available" || st.HTTPS.URL == "":
		return "", errors.New("the tailnet address is not ready yet (turn on MagicDNS and HTTPS in the tailnet's admin console)")
	}
	return st.HTTPS.URL, nil
}

// renderQR draws a QR code in a terminal, two rows a line with half
// blocks, dark on light, with the quiet zone scanners need.
func renderQR(c *qr.Code) string {
	const quiet = 2
	n := c.Size + 2*quiet
	dark := func(x, y int) bool { return c.Black(x-quiet, y-quiet) }
	var b strings.Builder
	for y := 0; y < n; y += 2 {
		for x := 0; x < n; x++ {
			top, bottom := dark(x, y), y+1 < n && dark(x, y+1)
			switch {
			case top && bottom:
				b.WriteRune(' ')
			case top:
				b.WriteRune('▄')
			case bottom:
				b.WriteRune('▀')
			default:
				b.WriteRune('█')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}
