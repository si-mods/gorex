package rexapi

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gorex/internal/rex"
)

// TestDirMkdirMethod checks gorex.dir.mkdir: the welcome lists it, it
// makes a directory with its parents as the sessions' user, making one
// that is there is no error, and the help documents describe it.
func TestDirMkdirMethod(t *testing.T) {
	home := dirListHome(t)
	f := startFixture(t)
	c := f.dialControl()
	c.hello(apiRevision)
	if features, _ := c.welcome["features"].([]any); !slices.Contains(features, any(featureDirMkdir)) {
		t.Fatalf("the welcome's features %v lack %s", features, featureDirMkdir)
	}

	nested := filepath.Join(home, "src", "new", "deep")
	callChecked(t, c, "gorex.dir.mkdir", map[string]any{"path": "~/src/new/deep"})
	if st, err := os.Stat(nested); err != nil || !st.IsDir() {
		t.Fatalf("no folder at %q: %v", nested, err)
	}
	// One already there is a success.
	callChecked(t, c, "gorex.dir.mkdir", map[string]any{"path": nested})
	r := callChecked(t, c, "gorex.dir.list", map[string]any{"path": "~/src/new"})
	if dirs, _ := r["dirs"].([]any); !slices.Contains(dirs, any("deep")) {
		t.Errorf("~/src/new lists %v, no deep", dirs)
	}

	for _, payload := range []map[string]any{nil, {"path": ""}, {"path": 3}, {"path": "~/x", "extra": 1}} {
		if e := failure(t, c.request("bad", "gorex.dir.mkdir", payload)); e["code"] != "bad_request" {
			t.Errorf("%v: %v, want bad_request", payload, e)
		}
	}

	if schemaFor(t, "gorex.dir.mkdir") == nil {
		t.Error("gorex.dir.mkdir has no result schema")
	}
	res, err := httpGet(f.srv.URL + "/llms.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if body, _ := io.ReadAll(res.Body); !strings.Contains(string(body), "### gorex.dir.mkdir") {
		t.Error("/llms.txt does not describe gorex.dir.mkdir")
	}
}

// TestDirMkdirOldServer: over a GoRex server that does not make
// directories, the welcome lacks gorex.dir_mkdir and the method fails
// with not_supported.
func TestDirMkdirOldServer(t *testing.T) {
	dirListHome(t)
	f := startFixture(t)
	f.g.hello.Capabilities = slices.DeleteFunc(slices.Clone(f.g.hello.Capabilities), func(s string) bool {
		return s == rex.CapabilityDirMkdir
	})
	c := f.dialControl()
	c.hello(apiRevision)
	if features, _ := c.welcome["features"].([]any); slices.Contains(features, any(featureDirMkdir)) {
		t.Errorf("the welcome lists %s without the capability: %v", featureDirMkdir, features)
	}
	if e := failure(t, c.request("old", "gorex.dir.mkdir", map[string]any{"path": "~/x"})); e["code"] != "not_supported" {
		t.Errorf("gorex.dir.mkdir on an old server: %v, want not_supported", e)
	}
}
