package rexapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"gorex/internal/rex"
)

// An upload is a file a client sends into the GoRex server user's
// temporary folder, as the app sends a file dropped on a pane of a host
// (rex.FilePut with Temp), to paste its path into a block. It is GoRex's
// own, outside Rex's API: POST /api/upload?block_id=…&name=…, the file the
// body, of a known length, as application/octet-stream. The body streams
// to the server as it arrives, so neither a control connection nor memory
// holds the file. The answer is the file's path there, and the path as a
// word of the block's shell, which is what to paste. The welcome lists
// featureUpload when the gateway takes uploads.

// featureUpload is the welcome's feature of a gateway that takes uploads.
const featureUpload = "gorex.upload"

// The limits of a terminal upload, which com.superlogical.terminal
// .upload_begin reserves and a rex.data.bytes.v1 stream carries.
const (
	// uploadWait is how long a reservation lasts without its stream.
	uploadWait = 10 * time.Minute
	// maxUploadFileBytes is the most bytes one file may hold.
	maxUploadFileBytes = 1 << 30
	// maxUploadBatchBytes is the most bytes one batch's files may hold.
	maxUploadBatchBytes = 2 << 30
	// maxUploadBatchFiles is the most files one batch may hold.
	maxUploadBatchFiles = 16
	// maxUploadsPerBlock is how many reservations one block may hold at
	// once, over all its clients.
	maxUploadsPerBlock = 16
)

// uploadReservation is one file com.superlogical.terminal.upload_begin
// reserved. The client sends its bytes on a rex.data.bytes.v1 stream
// whose ticket names the reservation's ref, and when every file of its
// batch has arrived, the terminal types their paths.
type uploadReservation struct {
	ref    string // the call's ref: upload:<uuid>
	name   string
	size   int64
	record string // the client that reserved it
	sid    string // the block's GoRex session
	// session is the API session the call named, which detach is given.
	session string
	block   string // the block's ID
	batch   string // the batch's ID, "" when the file stands alone
	index   int
	expires time.Time
	// path is where the file is on the terminal's machine, once its
	// stream has carried all of it.
	path string
}

// uploadBatch is the group of files whose paths are typed together.
type uploadBatch struct {
	id      string
	count   int
	size    int64
	filled  map[int]string // index → ref, for the files already reserved
	arrived int            // how many files' streams have carried all their bytes
	expires time.Time
}

// features are the welcome's features: GoRex's own, which Rex's clients
// ignore, as they ignore every name they do not know.
func (g *Gateway) features() []string {
	features := []string{featureSpaces}
	if g.hello.Can(rex.CapabilityFileTemp) {
		features = append(features, featureUpload)
	}
	return features
}

// uploadResult is an upload's answer.
type uploadResult struct {
	// Path is where the file is, absolute.
	Path string `json:"path"`
	// Word is Path as a word of the block's shell, "" when the shell
	// takes no such path; Reason then says why it is not to be pasted.
	Word   string `json:"word"`
	Reason string `json:"reason,omitempty"`
}

// handleUpload takes an upload into the temporary folder.
func (g *Gateway) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "an upload is a POST")
		return
	}
	// A browser asks before it posts a type no form sends, and this
	// server allows no other origin: a web page cannot post files here.
	if r.Header.Get("Content-Type") != "application/octet-stream" {
		writeError(w, http.StatusUnsupportedMediaType, "an upload is application/octet-stream")
		return
	}
	if !g.hello.Can(rex.CapabilityFileTemp) {
		writeError(w, http.StatusBadRequest, "the GoRex server takes no files into its temporary folder")
		return
	}
	if r.ContentLength < 0 {
		writeError(w, http.StatusLengthRequired, "an upload has a Content-Length")
		return
	}
	q := r.URL.Query()
	name := q.Get("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	m, err := g.current()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("reading the layout: %v", err))
		return
	}
	b := m.blockByID(q.Get("block_id"))
	if b == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no block %s", q.Get("block_id")))
		return
	}
	info, aerr := g.blockInfo(b)
	if aerr != nil {
		writeError(w, aerr.Status, aerr.Message)
		return
	}
	// The file goes by a name the block's shell takes, whose path pastes.
	// A client that goes away cancels the put, which removes what the
	// server has of the file.
	put := rex.FilePut{SID: b.sid, Name: name, Size: r.ContentLength, ID: fmt.Sprintf("%x", randomBytes(8)), Conflict: "rename", Temp: true}
	path, word, err := g.opt.Client.PutForShell(r.Context(), info.Shell, put, &streamAt{r: r.Body}, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, strings.TrimPrefix(err.Error(), "rex: "))
		return
	}
	res := uploadResult{Path: path, Word: word}
	if word == "" {
		res.Reason = fmt.Sprintf("%s is uploaded, but the shell can't take this path", path)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// streamAt reads a stream as an io.ReaderAt read from the start, in order,
// which is how a put reads a file that the server has none of yet.
type streamAt struct {
	r   io.Reader
	off int64
}

func (s *streamAt) ReadAt(p []byte, off int64) (int, error) {
	if off != s.off {
		return 0, errors.New("an upload is read in order")
	}
	// A short read is an upload that ended before its size, which the
	// put fails on, rather than a file the server writes short.
	n, err := io.ReadFull(s.r, p)
	s.off += int64(n)
	return n, err
}

// reserveUpload records a reservation, forgetting the stale ones first,
// and joins its batch when it has one.
func (g *Gateway) reserveUpload(u *uploadReservation, count int, now time.Time) *apiError {
	g.mu.Lock()
	defer g.mu.Unlock()
	for r, old := range g.uploads {
		if now.After(old.expires) {
			delete(g.uploads, r)
		}
	}
	for id, old := range g.uploadBatches {
		if now.After(old.expires) {
			delete(g.uploadBatches, id)
		}
	}
	for id, until := range g.abortedBatches {
		if now.After(until) {
			delete(g.abortedBatches, id)
		}
	}
	if u.batch != "" {
		if _, aborted := g.abortedBatches[u.batch]; aborted {
			return errConflict("batch %q was aborted, so start a new batch with a new ID", u.batch)
		}
	}
	live := 0
	for _, old := range g.uploads {
		if old.block == u.block {
			live++
		}
	}
	if live >= maxUploadsPerBlock {
		return errConflict("the block has %d uploads reserved, more than the %d it allows at once", live, maxUploadsPerBlock)
	}
	if g.uploads[u.ref] != nil {
		return errConflict("upload %s is already reserved", u.ref)
	}
	var batchState *uploadBatch
	if u.batch != "" {
		batchState = g.uploadBatches[u.batch]
		if batchState == nil {
			batchState = &uploadBatch{id: u.batch, count: count, filled: map[int]string{}, expires: now.Add(uploadWait)}
			g.uploadBatches[u.batch] = batchState
		}
		if batchState.count != count {
			return errBadRequest("batch %q counts %d files, not %d", u.batch, batchState.count, count)
		}
		if _, taken := batchState.filled[u.index]; taken {
			return errConflict("file %d of batch %q is already reserved", u.index, u.batch)
		}
		if batchState.size+u.size > maxUploadBatchBytes {
			return errTooLarge("the batch would hold %d bytes, more than the %d a batch may hold", batchState.size+u.size, maxUploadBatchBytes)
		}
	}
	g.uploads[u.ref] = u
	u.expires = now.Add(uploadWait)
	if batchState != nil {
		batchState.filled[u.index] = u.ref
		batchState.size += u.size
		batchState.expires = now.Add(uploadWait)
	}
	return nil
}

// uploadRefPattern is the shape of a ref: upload: followed by a UUID in
// lower-case canonical form.
var uploadRefPattern = regexp.MustCompile(`^upload:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// blockUploadBegin reserves a file to upload to the terminal's machine.
// The client opens a rex.data.bytes.v1 stream with the ticket of the
// result, sends the file's bytes, and half-closes; when every file of
// the batch has arrived, the terminal types their paths.
func blockUploadBegin(c *control, s *mSession, b *mBlock, a args) (any, *apiError) {
	if !c.g.hello.Can(rex.CapabilityFileTemp) {
		return nil, errNotSupported("this terminal does not accept uploads")
	}
	for name := range a {
		switch name {
		case "ref", "name", "size", "batch", "index", "count":
		default:
			return nil, errBadRequest("the method takes no %s", name)
		}
	}
	ref, aerr := a.reqStr("ref")
	if aerr != nil {
		return nil, aerr
	}
	if !uploadRefPattern.MatchString(ref) {
		return nil, errBadRequest("ref is upload: followed by a UUID in lower-case canonical form")
	}
	name, aerr := a.reqStr("name")
	if aerr != nil {
		return nil, aerr
	}
	// The name the server's put refuses is refused here, before any byte
	// is sent.
	if rex.CheckName(name) != nil {
		return nil, errBadRequest("upload name %q is not a file name", name)
	}
	size, ok, aerr := a.int("size")
	if aerr != nil {
		return nil, aerr
	}
	if !ok {
		return nil, errBadRequest("size is required")
	}
	if size < 0 {
		return nil, errBadRequest("size counts bytes from zero")
	}
	if int64(size) > maxUploadFileBytes {
		return nil, errTooLarge("the file is %d bytes, more than the %d a file may hold", size, maxUploadFileBytes)
	}
	batch, hasBatch, aerr := a.str("batch")
	if aerr != nil {
		return nil, aerr
	}
	index, hasIndex, aerr := a.int("index")
	if aerr != nil {
		return nil, aerr
	}
	count, hasCount, aerr := a.int("count")
	if aerr != nil {
		return nil, aerr
	}
	if (hasIndex || hasCount) && !hasBatch {
		return nil, errBadRequest("index and count need a batch")
	}
	if hasBatch {
		if !hasCount {
			return nil, errBadRequest("count is required with batch")
		}
		if count < 1 || count > maxUploadBatchFiles {
			return nil, errBadRequest("invalid batch size %d, maximum is %d", count, maxUploadBatchFiles)
		}
		if !hasIndex {
			return nil, errBadRequest("index is required with batch")
		}
		if index < 0 || index >= count {
			return nil, errBadRequest("index %d is not within a batch of %d files", index, count)
		}
	}
	g := c.g
	now := time.Now()
	if aerr := g.reserveUpload(&uploadReservation{
		ref: ref, name: name, size: int64(size),
		record: c.rec.id, sid: b.sid, session: s.id, block: b.id,
		batch: batch, index: index,
	}, count, now); aerr != nil {
		return nil, aerr
	}
	id := g.issueTicket(&ticket{
		record:      c.rec.id,
		session:     s.id,
		block:       b.id,
		sid:         b.sid,
		dataRef:     ref,
		subprotocol: bytesProtocol,
		expires:     now.Add(ticketWait),
	})
	return connectResult{Ticket: id, ExpiresInMS: int(ticketWait / time.Millisecond)}, nil
}

// uploadErrors carries the bytes a stream received, shared by its read
// loop and its put.
type uploadCount struct {
	mu  sync.Mutex
	n   int64
	err error
}

func (c *uploadCount) add(n int) {
	c.mu.Lock()
	c.n += int64(n)
	c.mu.Unlock()
}

func (c *uploadCount) received() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// startUploadStream takes the rex.data.bytes.v1 connection of an
// upload_begin reservation and carries the file's bytes.
func (g *Gateway) startUploadStream(ws *websocket.Conn, t *ticket) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g.mu.Lock()
	u := g.uploads[t.dataRef]
	g.mu.Unlock()
	if u == nil || time.Now().After(u.expires) {
		g.welcomeThenClose(ws, websocket.StatusPolicyViolation, "upload expired")
		return
	}
	// The stream starts with a WELCOME, which the handshake owes the
	// client, then the connection carries the file's bytes.
	if !writeData(ws, welcomeBytes(randomBytes(16), nonzeroNonce())) {
		ws.CloseNow()
		return
	}
	count := &uploadCount{}
	pr, pw := io.Pipe()
	putDone := make(chan error, 1)
	go func() {
		// The file goes by a name the block's shell takes, whose path
		// pastes, as the private upload's does.
		info, aerr := g.blockInfo(&mBlock{id: u.block, sid: u.sid})
		if aerr != nil {
			putDone <- errors.New(aerr.Message)
			return
		}
		put := rex.FilePut{SID: u.sid, Name: u.name, Size: u.size, ID: fmt.Sprintf("%x", randomBytes(8)), Conflict: "rename", Temp: true}
		path, _, err := g.opt.Client.PutForShell(ctx, info.Shell, put, &streamAt{r: pr}, nil)
		if err == nil {
			g.mu.Lock()
			u.path = path
			g.mu.Unlock()
		}
		putDone <- err
	}()
	done := make(chan struct{})
	defer close(done)
	go g.pingUpload(ws, done)
	over := false
	for {
		readCtx, readCancel := context.WithTimeout(ctx, uploadIdle)
		typ, msg, err := ws.Read(readCtx)
		readCancel()
		if err != nil {
			// The connection ended without the file's end. Cancel the
			// put before the pipe ends it, so its abort removes what
			// the server has of the file.
			cancel()
			break
		}
		if typ != websocket.MessageBinary {
			cancel()
			g.failUpload(ws, pw, u, websocket.StatusPolicyViolation, "an upload stream carries binary messages")
			return
		}
		// An empty message half-closes the stream: the file is whole.
		if len(msg) == 0 {
			break
		}
		if count.received()+int64(len(msg)) > u.size {
			over = true
			break
		}
		count.add(len(msg))
		if _, err := pw.Write(msg); err != nil {
			// The put already ended, and failed: make sure its abort
			// removes the partial.
			cancel()
			break
		}
	}
	if over {
		cancel()
		g.failUpload(ws, pw, u, websocket.StatusPolicyViolation,
			fmt.Sprintf("upload sent %d bytes, more than its %d", count.received(), u.size))
		return
	}
	// A half-close that arrives before the file is whole is a failed
	// upload, whose partial the put aborts.
	if count.received() != u.size {
		cancel()
	}
	pw.Close()
	if err := <-putDone; err != nil {
		g.dropUpload(u, ws, websocket.StatusInternalError, fmt.Sprintf("the upload failed: %v", err))
		return
	}
	if n := count.received(); n != u.size {
		g.dropUpload(u, ws, websocket.StatusPolicyViolation, fmt.Sprintf("upload sent %d of its %d bytes", n, u.size))
		return
	}
	// The file is on the terminal's machine. Type the paths of the
	// batch, which is the file alone when it has no batch, once every
	// file has arrived. A member of a batch stays reserved until the
	// batch completes, so that the last stream can name every path.
	g.mu.Lock()
	var paths []string
	if u.batch == "" {
		delete(g.uploads, u.ref)
		paths = []string{u.path}
	} else {
		batch := g.uploadBatches[u.batch]
		if batch != nil {
			batch.arrived++
			if batch.arrived == batch.count {
				delete(g.uploadBatches, u.batch)
				paths = make([]string, 0, batch.count)
				for i := range batch.count {
					ref := batch.filled[i]
					if other := g.uploads[ref]; other != nil {
						paths = append(paths, other.path)
					}
				}
				for i := range batch.count {
					delete(g.uploads, batch.filled[i])
				}
			}
		}
	}
	g.mu.Unlock()
	if len(paths) == 0 {
		// Some file of the batch has not arrived; its own stream types
		// the batch when it is the last one.
		ws.Close(websocket.StatusNormalClosure, "")
		return
	}
	if aerr := g.typeUploadPaths(u.sid, paths); aerr != nil {
		ws.Close(websocket.StatusInternalError, aerr.Message)
		return
	}
	ws.Close(websocket.StatusNormalClosure, "")
}

// uploadIdle is how long an upload stream may go without a message.
const uploadIdle = 60 * time.Second

// pingUpload pings an upload stream's connection until it ends, as the
// terminal's other data connections are pinged.
func (g *Gateway) pingUpload(ws *websocket.Conn, done chan struct{}) {
	ticker := time.NewTicker(keepaliveEvery)
	defer ticker.Stop()
	misses := 0
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), keepaliveWait)
		err := ws.Ping(ctx)
		late := errors.Is(ctx.Err(), context.DeadlineExceeded)
		cancel()
		switch {
		case err == nil:
			misses = 0
		case late:
			misses++
			if misses >= 3 {
				ws.Close(1001, "keepalive timeout")
				return
			}
		default:
			return
		}
	}
}

// typeUploadPaths types a batch's paths into the block's shell, as if
// the user had pasted them. A path the shell's quoting refuses is never
// pasted; when no path pastes, the call fails.
func (g *Gateway) typeUploadPaths(sid string, paths []string) *apiError {
	info, err := g.sessionInfo(sid)
	if err != nil {
		return errNotFound("block %s has no session", sid)
	}
	var words []string
	for _, p := range paths {
		if w, ok := rex.PathWord(info.Shell, p); ok {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		return errInternal("no path pastes in this shell")
	}
	if err := g.opt.Client.Input(sid, []byte(strings.Join(words, " "))); err != nil {
		return errInternal("typing: %v", err)
	}
	return nil
}

// dropUploads forgets the upload reservations a client holds, for one
// API session when session is set and for all of them when it is not,
// as dropTickets does: with the ticket gone, the reservation can never
// be used, and a batch missing its file must not complete.
func (g *Gateway) dropUploads(record, session string) {
	g.mu.Lock()
	var dropped []*uploadReservation
	for ref, u := range g.uploads {
		if u.record == record && (session == "" || u.session == session) {
			delete(g.uploads, ref)
			dropped = append(dropped, u)
		}
	}
	for _, u := range dropped {
		if u.batch != "" {
			if batch := g.uploadBatches[u.batch]; batch != nil {
				for _, r := range batch.filled {
					delete(g.uploads, r)
				}
				delete(g.uploadBatches, u.batch)
			}
			g.abortedBatches[u.batch] = time.Now().Add(uploadWait)
		}
	}
	g.mu.Unlock()
}

// failUpload ends a stream whose bytes were wrong, and drops its
// reservation, which aborts its batch.
func (g *Gateway) failUpload(ws *websocket.Conn, pw *io.PipeWriter, u *uploadReservation, status websocket.StatusCode, why string) {
	pw.CloseWithError(errors.New(why))
	g.dropUpload(u, ws, status, why)
}

// dropUpload forgets a reservation and its batch, marks the batch's ID
// aborted, and closes the stream with the status and reason.
func (g *Gateway) dropUpload(u *uploadReservation, ws *websocket.Conn, status websocket.StatusCode, why string) {
	g.mu.Lock()
	delete(g.uploads, u.ref)
	if u.batch != "" {
		if batch := g.uploadBatches[u.batch]; batch != nil {
			for _, r := range batch.filled {
				delete(g.uploads, r)
			}
			delete(g.uploadBatches, u.batch)
		}
		g.abortedBatches[u.batch] = time.Now().Add(uploadWait)
	}
	g.mu.Unlock()
	ws.Close(status, why)
}
