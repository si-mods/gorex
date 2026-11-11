//go:build darwin || linux || windows

package rex

import (
	"bufio"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
)

// transferEvery is how often at most an active transfer is told of.
const transferEvery = 250 * time.Millisecond

var errNoFileTransfer = errors.New("rex: the server does not transfer files; update GoRex there")

// errTransferring is the error of a transfer of a file another transfer
// writes yet.
var errTransferring = errors.New("rex: the file is being transferred already")

// Lost reports whether err is of a connection lost, which a try again may
// get past, and not an answer of the server's.
func Lost(err error) bool {
	var ne net.Error
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) ||
		errors.As(err, &ne) || err != nil && strings.HasSuffix(err.Error(), "connection to the server lost")
}

// Transferring reports whether err is the server refusing a transfer of a
// file another transfer writes yet, as one whose connection was lost
// does until the server notices (the error arrives as text).
func Transferring(err error) bool {
	return err != nil && err.Error() == errTransferring.Error()
}

// abortTimeout is how long a put cancelled tries to remove what the server
// has of its file.
const abortTimeout = 5 * time.Second

// partialAge is how long a partial is kept since it was last written: a
// put into its directory removes an older one.
const partialAge = 24 * time.Hour

// transfer serves a file.put or file.get connection of peer, whose first
// line is first. A remote connection that takes or gives nothing for
// writeTimeout is dropped.
func (s *Server) transfer(conn net.Conn, r *bufio.Reader, first []byte, op, peer string) {
	defer conn.Close()
	touch := func() {}
	if peer != "" {
		touch = func() { conn.SetDeadline(time.Now().Add(writeTimeout.get())) }
	}
	touch()
	answer := func(a FileAnswer) {
		touch() // after a long hash of a partial too
		b, _ := json.Marshal(a)
		conn.Write(append(b, '\n'))
	}
	if op == "file.get" {
		if err := s.fileGet(conn, first, touch, answer); err != nil {
			answer(FileAnswer{Error: err.Error()})
		}
		return
	}
	if err := s.filePut(conn, r, first, peer, touch, answer); err != nil {
		// The client may be sending yet: what it sends is read meanwhile,
		// and a while after, so that it reads the error and not a reset.
		drained := make(chan struct{})
		go func() {
			io.Copy(io.Discard, r)
			close(drained)
		}()
		answer(FileAnswer{Error: err.Error()})
		conn.SetReadDeadline(time.Now().Add(drainTimeout))
		if cw, ok := conn.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
		<-drained
	}
}

// drainTimeout is how long a put that failed reads what its client sends
// yet, after the error; resumeRate how fast at least a client hashes what
// the server has of its file, before it answers.
const (
	drainTimeout = 2 * time.Second
	resumeRate   = 10 << 20 // bytes a second
)

// filePath is the path dir names in the session sid: absolute, of the
// home directory of this user when it is "~" or starts with "~/", or
// relative to the directory of the program in the foreground
// (SessionInfo.Dir), which it is when "".
func (s *Server) filePath(sid, dir string) (string, error) {
	ss, err := s.session(sid)
	if err != nil {
		return "", err
	}
	if rest, ok := strings.CutPrefix(dir, "~"); ok && (rest == "" || rest[0] == '/') {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, rest), nil
	}
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir), nil
	}
	return filepath.Join(ss.imageDir(), dir), nil
}

// dropDir is this user's folder in the temporary directory, where a put
// with Temp goes, made when it is not there. Another user may have made
// the path first in a temporary directory all share: it is taken only as
// a directory of this user's, not a link, which it then lets no other
// user read or write in, whatever the umask or a chmod made it.
func dropDir() (string, error) {
	name := "gorex-drops"
	if uid := os.Getuid(); uid >= 0 { // Windows has none, and a temporary directory per user
		name = fmt.Sprintf("gorex-drops-%d", uid)
	}
	dir := filepath.Join(os.TempDir(), name)
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", err
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !st.IsDir() || !ownDir(st) {
		return "", fmt.Errorf("rex: %s is not a folder of this user's", dir)
	}
	if st.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func (s *Server) filePut(conn net.Conn, r *bufio.Reader, first []byte, peer string, touch func(), answer func(FileAnswer)) error {
	var req FilePut
	if err := json.Unmarshal(first, &req); err != nil {
		return err
	}
	if req.Conflict == "" {
		req.Conflict = "rename"
	}
	if err := checkPut(req.Name, req.ID, req.Conflict); err != nil {
		return err
	}
	if req.Size < 0 {
		return errors.New("rex: a negative size")
	}
	dir, err := s.filePath(req.SID, req.Dir)
	if err != nil {
		// An abort names the directory absolute, as the put's first answer
		// told it, and needs no session: the session of a pane closing
		// ends as its uploads stop. It is taken only for a partial a put of
		// the same peer opened in this run of the server, and touches no
		// other file; after a restart the sweep removes the partial.
		if !req.Abort || !filepath.IsAbs(req.Dir) {
			return err
		}
		path := filepath.Join(filepath.Clean(req.Dir), partialName(req.Name, req.ID))
		if by, ok := s.puts.Load(path); !ok || by != peer {
			return err
		}
		return s.abortPut(path, answer)
	}
	if req.Temp {
		if dir, err = dropDir(); err != nil {
			return err
		}
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("rex: %s is not a directory", dir)
	}
	sweepPartials(dir)
	if req.Abort {
		return s.abortPut(filepath.Join(dir, partialName(req.Name, req.ID)), answer)
	}
	t := Transfer{ID: req.ID, SID: req.SID, Name: req.Name, Direction: "in", Size: req.Size, State: "active"}
	if target := filepath.Join(dir, req.Name); req.Conflict == "skip" && exists(target) {
		t.Path, t.State = target, "skipped"
		s.transferred(t)
		answer(FileAnswer{OK: true, Skipped: true, Path: target})
		return nil
	}
	// A try again of a drop that finished, whose answer was lost, learns
	// where its file went.
	if path, ok := s.finishedPut(filepath.Join(dir, partialName(req.Name, req.ID)), peer, req.Drop, req.Size); ok {
		t.Path, t.State = path, "skipped"
		s.transferred(t)
		answer(FileAnswer{OK: true, Skipped: true, Path: path})
		return nil
	}
	p, err := openPartial(dir, req.Name, req.ID)
	if err != nil {
		return err
	}
	defer p.close()
	s.puts.Store(p.path, peer)
	have := p.size
	if have > req.Size {
		have = 0
	}
	sum, err := hashOf(io.NewSectionReader(p.f, 0, have))
	if err != nil {
		return err
	}
	answer(FileAnswer{OK: true, Offset: have, Sum: sum, Dir: dir})
	if peer != "" {
		conn.SetReadDeadline(time.Now().Add(writeTimeout.get() + time.Duration(have/resumeRate)*time.Second))
	}
	line, err := readLine(r)
	if err != nil {
		return err
	}
	var from FileAnswer
	if err := json.Unmarshal(line, &from); err != nil {
		return err
	}
	if from.Offset != 0 && from.Offset != have {
		return errors.New("rex: the offset is neither 0 nor the server's")
	}
	t.Done = from.Offset
	s.transferred(t)
	last := time.Now()
	err = p.receive(r, from.Offset, req.Size, func(done int64) {
		touch()
		t.Done = done
		if time.Since(last) >= transferEvery {
			last = time.Now()
			s.transferred(t)
		}
	})
	var skipped bool
	if err == nil {
		t.Path, skipped, err = p.finish(dir, req.Name, req.Conflict)
	}
	if err == nil {
		s.puts.Delete(p.path)
		if req.Drop != "" {
			s.finished.Store(p.path, finishedPut{peer, req.Drop, t.Path, time.Now()})
		}
	}
	if err != nil {
		t.State = "failed"
		s.transferred(t)
		return err
	}
	t.State = "done"
	if skipped {
		t.State = "skipped"
	}
	s.transferred(t)
	answer(FileAnswer{OK: true, Path: t.Path, Skipped: skipped})
	return nil
}

func (s *Server) fileGet(conn net.Conn, first []byte, touch func(), answer func(FileAnswer)) error {
	var req FileGet
	if err := json.Unmarshal(first, &req); err != nil {
		return err
	}
	if req.ID == "" {
		req.ID = newID()
	} else if err := checkID(req.ID); err != nil {
		return err
	}
	if req.Path == "" {
		return errors.New("rex: no path")
	}
	path, err := s.filePath(req.SID, req.Path)
	if err != nil {
		return err
	}
	f, st, err := openToSend(path)
	if err != nil {
		return err
	}
	defer f.Close()
	size := st.Size()
	if req.Offset < 0 || req.Offset > size {
		return errors.New("rex: the offset is past the end of the file")
	}
	if _, err := f.Seek(req.Offset, io.SeekStart); err != nil {
		return err
	}
	answer(FileAnswer{OK: true, Size: size, Name: filepath.Base(path), MTime: st.ModTime()})
	t := Transfer{ID: req.ID, SID: req.SID, Name: filepath.Base(path), Path: path, Direction: "out", Size: size, Done: req.Offset, State: "active"}
	s.transferred(t)
	last := time.Now()
	err = copyN(conn, f, req.Offset, size, func(done int64) {
		touch()
		t.Done = done
		if time.Since(last) >= transferEvery {
			last = time.Now()
			s.transferred(t)
		}
	})
	t.State = "done"
	if err != nil {
		t.State = "failed"
	}
	s.transferred(t)
	return nil // the connection carries no answer after the bytes
}

// checkPut checks a put's name, id and conflict rule.
func checkPut(name, id, conflict string) error {
	if err := checkName(name); err != nil {
		return err
	}
	if err := checkID(id); err != nil {
		return err
	}
	if conflict != "rename" && conflict != "overwrite" && conflict != "skip" {
		return fmt.Errorf("rex: no conflict rule %q", conflict)
	}
	return nil
}

// checkName checks that name is a file's name, without a directory: not
// "." nor "..", no separator, no control character, at most 255 bytes;
// on Windows no ":", which names a stream of a file.
func checkName(name string) error {
	if name == "" || name == "." || name == ".." || len(name) > 255 ||
		strings.ContainsAny(name, `/\`) || strings.ContainsFunc(name, unicode.IsControl) ||
		runtime.GOOS == "windows" && strings.Contains(name, ":") {
		return fmt.Errorf("rex: %q is not a file name", name)
	}
	return nil
}

// checkID checks a transfer's id: letters, digits, "_" and "-", at most 64.
func checkID(id string) error {
	ok := id != "" && len(id) <= 64
	for _, c := range id {
		ok = ok && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-')
	}
	if !ok {
		return fmt.Errorf("rex: %q is not a transfer id", id)
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// hashOf is the SHA-256 of what r reads, in hex.
func hashOf(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// copyN copies the bytes from done to size of a file from src to dst,
// telling progress of the bytes done after each piece.
func copyN(dst io.Writer, src io.Reader, done, size int64, progress func(int64)) error {
	_, err := io.CopyN(io.MultiWriter(dst, progressWriter{&done, progress}), src, size-done)
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return err
}

type progressWriter struct {
	done *int64
	f    func(int64)
}

func (w progressWriter) Write(b []byte) (int, error) {
	*w.done += int64(len(b))
	w.f(*w.done)
	return len(b), nil
}

// finishedPut is a put that finished: the peer that sent it, its drop,
// where its file went, and when.
type finishedPut struct {
	peer, drop, path string
	at               time.Time
}

// finishedFor is how long a finished put answers a try again of its drop.
const finishedFor = 2 * time.Minute

// finishedPut returns where the file of the put of partial, of drop by
// peer and of size bytes, went, when that drop finished lately and its
// file is there, and no partial of it is being written again. It forgets
// the puts finished longer ago, and one of partial another put now
// writes.
func (s *Server) finishedPut(partial, peer, drop string, size int64) (string, bool) {
	s.finished.Range(func(k, v any) bool {
		if time.Since(v.(finishedPut).at) > finishedFor {
			s.finished.Delete(k)
		}
		return true
	})
	v, ok := s.finished.Load(partial)
	if !ok {
		return "", false
	}
	f := v.(finishedPut)
	if f.peer != peer || f.drop != drop || drop == "" || exists(partial) {
		s.finished.Delete(partial)
		return "", false
	}
	if st, err := os.Stat(f.path); err != nil || st.Size() != size {
		return "", false
	}
	return f.path, true
}

// partials are the partial files being written, which one transfer at a
// time may write.
var partials sync.Map

// partial is a file being received: a hidden file beside where it goes,
// of a name of a fixed length (partialName), which a transfer of the same
// file tried again goes on writing.
type partial struct {
	path  string
	f     *os.File
	size  int64
	mtime time.Time
}

// openPartial opens the partial of name in dir for the transfer id, with
// what an earlier try left of it.
// A link or another file in its place, as one planted in a directory
// others write, is an error: it is not followed.
func openPartial(dir, name, id string) (*partial, error) {
	path := filepath.Join(dir, partialName(name, id))
	if _, busy := partials.LoadOrStore(path, true); busy {
		return nil, errTransferring
	}
	f, st, err := openRegular(path)
	if err != nil {
		partials.Delete(path)
		return nil, err
	}
	return &partial{path: path, f: f, size: st.Size(), mtime: st.ModTime()}, nil
}

// abortPut removes the partial at path, unless a transfer writes it.
func (s *Server) abortPut(path string, answer func(FileAnswer)) error {
	if _, busy := partials.LoadOrStore(path, true); busy {
		return errTransferring
	}
	defer partials.Delete(path)
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	s.puts.Delete(path)
	answer(FileAnswer{OK: true})
	return nil
}

// sweepPartials removes the partials in dir older than partialAge that no
// transfer writes.
func sweepPartials(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, ".gorex-") || !strings.HasSuffix(name, ".partial") {
			continue
		}
		path := filepath.Join(dir, name)
		if st, err := e.Info(); err != nil || time.Since(st.ModTime()) < partialAge {
			continue
		}
		if _, busy := partials.LoadOrStore(path, true); !busy {
			os.Remove(path)
			partials.Delete(path)
		}
	}
}

// partialName is the name of the partial of name for the transfer id:
// ".gorex-" and 16 hex digits of the SHA-256 of both, ".partial", short
// whatever name's length.
func partialName(name, id string) string {
	sum := sha256.Sum256([]byte(name + "\x00" + id))
	return ".gorex-" + hex.EncodeToString(sum[:8]) + ".partial"
}

// openRegular opens the regular file at path to write, made when there is
// none, and no link: the file opened must be the one at path itself.
func openRegular(path string) (*os.File, os.FileInfo, error) {
	flag := os.O_RDWR
	lst, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		flag |= os.O_CREATE | os.O_EXCL
	case err != nil:
		return nil, nil, err
	case !lst.Mode().IsRegular():
		return nil, nil, fmt.Errorf("rex: %s is not a regular file", path)
	}
	f, err := os.OpenFile(path, flag, 0o666)
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err == nil {
		lst, err = os.Lstat(path)
	}
	if err == nil && !os.SameFile(lst, st) {
		err = fmt.Errorf("rex: %s changed as it opened", path)
	}
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, st, nil
}

// receive writes the bytes from off to size of the file, as r reads them,
// over what the partial has from off.
func (p *partial) receive(r io.Reader, off, size int64, progress func(int64)) error {
	if err := p.f.Truncate(off); err != nil {
		return err
	}
	if _, err := p.f.Seek(off, io.SeekStart); err != nil {
		return err
	}
	if err := copyN(p.f, r, off, size, progress); err != nil {
		return err
	}
	return p.f.Sync()
}

// finish moves the whole partial to name in dir, as conflict says of a
// file there already: "overwrite" replaces it, "skip" keeps it and drops
// the partial, and "rename" takes the first name free of "name (1).ext",
// "name (2).ext" and on. It returns where the file is, and whether it
// was skipped.
func (p *partial) finish(dir, name, conflict string) (string, bool, error) {
	if err := p.f.Close(); err != nil {
		return "", false, err
	}
	target := filepath.Join(dir, name)
	switch conflict {
	case "overwrite":
		// The file replaced keeps its permissions.
		if st, err := os.Stat(target); err == nil {
			os.Chmod(p.path, st.Mode().Perm())
		}
		return target, false, os.Rename(p.path, target)
	case "skip":
		err := placeNew(p.path, target)
		if errors.Is(err, fs.ErrExist) {
			return target, true, os.Remove(p.path)
		}
		return target, false, err
	}
	ext := filepath.Ext(name)
	if ext == name {
		ext = "" // a dot file, as ".bashrc"
	}
	stem := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		err := placeNew(p.path, target)
		if !errors.Is(err, fs.ErrExist) {
			return target, false, err
		}
		target = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
	}
}

// close lets the partial go, kept for the next try unless finished or
// empty.
func (p *partial) close() {
	p.f.Close()
	if st, err := os.Stat(p.path); err == nil && st.Size() == 0 {
		os.Remove(p.path)
	}
	partials.Delete(p.path)
}

// placeNew moves from to to, where no file may be: a hard link fails
// on one there, where a rename would replace it. A file system without
// hard links gets a rename, after a look.
func placeNew(from, to string) error {
	err := os.Link(from, to)
	if err == nil {
		return os.Remove(from)
	}
	if errors.Is(err, fs.ErrExist) || exists(to) {
		return fs.ErrExist
	}
	return os.Rename(from, to)
}

// fileConn is a transfer connection, closed as its context is done.
type fileConn struct {
	net.Conn
	r    *bufio.Reader
	stop func() bool
}

func (c fileConn) Close() error {
	c.stop()
	return c.Conn.Close()
}

// answer reads a line of the server's, its error as one.
func (c fileConn) answer() (FileAnswer, error) {
	line, err := readLine(c.r)
	if err != nil {
		return FileAnswer{}, err
	}
	var a FileAnswer
	if err := json.Unmarshal(line, &a); err != nil {
		return a, err
	}
	if !a.OK {
		return a, fmt.Errorf("rex: %s", strings.TrimPrefix(a.Error, "rex: "))
	}
	return a, nil
}

// send writes v as a line.
func (c fileConn) send(v any) error {
	b, _ := json.Marshal(v)
	_, err := c.Write(append(b, '\n'))
	return err
}

// canTransfer tells whether the server transfers files, as an error.
func (c *Client) canTransfer() error {
	h, err := c.Hello()
	if err == nil && !h.Can(CapabilityFileTransfer) {
		err = errNoFileTransfer
	}
	return err
}

// openFile opens a transfer connection with its first line, first, and
// reads the server's answer.
func (c *Client) openFile(ctx context.Context, first any) (fileConn, FileAnswer, error) {
	if c.dial == nil {
		return fileConn{}, FileAnswer{}, errors.New("rex: this connection opens no other")
	}
	conn, err := c.dial()
	if err != nil {
		return fileConn{}, FileAnswer{}, err
	}
	fc := fileConn{Conn: conn, r: bufio.NewReader(conn), stop: context.AfterFunc(ctx, func() { conn.Close() })}
	err = fc.send(first)
	var a FileAnswer
	if err == nil {
		a, err = fc.answer()
	}
	if err != nil {
		fc.Close()
		return fileConn{}, a, ctxErr(ctx, err)
	}
	return fc, a, nil
}

// ctxErr is err, or the context's when it is done: what closed the
// connection.
func ctxErr(ctx context.Context, err error) error {
	if err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// PutFile sends the file src, of f.Size bytes, into the session f.SID's
// directory, as f says (FilePut), going on from what an earlier try with
// the same f.ID left there when its bytes are src's. It returns where the
// file is, and whether it was skipped. progress, when set, is told of the
// bytes there so far. Cancelled through ctx once the server took it, it
// removes what the server has of the file (AbortFile), in the directory
// the server named: the session may end as it is cancelled.
func (c *Client) PutFile(ctx context.Context, f FilePut, src io.ReaderAt, progress func(done int64)) (path string, skipped bool, err error) {
	if err := c.canTransfer(); err != nil {
		return "", false, err
	}
	f.Op = "file.put"
	fc, a, err := c.openFile(ctx, f)
	if err != nil || a.Skipped {
		return a.Path, a.Skipped, err
	}
	defer fc.Close()
	defer func(dir string) {
		if err == nil || ctx.Err() == nil {
			return
		}
		fc.Close() // the server lets the partial go
		// A server before FileAnswer.Dir names none: the session's then.
		f.Dir = cmp.Or(dir, f.Dir)
		actx, stop := context.WithTimeout(context.Background(), abortTimeout)
		defer stop()
		if aerr := c.AbortFile(actx, f); aerr != nil {
			log.Printf("rex: removing the partial of %s: %v", f.Name, aerr)
		}
	}(a.Dir)
	off := a.Offset
	if off > 0 {
		if sum, err := hashOf(io.NewSectionReader(src, 0, off)); off > f.Size || err != nil || sum != a.Sum {
			off = 0
		}
	}
	if err := fc.send(struct {
		Offset int64 `json:"offset"`
	}{off}); err != nil {
		return "", false, ctxErr(ctx, err)
	}
	if progress == nil {
		progress = func(int64) {}
	}
	progress(off)
	if err := copyN(fc, io.NewSectionReader(src, off, f.Size-off), off, f.Size, progress); err != nil {
		// The server may have stopped taking it with an error of its own.
		fc.SetReadDeadline(time.Now().Add(drainTimeout))
		if a, aerr := fc.answer(); a.Error != "" {
			return "", false, aerr
		}
		return "", false, ctxErr(ctx, err)
	}
	a, err = fc.answer()
	return a.Path, a.Skipped, ctxErr(ctx, err)
}

// PutForShell sends src as PutFile does, for a pane or a block whose
// shell is at shell: into the temporary folder (f.Temp) by a name the
// shell takes (PasteableName). It returns where the file is, and that
// path as a word of the shell, "" when the shell takes no such path, as
// one of a folder it refuses.
func (c *Client) PutForShell(ctx context.Context, shell string, f FilePut, src io.ReaderAt, progress func(done int64)) (path, word string, err error) {
	if f.Temp {
		f.Name = PasteableName(shell, f.Name)
	}
	if path, _, err = c.PutFile(ctx, f, src, progress); err != nil {
		return "", "", err
	}
	word, _ = PathWord(shell, path)
	return path, word, nil
}

// AbortFile removes what a put of f left in the session's directory: the
// partial of f.Name for f.ID in f.Dir, once no transfer writes it, waiting
// for one stopping to let it go until ctx is done. With f.Dir absolute,
// as a put's first answer names it (FileAnswer.Dir), the session need not
// be there any more.
func (c *Client) AbortFile(ctx context.Context, f FilePut) error {
	if err := c.canTransfer(); err != nil {
		return err
	}
	f.Op, f.Abort, f.Size = "file.put", true, 0
	for {
		fc, _, err := c.openFile(ctx, f)
		if err == nil {
			return fc.Close()
		}
		if !Transferring(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// isFolder ends the error of a get of a folder, which only the server
// gives.
const isFolder = "is a folder, which is not sent"

// notRegular ends the error of a get of what is neither a folder nor a
// regular file: a FIFO, a device, a socket.
const notRegular = "is not a regular file"

// checkRegular refuses st, of path, unless it is a regular file.
func checkRegular(path string, st fs.FileInfo) error {
	if st.IsDir() {
		return fmt.Errorf("rex: %s %s", path, isFolder)
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("rex: %s %s", path, notRegular)
	}
	return nil
}

// NotRegular reports whether err is the server refusing to send what is
// not a regular file (the error arrives as text).
func NotRegular(err error) bool {
	return err != nil && strings.HasSuffix(err.Error(), notRegular)
}

// Folder reports whether err is the server refusing to send a folder (the
// error arrives as text).
func Folder(err error) bool {
	return err != nil && strings.HasSuffix(err.Error(), isFolder)
}

// DownloadFile takes the file at path, absolute, of the server user's home
// directory ("~/"), or relative to the directory of the session sid's
// program in the foreground, into destDir, by the name path ends in
// (a separator after it trimmed), as conflict says of a file of
// its name there already (FilePut.Conflict; "skip" returns the file
// kept). It writes into a partial file there first, which a later try for
// the same file goes on from (DownloadID), when the file there has its
// time and is no shorter; a try cancelled (ctx's error is
// context.Canceled) removes it. It returns where the file is. progress,
// when set, is told of the bytes there so far, and of the file's size.
func (c *Client) DownloadFile(ctx context.Context, sid, path, destDir, conflict string, progress func(done, size int64)) (_ string, err error) {
	if err := c.canTransfer(); err != nil {
		return "", err
	}
	name := strings.TrimRight(path, `/\`)
	name = name[strings.LastIndexAny(name, `/\`)+1:]
	if conflict == "" {
		conflict = "rename"
	}
	id := DownloadID(sid, path)
	if err := checkPut(name, id, conflict); err != nil {
		return "", err
	}
	if target := filepath.Join(destDir, name); conflict == "skip" && exists(target) {
		return target, nil
	}
	p, err := openPartial(destDir, name, id)
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil && errors.Is(ctx.Err(), context.Canceled) {
			p.f.Close()
			os.Remove(p.path)
		}
		p.close()
	}()
	// The partial has the time of the file it is of: one of another
	// version of it, or longer than it, starts again.
	off := p.size
	fc, a, err := c.openFile(ctx, FileGet{Op: "file.get", SID: sid, Path: path, Offset: off, ID: id})
	if off > 0 && (err != nil || a.Size < off || !a.MTime.Truncate(time.Second).Equal(p.mtime.Truncate(time.Second))) {
		if err == nil {
			fc.Close()
		}
		off = 0
		fc, a, err = c.openFile(ctx, FileGet{Op: "file.get", SID: sid, Path: path, ID: id})
	}
	if err != nil {
		return "", err
	}
	defer fc.Close()
	if progress == nil {
		progress = func(int64, int64) {}
	}
	progress(off, a.Size)
	if err := p.receive(fc.r, off, a.Size, func(done int64) { progress(done, a.Size) }); err != nil {
		p.f.Close() // before the time, which Windows may set as it closes
		os.Chtimes(p.path, a.MTime, a.MTime)
		return "", ctxErr(ctx, err)
	}
	target, skipped, err := p.finish(destDir, name, conflict)
	if err == nil && !skipped {
		os.Chtimes(target, a.MTime, a.MTime)
	}
	return target, err
}

// DownloadID is the transfer id of a download of path of the session sid:
// the same for each try, which goes on from the partial of the last. A
// relative path is of the directory the program is in as the try begins:
// a client that knows it keys the id on it by making path absolute.
func DownloadID(sid, path string) string {
	sum := sha256.Sum256([]byte(sid + "\x00" + path))
	return hex.EncodeToString(sum[:8])
}

// GetFile opens the file at path of the session sid to read, as
// DownloadFile takes it (FileGet.Path): its answer tells the file's size,
// name and time, and the reader gives its bytes from the start. It
// fails at once when the server refuses the file: a folder (Folder),
// what is not a regular file (NotRegular), one missing or denied. The
// caller closes the reader, which ends the get; so does ctx ending.
func (c *Client) GetFile(ctx context.Context, sid, path string) (io.ReadCloser, FileAnswer, error) {
	if err := c.canTransfer(); err != nil {
		return nil, FileAnswer{}, err
	}
	fc, a, err := c.openFile(ctx, FileGet{Op: "file.get", SID: sid, Path: path})
	if err != nil {
		return nil, a, err
	}
	return fileReader{fc}, a, nil
}

// fileReader reads the bytes of a get, after its answer.
type fileReader struct{ fc fileConn }

func (r fileReader) Read(b []byte) (int, error) { return r.fc.r.Read(b) }
func (r fileReader) Close() error               { return r.fc.Close() }
