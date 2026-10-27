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
	"time"

	"gorex/internal/rex"
)

// version is the herdr version the bridge says it is: rootshell needs
// 0.9.0 at least, and stream protocol 2.
const version = "0.9.0-gorex"

// Intervals of the bridge: how often it reads the server's list and
// layout, which push nothing, and renews the size locks it holds.
var (
	pollEvery  = 500 * time.Millisecond
	renewEvery = 5 * time.Second
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
	}
	b.id = connectionID(b.owner)
	go b.write(out)
	b.send(map[string]any{"id": "control.open", "result": map[string]any{
		"connection_id": b.id,
		"boot_id":       fmt.Sprintf("gorex-%d-%d-%d", h.PID, h.Started.Unix(), os.Getpid()),
		"version":       version, "protocol": 1, "control_protocol": 2,
		"capabilities": map[string]any{
			"terminal_control_stream": 2,
			"control_features":        []string{"shared_attach", "geometry_ownership", "geometry_controller"},
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
	poll, renew := time.NewTicker(pollEvery), time.NewTicker(renewEvery)
	defer poll.Stop()
	defer renew.Stop()
	for {
		select {
		case line, ok := <-lines:
			if !ok || b.serve(line) {
				b.close()
				return 0
			}
		case <-poll.C:
			if b.poll() {
				b.tell()
			}
		case <-renew.C:
			b.renew()
			b.tell()
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
