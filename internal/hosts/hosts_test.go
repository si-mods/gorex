package hosts

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func write(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(Path(dir), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The app, the CLI and the server read one rule: a host with no ID or
// address, or one a coordinator network brought, is no host.
func TestLoadLeavesOutWhatTheAppDropped(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `{"hosts":[
		{"id":"h1","name":"Studio","address":"tcat://x"},
		{"id":"","name":"no id","address":"tcat://y"},
		{"id":"h3","name":"no address"},
		{"id":"net:abc","name":"network","address":"tcat://z"}
	]}`)
	list, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "h1" {
		t.Fatalf("Load = %+v, want only h1", list)
	}
}

func TestLoadNoneAndInvalid(t *testing.T) {
	dir := t.TempDir()
	if list, err := Load(dir); err != nil || list != nil {
		t.Errorf("Load of no file = %+v, %v", list, err)
	}
	write(t, dir, "{")
	if _, err := Load(dir); !errors.Is(err, ErrInvalid) {
		t.Errorf("Load of a broken file = %v, want ErrInvalid", err)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	in := []Host{{ID: "h1", Name: "Studio", Alias: "dev", Address: "tcat://x", Machine: "m1", Kills: []string{"s1"}}}
	if err := Save(dir, in); err != nil {
		t.Fatal(err)
	}
	out, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].ID != "h1" || out[0].Machine != "m1" || len(out[0].Kills) != 1 || out[0].Label() != "dev" {
		t.Fatalf("Load after Save = %+v", out)
	}
	if fi, err := os.Stat(Path(dir)); err != nil || runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("hosts.json mode = %v, %v, want 0600", fi, err)
	}
}

// A file that does not parse holds addresses that cannot be had again:
// Save leaves it as it is.
func TestSaveRefusesOverInvalid(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "{")
	if err := Save(dir, []Host{{ID: "h1", Address: "tcat://x"}}); err == nil {
		t.Fatal("Save wrote over a file that does not parse")
	}
}
