package cli

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gorex/internal/rexpeer"
)

// rexMirrorCmd mirrors a Rex block in this terminal: what the block shows
// is printed, and what is typed here is written to it. A GoRex pane runs it
// to show a block of a Rex session.
func rexMirrorCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("rex-mirror", errOut)
	socket := fs.String("socket", "", "the Rex server's socket (default REX_SERVER)")
	session := fs.String("session", "", "the session that holds the block")
	block := fs.String("block", "", "the block to mirror")
	label := fs.String("label", "", "the block's label, for the window title")
	resize := fs.Bool("resize", false, "give the Rex block this terminal's size")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	if *session == "" || *block == "" {
		fmt.Fprintln(errOut, "gorex rex-mirror: --session and --block are needed")
		return ExitUsage, true
	}
	sock := *socket
	if sock == "" {
		sock = defaultRexSocket()
	}
	if sock == "" {
		fmt.Fprintln(errOut, "gorex rex-mirror: no Rex server: set --socket or REX_SERVER")
		return ExitUsage, true
	}
	ctx := context.Background()
	peer, err := rexpeer.Dial(ctx, sock)
	if err != nil {
		fmt.Fprintf(errOut, "gorex rex-mirror: %v\n", err)
		return ExitFailed, true
	}
	defer peer.Close()
	if _, err := peer.Attach(ctx, *session); err != nil {
		fmt.Fprintf(errOut, "gorex rex-mirror: %v\n", err)
		return ExitFailed, true
	}
	// With --resize, the Rex block takes this terminal's size, so that
	// what it prints fits.
	if *resize {
		if cols, rows := inputSize(os.Stdin); cols > 0 && rows > 0 {
			if err := peer.Resize(ctx, *session, *block, cols, rows); err != nil {
				fmt.Fprintf(errOut, "gorex rex-mirror: resizing: %v\n", err)
			}
		}
	}
	if *label != "" {
		fmt.Fprintf(out, "\x1b]2;%s\x07", *label)
	}
	// The data stream starts with the screen in Ghostty's binary snapshot
	// format, which only a terminal of Ghostty's restores, so the screen is
	// drawn from the escape sequences Rex renders, and the stream goes on
	// with what the program prints.
	if screen, err := peer.Format(ctx, *session, *block, "vt"); err == nil && screen != "" {
		io.WriteString(out, screen)
	}
	stream, err := peer.Data(ctx, *session, *block)
	if err != nil {
		fmt.Fprintf(errOut, "gorex rex-mirror: %v\n", err)
		return ExitFailed, true
	}
	defer stream.Close()
	if err := stream.SkipSnapshot(); err != nil {
		fmt.Fprintf(errOut, "gorex rex-mirror: %v\n", err)
		return ExitFailed, true
	}
	if restore, err := rawInput(os.Stdin); err == nil {
		defer restore()
	}
	var stop func()
	if *resize {
		// The terminal's size can change as the pane is dragged; the Rex
		// block follows it.
		stop = onResize(func() {
			if c, r := inputSize(os.Stdin); c > 0 && r > 0 {
				_ = peer.Resize(ctx, *session, *block, c, r)
			}
		})
		defer stop()
	}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				if werr := peer.Write(ctx, *session, *block, string(buf[:n])); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	w := newAPCWriter(out, stream.Nonce())
	_, err = io.Copy(w, stream)
	w.Flush()
	if err != nil && ctx.Err() == nil && !streamClosed(err) {
		fmt.Fprintf(errOut, "gorex rex-mirror: the stream ended: %v\n", err)
	}
	return ExitOK, true
}

// streamClosed tells whether an error is the stream ending.
func streamClosed(err error) bool {
	return strings.Contains(err.Error(), "status = 1000") ||
		strings.Contains(err.Error(), "status = 1001") ||
		strings.Contains(err.Error(), "status = 4000")
}

// defaultRexSocket returns the socket of the Rex server this machine runs,
// from REX_SERVER when it is set.
func defaultRexSocket() string {
	if s := os.Getenv("REX_SERVER"); s != "" {
		if strings.HasPrefix(s, "unix://") {
			if u, err := url.Parse(s); err == nil {
				return u.Path
			}
		}
		if strings.HasPrefix(s, "/") {
			return s
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "rex", "server.sock")
	}
	return filepath.Join(home, ".local", "share", "rex", "server.sock")
}
