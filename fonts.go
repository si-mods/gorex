package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"golang.org/x/image/font/sfnt"
)

// The fonts the app draws the terminals' text in: a family is drawn only
// once the system can activate its files, and one macOS has not activated
// — a download it quarantined, so that the Finder asks before an app uses
// it — is drawn in another family instead, its icons of the private use
// areas as boxes. As Ghostty does, GoRex reads the files of a configured
// family that the system does not list, and registers them.

// fontDirs are the directories fonts are installed in, this user's own
// first.
func fontDirs() []string {
	var dirs []string
	home, err := os.UserHomeDir()
	if err == nil {
		switch runtime.GOOS {
		case "darwin":
			dirs = append(dirs, filepath.Join(home, "Library", "Fonts"))
		case "windows":
			dirs = append(dirs, filepath.Join(home, "AppData", "Local", "Microsoft", "Windows", "Fonts"))
		default:
			dirs = append(dirs, filepath.Join(home, ".local", "share", "fonts"), filepath.Join(home, ".fonts"))
		}
	}
	switch runtime.GOOS {
	case "darwin":
		dirs = append(dirs, "/Library/Fonts", "/System/Library/Fonts", "/System/Library/Fonts/Supplemental")
	case "windows":
		if windir := os.Getenv("WINDIR"); windir != "" {
			dirs = append(dirs, filepath.Join(windir, "Fonts"))
		}
	default:
		dirs = append(dirs, "/usr/share/fonts", "/usr/local/share/fonts")
	}
	return dirs
}

// fontFaces are the subfamilies the app registers: the upright faces of
// the weights its text is drawn in. A family without one of them draws in
// the faces it has.
var fontFaces = []string{"regular", "medium", "bold", "italic", "medium italic", "bold italic"}

// fontTried are the families registerFamilies has looked for. The system's
// list of families holds until the app registers a font, so a family is
// looked for once, and a font file installed while the app runs shows
// after a restart.
var fontTried = map[string]bool{}

// registerFamilies registers the files of families in dirs that the
// system does not list: one the system lists has its files activated
// already.
func registerFamilies(dirs, families []string) {
	var lookup []string
	for _, family := range families {
		key := strings.ToLower(strings.TrimSpace(family))
		if key == "" || fontTried[key] {
			continue
		}
		fontTried[key] = true
		lookup = append(lookup, family)
	}
	if len(lookup) == 0 {
		return
	}
	listed := map[string]bool{}
	for _, f := range ui.FontFamilies() {
		listed[strings.ToLower(f.Name)] = true
	}
	for _, family := range lookup {
		if listed[strings.ToLower(strings.TrimSpace(family))] {
			continue
		}
		for _, data := range familyFaces(dirs, family) {
			ui.RegisterFont(data, family)
		}
	}
}

// familyFaces is the data of family's faces in dirs: the files whose name
// says the family's, and whose name table names the family and one of the
// faces the app draws with. A file is read only when its name says the
// family's, as the name table is in the file; the table decides, so that
// a family whose name holds another's — Maple Mono NF and Maple Mono NF CN
// — takes not the other's files.
func familyFaces(dirs []string, family string) [][]byte {
	key := fontKey(family)
	var out [][]byte
	taken := map[string]bool{}
	for _, dir := range dirs {
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				// Two levels: the user's own directories, and those the
				// distributions keep fonts in, which nest.
				if rel, e := filepath.Rel(dir, path); e == nil && strings.Count(rel, string(filepath.Separator)) >= 2 {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.Contains(fontKey(d.Name()), key) {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			// The name table is the font's; a collection's first font is
			// not the family a user asks for.
			f, err := sfnt.Parse(data)
			if err != nil {
				return nil
			}
			if !strings.EqualFold(nameField(f, sfnt.NameIDTypographicFamily, sfnt.NameIDFamily), family) {
				return nil
			}
			face := strings.ToLower(nameField(f, sfnt.NameIDTypographicSubfamily, sfnt.NameIDSubfamily))
			if !slices.Contains(fontFaces, face) || taken[face] {
				return nil
			}
			taken[face] = true
			out = append(out, data)
			return nil
		})
	}
	return out
}

// nameField is the font's name of the first of ids it has.
func nameField(f *sfnt.Font, ids ...sfnt.NameID) string {
	var buf sfnt.Buffer
	for _, id := range ids {
		if s, err := f.Name(&buf, id); err == nil && s != "" {
			return s
		}
	}
	return ""
}

// fontKey is a family's or a file's name as fonts spell them differently:
// lowercase, without the spaces and punctuation a file name has instead.
func fontKey(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 0x7f {
			b.WriteRune(r)
		}
	}
	return b.String()
}
