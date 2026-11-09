//go:build darwin || linux

package rex

import (
	"io"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// A descriptor that code this process runs opened without close-on-exec
// does not reach a session's shell.
func TestPTYKeepsDescriptors(t *testing.T) {
	// High, past those ls opens to list them.
	const fd = 200
	null, err := syscall.Open("/dev/null", syscall.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(null)
	if err := unix.Dup2(null, fd); err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	p, err := startPTY("/bin/sh", []string{"sh", "-c", "ls /dev/fd; echo END"}, t.TempDir(), nil, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	var out strings.Builder
	b := make([]byte, 4096)
	for !strings.Contains(out.String(), "END") {
		n, err := p.read(b)
		out.Write(b[:n])
		if err == io.EOF || err != nil && n == 0 {
			break
		}
	}
	p.wait()
	for _, f := range strings.Fields(out.String()) {
		if f == strconv.Itoa(fd) {
			t.Fatalf("the shell has descriptor %d: %q", fd, out.String())
		}
	}
}
