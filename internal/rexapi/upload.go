package rexapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

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

// features are the welcome's features: GoRex's own, which Rex's clients
// ignore, as they ignore every name they do not know.
func (g *Gateway) features() []string {
	if g.hello.Can(rex.CapabilityFileTemp) {
		return []string{featureUpload}
	}
	return nil
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
	n, err := io.ReadFull(s.r, p)
	s.off += int64(n)
	if err == io.ErrUnexpectedEOF {
		err = io.EOF
	}
	return n, err
}
