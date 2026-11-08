//go:build darwin || linux || windows

package rex

import (
	"bytes"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"slices"
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
// file where the program runs: a window, which may run elsewhere, cannot.
type graphics struct {
	pending []byte
}

// filter returns p with its file-medium Kitty images rewritten. cwd
// resolves relative paths. It runs outside the session's lock: opening a
// file must not hold up the session.
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
		if len(out) < maxGraphicsRewrite {
			if part := rewriteSequence(seq, cwd); len(out)+len(part) <= maxGraphicsRewrite {
				seq = part
			}
		}
		out = append(out, seq...)
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

// rewriteSequence returns the Kitty sequence seq, rewritten when it transmits a
// file.
func rewriteSequence(seq []byte, cwd string) []byte {
	body := seq[len(apcGraphicsStart) : len(seq)-len(apcEnd)]
	header, payload, ok := strings.Cut(string(body), ";")
	if !ok || payload == "" {
		return seq
	}
	params := strings.Split(header, ",")
	if !slices.ContainsFunc(params, func(p string) bool { return strings.HasPrefix(p, "t=f") }) {
		return seq
	}
	data, ok := readImageFile(payload, cwd)
	if !ok {
		return seq
	}
	return bytes.Join(rewriteFileImage(params, data), nil)
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
