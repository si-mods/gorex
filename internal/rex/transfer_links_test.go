//go:build darwin || linux || windows

package rex

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// linkTree makes, in a temporary folder, secret/key outside a folder repo
// and repo/img/a.png in it, and the links of links (name to target, a
// target relative to the link's folder, or "/"-led for one of the
// temporary folder). It skips the test where this process may not make a
// symlink, as Windows without developer mode.
func linkTree(t *testing.T, links [][2]string) (root string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"secret/key": "secret", "repo/img/a.png": "png", "repo/b2": "b2"} {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, l := range links {
		target := filepath.FromSlash(l[1])
		if strings.HasPrefix(l[1], "/") {
			target = filepath.Join(root, target)
		}
		if err := os.Symlink(target, filepath.Join(root, filepath.FromSlash(l[0]))); err != nil {
			t.Skipf("no symlink here (on Windows it needs developer mode; Linux CI runs this): %v", err)
		}
	}
	return root
}

// TestOpenToSendRefusesLinkOut: a get through a symlink, the file's own
// or a folder's on the way, whose target leaves the folder the link is
// in, is refused, a chain that leaves at a later link too (#730).
func TestOpenToSendRefusesLinkOut(t *testing.T) {
	root := linkTree(t, [][2]string{
		{"repo/chart.png", "../secret/key"},
		{"repo/abs.png", "/secret/key"},
		{"repo/docs", "/secret"},
		{"repo/a.png", "b.png"},
		{"repo/b.png", "img/c.png"},
		{"repo/img/c.png", "../../secret/key"},
		{"repo/img/up.png", "../b2"},
	})
	for _, name := range []string{"chart.png", "abs.png", "docs/key", "a.png", "b.png", "img/up.png"} {
		p := filepath.Join(root, "repo", filepath.FromSlash(name))
		f, _, err := openToSend(p)
		if err == nil {
			f.Close()
		}
		if !LinksOutside(err) || !strings.Contains(err.Error(), p) {
			t.Errorf("get of %s: %v, want it refused as linking outside", name, err)
		}
	}
}

// TestOpenToSendFollowsLinkIn: a symlink that stays in its folder, with
// ".." in its target among them, still sends the file it leads to.
func TestOpenToSendFollowsLinkIn(t *testing.T) {
	root := linkTree(t, [][2]string{
		{"repo/ok.png", "img/a.png"},
		{"repo/latest", "img"},
		{"repo/abs.png", "/repo/img/a.png"},
		{"repo/dots.png", "img/../img/./a.png"},
		{"repo/chain.png", "ok.png"},
		{"repo/here", "."},
	})
	for _, name := range []string{"ok.png", "latest/a.png", "abs.png", "dots.png", "chain.png", "here/latest/a.png"} {
		f, st, err := openToSend(filepath.Join(root, "repo", filepath.FromSlash(name)))
		if err != nil {
			t.Errorf("get of %s: %v", name, err)
			continue
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil || string(data) != "png" || st.Size() != 3 {
			t.Errorf("get of %s: %q, %v, size %d", name, data, err, st.Size())
		}
	}
}

// TestOpenToSendRefusesSwap: a file swapped for a link out of its folder
// after openToSend looked at it, before it opened it, is refused.
func TestOpenToSendRefusesSwap(t *testing.T) {
	root := linkTree(t, nil)
	p := filepath.Join(root, "repo", "img", "a.png")
	checkedHook = func() {
		if err := os.Remove(p); err != nil {
			t.Error(err)
		}
		if err := os.Symlink(filepath.Join(root, "secret", "key"), p); err != nil {
			t.Skipf("no symlink here (on Windows it needs developer mode; Linux CI runs this): %v", err)
		}
	}
	t.Cleanup(func() { checkedHook = nil })
	f, _, err := openToSend(p)
	if err == nil {
		f.Close()
	}
	if err == nil || !strings.HasSuffix(err.Error(), "changed as it was opened") {
		t.Errorf("get of a file swapped for a link: %v, want it refused", err)
	}
}
