//go:build darwin || linux || windows

package rex

import (
	"bytes"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	// maxGraphicsImage is the largest image file a session reads and
	// rewrites. A larger one reaches the window as the program sent it,
	// which refuses the file medium.
	maxGraphicsImage = 16 << 20
	// maxGraphicsHistory bounds what a session keeps to replay on attach.
	maxGraphicsHistory = 32 << 20
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
	pending []byte
	history [][]byte
	kept    int
}

// filter returns p with its file-medium Kitty images rewritten, and
// records the graphics sequences for replay. cwd resolves relative paths.
func (g *graphics) filter(p []byte, cwd string) []byte {
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
		return p
	}
	var out []byte
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
		out = append(out, g.sequence(seq, cwd)...)
		p = rest[j+len(apcEnd):]
	}
	return out
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

// sequence returns the Kitty sequence seq, rewritten when it transmits a
// file, and records it for replay.
func (g *graphics) sequence(seq []byte, cwd string) []byte {
	body := seq[len(apcGraphicsStart) : len(seq)-len(apcEnd)]
	header, payload, ok := strings.Cut(string(body), ";")
	if !ok {
		g.add(seq)
		return seq
	}
	params := strings.Split(header, ",")
	isFile := false
	for _, p := range params {
		if strings.HasPrefix(p, "t=f") {
			isFile = true
			break
		}
	}
	if !isFile || payload == "" {
		g.add(seq)
		return seq
	}
	data, ok := readImageFile(payload, cwd)
	if !ok {
		return seq
	}
	out := rewriteFileImage(params, data)
	g.addAll(out)
	return bytes.Join(out, nil)
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
	f, err := os.Open(filepath.Clean(path))
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

// add records seq for replay, keeping the most recent maxGraphicsHistory
// bytes.
func (g *graphics) add(seq []byte) {
	if len(seq) > maxGraphicsHistory {
		return
	}
	g.history = append(g.history, seq)
	g.kept += len(seq)
	for g.kept > maxGraphicsHistory && len(g.history) > 0 {
		g.kept -= len(g.history[0])
		g.history = g.history[1:]
	}
}

// addAll records one image's chunks as one entry, so that replay never
// starts in the middle of it.
func (g *graphics) addAll(parts [][]byte) {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	if n > maxGraphicsHistory {
		return
	}
	seq := make([]byte, 0, n)
	for _, p := range parts {
		seq = append(seq, p...)
	}
	g.add(seq)
}

// replay returns the graphics sequences an attaching window needs to show
// the images the session showed.
func (g *graphics) replay() []byte {
	var b bytes.Buffer
	for _, seq := range g.history {
		b.Write(seq)
	}
	return b.Bytes()
}
