package cli

import (
	"bytes"
	"io"
	"strconv"
)

// maxAPC is the longest escape sequence a stream may wait for: Rex's own
// messages are short, and one without an end must not hold the stream.
const maxAPC = 64 << 10

// apcWriter passes a Rex terminal stream on, leaving out Rex's own messages
// in it: each is an APC escape sequence ESC _ rex1;<nonce>;<json> ESC \,
// which the server marks with the nonce of the data connection.
type apcWriter struct {
	out   io.Writer
	nonce string
	buf   []byte
}

// newAPCWriter wraps out, dropping the messages of the stream with nonce.
func newAPCWriter(out io.Writer, nonce uint64) *apcWriter {
	return &apcWriter{out: out, nonce: strconv.FormatUint(nonce, 10)}
}

// Write passes what it can on, keeping a partial message for the next
// write.
func (w *apcWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	return len(p), w.pass(false)
}

// Flush passes on what is left, even a partial message: the stream ended.
func (w *apcWriter) Flush() error { return w.pass(true) }

// pass writes everything before an incomplete message, dropping the
// stream's own messages.
func (w *apcWriter) pass(final bool) error {
	for {
		i := bytes.Index(w.buf, []byte("\x1b_"))
		if i < 0 {
			keep := 0
			if !final && len(w.buf) > 0 && w.buf[len(w.buf)-1] == 0x1b {
				keep = 1
			}
			if len(w.buf) > keep {
				if _, err := w.out.Write(w.buf[:len(w.buf)-keep]); err != nil {
					return err
				}
				w.buf = append(w.buf[:0], w.buf[len(w.buf)-keep:]...)
			}
			return nil
		}
		if i > 0 {
			if _, err := w.out.Write(w.buf[:i]); err != nil {
				return err
			}
			w.buf = append(w.buf[:0], w.buf[i:]...)
			continue
		}
		end := bytes.Index(w.buf[2:], []byte("\x1b\\"))
		if end < 0 {
			if final {
				w.buf = w.buf[:0]
				return nil
			}
			if len(w.buf) > maxAPC {
				// No end within a message's length: pass the escape on and
				// stop waiting for one, so that a broken sequence cannot
				// hold the stream back or grow without bound.
				if _, err := w.out.Write(w.buf[:1]); err != nil {
					return err
				}
				w.buf = append(w.buf[:0], w.buf[1:]...)
				continue
			}
			return nil
		}
		// The sequence has its own copy: dropping it from the buffer
		// rewrites the buffer's array in place.
		seq := append([]byte(nil), w.buf[:2+end+2]...)
		w.buf = append(w.buf[:0], w.buf[2+end+2:]...)
		if w.mine(seq) {
			continue
		}
		if _, err := w.out.Write(seq); err != nil {
			return err
		}
	}
}

// mine tells whether the APC sequence is one of the stream's own.
func (w *apcWriter) mine(seq []byte) bool {
	payload := seq[2 : len(seq)-2]
	rest, ok := bytes.CutPrefix(payload, []byte("rex1;"))
	if !ok {
		return false
	}
	nonce, _, ok := bytes.Cut(rest, []byte(";"))
	return ok && string(nonce) == w.nonce
}
