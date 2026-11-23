package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"gorex/internal/iconpack"
)

func packCmd(args []string, out, errOut io.Writer) (int, bool) {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "gorex pack: use ls, install, or rm")
		return ExitUsage, true
	}
	packsDir := iconpack.Dir()
	switch args[0] {
	case "ls", "list":
		fs := flagSet("pack ls", errOut)
		asJSON := fs.Bool("json", false, "print JSON")
		if code, stop := parse(fs, args[1:]); stop {
			return code, true
		}
		list, err := iconpack.ListPacks(packsDir)
		if err != nil {
			fmt.Fprintf(errOut, "gorex pack ls: %v\n", err)
			return ExitFailed, true
		}
		if *asJSON {
			if list == nil {
				list = []iconpack.Summary{}
			}
			WriteJSON(out, list)
			return ExitOK, true
		}
		for _, s := range list {
			if s.Error != "" {
				fmt.Fprintf(out, "%s\tinvalid\t%s\n", s.ID, s.Error)
				continue
			}
			fmt.Fprintf(out, "%s\t%s\t%s\t%d entries\n", s.ID, s.Name, s.Version, s.Entries)
		}
		return ExitOK, true
	case "install":
		fs := flagSet("pack install", errOut)
		name := fs.String("name", "", "override installed pack ID")
		asJSON := fs.Bool("json", false, "print JSON")
		if code, stop := parse(fs, args[1:]); stop {
			return code, true
		}
		if fs.NArg() != 1 {
			fmt.Fprintln(errOut, "gorex pack install: pass a local directory or git repository URL")
			return ExitUsage, true
		}
		src := fs.Arg(0)
		var pk *iconpack.Pack
		var err error
		if st, statErr := os.Stat(src); statErr == nil && st.IsDir() {
			pk, err = iconpack.InstallLocal(src, packsDir, *name)
		} else if isGitURL(src) {
			pk, err = iconpack.InstallGit(src, packsDir, *name)
		} else {
			fmt.Fprintf(errOut, "gorex pack install: %q is not an existing directory or git URL\n", src)
			return ExitUsage, true
		}
		if err != nil {
			fmt.Fprintf(errOut, "gorex pack install: %v\n", err)
			return ExitFailed, true
		}
		if *asJSON {
			WriteJSON(out, pk.Manifest)
			return ExitOK, true
		}
		fmt.Fprintf(out, "installed %s (%s)\n", pk.ID, pk.Dir)
		return ExitOK, true
	case "rm", "remove":
		fs := flagSet("pack rm", errOut)
		if code, stop := parse(fs, args[1:]); stop {
			return code, true
		}
		if fs.NArg() != 1 {
			fmt.Fprintln(errOut, "gorex pack rm: pass the pack ID to remove")
			return ExitUsage, true
		}
		if err := iconpack.Remove(packsDir, fs.Arg(0)); err != nil {
			fmt.Fprintf(errOut, "gorex pack rm: %v\n", err)
			return ExitFailed, true
		}
		return ExitOK, true
	default:
		fmt.Fprintf(errOut, "gorex pack: unknown subcommand %q\n", args[0])
		return ExitUsage, true
	}
}

func isGitURL(s string) bool {
	return strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "http://") ||
		strings.HasPrefix(s, "ssh://") ||
		strings.HasPrefix(s, "git@") ||
		strings.HasSuffix(s, ".git")
}
