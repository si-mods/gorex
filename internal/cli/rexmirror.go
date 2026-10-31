package cli

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

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
	// One writer for everything the terminal shows: the screen, the
	// title, and the status, which a later goroutine updates.
	sw := &syncWriter{w: out}
	title := *label
	if t := peer.Title(ctx, *session, *block); t != "" {
		title = t
	}
	if title != "" {
		fmt.Fprintf(sw, "\x1b]2;%s\x1b\\", oscSafe(title))
	}
	status, hasStatus := peer.ProgramStatus(ctx, *session, *block)
	if hasStatus {
		io.WriteString(sw, statusOSC(status))
	}
	// The data stream starts with the screen in Ghostty's binary snapshot
	// format, which only a terminal of Ghostty's restores, so the screen is
	// drawn from the escape sequences Rex renders, and the stream goes on
	// with what the program prints.
	if screen, err := peer.Format(ctx, *session, *block, "vt"); err == nil && screen != "" {
		io.WriteString(sw, screen)
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
	// The program's own reports of its title and status come with its
	// output; what Rex kept from before this connection is asked for now
	// and then, so a pane shows them too.
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(3 * time.Second)
		defer t.Stop()
		lastTitle, lastStatus := title, statusKey(status, hasStatus)
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if nt := peer.Title(ctx, *session, *block); nt != "" && nt != lastTitle {
					lastTitle = nt
					fmt.Fprintf(sw, "\x1b]2;%s\x1b\\", oscSafe(nt))
				}
				st, ok := peer.ProgramStatus(ctx, *session, *block)
				if key := statusKey(st, ok); key != lastStatus {
					lastStatus = key
					if ok {
						io.WriteString(sw, statusOSC(st))
					} else {
						io.WriteString(sw, "\x1b]7501;state=clear\x1b\\")
					}
				}
			}
		}
	}()
	w := newAPCWriter(sw, stream.Nonce())
	_, err = io.Copy(w, stream)
	w.Flush()
	if err != nil && ctx.Err() == nil && !streamClosed(err) {
		fmt.Fprintf(errOut, "gorex rex-mirror: the stream ended: %v\n", err)
	}
	return ExitOK, true
}

// syncWriter is one writer for the several goroutines that draw a pane.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// oscSafe drops the control characters an OSC title cannot hold.
func oscSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// statusOSC is a program status report, as OSC 7501 carries it.
func statusOSC(st rexpeer.Status) string {
	parts := []string{"state=" + st.State}
	if st.Kind != "" {
		parts = append(parts, "kind="+st.Kind)
	}
	if st.App != "" {
		parts = append(parts, "app="+st.App)
	}
	if st.Progress >= 0 {
		parts = append(parts, "progress="+strconv.Itoa(st.Progress))
	}
	if st.Title != "" {
		parts = append(parts, "title="+base64.RawStdEncoding.EncodeToString([]byte(st.Title)))
	}
	if st.Msg != "" {
		parts = append(parts, "msg="+base64.RawStdEncoding.EncodeToString([]byte(st.Msg)))
	}
	return "\x1b]7501;" + strings.Join(parts, ":") + "\x1b\\"
}

// statusKey names a status, so that only changes are reported.
func statusKey(st rexpeer.Status, ok bool) string {
	if !ok {
		return ""
	}
	return st.State + "|" + st.Kind + "|" + st.App + "|" + st.Title + "|" + st.Msg + "|" + strconv.Itoa(st.Progress)
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
