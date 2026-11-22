package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"gorex/internal/rex"
)

func updateLocal(out io.Writer) error {
	exe, _ := rex.Executable()
	if err := checkInstalledDir(exe); err != nil {
		return err
	}
	if pkg := packageOf(exe); pkg != "" {
		fmt.Fprintf(out, "note: %s is part of the %s package; that package replaces this build at its next upgrade\n", exe, pkg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	release, err := mygo.Updater.Check(ctx)
	if err != nil {
		return err
	}
	if release == nil {
		fmt.Fprintln(out, "GoRex is up to date")
		return nil
	}
	client, err := rex.ConnectExisting()
	if err != nil {
		if _, err := os.Lstat(rex.SocketPath()); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("cannot reach the local session server; check %s; if no GoRex server is running, remove the stale socket %s and retry gorex update", filepath.Join(rex.Dir(), "server.log"), rex.SocketPath())
		}
	} else {
		defer client.Close()
		hello, err := client.Hello()
		if err != nil {
			return err
		}
		if !hello.Upgrade {
			return errors.New("the running server cannot upgrade in place")
		}
	}

	fmt.Fprintf(out, "Installing GoRex %s\n", release.Version)
	if err := release.Install(ctx, nil); err != nil {
		return err
	}
	// Reconnect in case the old connection was lost during the download
	// or a server started while the update was being installed.
	client, err = rex.ConnectExisting()
	if err != nil {
		if _, err := os.Lstat(rex.SocketPath()); errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return errors.New("the update was installed, but the server could not be reached; retry with gorex server upgrade")
	}
	defer client.Close()
	hello, err := client.Hello()
	if err == nil {
		err = upgradeServer(client, hello, exe, out)
	}
	if err != nil {
		return fmt.Errorf("the update was installed, but the server could not upgrade; retry with gorex server upgrade: %w", err)
	}
	return nil
}

// packageOf returns the dpkg package that installed exe, or "" when none did,
// or when the system has no dpkg. An update of the install leaves that
// package's records behind, so the command says so first.
func packageOf(exe string) string {
	dpkg, err := exec.LookPath("dpkg-query")
	if err != nil {
		return ""
	}
	data, err := exec.Command(dpkg, "-S", exe).Output()
	if err != nil {
		return ""
	}
	name, _, _ := strings.Cut(strings.TrimSpace(string(data)), ": ")
	return strings.TrimSpace(name)
}
