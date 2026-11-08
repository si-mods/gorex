//go:build darwin || linux || windows

package rex

import (
	"bytes"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const (
	// maxGraphicsImage is the largest image file a session reads and
	// rewrites. A larger one reaches the window as the program sent it,
	// which refuses the file medium. Its base64 form must fit one client
	// message (maxQueuedBytes).
	maxGraphicsImage = 4 << 20
	// maxGraphicsRewrite bounds what one read's images add to the output:
	// a program naming many files must not multiply what it printed.
	maxGraphicsRewrite = 6 << 20
	// maxGraphicsHistory bounds what a session keeps to replay on attach.
	maxGraphicsHistory = 8 << 20
	// graphicsChunk is the file bytes of one in-band chunk: base64 keeps
	// it within Kitty's 4096-byte payload.
	graphicsChunk = 3072
)

var (
	apcGraphicsStart = []byte("\x1b_G")
	apcEnd           = []byte("\x1b\\")
	pngHeader        = []byte("\x89PNG\r\n\x1a\n")
)

// graphics rewrites file-medium Kitty images to in-band ones, reading the
// file where the program runs, and keeps the sequences to replay when a
// window attaches.
type graphics struct {
	pending    []byte
	chunks     [][]byte
	chunkBytes int
	chunkSkip  bool
	// chunkHead is what the first chunk of the group in chunks does.
	chunkHead graphicsSeq
	history   []graphicsSeq
	kept      int
	// cut tells that a transfer may have started before the session read
	// its output, as across an upgrade, until the next sequence.
	cut bool
}

// graphicsSeq is a graphics sequence to replay, with the image it names by
// id, 0 for none, and what it does to that image. One without its bytes,
// as a transmit too large to keep, does only that.
type graphicsSeq struct {
	seq []byte
	id  uint32
	op  graphicsOp
}

// graphicsOp is what a sequence does to the image it names by id.
type graphicsOp uint8

const (
	graphicsOther graphicsOp = iota
	// graphicsTransmit replaces the image and its placements, as a
	// terminal does when an id is sent again.
	graphicsTransmit
	// graphicsFree deletes all the image's placements, and the image (d=I
	// with no placement).
	graphicsFree
)

// filter returns p with its file-medium Kitty images rewritten, and the
// complete graphics sequences to record for replay. cwd resolves relative
// paths. It runs outside the session's lock: opening a file must not hold
// up the session.
func (g *graphics) filter(p []byte, cwd string) ([]byte, []graphicsSeq) {
	if len(g.pending) > 0 {
		p = append(g.pending, p...)
		g.pending = nil
	}
	if !bytes.Contains(p, apcGraphicsStart) {
		hold := graphicsPrefix(p)
		if hold > 0 {
			g.pending = append(g.pending, p[len(p)-hold:]...)
			p = p[:len(p)-hold]
		}
		return p, nil
	}
	var out []byte
	var records []graphicsSeq
	for {
		i := bytes.Index(p, apcGraphicsStart)
		if i < 0 {
			hold := graphicsPrefix(p)
			if hold > 0 {
				g.pending = append(g.pending, p[len(p)-hold:]...)
				p = p[:len(p)-hold]
			}
			out = append(out, p...)
			break
		}
		out = append(out, p[:i]...)
		rest := p[i:]
		j := bytes.Index(rest, apcEnd)
		if j < 0 {
			if len(rest) > maxGraphicsImage+64<<10 {
				// Too large to hold and rewrite: let it through unchanged.
				out = append(out, rest...)
			} else {
				g.pending = append(g.pending, rest...)
			}
			break
		}
		seq := rest[:j+len(apcEnd)]
		if len(out) >= maxGraphicsRewrite {
			out = append(out, seq...)
			records = append(records, g.skipChunks(seq)...)
		} else {
			part, recs := g.sequence(seq, cwd)
			if len(out)+len(part) > maxGraphicsRewrite {
				out = append(out, seq...)
				records = append(records, g.skipChunks(seq)...)
			} else {
				out = append(out, part...)
				records = append(records, recs...)
			}
		}
		p = rest[j+len(apcEnd):]
	}
	return out, records
}

// graphicsPrefix returns how many bytes at the end of p start an APC
// graphics sequence that may continue in the next read.
func graphicsPrefix(p []byte) int {
	if len(p) > 0 && p[len(p)-1] == 0x1b {
		return 1
	}
	if len(p) >= 2 && p[len(p)-2] == 0x1b && p[len(p)-1] == '_' {
		return 2
	}
	return 0
}

// graphicsParams returns the keys of a Kitty sequence's header and its
// payload; ok is false without one.
func graphicsParams(seq []byte) (params []string, payload string, ok bool) {
	body := bytes.TrimSuffix(bytes.TrimPrefix(seq, apcGraphicsStart), apcEnd)
	header, payload, ok := strings.Cut(string(body), ";")
	return strings.Split(header, ","), payload, ok
}

// graphicsHead returns what a sequence of these header keys and payload
// does to the image it names by id.
func graphicsHead(params []string, payload string) graphicsSeq {
	var h graphicsSeq
	action, del, placement := "t", "a", uint64(0)
	for _, p := range params {
		key, value, _ := strings.Cut(p, "=")
		switch key {
		case "a":
			action = value
		case "d":
			del = value
		case "i":
			id, _ := strconv.ParseUint(value, 10, 32)
			h.id = uint32(id)
		case "p":
			placement, _ = strconv.ParseUint(value, 10, 32)
		}
	}
	switch {
	case h.id == 0:
	case (action == "t" || action == "T") && (payload != "" || graphicsMore(params)):
		h.op = graphicsTransmit
	case action == "d" && del == "I" && placement == 0:
		h.op = graphicsFree
	}
	return h
}

// sequence returns the Kitty sequence seq, rewritten when it transmits a
// file, and the sequences to record for replay.
func (g *graphics) sequence(seq []byte, cwd string) ([]byte, []graphicsSeq) {
	params, payload, ok := graphicsParams(seq)
	head := graphicsHead(params, payload)
	head.seq = seq
	if !ok {
		return seq, []graphicsSeq{head}
	}
	isFile := false
	for _, p := range params {
		if strings.HasPrefix(p, "t=f") {
			isFile = true
			break
		}
	}
	if g.chunkSkip {
		if !graphicsMore(params) {
			g.chunkSkip, g.chunks, g.chunkBytes = false, nil, 0
		}
		return seq, nil
	}
	if g.cut {
		g.cut = false
		if graphicsContinues(params) {
			// The rest of a transfer whose start the session did not read:
			// no image to replay.
			g.chunkSkip = graphicsMore(params)
			return seq, nil
		}
	}
	if !isFile || payload == "" {
		if graphicsMore(params) {
			if len(g.chunks) == 0 {
				g.chunkHead = head
			}
			if g.chunkBytes+len(seq) > maxGraphicsHistory {
				// Too large to keep: it replaces the image all the same.
				g.chunks, g.chunkBytes, g.chunkSkip = nil, 0, true
				return seq, []graphicsSeq{{id: g.chunkHead.id, op: g.chunkHead.op}}
			}
			g.chunks = append(g.chunks, seq)
			g.chunkBytes += len(seq)
			return seq, nil
		}
		if len(g.chunks) > 0 {
			head = g.chunkHead
			head.seq = bytes.Join(append(g.chunks, seq), nil)
			g.chunks, g.chunkBytes = nil, 0
		}
		return seq, []graphicsSeq{head}
	}
	data, ok := readImageFile(payload, cwd)
	if !ok {
		return seq, nil
	}
	head.seq = bytes.Join(rewriteFileImage(params, data), nil)
	return head.seq, []graphicsSeq{head}
}

// readImageFile reads the file payload, resolved against cwd, at most
// maxGraphicsImage bytes.
func readImageFile(payload, cwd string) ([]byte, bool) {
	path := strings.TrimSpace(payload)
	if strings.HasPrefix(path, "file://") {
		path = strings.TrimPrefix(path, "file://")
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	f, err := openImageFile(filepath.Clean(path))
	if err != nil {
		return nil, false
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(f, maxGraphicsImage+1))
	if err != nil || len(data) == 0 || len(data) > maxGraphicsImage {
		return nil, false
	}
	return data, true
}

// rewriteFileImage turns a file-medium image into in-band chunks, keeping
// its placement parameters. A PNG keeps its format; without one, the
// image is sent as a PNG only when its bytes are.
// skipChunks drops the chunk group seq continues, when the rewrite budget
// keeps it from being parsed, and returns what seq, or the group it
// belongs to, does to an image, which the terminal does all the same.
func (g *graphics) skipChunks(seq []byte) []graphicsSeq {
	params, payload, ok := graphicsParams(seq)
	head := graphicsHead(params, payload)
	if len(g.chunks) > 0 || g.chunkSkip {
		head = g.chunkHead
	}
	head.seq = nil
	if ok {
		g.chunks, g.chunkBytes, g.chunkSkip = nil, 0, graphicsMore(params)
	}
	if head.op == graphicsOther {
		return nil
	}
	return []graphicsSeq{head}
}

// graphicsMore reports whether params say more chunks follow.
func graphicsMore(params []string) bool {
	for _, p := range params {
		if p == "m=1" || strings.HasPrefix(p, "m=1,") {
			return true
		}
	}
	return false
}

// graphicsContinues reports whether params are those of a chunk after a
// transfer's first, which has the m and q keys alone.
func graphicsContinues(params []string) bool {
	for _, p := range params {
		if key, _, _ := strings.Cut(p, "="); key != "m" && key != "q" {
			return false
		}
	}
	return true
}

func rewriteFileImage(params []string, data []byte) [][]byte {
	kept := make([]string, 0, len(params)+2)
	hasFormat := false
	for _, p := range params {
		key, _, _ := strings.Cut(p, "=")
		switch key {
		case "t", "m":
		case "f":
			hasFormat = true
			kept = append(kept, p)
		default:
			kept = append(kept, p)
		}
	}
	if !hasFormat && bytes.HasPrefix(data, pngHeader) {
		kept = append(kept, "f=100")
	}
	header := strings.Join(kept, ",")
	if header != "" {
		header += ","
	}
	header += "t=d"
	var out [][]byte
	for i := 0; i < len(data); i += graphicsChunk {
		end := min(i+graphicsChunk, len(data))
		last := end == len(data)
		more := "m=1"
		if last {
			more = "m=0"
		}
		var b bytes.Buffer
		b.Write(apcGraphicsStart)
		if i == 0 {
			b.WriteString(header)
			b.WriteByte(',')
		}
		b.WriteString(more)
		b.WriteByte(';')
		b.WriteString(base64.StdEncoding.EncodeToString(data[i:end]))
		b.Write(apcEnd)
		out = append(out, b.Bytes())
	}
	return out
}

// record adds sequences for replay, keeping the most recent
// maxGraphicsHistory bytes. It keeps the images as they are, not each
// sequence sent: a transmit by id replaces the image and its placements,
// as in the terminal, so what the history had of that id goes, even when
// the transmit is too large to keep; a delete that frees an image by its
// id (d=I, all its placements) takes it out and, when it did, is not
// kept. The session holds its lock.
func (g *graphics) record(seqs []graphicsSeq) {
	for _, r := range seqs {
		if r.op != graphicsOther {
			n := len(g.history)
			g.history = slices.DeleteFunc(g.history, func(h graphicsSeq) bool {
				if h.id == r.id {
					g.kept -= len(h.seq)
					return true
				}
				return false
			})
			if r.op == graphicsFree && len(g.history) < n {
				continue
			}
		}
		if r.seq == nil || len(r.seq) > maxGraphicsHistory {
			continue
		}
		r.seq = append([]byte(nil), r.seq...)
		g.history = append(g.history, r)
		g.kept += len(r.seq)
		for g.kept > maxGraphicsHistory && len(g.history) > 0 {
			g.kept -= len(g.history[0].seq)
			g.history = g.history[1:]
		}
	}
}

// restore records the sequences a handoff has, as sequences returns them.
func (g *graphics) restore(seqs [][]byte) {
	for _, seq := range seqs {
		params, payload, _ := graphicsParams(seq)
		head := graphicsHead(params, payload)
		head.seq = seq
		g.record([]graphicsSeq{head})
	}
}

// sequences returns the sequences replay sends, one by one.
func (g *graphics) sequences() [][]byte {
	out := make([][]byte, len(g.history))
	for i, h := range g.history {
		out[i] = h.seq
	}
	return out
}

// replay returns the graphics sequences an attaching window needs to show
// the images the session showed.
func (g *graphics) replay() []byte {
	var b bytes.Buffer
	for _, h := range g.history {
		b.Write(h.seq)
	}
	return b.Bytes()
}
