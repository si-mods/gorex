package rex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"runtime"
	"sync"
	"time"
)

// A voice is what a phone's microphone hears, for the program of a
// session that records with rec, as Claude Code's /voice does on a
// machine with no microphone of its own. The Rex API gateway feeds it, as
// raw PCM: signed 16-bit little-endian, 16 kHz, mono, which is what rec
// writes. Windows' Claude Code records no other way than its own, so only
// a Unix server says it takes voices.
//
// Deprecated: nothing on the host reads a voice any more, since
// GoRex's rec is gone, and the interface will go.

// CapabilityVoice is what a server that carries a voice to a session's rec
// says it does: the "voice.put" and "voice.get" connections.
//
// Deprecated: nothing on the host reads a voice any more, since
// GoRex's rec is gone, and the interface will go.
const CapabilityVoice = "voice"

func init() {
	if runtime.GOOS != "windows" {
		capabilities = append(capabilities, CapabilityVoice)
	}
}

// Voice is the first line of a connection that feeds a session's voice
// ("voice.put") or reads it ("voice.get"), from this machine only. The
// server answers a FileAnswer: OK, or Error, after which it closes the
// connection. A put then sends the voice's bytes, and closes the
// connection as the voice ends; the server closes it as the voice ends
// first, as a new put of the session or the going of the get that took
// it ends it. A get waits voiceWait for a voice no other get took to
// begin, then the server sends what the voice carried since it began, and
// what it carries after, and closes the connection once it ended. A voice
// no get took or waits for as it ends is dropped.
//
// Deprecated: nothing on the host reads a voice any more, since
// GoRex's rec is gone, and the interface will go.
type Voice struct {
	Op  string `json:"op"` // "voice.put" or "voice.get"
	SID string `json:"sid"`
}

// voiceWait is how long a get waits for a voice to begin: the phone's may
// come a moment after the program started rec, as it opens a connection
// of its own, maybe through a relay, while the Spaces that start rec go
// out at once on the one already open. maxVoiceBytes is the most
// of a voice the server keeps that no get sent yet, ten seconds of it;
// the oldest bytes go.
const (
	voiceWait     = 3 * time.Second
	maxVoiceBytes = 10 * 16000 * 2
)

// voice is a session's voice as the server has it: the bytes no get sent
// yet, and whether it ended. wake closes as bytes come or it ends; done,
// as it ends. taken, held by Server.voiceMu, tells that a get took it.
type voice struct {
	mu    sync.Mutex
	buf   []byte
	ended bool
	wake  chan struct{}
	done  chan struct{}
	taken bool
}

// add keeps b, of a voice that did not end.
func (v *voice) add(b []byte) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.ended {
		return
	}
	v.buf = append(v.buf, b...)
	if len(v.buf) > maxVoiceBytes {
		v.buf = v.buf[len(v.buf)-maxVoiceBytes:]
	}
	close(v.wake)
	v.wake = make(chan struct{})
}

// end ends the voice; its wake stays closed.
func (v *voice) end() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.ended {
		v.ended = true
		close(v.wake)
		close(v.done)
	}
}

// over tells whether the voice ended.
func (v *voice) over() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.ended
}

// next takes the bytes kept, and tells whether the voice ended and what
// closes as that changes.
func (v *voice) next() (b []byte, ended bool, wake chan struct{}) {
	v.mu.Lock()
	defer v.mu.Unlock()
	b, v.buf = v.buf, nil
	return b, v.ended, v.wake
}

// voiceConn serves a voice.put or voice.get connection of this machine,
// whose first line is first.
func (s *Server) voiceConn(conn net.Conn, r *bufio.Reader, first []byte, op string) {
	defer conn.Close()
	answer := func(a FileAnswer) {
		b, _ := json.Marshal(a)
		conn.Write(append(b, '\n'))
	}
	var req Voice
	if err := json.Unmarshal(first, &req); err != nil {
		answer(FileAnswer{Error: err.Error()})
		return
	}
	if _, err := s.session(req.SID); err != nil {
		answer(FileAnswer{Error: err.Error()})
		return
	}
	if op == "voice.put" {
		s.voicePut(conn, req.SID, r, answer)
		return
	}
	s.voiceGet(conn, req.SID, answer)
}

// voicePut keeps what r carries as the session's voice, until it ends,
// and closes conn as it does.
func (s *Server) voicePut(conn net.Conn, sid string, r io.Reader, answer func(FileAnswer)) {
	v := &voice{wake: make(chan struct{}), done: make(chan struct{})}
	go func() {
		<-v.done
		conn.Close()
	}()
	s.voiceMu.Lock()
	if s.voices == nil {
		s.voices = map[string]*voice{}
	}
	old := s.voices[sid]
	s.voices[sid] = v
	if s.voiceWake != nil {
		close(s.voiceWake)
		s.voiceWake = nil
	}
	s.voiceMu.Unlock()
	if old != nil {
		old.end()
	}
	answer(FileAnswer{OK: true})
	buf := make([]byte, 32<<10)
	for {
		n, err := r.Read(buf)
		v.add(buf[:n])
		if err != nil {
			break
		}
	}
	v.end()
	// A get that waits takes it yet, though it ended.
	s.voiceMu.Lock()
	if s.voices[sid] == v && !v.taken && s.voiceGets[sid] == 0 {
		delete(s.voices, sid)
	}
	s.voiceMu.Unlock()
}

// voiceGet sends the session's voice, once one begins, until it ends or
// the client goes.
func (s *Server) voiceGet(conn net.Conn, sid string, answer func(FileAnswer)) {
	timeout := time.NewTimer(voiceWait)
	defer timeout.Stop()
	var v *voice
	s.voiceMu.Lock()
	if s.voiceGets == nil {
		s.voiceGets = map[string]int{}
	}
	s.voiceGets[sid]++
	s.voiceMu.Unlock()
	// As the get goes, so does the voice it took, which ends, and one
	// that ended which no get waits for.
	defer func() {
		if v != nil {
			v.end()
		}
		s.voiceMu.Lock()
		if s.voiceGets[sid]--; s.voiceGets[sid] == 0 {
			delete(s.voiceGets, sid)
		}
		if cur := s.voices[sid]; cur != nil && (cur == v || s.voiceGets[sid] == 0 && cur.over()) {
			delete(s.voices, sid)
		}
		s.voiceMu.Unlock()
	}()
	// The client sends nothing: its connection ends as it goes.
	gone := make(chan struct{})
	go func() {
		io.Copy(io.Discard, conn)
		close(gone)
	}()
	for v == nil {
		s.voiceMu.Lock()
		if old := s.voices[sid]; old != nil && !old.taken {
			old.taken = true
			v = old
		}
		if s.voiceWake == nil {
			s.voiceWake = make(chan struct{})
		}
		wake := s.voiceWake
		s.voiceMu.Unlock()
		if v != nil {
			break
		}
		select {
		case <-wake:
		case <-gone:
			return
		case <-timeout.C:
			answer(FileAnswer{Error: errNoVoice.Error()})
			return
		}
	}
	answer(FileAnswer{OK: true})
	for {
		b, ended, wake := v.next()
		if len(b) > 0 {
			if _, err := conn.Write(b); err != nil {
				return
			}
			continue
		}
		if ended {
			return
		}
		select {
		case <-wake:
		case <-gone:
			return
		}
	}
}

var (
	errNoVoice       = errors.New("rex: no voice began")
	errNoVoiceServer = errors.New("rex: the server takes no voice; update GoRex there")
)

// canVoice tells whether the server takes voices, as an error.
func (c *Client) canVoice() error {
	h, err := c.Hello()
	if err == nil && !h.Can(CapabilityVoice) {
		err = errNoVoiceServer
	}
	return err
}

// FeedVoice begins the voice of the session sid (Voice): what is written
// to it, ReadVoice reads, and closing it ends the voice, as ctx being
// done does. The caller knows the server takes voices (CapabilityVoice),
// as the gateway does from its hello.
//
// Deprecated: nothing on the host reads a voice any more, since
// GoRex's rec is gone, and the interface will go.
func (c *Client) FeedVoice(ctx context.Context, sid string) (io.WriteCloser, error) {
	fc, _, err := c.openFile(ctx, Voice{Op: "voice.put", SID: sid})
	if err != nil {
		return nil, err
	}
	return fc, nil
}

// ReadVoice reads the voice of the session sid (Voice), waiting a moment
// for one to begin: what it carried since it began, then what it carries,
// until it ends.
//
// Deprecated: nothing on the host reads a voice any more, since
// GoRex's rec is gone, and the interface will go.
func (c *Client) ReadVoice(ctx context.Context, sid string) (io.ReadCloser, error) {
	if err := c.canVoice(); err != nil {
		return nil, err
	}
	fc, _, err := c.openFile(ctx, Voice{Op: "voice.get", SID: sid})
	if err != nil {
		return nil, err
	}
	return voiceReader{fc}, nil
}

// voiceReader reads a voice.get connection through its buffer, which may
// hold the voice's first bytes.
type voiceReader struct{ fileConn }

func (v voiceReader) Read(p []byte) (int, error) { return v.r.Read(p) }
