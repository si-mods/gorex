package rexapi

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"rsc.io/qr"
)

// publicMain configures the listener offline. Credential administration is
// serialized by the running gateway over the user's local socket.
func publicMain(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: gorex rexapi public on|off|status|pair|devices|revoke")
		return 2
	}
	dir := TailnetDir()
	switch args[0] {
	case "on":
		fs := flag.NewFlagSet("public on", flag.ContinueOnError)
		base := fs.String("url", "", "public HTTPS address")
		listen := fs.String("listen", "127.0.0.1:7298", "listen address (loopback unless TLS is configured)")
		cert := fs.String("cert", "", "TLS certificate file")
		key := fs.String("key", "", "TLS private key file")
		if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
			return 2
		}
		c := PublicConfig{Enabled: true, URL: *base, Listen: *listen, Cert: *cert, Key: *key}
		if c.Cert != "" {
			c.Cert, _ = filepath.Abs(c.Cert)
		}
		if c.Key != "" {
			c.Key, _ = filepath.Abs(c.Key)
		}
		if err := SavePublic(dir, c); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		fmt.Println("Public access enabled; the GoRex server applies it within a second.")
		return 0
	case "off":
		if len(args) != 1 {
			return 2
		}
		c, err := LoadPublic(dir)
		if err == nil {
			c.Enabled = false
			err = writeJSON(publicConfPath(dir), c)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("Public access disabled.")
		return 0
	case "status":
		if len(args) != 1 {
			return 2
		}
		c, err := LoadPublic(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		var s LANStatus
		if b, err := os.ReadFile(filepath.Join(dir, "public-status.json")); err == nil {
			json.Unmarshal(b, &s)
		}
		json.NewEncoder(os.Stdout).Encode(map[string]any{"config": c, "status": s})
		return 0
	case "pair", "devices", "revoke":
		method, path, payload := http.MethodGet, "/api/access/devices", any(nil)
		if args[0] == "pair" {
			if len(args) != 1 {
				return 2
			}
			method, path = http.MethodPost, "/api/access/pairing"
		} else if args[0] == "revoke" {
			if len(args) != 2 {
				fmt.Fprintln(os.Stderr, "usage: gorex rexapi public revoke DEVICE_ID")
				return 2
			}
			method, path, payload = http.MethodPost, "/api/access/revoke", map[string]string{"id": args[1]}
		} else if len(args) != 1 {
			return 2
		}
		raw, err := callPublicAdmin(method, path, payload)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if args[0] == "pair" {
			var pair struct {
				Code    string    `json:"code"`
				URL     string    `json:"url"`
				Expires time.Time `json:"expires"`
			}
			if err := json.Unmarshal(raw, &pair); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			link := pair.URL + "/#gorex-pair=" + pair.Code
			code, err := qr.Encode(link, qr.M)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			fmt.Print(renderQR(code))
			fmt.Printf("%s\nPairing code: %s\nExpires: %s\n", link, pair.Code, pair.Expires.Format(time.RFC3339))
		} else {
			fmt.Println(string(raw))
		}
		return 0
	default:
		fmt.Fprintln(os.Stderr, "usage: gorex rexapi public on|off|status|pair|devices|revoke")
		return 2
	}
}

func callPublicAdmin(method, path string, payload any) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", SocketPath())
	}}
	defer transport.CloseIdleConnections()
	var body bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&body).Encode(payload); err != nil {
			return nil, err
		}
	}
	r, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, &body)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Transport: transport}).Do(r)
	if err != nil {
		return nil, fmt.Errorf("no gateway on %s; enable public access and run the GoRex server: %w", SocketPath(), err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("device administration: %s", b)
	}
	return b, nil
}
