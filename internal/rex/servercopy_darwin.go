//go:build darwin

package rex

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// The server of a build is a copy of the app, not the app itself.
//
// An update replaces the app's bundle while the server that ran from it
// goes on: the updater moves the old bundle away and the new one into its
// place. A server that ran from the app's bundle would have to be replaced
// with the app, before it could take the new build's place. A copy of its
// own, under Versions/<build>/, keeps the files of the build it serves; a
// new build's copy takes its place (Current), and the server upgrades to
// it when the app asks.
//
// Rex keeps its server the same way: …/RexServer/Versions/<build>/Rex
// Server.app with a Current beside it, and its launchd job runs
// …/Versions/Current/Rex Server.app/Contents/MacOS/rex.

// errNoCopy is what serverCopy returns for an executable that is not in an
// app bundle, as a test's or `go run . -server`'s: there is no bundle to
// copy, and the server runs from the executable itself.
var errNoCopy = errors.New("rex: not an app bundle")

// ServerExecutable is the executable a server of this build runs, and when
// this build was made: a copy of the app in Dir()/server/Versions/<build>/,
// with a Current beside it. It is the app's own executable when there is no
// app bundle to copy, or the copy cannot be made.
func ServerExecutable() (string, time.Time) {
	exe, err := os.Executable()
	if err != nil {
		return Executable()
	}
	copied, at, err := serverCopy(exe)
	if err != nil {
		if !errors.Is(err, errNoCopy) {
			log.Printf("rex: %v; the server runs from the app itself", err)
		}
		return Executable()
	}
	return copied, at
}

// serverCopy makes sure the copy of build src is in place, and returns its
// executable and the app's time of modification.
func serverCopy(src string) (string, time.Time, error) {
	src, at := executable(src)
	bundle, ok := appBundle(src)
	if !ok {
		return "", time.Time{}, errNoCopy
	}
	versions := filepath.Join(Dir(), "server", "Versions")
	if strings.HasPrefix(bundle, versions+string(filepath.Separator)) {
		return src, at, nil // a copy already: never a copy of a copy
	}
	// The app and a command run in a pane find no server at the same time:
	// one copies this build while the other waits.
	unlock, err := lockCopies()
	if err != nil {
		return "", time.Time{}, err
	}
	defer unlock()
	name, bin := filepath.Base(bundle), filepath.Base(src)
	id := buildID(bundle, src, at)
	dir := filepath.Join(versions, id, name)
	if _, err := os.Stat(filepath.Join(dir, "Contents", "MacOS", bin)); err != nil {
		if err := copyBundle(bundle, dir); err != nil {
			return "", time.Time{}, err
		}
		pruneVersions(versions, id)
	}
	if err := setCurrent(versions, id, name, bin); err != nil {
		return "", time.Time{}, err
	}
	return filepath.Join(versions, "Current", name, "Contents", "MacOS", bin), at, nil
}

// lockCopies keeps two processes from making or replacing the copies at
// once: one of them holds the file's lock until its copy and Current are
// in place.
func lockCopies() (func(), error) {
	dir := filepath.Join(Dir(), "server")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "copies.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		unix.Flock(int(f.Fd()), unix.LOCK_UN)
		f.Close()
	}, nil
}

// appBundle returns the app bundle an executable is in, its links followed.
func appBundle(exe string) (string, bool) {
	dir := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	if filepath.Ext(dir) != ".app" {
		return "", false
	}
	return dir, true
}

// buildID names the copy of a build: when the app's executable was made,
// and a hash of it, so that two builds of a second are told apart.
func buildID(bundle, exe string, at time.Time) string {
	size := int64(0)
	if fi, err := os.Stat(exe); err == nil {
		size = fi.Size()
	}
	key := bundle + "\x00" + strconv.FormatInt(size, 10) + "\x00" + strconv.FormatInt(at.UnixNano(), 10)
	sum := sha256.Sum256([]byte(key))
	return at.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(sum[:4])
}

// copyBundle copies the bundle src to dst: a clone where the volume has
// them (APFS), which writes the bytes once, with the times and modes of
// every file as they were, so that the copy is the same build, its
// signature included.
func copyBundle(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	_ = os.RemoveAll(dst)
	out, err := exec.Command("/bin/cp", "-Rpc", src, dst).CombinedOutput()
	if err == nil {
		return nil
	}
	// No clones on this volume: copy the files themselves.
	if out2, err2 := exec.Command("/bin/cp", "-Rp", src, dst).CombinedOutput(); err2 != nil {
		_ = os.RemoveAll(dst)
		return fmt.Errorf("rex: copying %s to %s: %w: %s %s", src, dst, err2,
			strings.TrimSpace(string(out)), strings.TrimSpace(string(out2)))
	}
	return nil
}

// setCurrent makes Versions/Current the copy of build id: the server's job
// runs the server through Current, so its arguments never change, and a
// build replaces the copy beside the one it leaves.
func setCurrent(versions, id, name, bin string) error {
	marker := filepath.Join(versions, ".current")
	there := func() bool {
		_, err := os.Stat(filepath.Join(versions, "Current", name, "Contents", "MacOS", bin))
		return err == nil
	}
	if b, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(b)) == id && there() {
		return nil
	}
	tmp := filepath.Join(versions, ".current-"+strconv.Itoa(os.Getpid()))
	_ = os.RemoveAll(tmp)
	if err := copyBundle(filepath.Join(versions, id, name), filepath.Join(tmp, name)); err != nil {
		return err
	}
	// A server that runs from Current goes on: its files are open, and a
	// name that leads nowhere changes nothing for it.
	_ = os.RemoveAll(filepath.Join(versions, "Current"))
	if err := os.Rename(tmp, filepath.Join(versions, "Current")); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	return os.WriteFile(marker, []byte(id+"\n"), 0o600)
}

// pruneVersions removes the copies of the builds before id: a build's copy
// is its own, and a server that still runs from one keeps its files open.
func pruneVersions(versions, id string) {
	ents, err := os.ReadDir(versions)
	if err != nil {
		return
	}
	for _, e := range ents {
		if e.Name() == id || e.Name() == "Current" || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		_ = os.RemoveAll(filepath.Join(versions, e.Name()))
	}
}
