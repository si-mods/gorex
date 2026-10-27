// Package control is "gorex control": a bridge on stdin and stdout that
// lets rootshell drive this machine's sessions as it drives herdr in its
// full control mode, one JSON object a line both ways. It shows the tabs
// of the layout the app keeps on the server, read-only, and sizes a tab's
// sessions while rootshell holds the tab, with the server's size lock.
package control

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"os"
	"strconv"
	"time"

	"gorex/internal/rex"
)

// version is the herdr version the bridge says it is: rootshell needs
// 0.9.0 at least, and stream protocol 2; protocol 3, which a client asks
// for in HERDR_CONTROL_PROTOCOL, sizes each pane on its own.
const version = "0.9.0-gorex"

// Intervals of the bridge: how often it reads the server's list and
// layout, from a server that pushes nothing, or one that tells of its
// changes (rex.Client.Subscribe), for what it does not tell; how long it
// lets a burst of changes told of settle before it reads them; how often
// it renews the size locks it holds.
var (
	pollEvery   = 500 * time.Millisecond
	safetyEvery = 30 * time.Second
	settle      = 20 * time.Millisecond
	renewEvery  = 5 * time.Second
	// geometrySettle is how long the sizes rootshell gives a tab it holds
	// must stay the same before the bridge resizes its sessions, and
	// redrawAfter how long after a session's size changed its panes get
	// its screen anew.
	geometrySettle = 120 * time.Millisecond
	redrawAfter    = 250 * time.Millisecond
)

// Main runs the bridge on the process's stdin and stdout, and returns its
// exit status: never 2, which rootshell takes for a herdr without control
// mode, and falls back to commands GoRex does not have.
func Main() int {
	c, err := rex.ConnectExisting()
	if err != nil {
		line, _ := json.Marshal(map[string]string{"type": "control.error", "code": "not_found", "message": err.Error()})
		os.Stdout.Write(append(line, '\n'))
		return 1
	}
	return Run(c, os.Stdin, os.Stdout)
}

// Run bridges the server of c to in and out until in ends or asks to
// close, then unlocks what it locked; it returns the exit status, 1 when
// it lost the server or out.
func Run(c *rex.Client, in io.Reader, out io.Writer) int {
	defer c.Close()
	h, err := c.Hello()
	if err != nil || h.Conn == "" {
		log.Printf("control: hello: %v", err)
		return 1
	}
	// The owner is the bridge's own, which the sessions' list shows: not
	// the connection's id, which would let others attach through it.
	token := make([]byte, 8)
	rand.Read(token)
	b := &bridge{
		c: c, hello: h, owner: "control-" + hex.EncodeToString(token),
		out: make(chan []byte, 256), quit: make(chan struct{}), flushed: make(chan struct{}),
		infos: map[string]rex.SessionInfo{}, sizes: map[string][2]int{}, owned: map[string]bool{},
		locked: map[string]bool{}, attaches: map[string]*attach{},
		told: map[string]string{}, ctl: map[string]controller{},
		picked: map[string]string{}, jobs: make(chan *job, 16), done: make(chan *job),
		paneSizes: map[string]map[string]paneSize{}, proto: 2,
		wanted: map[string]geometry{}, redraws: map[string]time.Time{},
	}
	b.id = connectionID(b.owner)
	features := []string{"shared_attach", "geometry_ownership", "geometry_controller"}
	if n, _ := strconv.Atoi(os.Getenv("HERDR_CONTROL_PROTOCOL")); n >= 3 {
		b.proto = 3
		features = append(features, "pane_geometry")
	}
	// Subscribed before the first poll: no change after it goes untold.
	b.subscribed = c.Subscribe() == nil
	go b.write(out)
	b.send(map[string]any{"id": "control.open", "result": map[string]any{
		"connection_id": b.id,
		"boot_id":       fmt.Sprintf("gorex-%d-%d-%d", h.PID, h.Started.Unix(), os.Getpid()),
		"version":       version, "protocol": 1, "control_protocol": b.proto,
		"capabilities": map[string]any{
			"terminal_control_stream": b.proto,
			"control_features":        features,
			"server_pid":              h.PID, "live_handoff": false,
		},
	}})
	b.poll()
	b.shape = b.topology()
	b.tellQuietly()

	lines := make(chan []byte)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(in)
		sc.Buffer(make([]byte, 64<<10), 16<<20)
		for sc.Scan() {
			select {
			case lines <- append([]byte(nil), sc.Bytes()...):
			case <-b.quit:
				return
			}
		}
	}()
	go b.work()
	every := pollEvery
	if b.subscribed {
		every = safetyEvery
	}
	poll, renew := time.NewTicker(every), time.NewTicker(renewEvery)
	defer poll.Stop()
	defer renew.Stop()
	var settled <-chan time.Time
	for {
		select {
		case line, ok := <-lines:
			if !ok || b.serve(line) {
				b.close()
				return 0
			}
		case <-c.Changed():
			if settled == nil {
				settled = time.After(settle)
			}
		case <-settled:
			settled = nil
			if layout, sessions := c.Changes(); b.fetch(sessions, layout) {
				b.tell()
			}
			b.check()
		case <-poll.C:
			if b.poll() {
				b.tell()
			}
			b.check()
		case <-b.geometryDue():
			b.settleGeometry()
		case <-b.redrawDue():
			b.redraw()
		case <-b.recheck():
			if !b.subscribed && b.poll() {
				b.tell()
			}
			b.check()
		case <-renew.C:
			b.renew()
			b.tell()
		case j := <-b.done:
			b.finish(j)
		case <-c.Closed():
			log.Print("control: lost the server")
			b.close()
			return 1
		case <-b.flushed:
			b.close() // rootshell is gone
			return 1
		}
	}
}

// connectionID is the herdr connection id of a lock's owner: rootshell
// compares it with its own to tell whose a tab is.
func connectionID(owner string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(owner))
	return h.Sum64()&(1<<53-1) | 1
}

// write writes out the lines sent, one at a time, until a nil one or an
// error, after which it closes flushed.
func (b *bridge) write(out io.Writer) {
	defer close(b.flushed)
	for line := range b.out {
		if line == nil {
			return
		}
		if _, err := out.Write(line); err != nil {
			log.Printf("control: %v", err)
			return
		}
	}
}

// send writes v as a line, after what was sent before.
func (b *bridge) send(v any) {
	line, err := json.Marshal(v)
	if err != nil {
		log.Printf("control: %v", err)
		return
	}
	select {
	case b.out <- append(line, '\n'):
	case <-b.quit:
	case <-b.flushed:
	}
}

// close unlocks the sizes the bridge holds, detaches, and writes what is
// left to write.
func (b *bridge) close() {
	close(b.quit)
	for sid := range b.locked {
		b.c.UnlockSize(sid, b.owner, 0, 0)
	}
	for _, at := range b.attaches {
		at.detach()
	}
	select {
	case b.out <- nil:
	case <-b.flushed:
	}
	<-b.flushed
}
