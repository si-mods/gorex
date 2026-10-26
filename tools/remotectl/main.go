// Command remotectl drives GoRex's remote access without the app: it runs a
// session server and manages or probes it, for headless machines and for
// testing across a real network.
//
//	remotectl serve                   run the session server (GOREX_DIR names its directory)
//	remotectl pubkey                  print this machine's key as a remote client
//	remotectl allow NAME PUBLICKEY    allow a client of the running server
//	remotectl enable | disable | status | regenerate
//	remotectl pairstart | pairstop    open or close the pairing window
//	remotectl pending                 list the pairing requests, with fingerprints
//	remotectl approve | reject PUBLICKEY
//	remotectl pair ADDRESS NAME       ask a remote server to allow this machine
//	remotectl probe ADDRESS           connect remotely, run a command, check what is denied
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"gorex/internal/rex"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: remotectl serve|pubkey|allow|enable|disable|status|regenerate|pairstart|pairstop|pending|approve|reject|pair|probe")
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "serve":
		fail(rex.Serve())
	case "-server": // rex.Spawn starts the executable with this
		fail(rex.Serve())
	case "pubkey":
		fmt.Println(rex.ClientPublicKey())
	case "probe":
		if len(args) != 1 {
			fail(fmt.Errorf("probe ADDRESS"))
		}
		fail(probe(args[0]))
	case "pair":
		if len(args) != 2 {
			fail(fmt.Errorf("pair ADDRESS NAME"))
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		c, err := rex.ConnectRemote(ctx, args[0])
		fail(err)
		defer c.Close()
		fp, err := c.RequestPairing(args[1])
		fail(err)
		fmt.Println("requested; approve it on the server if it shows this fingerprint:", fp)
	default:
		c, err := rex.Connect()
		fail(err)
		defer c.Close()
		switch cmd {
		case "allow":
			if len(args) != 2 {
				fail(fmt.Errorf("allow NAME PUBLICKEY"))
			}
			fail(c.AllowRemote(args[0], args[1]))
		case "enable":
			fail(c.EnableRemote())
		case "disable":
			fail(c.DisableRemote())
		case "regenerate":
			fail(c.RegenerateRemote())
		case "pairstart":
			fail(c.StartPairing())
		case "pairstop":
			fail(c.StopPairing())
		case "pending":
			st, err := c.RemoteStatus()
			fail(err)
			if !st.Pairing.Open {
				fmt.Println("not pairing")
			}
			for _, p := range st.Pending {
				fmt.Printf("%s\t%s\t%s\n", rex.Fingerprint(p.PublicKey), p.Name, p.PublicKey)
			}
		case "approve", "reject":
			if len(args) != 1 {
				fail(fmt.Errorf("%s PUBLICKEY", cmd))
			}
			if cmd == "approve" {
				fail(c.ApprovePairing(args[0]))
			} else {
				fail(c.RejectPairing(args[0]))
			}
		case "status":
			st, err := c.RemoteStatus()
			fail(err)
			b, _ := json.MarshalIndent(st, "", "  ")
			fmt.Println(string(b))
		default:
			fail(fmt.Errorf("unknown command %q", cmd))
		}
	}
}

func probe(addr string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	t0 := time.Now()
	c, err := rex.ConnectRemote(ctx, addr)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer c.Close()
	h, err := c.Hello()
	if err != nil {
		return fmt.Errorf("hello: %w", err)
	}
	fmt.Printf("connected in %v; hello: %+v\n", time.Since(t0).Round(time.Millisecond), h)
	if err := c.Shutdown(); err == nil {
		return fmt.Errorf("a remote client shut the server down")
	} else {
		fmt.Println("shutdown denied:", err)
	}
	if _, err := c.RemoteStatus(); err == nil {
		return fmt.Errorf("a remote client read the remote settings")
	} else {
		fmt.Println("remote.status denied:", err)
	}
	info, err := c.Create(rex.CreateOptions{Command: []string{"/bin/sh"}, Dir: "/tmp", Cols: 80, Rows: 24})
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	s := c.Stream(info.ID, 80, 24)
	s.Resize(80, 24)
	s.Write([]byte("echo HOST=$(uname -n) PROBE=$((6*7))\n"))
	var out strings.Builder
	buf := make([]byte, 4096)
	for deadline := time.Now().Add(20 * time.Second); !strings.Contains(out.String(), "PROBE=42") && time.Now().Before(deadline); {
		n, err := s.Read(buf)
		out.Write(buf[:n])
		if err != nil {
			break
		}
	}
	s.Close()
	c.Kill(info.ID)
	if !strings.Contains(out.String(), "PROBE=42") {
		return fmt.Errorf("no output from the remote shell: %q", out.String())
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "PROBE=42") && strings.Contains(line, "HOST=") {
			fmt.Println("remote shell says:", strings.TrimSpace(line))
		}
	}
	fmt.Println("probe ok")
	return nil
}

func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "remotectl:", err)
		os.Exit(1)
	}
}
