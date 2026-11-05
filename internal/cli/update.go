package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

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
	next, err := c.Upgrade(exe)
	if err != nil {
		return err
	}
	next.Close()
	fmt.Fprintln(out, "upgraded in place; terminal sessions kept")
	return nil
}
