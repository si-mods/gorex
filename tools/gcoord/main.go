// Command gcoord runs GoRex's network coordinator, the mini control
// plane that members join once instead of pairing machine by machine
// (see docs/coord.md). It belongs on an always-on machine, behind no
// proxy: it must see the caller's address to lock out wrong keys. The
// state directory holds secrets — every member's address — so it stays
// 0600 and off every backup that is not already secret.
//
//	gcoord init DIR HOST[:PORT]  make a network: key, certificate, state;
//	                            prints the network key once, then the join URL
//	                            (the port is 443 when left out)
//	gcoord serve DIR [LISTEN]   answer members (default :443)
//	gcoord members DIR          list the members
//	gcoord kick DIR NAME        drop a member, who is then refused everywhere
package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"text/tabwriter"
	"time"

	"gorex/internal/coord"
)

func main() {
	if len(os.Args) < 2 {
		fail(errors.New("usage: gcoord init|serve|members|kick"))
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "init":
		if len(args) != 2 {
			fail(errors.New("gcoord init DIR HOST[:PORT]"))
		}
		fail(initNetwork(args[0], args[1]))
	case "serve":
		listen := fmt.Sprint(":", coord.DefaultPort)
		if len(args) == 2 {
			listen, args = args[1], args[:1]
		}
		if len(args) != 1 {
			fail(errors.New("gcoord serve DIR [LISTEN]"))
		}
		fail(serve(args[0], listen))
	case "members":
		if len(args) != 1 {
			fail(errors.New("gcoord members DIR"))
		}
		fail(members(args[0]))
	case "kick":
		if len(args) != 2 {
			fail(errors.New("gcoord kick DIR NAME"))
		}
		fail(kick(args[0], args[1]))
	default:
		fail(fmt.Errorf("gcoord %s: no such command", cmd))
	}
}

func initNetwork(dir, hostport string) error {
	key, fingerprint, err := coord.Init(dir)
	if err != nil {
		return err
	}
	fmt.Println("network key (shown once, keep it secret):")
	fmt.Println(key)
	fmt.Println()
	fmt.Println("join URL (a secret too: it pins the coordinator):")
	fmt.Println(coord.JoinURL(hostport, fingerprint))
	return nil
}

func serve(dir, listen string) error {
	s, err := coord.NewServer(dir)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				c, err := tls.LoadX509KeyPair(dir+"/tls.crt", dir+"/tls.key")
				return &c, err
			},
		},
	}
	log.Printf("gcoord: answering on %s", listen)
	// No certificate files: GetCertificate above loads them.
	return srv.ListenAndServeTLS("", "")
}

func members(dir string) error {
	ms, err := coord.Members(dir)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	for _, m := range ms {
		seen := m.LastSeen.Format(time.DateTime)
		if time.Since(m.LastSeen) > 7*24*time.Hour {
			seen = "stale"
		}
		fmt.Fprintf(w, "%s\t%s\tjoined %s\tseen %s\n", m.Name, m.Fingerprint(), m.Joined.Format(time.DateOnly), seen)
	}
	return w.Flush()
}

func kick(dir, name string) error {
	s, err := coord.NewServer(dir)
	if err != nil {
		return err
	}
	if err := s.Kick(name, ""); err != nil {
		return err
	}
	fmt.Println("kicked; every member learns within its next poll")
	return nil
}

func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
