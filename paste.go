package main

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/egoist/mygo/transfer"
)

const (
	// maxPasteImage is the largest file a paste reads to offer its bytes:
	// a larger one is pasted as the path or file URL it was, rather than
	// read whole.
	maxPasteImage = 32 << 20
	// pasteSniff is how much of a file tells its type.
	pasteSniff = 512
)

// pasteImageTypes are the images a paste offers as their own bytes: those
// a program that reads the clipboard itself (Kitty's paste events) takes,
// as the agents that attach images do.
var pasteImageTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

// pasteImageData returns d with the first image it holds or names offered
// as its bytes ahead of the text, and reports whether it found one.
//
// A paste of a file is a reference to a path of this machine, which a
// program on another machine cannot open, over SSH or on a host; its
// bytes it can. A paste of text that is one absolute path to an image
// here is a reference too: it is offered as the image, and keeps the path
// as its text, which a shell, or a program that wants a file to open,
// still gets.
func pasteImageData(d transfer.Data) (transfer.Data, bool) {
	for _, f := range d.Formats() {
		if slices.Contains(pasteImageTypes, string(f)) {
			// The clipboard holds the image itself, as a browser's copy
			// of one does: nothing to add.
			return d, false
		}
	}
	mime, data, ok := pasteImageFile(d)
	if !ok {
		return d, false
	}
	reps := []transfer.Representation{transfer.Bytes(transfer.Format(mime), data)}
	for _, f := range []transfer.Format{transfer.Text, transfer.URIList, transfer.FileList} {
		if b, err := d.Read(f); err == nil {
			reps = append(reps, transfer.Bytes(f, b))
		}
	}
	return transfer.New(transfer.NewItem(reps...)), true
}

// pasteImageFile returns the type and bytes of the first image the paste
// holds: of the files it lists, or of the one path it is.
func pasteImageFile(d transfer.Data) (mime string, data []byte, ok bool) {
	for _, path := range pasteFiles(d) {
		if mime, data, ok := readPasteImage(path); ok {
			return mime, data, true
		}
	}
	return "", nil, false
}

// pasteFiles returns the files a paste holds: those its file URLs name,
// or the one absolute path it is, as a file manager's copy of a path
// pastes.
func pasteFiles(d transfer.Data) []string {
	paths := filePaths(d, transfer.URIList)
	if len(paths) == 0 {
		paths = filePaths(d, transfer.FileList)
	}
	if len(paths) > 0 {
		return paths
	}
	text, err := d.Read(transfer.Text)
	if err != nil {
		return nil
	}
	path := strings.TrimSpace(string(text))
	if path == "" || strings.ContainsAny(path, "\r\n\x00") || !filepath.IsAbs(path) {
		return nil
	}
	return []string{path}
}

// filePaths returns the local paths of d's URIs of format f: those of
// files, not of other schemes.
func filePaths(d transfer.Data, f transfer.Format) []string {
	b, err := d.Read(f)
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		u, err := url.Parse(line)
		if err != nil || u.Scheme != "file" {
			continue
		}
		path := u.Path
		if u.Host != "" {
			// A share of another machine, \\host\path on Windows.
			if runtime.GOOS != "windows" {
				continue
			}
			path = "//" + u.Host + path
		}
		if runtime.GOOS == "windows" {
			path = filepath.FromSlash(strings.TrimPrefix(path, "/"))
		}
		paths = append(paths, path)
	}
	return paths
}

// readPasteImage returns the type and bytes of the image at path: its
// type as the head of the file tells it, one a paste offers.
func readPasteImage(path string) (mime string, data []byte, ok bool) {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxPasteImage {
		return "", nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return "", nil, false
	}
	defer f.Close()
	head := make([]byte, pasteSniff)
	n, _ := io.ReadFull(f, head)
	typ := http.DetectContentType(head[:n])
	if !slices.Contains(pasteImageTypes, typ) {
		return "", nil, false
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", nil, false
	}
	data, err = io.ReadAll(io.LimitReader(f, maxPasteImage+1))
	if err != nil || len(data) > maxPasteImage {
		return "", nil, false
	}
	return typ, data, true
}
