package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gorex/internal/rex"
)

func updateCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("update", errOut)
	if code, done := parse(fs, args); done {
		return code, true
	}
	if serverTarget != "" || fs.NArg() != 0 {
		fs.Usage()
		return ExitUsage, true
	}
	if err := updateLocal(out); err != nil {
		fmt.Fprintln(errOut, "gorex update:", err)
		return ExitFailed, true
	}
	return ExitOK, true
}

func upgradeServer(c *rex.Client, hello rex.Hello, exe string, out io.Writer) error {
	if !hello.Upgrade {
		return errors.New("the running server cannot upgrade in place")
	}
	info, err := os.Stat(exe)
	if err != nil {
		return err
	}
	if hello.Exe == exe && hello.ExeTime.Equal(info.ModTime()) {
		fmt.Fprintln(out, "the server already runs this build")
		return nil
	}
	// Match the app's handoff rule: another path alone does not make a
	// newer build, and an older one may not understand the server's state.
	if !info.ModTime().After(hello.ExeTime) {
		return errors.New("the running server is as new as or newer than this build; use a newer build to upgrade")
	}
	next, err := c.Upgrade(exe)
	if err != nil {
		return err
	}
	next.Close()
	fmt.Fprintln(out, "upgraded in place; terminal sessions kept")
	return nil
}

// checkInstalledDir reports why the GoRex at exe cannot update itself. An
// update replaces the files next to the executable, so that directory must
// be writable by the user running the command: the per-user install of
// install.sh, or a system install that its owner updates.
func checkInstalledDir(exe string) error {
	dir := filepath.Dir(exe)
	probe, err := os.MkdirTemp(dir, ".gorex-update-")
	if err != nil {
		return fmt.Errorf("cannot write to %s, where GoRex is installed; run gorex update as the user that installed it: %w", dir, err)
	}
	return os.Remove(probe)
}
