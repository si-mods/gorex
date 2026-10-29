package main

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/transfer"
)

const (
	// maxPasteImage is the largest file a paste offers as its bytes: a
	// larger one is pasted as the path or file URL it was, rather than
	// read whole.
	maxPasteImage = 32 << 20
	// pasteSniff is how much of a file tells its type.
	pasteSniff = 512
)

// pasteImageTypes are the images a paste offers as their own bytes: those
// a program that reads the clipboard itself (Kitty's paste events) takes,
// as the agents that attach images do.
var pasteImageTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

// errPasteImage means the file a paste named is no longer an image it
// offers, as one replaced since the paste began.
var errPasteImage = errors.New("gorex: not an image a paste offers")

// pasteImageData returns d with the first image it holds or names offered
// as its bytes ahead of the text, and reports whether it found one.
//
// A paste of a file is a reference to a path of this machine, which a
// program on another machine cannot open, over SSH or on a host; its
// bytes it can. A paste of text that is one absolute path to an image
// here, quoted by a file manager or not, is a reference too: it is
// offered as the image, and keeps the text, which a shell, or a program
// that wants a file to open, still gets.
func pasteImageData(d transfer.Data) (transfer.Data, bool) {
	for _, f := range d.Formats() {
		if slices.Contains(pasteImageTypes, string(f)) {
			// The clipboard holds the image itself, as a browser's copy
			// of one does: nothing to add.
			return d, false
		}
	}
	mime, path, ok := pasteImage(d)
	if !ok {
		return d, false
	}
	// The file is read when the program asks for it, on the terminal's
	// goroutine: a paste into a shell never reads it.
	reps := []transfer.Representation{transfer.Lazy(transfer.Format(mime), func() ([]byte, error) {
		return readPasteImage(path, mime)
	})}
	for _, f := range d.Formats() {
		// MyGo's own formats are its drag sessions and files' metadata,
		// which no paste carries; the file list is one a program reads.
		if f != transfer.FileList && strings.HasPrefix(string(f), "application/x-mygo-") {
			continue
		}
		if b, err := d.Read(f); err == nil {
			reps = append(reps, transfer.Bytes(f, b))
		}
	}
	return transfer.New(transfer.NewItem(reps...)), true
}

// pasteImage returns the type and path of the first image the paste
// holds: of the files it lists, or of the one path it is.
func pasteImage(d transfer.Data) (mime, path string, ok bool) {
	for _, path := range pasteFiles(d) {
		if mime, ok := sniffPasteImage(path); ok {
			return mime, path, true
		}
	}
	return "", "", false
}

// pasteFiles returns the files a paste holds: those its file URLs name,
// or the one absolute path it is, as a file manager's copy of a path
// pastes.
func pasteFiles(d transfer.Data) []string {
	if paths, err := d.Files(); err == nil && len(paths) > 0 {
		return paths
	}
	text, err := d.Read(transfer.Text)
	if err != nil {
		return nil
	}
	path := unquoted(strings.TrimSpace(string(text)))
	if path == "" || strings.ContainsAny(path, "\r\n\x00") || !filepath.IsAbs(path) {
		return nil
	}
	return []string{path}
}

// unquoted returns path without the quotes a file manager's copy of a
// path puts around it, as Windows' "Copy as path" does.
func unquoted(path string) string {
	if len(path) >= 2 && (path[0] == '"' || path[0] == '\'') && path[len(path)-1] == path[0] {
		return path[1 : len(path)-1]
	}
	return path
}

// sniffPasteImage returns the type of the image at path, one a paste
// offers, read from the head of the file.
func sniffPasteImage(path string) (mime string, ok bool) {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxPasteImage {
		return "", false
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	head := make([]byte, pasteSniff)
	n, _ := io.ReadFull(f, head)
	typ := http.DetectContentType(head[:n])
	if !slices.Contains(pasteImageTypes, typ) {
		return "", false
	}
	return typ, true
}

// readPasteImage returns the image at path, of type mime, for a program
// that asked for it: a file that is no longer one it offers is an error,
// not bytes of another type.
func readPasteImage(path, mime string) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxPasteImage {
		return nil, errPasteImage
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) > maxPasteImage || http.DetectContentType(data[:min(len(data), pasteSniff)]) != mime {
		return nil, errPasteImage
	}
	return data, nil
}
