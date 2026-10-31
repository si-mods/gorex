package rexapi

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"time"

	"gorex/internal/rex"
)

// Main runs the Rex API gateway: "GoRex rexapi --listen
// http://127.0.0.1:PORT" connects to the GoRex server that runs, serves
// Rex's endpoints on that address, and returns the process's exit
// status. It does not start a GoRex server.
func Main(args []string) int {
	fs := flag.NewFlagSet("rexapi", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	listen := fs.String("listen", "", "the address to serve, as http://127.0.0.1:PORT")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *listen == "" {
		fmt.Fprintln(os.Stderr, "gorex rexapi: --listen http://127.0.0.1:PORT is needed")
		return 2
	}
	u, err := url.Parse(*listen)
	if err != nil || u.Scheme != "http" || u.Host == "" {
		fmt.Fprintf(os.Stderr, "gorex rexapi: %q is not an http:// address\n", *listen)
		return 2
	}
	c, err := rex.ConnectExisting()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gorex rexapi: %v\n", err)
		return 1
	}
	g, err := New(Options{Client: c})
	if err != nil {
		fmt.Fprintf(os.Stderr, "gorex rexapi: %v\n", err)
		return 1
	}
	defer g.Close()
	ln, err := net.Listen("tcp", u.Host)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gorex rexapi: %v\n", err)
		return 1
	}
	srv := &http.Server{
		Handler:           g.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			serveErr <- err
		}
		close(serveErr)
	}()
	fmt.Printf("rexapi: listening on http://%s\n", ln.Addr())
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	code := 0
	select {
	case <-interrupt:
	case <-c.Closed():
	case err := <-serveErr:
		if err != nil {
			logf("%v", err)
			code = 1
		}
	}
	srv.Close()
	return code
}
