package rexapi

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/coder/websocket"
	"gorex/internal/rex"
)

// A download is a file of a block's machine a web page reads, as the
// desktop downloads a path printed in a pane of a host
// (docs/web-open-files.md). It is GoRex's own, outside Rex's API, and
// takes two steps. com.superlogical.terminal.download_begin, on the
// control connection, opens the file with file.get for the block's
// session, so that it fails early with a clear error, and answers a
// ticket. GET /api/download?ticket=… then streams that file once. A
// browser sends its page's origin on a WebSocket even from the same
// origin, so only a page the gateway trusts gets a ticket: a page that
// rebinds a name of its own to the gateway's address has none to use.
// The welcome lists featureDownload when the server sends files.

// featureDownload is the welcome's feature of a gateway that serves
// downloads.
const featureDownload = "gorex.download"

// The limits of downloads.
const (
	// maxDownloadsPerControl is how many tickets one control connection
	// may hold open at once.
	maxDownloadsPerControl = 4
	downloadWriteWait      = 30 * time.Second
)

var (
	// maxDownloadFileBytes is the most bytes one downloaded file may
	// hold, the upload's limit; tests lower it.
	maxDownloadFileBytes int64 = maxUploadFileBytes
	// downloadWait is how long a ticket lasts unused; tests shorten it.
	downloadWait = 60 * time.Second
)

// download is a file download_begin opened, waiting for its GET.
type download struct {
	conn  *control // the control connection that asked for it
	rc    io.ReadCloser
	block string
	path  string
	name  string
	size  int64
	mtime time.Time
	timer *time.Timer
}

// downloadResult is the legacy GoRex download_begin's result (pre-rev 13).
type downloadResult struct {
	Ticket string `json:"ticket"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	MTime  string `json:"mtime,omitempty"`
	// Type is a guess from the name's extension, for the page to choose
	// between its viewer and a download; it is not to be trusted.
	Type string `json:"type"`
}

// bytesDownloadResult is Rex API revision 13's download_begin result.
type bytesDownloadResult struct {
	Ticket      string `json:"ticket,omitempty"`
	ExpiresInMS int    `json:"expires_in_ms,omitempty"`
	NotModified bool   `json:"not_modified,omitempty"`
	Path        string `json:"path"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	ModifiedAt  string `json:"modified_at"`
	Version     string `json:"version,omitempty"`
}

var downloadRefPattern = regexp.MustCompile(`^download:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// blockDownloadBegin opens a file of the block's machine for a download
// and answers its ticket.
func blockDownloadBegin(c *control, s *mSession, b *mBlock, a args) (any, *apiError) {
	res, aerr := beginDownload(c, s, b, a)
	if aerr != nil {
		p, _, _ := a.str("path")
		logf("download refused %s %s %q: %s", c.remote, b.id, p, aerr.Code)
	}
	return res, aerr
}

func beginDownload(c *control, s *mSession, b *mBlock, a args) (any, *apiError) {
	g := c.g
	if !g.hello.Can(rex.CapabilityFileTransfer) {
		return nil, errNotSupported("the GoRex server sends no files; update GoRex there")
	}
	ref, hasRef, aerr := a.str("ref")
	if aerr != nil {
		return nil, aerr
	}
	ifNoneMatch, hasIfNoneMatch, aerr := a.str("if_none_match")
	if aerr != nil {
		return nil, aerr
	}
	official := hasRef || hasIfNoneMatch
	if official && c.rev < 13 {
		return nil, errBadRequest("the method takes no ref")
	}
	for name := range a {
		switch name {
		case "path":
		case "ref", "if_none_match":
			if !official {
				return nil, errBadRequest("the method takes no %s", name)
			}
		default:
			return nil, errBadRequest("the method takes no %s", name)
		}
	}
	if official {
		if !hasRef || !downloadRefPattern.MatchString(ref) {
			return nil, errBadRequest("ref is download: followed by a UUID in lower-case canonical form")
		}
	}
	p, aerr := a.reqStr("path")
	if aerr != nil {
		return nil, aerr
	}
	if p == "" {
		return nil, errBadRequest("path is required")
	}
	if _, aerr := g.blockInfo(b); aerr != nil {
		return nil, aerr
	}
	if g.openDownloads(c) >= maxDownloadsPerControl {
		return nil, errConflict("the connection has %d downloads waiting, the most it may", maxDownloadsPerControl)
	}
	rc, ans, err := g.opt.Client.GetFile(context.Background(), b.sid, p)
	if err != nil {
		return nil, downloadError(err)
	}
	if ans.Size > maxDownloadFileBytes {
		rc.Close()
		return nil, errTooLarge("the file is %d bytes, more than the %d a download may hold", ans.Size, maxDownloadFileBytes)
	}
	if official {
		absPath := ans.Path
		if absPath == "" {
			absPath = p
		}
		modAt := time.Now().UTC().Format(time.RFC3339)
		if !ans.MTime.IsZero() {
			modAt = ans.MTime.UTC().Format(time.RFC3339)
		}
		var version string
		if !ans.MTime.IsZero() && time.Since(ans.MTime) >= time.Second {
			version = fmt.Sprintf("%d:%d", ans.Size, ans.MTime.UnixNano())
		}
		if ifNoneMatch != "" && version != "" && ifNoneMatch == version {
			rc.Close()
			return bytesDownloadResult{
				NotModified: true,
				Path:        absPath,
				Name:        ans.Name,
				Size:        ans.Size,
				ModifiedAt:  modAt,
				Version:     version,
			}, nil
		}
		d := &download{conn: c, rc: rc, block: b.id, path: absPath, name: ans.Name, size: ans.Size, mtime: ans.MTime}
		if aerr := g.addByteDownload(ref, d); aerr != nil {
			rc.Close()
			return nil, aerr
		}
		tID := g.issueTicket(&ticket{
			record:      c.rec.id,
			session:     s.id,
			block:       b.id,
			sid:         b.sid,
			dataRef:     ref,
			subprotocol: bytesProtocol,
			expires:     time.Now().Add(ticketWait),
		})
		return bytesDownloadResult{
			Ticket:      tID,
			ExpiresInMS: int(ticketWait / time.Millisecond),
			Path:        absPath,
			Name:        ans.Name,
			Size:        ans.Size,
			ModifiedAt:  modAt,
			Version:     version,
		}, nil
	}
	d := &download{conn: c, rc: rc, block: b.id, path: p, name: ans.Name, size: ans.Size, mtime: ans.MTime}
	id, aerr := g.addDownload(d)
	if aerr != nil {
		rc.Close()
		return nil, aerr
	}
	res := downloadResult{Ticket: id, Name: ans.Name, Size: ans.Size, Type: guessType(ans.Name)}
	if !ans.MTime.IsZero() {
		res.MTime = ans.MTime.UTC().Format(time.RFC3339)
	}
	return res, nil
}

// downloadError maps a get's error, which arrives as text, to a code. The
// server runs on the gateway's machine, so the system's texts are the
// same on both sides.
func downloadError(err error) *apiError {
	msg := strings.TrimPrefix(err.Error(), "rex: ")
	switch {
	case rex.Folder(err):
		return errorf("is_folder", 409, "%s", msg)
	case rex.NotRegular(err):
		return errorf("not_regular", 409, "%s", msg)
	case rex.LinksOutside(err):
		return errorf("denied", 403, "%s", msg)
	case endsWithAny(msg, noFileErrors):
		return errorf("no_file", 404, "%s", msg)
	case endsWithAny(msg, deniedErrors):
		return errorf("denied", 403, "%s", msg)
	case strings.HasPrefix(msg, "the server does not transfer files"):
		return errNotSupported("%s", msg)
	}
	return errInternal("%s", msg)
}

// endsWithAny reports whether msg ends with one of the errors' texts.
func endsWithAny(msg string, errs []error) bool {
	for _, e := range errs {
		if strings.HasSuffix(msg, e.Error()) {
			return true
		}
	}
	return false
}

// noFileErrors are the system's errors of a path that names no file.
var noFileErrors = append([]error{syscall.ENOENT, syscall.ENOTDIR}, extraNoFileErrors...)

// openDownloads counts the tickets a control connection holds.
func (g *Gateway) openDownloads(c *control) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, d := range g.downloads {
		if d.conn == c {
			n++
		}
	}
	for _, d := range g.byteDownloads {
		if d.conn == c {
			n++
		}
	}
	return n
}

// addByteDownload records d under ref until its rex.data.bytes.v1 stream
// takes it or ticketWait elapses.
func (g *Gateway) addByteDownload(ref string, d *download) *apiError {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closing {
		return errInternal("the gateway is closing")
	}
	if _, exists := g.byteDownloads[ref]; exists {
		return errConflict("download %s is already reserved", ref)
	}
	n := 0
	for _, old := range g.downloads {
		if old.conn == d.conn {
			n++
		}
	}
	for _, old := range g.byteDownloads {
		if old.conn == d.conn {
			n++
		}
	}
	if n >= maxDownloadsPerControl {
		return errConflict("the connection has %d downloads waiting, the most it may", maxDownloadsPerControl)
	}
	g.byteDownloads[ref] = d
	d.timer = time.AfterFunc(ticketWait, func() {
		if d := g.takeByteDownload(ref); d != nil {
			d.rc.Close()
		}
	})
	return nil
}

func (g *Gateway) takeByteDownload(ref string) *download {
	if ref == "" {
		return nil
	}
	g.mu.Lock()
	d := g.byteDownloads[ref]
	delete(g.byteDownloads, ref)
	g.mu.Unlock()
	if d != nil {
		d.timer.Stop()
	}
	return d
}

// startDownloadStream serves a rev 13 download_begin reservation over a
// rex.data.bytes.v1 WebSocket stream.
func (g *Gateway) startDownloadStream(ws *websocket.Conn, t *ticket) {
	d := g.takeByteDownload(t.dataRef)
	if d == nil {
		g.welcomeThenClose(ws, websocket.StatusPolicyViolation, "download expired")
		return
	}
	defer d.rc.Close()
	if !writeData(ws, welcomeBytes(randomBytes(16), nonzeroNonce())) {
		ws.CloseNow()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Drain/ignore any client messages until the connection closes.
	go func() {
		for {
			if _, _, err := ws.Read(ctx); err != nil {
				cancel()
				return
			}
		}
	}()
	buf := make([]byte, maxChunk)
	for {
		n, err := d.rc.Read(buf)
		if n > 0 {
			wctx, wcancel := context.WithTimeout(ctx, downloadWriteWait)
			werr := ws.Write(wctx, websocket.MessageBinary, buf[:n])
			wcancel()
			if werr != nil {
				ws.CloseNow()
				return
			}
		}
		if err == io.EOF {
			wctx, wcancel := context.WithTimeout(ctx, downloadWriteWait)
			_ = ws.Write(wctx, websocket.MessageBinary, []byte{})
			wcancel()
			ws.Close(websocket.StatusNormalClosure, "ok")
			return
		}
		if err != nil {
			ws.Close(websocket.StatusInternalError, "internal")
			return
		}
	}
}

// addDownload records d under a new ticket, which lapses after
// downloadWait unused, closing its get.
func (g *Gateway) addDownload(d *download) (string, *apiError) {
	id := base64url(randomBytes(16))
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closing {
		return "", errInternal("the gateway is closing")
	}
	n := 0
	for _, old := range g.downloads {
		if old.conn == d.conn {
			n++
		}
	}
	if n >= maxDownloadsPerControl {
		return "", errConflict("the connection has %d downloads waiting, the most it may", maxDownloadsPerControl)
	}
	g.downloads[id] = d
	d.timer = time.AfterFunc(downloadWait, func() {
		if d := g.takeDownload(id); d != nil {
			d.rc.Close()
		}
	})
	return id, nil
}

// takeDownload spends a ticket: the download it names, or nil when there
// is none (unknown, used or lapsed).
func (g *Gateway) takeDownload(id string) *download {
	if id == "" {
		return nil
	}
	g.mu.Lock()
	d := g.downloads[id]
	delete(g.downloads, id)
	g.mu.Unlock()
	if d != nil {
		d.timer.Stop()
	}
	return d
}

// dropDownloads lets go of the tickets of a control connection that
// ended, or of all of them when c is nil, closing their gets.
func (g *Gateway) dropDownloads(c *control) {
	g.mu.Lock()
	var dropped []*download
	for id, d := range g.downloads {
		if c == nil || d.conn == c {
			delete(g.downloads, id)
			dropped = append(dropped, d)
		}
	}
	for id, d := range g.byteDownloads {
		if c == nil || d.conn == c {
			delete(g.byteDownloads, id)
			dropped = append(dropped, d)
		}
	}
	g.mu.Unlock()
	for _, d := range dropped {
		d.timer.Stop()
		d.rc.Close()
	}
}

// handleDownload streams the file of a ticket download_begin gave.
func (g *Gateway) handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "a download is a GET")
		return
	}
	d := g.takeDownload(r.URL.Query().Get("ticket"))
	if d == nil {
		writeError(w, http.StatusNotFound, "no such download: a ticket is good for one download, within a minute")
		return
	}
	defer d.rc.Close()
	// A client that goes away ends the get.
	stop := context.AfterFunc(r.Context(), func() { d.rc.Close() })
	defer stop()
	h := w.Header()
	// Never served inline, so the file never runs as a page of the
	// gateway's origin.
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Length", strconv.FormatInt(d.size, 10))
	if !d.mtime.IsZero() {
		h.Set("Last-Modified", d.mtime.UTC().Format(http.TimeFormat))
	}
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Disposition", contentDisposition(d.name))
	if h.Get("Access-Control-Allow-Origin") != "" {
		// A trusted page (checkOrigin) reads the name and the length.
		h.Set("Access-Control-Expose-Headers", "Content-Disposition, Content-Length")
	}
	w.WriteHeader(http.StatusOK)
	n, _ := io.CopyN(w, d.rc, d.size)
	logf("download %s %s %q %d", r.RemoteAddr, d.block, d.path, n)
	if n < d.size {
		// The file ended short, or the get failed: abort the response,
		// so that the client never takes a part for the whole file.
		panic(http.ErrAbortHandler)
	}
}

// contentDisposition is an attachment's header for a file of name: an
// ASCII filename for old clients, and filename* with the name itself
// when it is not plain ASCII.
func contentDisposition(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r > unicode.MaxASCII || unicode.IsControl(r) || r == '"' || r == '\\' || r == '/' {
			return '_'
		}
		return r
	}, name)
	if ascii == "" {
		ascii = "download"
	}
	v := mime.FormatMediaType("attachment", map[string]string{"filename": ascii})
	if ascii != name {
		if ext := mime.FormatMediaType("attachment", map[string]string{"filename": name}); ext != "" {
			v += strings.TrimPrefix(ext, "attachment")
		}
	}
	return v
}

// knownTypes are the types guessed from an extension before the
// system's table, which differs between systems: the images the web
// client shows among them.
var knownTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".webp": "image/webp", ".avif": "image/avif", ".bmp": "image/bmp", ".ico": "image/x-icon",
	".svg": "image/svg+xml", ".pdf": "application/pdf", ".txt": "text/plain",
	".json": "application/json", ".html": "text/html", ".htm": "text/html",
}

// guessType guesses a file's type from its name's extension, without
// parameters; application/octet-stream when it can't tell.
func guessType(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if ext == "" {
		return "application/octet-stream"
	}
	if t, ok := knownTypes[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		if mt, _, err := mime.ParseMediaType(t); err == nil {
			return mt
		}
	}
	return "application/octet-stream"
}
