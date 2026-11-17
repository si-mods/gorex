//go:build windows

package rex

import (
	"io"
	"path/filepath"
	"testing"
)

// TestOpenToSendJunction: a junction counts as a symlink. One to a folder
// outside its own is refused; one into a folder below it, as pnpm makes
// in node_modules, still sends the file (#730).
func TestOpenToSendJunction(t *testing.T) {
	root := linkTree(t, nil)
	junction(t, filepath.Join(root, "repo", "out"), filepath.Join(root, "secret"))
	p := filepath.Join(root, "repo", "out", "key")
	f, _, err := openToSend(p)
	if err == nil {
		f.Close()
	}
	if !LinksOutside(err) {
		t.Errorf("get through a junction out: %v, want it refused as linking outside", err)
	}

	junction(t, filepath.Join(root, "repo", "pics"), filepath.Join(root, "repo", "img"))
	f, _, err = openToSend(filepath.Join(root, "repo", "pics", "a.png"))
	if err != nil {
		t.Fatalf("get through a junction in: %v", err)
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil || string(data) != "png" {
		t.Errorf("get through a junction in: %q, %v", data, err)
	}
}
