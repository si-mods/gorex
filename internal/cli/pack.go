package cli

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gorex/internal/config"
	"gorex/internal/iconpack"
	"gorex/internal/theme"
)

// kindIconPack is the Kind `gorex pack ls` gives an icon pack.
const kindIconPack = "icon-pack"

func packCmd(args []string, out, errOut io.Writer) (int, bool) {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "gorex pack: use ls, search, install, or rm")
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
		for i := range list {
			list[i].Kind = kindIconPack
		}
		themesDir := theme.Dir(config.Dir())
		for _, t := range theme.List(themesDir) {
			list = append(list, iconpack.Summary{
				ID: t.ID, Kind: iconpack.KindTheme, Name: t.Name, Error: t.Error,
				Dir: filepath.Join(themesDir, t.ID+".json"),
			})
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
				fmt.Fprintf(out, "%s	invalid	%s	%s\n", s.ID, s.Error, s.Kind)
				continue
			}
			if s.Kind == iconpack.KindTheme {
				fmt.Fprintf(out, "%s	%s			%s\n", s.ID, s.Name, s.Kind)
				continue
			}
			fmt.Fprintf(out, "%s	%s	%s	%d entries	%s\n", s.ID, s.Name, s.Version, s.Entries, s.Kind)
		}
		return ExitOK, true
	case "search":
		fs := flagSet("pack search", errOut)
		asJSON := fs.Bool("json", false, "print JSON")
		if code, stop := parse(fs, args[1:]); stop {
			return code, true
		}
		query := strings.Join(fs.Args(), " ")
		idx, err := iconpack.FetchRegistry(context.Background(), "")
		if err != nil {
			fmt.Fprintf(errOut, "gorex pack search: %v\n", err)
			return ExitFailed, true
		}
		matches := iconpack.SearchRegistry(idx, query)
		for _, p := range iconpack.SearchRegistry(&iconpack.RegistryIndex{Packs: idx.Themes}, query) {
			p.Kind = iconpack.KindTheme
			matches = append(matches, p)
		}
		if *asJSON {
			if matches == nil {
				matches = []iconpack.RegistryPack{}
			}
			WriteJSON(out, matches)
			return ExitOK, true
		}
		for _, p := range matches {
			fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", p.ID, p.Name, p.Version, p.Description)
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
			fmt.Fprintln(errOut, "gorex pack install: pass a pack ID, local directory, or git URL")
			return ExitUsage, true
		}
		src := fs.Arg(0)
		var pk *iconpack.Pack
		var err error
		if st, statErr := os.Stat(src); statErr == nil && st.IsDir() {
			if _, e := os.Stat(filepath.Join(src, theme.PackFile)); e == nil {
				id := cmp.Or(*name, filepath.Base(filepath.Clean(src)))
				return installTheme(id, out, errOut, func(dir string) ([]string, error) { return theme.Install(src, dir, id) })
			}
			pk, err = iconpack.InstallLocal(src, packsDir, *name)
		} else if isGitURL(src) {
			pk, err = iconpack.InstallGit(src, packsDir, *name)
		} else if iconpack.ValidID(src) {
			var idx *iconpack.RegistryIndex
			idx, err = iconpack.FetchRegistry(context.Background(), "")
			if err == nil {
				var rp iconpack.RegistryPack
				rp, err = iconpack.LookupRegistry(idx, src)
				if err == nil && rp.Kind == iconpack.KindTheme {
					id := cmp.Or(*name, rp.ID)
					return installTheme(id, out, errOut, func(dir string) (warn []string, err error) {
						err = iconpack.Checkout(context.Background(), rp, func(src string) (err error) {
							warn, err = theme.Install(src, dir, id)
							return err
						})
						return warn, err
					})
				}
				if err == nil {
					pk, err = iconpack.InstallFromRegistry(context.Background(), rp, packsDir, *name)
				}
			}
		} else {
			fmt.Fprintf(errOut, "gorex pack install: %q is not a valid pack ID, directory, or git URL\n", src)
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
		id := fs.Arg(0)
		// An ID names an icon pack, a theme, or (rarely) both; remove each.
		themesDir := theme.Dir(config.Dir())
		removed := false
		if iconpack.ValidID(id) {
			if _, err := os.Stat(filepath.Join(packsDir, id)); err == nil {
				if err := iconpack.Remove(packsDir, id); err != nil {
					fmt.Fprintf(errOut, "gorex pack rm: %v\n", err)
					return ExitFailed, true
				}
				removed = true
			}
		}
		if p, err := theme.Path(themesDir, id); err == nil {
			if _, err := os.Stat(p); err == nil {
				if err := theme.Remove(themesDir, id); err != nil {
					fmt.Fprintf(errOut, "gorex pack rm: %v\n", err)
					return ExitFailed, true
				}
				removed = true
			}
		}
		if !removed {
			fmt.Fprintf(errOut, "gorex pack rm: no icon pack or theme %q\n", id)
			return ExitFailed, true
		}
		return ExitOK, true
	default:
		fmt.Fprintf(errOut, "gorex pack: unknown subcommand %q\n", args[0])
		return ExitUsage, true
	}
}

// installTheme installs a theme pack with install, into the themes folder
// of the config directory, and says what appearance.theme names it with
// (docs/DSK-008-theme.md).
func installTheme(id string, out, errOut io.Writer, install func(dir string) ([]string, error)) (int, bool) {
	dir := theme.Dir(config.Dir())
	warn, err := install(dir)
	if err != nil {
		fmt.Fprintf(errOut, "gorex pack install: %v\n", err)
		return ExitFailed, true
	}
	for _, w := range warn {
		fmt.Fprintf(errOut, "gorex pack install: %s: %s (dropped)\n", id, w)
	}
	fmt.Fprintf(out, "installed theme %s (%s); set \"appearance.theme\": %q to use it\n", id, filepath.Join(dir, id+".json"), id)
	return ExitOK, true
}

func isGitURL(s string) bool {
	return strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "http://") ||
		strings.HasPrefix(s, "ssh://") ||
		strings.HasPrefix(s, "git@") ||
		strings.HasSuffix(s, ".git")
}
