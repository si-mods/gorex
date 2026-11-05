//go:build darwin || linux || windows

package rex

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"
)

// transferEvery is how often at most an active transfer is told of.
const transferEvery = 250 * time.Millisecond

var errNoFileTransfer = errors.New("rex: the server does not transfer files; update GoRex there")

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
		if err := s.fileGet(conn, first, peer, touch, answer); err != nil {
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

// fileSession is the session sid, for peer (mayUse), and the directory
// dir names in it: absolute, or relative to that of the program in the
// foreground (SessionInfo.Dir), which it is when "".
func (s *Server) fileSession(sid, peer, dir string) (*session, string, error) {
	ss, err := s.use(sid, peer)
	if err != nil {
		return nil, "", err
	}
	if filepath.IsAbs(dir) {
		return ss, filepath.Clean(dir), nil
	}
	return ss, filepath.Join(ss.imageDir(), dir), nil
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
	ss, dir, err := s.fileSession(req.SID, peer, req.Dir)
	if err != nil {
		return err
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("rex: %s is not a directory", dir)
	}
	t := Transfer{ID: req.ID, SID: req.SID, Name: req.Name, Direction: "in", Size: req.Size, State: "active"}
	if target := filepath.Join(dir, req.Name); req.Conflict == "skip" && exists(target) {
		t.Path, t.State = target, "skipped"
		s.transferred(t, ss)
		answer(FileAnswer{OK: true, Skipped: true, Path: target})
		return nil
	}
	p, err := openPartial(dir, req.Name, req.ID)
	if err != nil {
		return err
	}
	defer p.close()
	have := p.size
	if have > req.Size {
		have = 0
	}
	sum, err := hashOf(io.NewSectionReader(p.f, 0, have))
	if err != nil {
		return err
	}
	answer(FileAnswer{OK: true, Offset: have, Sum: sum})
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
	s.transferred(t, ss)
	last := time.Now()
	err = p.receive(r, from.Offset, req.Size, func(done int64) {
		touch()
		t.Done = done
		if time.Since(last) >= transferEvery {
			last = time.Now()
			s.transferred(t, ss)
		}
	})
	var skipped bool
	if err == nil {
		t.Path, skipped, err = p.finish(dir, req.Name, req.Conflict)
	}
	if err != nil {
		t.State = "failed"
		s.transferred(t, ss)
		return err
	}
	t.State = "done"
	if skipped {
		t.State = "skipped"
	}
	s.transferred(t, ss)
	answer(FileAnswer{OK: true, Path: t.Path, Skipped: skipped})
	return nil
}

func (s *Server) fileGet(conn net.Conn, first []byte, peer string, touch func(), answer func(FileAnswer)) error {
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
	ss, path, err := s.fileSession(req.SID, peer, req.Path)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("rex: %s is not a regular file", path)
	}
	size := st.Size()
	if req.Offset < 0 || req.Offset > size {
		return errors.New("rex: the offset is past the end of the file")
	}
	if _, err := f.Seek(req.Offset, io.SeekStart); err != nil {
		return err
	}
	answer(FileAnswer{OK: true, Size: size, Name: filepath.Base(path), MTime: st.ModTime()})
	t := Transfer{ID: req.ID, SID: req.SID, Name: filepath.Base(path), Path: path, Direction: "out", Size: size, Done: req.Offset, State: "active"}
	s.transferred(t, ss)
	last := time.Now()
	err = copyN(conn, f, req.Offset, size, func(done int64) {
		touch()
		t.Done = done
		if time.Since(last) >= transferEvery {
			last = time.Now()
			s.transferred(t, ss)
		}
	})
	t.State = "done"
	if err != nil {
		t.State = "failed"
	}
	s.transferred(t, ss)
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
		return nil, errors.New("rex: the file is being transferred already")
	}
	f, st, err := openRegular(path)
	if err != nil {
		partials.Delete(path)
		return nil, err
	}
	return &partial{path: path, f: f, size: st.Size(), mtime: st.ModTime()}, nil
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
// bytes there so far.
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

// DownloadFile takes the file at path, absolute or relative to the
// directory of the session sid's program in the foreground, into destDir,
// as conflict says of a file of its name there already (FilePut.Conflict;
// "skip" returns the file kept). It writes into a partial file there
// first, which a later try for the same file goes on from. It returns
// where the file is. progress, when set, is told of the bytes there so
// far, and of the file's size.
func (c *Client) DownloadFile(ctx context.Context, sid, path, destDir, conflict string, progress func(done, size int64)) (string, error) {
	if err := c.canTransfer(); err != nil {
		return "", err
	}
	name := path[strings.LastIndexAny(path, `/\`)+1:]
	if conflict == "" {
		conflict = "rename"
	}
	id := downloadID(sid, path)
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
	defer p.close()
	// The partial has the time of the file it is of: one of another
	// version of it starts again.
	off := p.size
	fc, a, err := c.openFile(ctx, FileGet{Op: "file.get", SID: sid, Path: path, Offset: off, ID: id})
	if off > 0 && (err != nil || !a.MTime.Truncate(time.Second).Equal(p.mtime.Truncate(time.Second))) {
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

// downloadID is the transfer id of a download of path of the session sid:
// the same for each try, which goes on from the partial of the last.
func downloadID(sid, path string) string {
	sum := sha256.Sum256([]byte(sid + "\x00" + path))
	return hex.EncodeToString(sum[:8])
}
