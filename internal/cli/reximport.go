package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gorex/internal/rex"
	"gorex/internal/reximp"
	"gorex/internal/rexpeer"
)

// stringList collects a repeated flag.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// rexImportCmd makes GoRex spaces, tabs and panes of a Rex server's
// sessions: each pane mirrors a Rex block, or runs a shell in its
// directory.
func rexImportCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("rex-import", errOut)
	rexSocket := fs.String("rex", "", "the Rex server's socket (default REX_SERVER)")
	shell := fs.Bool("shell", false, "run a shell in each block's directory instead of mirroring the block")
	resize := fs.Bool("resize", false, "give a mirrored Rex block the pane's size")
	asJSON := fs.Bool("json", false, "print JSON")
	var sessions stringList
	fs.Var(&sessions, "session", "import a session, by label or ID (repeatable; every session when none)")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	sock := *rexSocket
	if sock == "" {
		sock = defaultRexSocket()
	}
	if sock == "" {
		fmt.Fprintln(errOut, "gorex rex-import: no Rex server: set --rex or REX_SERVER")
		return ExitUsage, true
	}
	ctx := context.Background()
	peer, err := rexpeer.Dial(ctx, sock)
	if err != nil {
		fmt.Fprintf(errOut, "gorex rex-import: %v\n", err)
		return ExitFailed, true
	}
	defer peer.Close()
	gorex, code := connect(errOut)
	if gorex == nil {
		return code, true
	}
	defer gorex.Close()
	res, err := reximp.Run(ctx, gorex, peer, reximp.Options{
		Sessions:  sessions,
		Shell:     *shell,
		Resize:    *resize,
		Mirror:    mirrorCommand(sock),
		StatePath: filepath.Join(rex.Dir(), "rex-import.json"),
		RexSocket: sock,
	})
	if err != nil {
		fmt.Fprintf(errOut, "gorex rex-import: %v\n", err)
		return ExitFailed, true
	}
	if *asJSON {
		WriteJSON(out, res)
		return ExitOK, true
	}
	for _, s := range res.Skipped {
		fmt.Fprintf(out, "skipped %q: %s\n", s.Label, s.Why)
	}
	for _, s := range res.Imported {
		fmt.Fprintf(out, "imported %q: space %s, %d windows, %d blocks\n", s.Label, s.Space, s.Windows, s.Blocks)
	}
	if len(res.Imported) == 0 && len(res.Skipped) == 0 {
		fmt.Fprintln(out, "no sessions to import")
	}
	return ExitOK, true
}

// mirrorCommand is the command a pane runs to mirror a Rex block: this
// program, as rex-mirror.
func mirrorCommand(sock string) []string {
	exe, err := os.Executable()
	if err != nil {
		exe = "gorex"
	}
	return []string{exe, "rex-mirror", "--socket", sock}
}
