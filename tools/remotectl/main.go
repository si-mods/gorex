// Command remotectl is what `gorex remote` does not do: act as a remote
// client without the app, for headless machines and for testing across a
// real network. The server's side is `gorex remote`.
//
//	remotectl pubkey                  print this machine's key as a remote client
//	remotectl regenerate              make new server keys; the address changes
//	remotectl pair ADDRESS NAME       ask a remote server to allow this machine
//	remotectl probe ADDRESS           connect remotely, run a command, check what is denied
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"gorex/internal/rex"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: remotectl pubkey|regenerate|pair ADDRESS NAME|probe ADDRESS")
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "pubkey":
		fmt.Println(rex.ClientPublicKey())
	case "regenerate":
		c, err := rex.Connect()
		fail(err)
		defer c.Close()
		fail(c.RegenerateRemote())
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
		fail(fmt.Errorf("unknown command %q", cmd))
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
