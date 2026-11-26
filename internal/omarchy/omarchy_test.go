package omarchy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorex/internal/theme"
)

// darkPalette is a palette as Omarchy writes it, with a comment, a blank
// line and a key the manifest does not use.
const darkPalette = `# Tokyo Night
mode = "dark"
accent = "#7aa2f7"
background = "#1a1b26"
foreground = "#c0caf5"
muted = "#565f89"
selection = "#283457"
lighter_background = "#24283b"
border = "#3b4261"
`

// legacyPalette is a palette of the ANSI names only, as an older theme
// writes it.
const legacyPalette = `color0 = "#1a1b26"
color4 = "#7aa2f7"
color7 = "#c0caf5"
color8 = "#565f89"
`

// testPaths makes an Omarchy desktop and a GoRex home in temporary folders,
// with colors as the palette and name as the theme's name.
func testPaths(t *testing.T, colors, name string) Paths {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "version"), "3.0.0\n", 0o644)
	writeTestFile(t, filepath.Join(root, "bin", "omarchy-theme-current"), "#!/bin/sh\n", 0o755)
	state := t.TempDir()
	writeTestFile(t, filepath.Join(state, "current", "theme", "colors.toml"), colors, 0o644)
	writeTestFile(t, filepath.Join(state, "current", "theme.name"), name+"\n", 0o644)
	gorex := t.TempDir()
	return Paths{
		OmarchyRoot:  root,
		StateDir:     state,
		ConfigHome:   filepath.Join(t.TempDir(), ".config"),
		SettingsPath: filepath.Join(gorex, "settings.json"),
	}
}

func writeTestFile(t *testing.T, path, body string, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), perm); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestPaletteCascade is the doc's palette table: each fallback in order, a
// legacy ANSI-only palette, and a palette with no accent.
func TestPaletteCascade(t *testing.T) {
	cases := []struct {
		name   string
		colors string
		want   Palette
	}{
		{
			name:   "dark",
			colors: darkPalette,
			want: Palette{
				Name: "Tokyo Night", Mode: "dark",
				Background: "#1a1b26", LighterBackground: "#24283b", DarkerBackground: "#0d0e13",
				Foreground: "#c0caf5", Muted: "#565f89",
				Accent: "#7aa2f7", Selection: "#283457",
				Red: "#7aa2f7", Green: "#7aa2f7", Yellow: "#7aa2f7",
			},
		},
		{
			name:   "legacy ANSI names",
			colors: legacyPalette,
			want: Palette{
				Name: "Tokyo Night", Mode: "dark",
				Background: "#1a1b26", LighterBackground: "#1a1b26", DarkerBackground: "#0d0e13",
				Foreground: "#c0caf5", Muted: "#565f89",
				Accent: "#7aa2f7", Selection: "#565f89",
				Red: "#7aa2f7", Green: "#7aa2f7", Yellow: "#7aa2f7",
			},
		},
		{
			name:   "no accent",
			colors: strings.Replace(strings.Replace(darkPalette, `accent = "#7aa2f7"`, "", 1), `mode = "dark"`, `mode = "light"`, 1),
			want: Palette{
				Name: "Tokyo Night", Mode: "light",
				Background: "#1a1b26", LighterBackground: "#24283b", DarkerBackground: "#0d0e13",
				Foreground: "#c0caf5", Muted: "#565f89",
				Accent: "#c0caf5", Selection: "#283457",
				Red: "#c0caf5", Green: "#c0caf5", Yellow: "#c0caf5",
			},
		},
		{
			name: "a value that is not a color is dropped",
			colors: `mode = "dark"
accent = "blue"
background = "#010203"
color4 = "#040506"
lighter_bg = "#070809"
foreground = "#0a0b0c"
`,
			want: Palette{
				Name: "Tokyo Night", Mode: "dark",
				Background: "#010203", LighterBackground: "#070809", DarkerBackground: "#010102",
				Foreground: "#0a0b0c", Muted: "#0a0b0c",
				Accent: "#040506", Selection: "#010203",
				Red: "#040506", Green: "#040506", Yellow: "#040506",
			},
		},
		{
			name: "dark foreground is the muted fallback",
			colors: `background = "#000000"
foreground = "#ffffff"
dark_foreground = "#888888"
`,
			want: Palette{
				Name: "Tokyo Night", Mode: "dark",
				Background: "#000000", LighterBackground: "#000000", DarkerBackground: "#000000",
				Foreground: "#ffffff", Muted: "#888888",
				Accent: "#ffffff", Selection: "#000000",
				Red: "#ffffff", Green: "#ffffff", Yellow: "#ffffff",
			},
		},
		{
			name: "unquoted values read as omarchy-theme-color reads them",
			colors: `mode = dark
background = #101010
foreground = #e0e0e0
accent = #123456
`,
			want: Palette{
				Name: "Tokyo Night", Mode: "dark",
				Background: "#101010", LighterBackground: "#101010", DarkerBackground: "#080808",
				Foreground: "#e0e0e0", Muted: "#e0e0e0",
				Accent: "#123456", Selection: "#101010",
				Red: "#123456", Green: "#123456", Yellow: "#123456",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReadPalette(testPaths(t, tc.colors, "Tokyo Night"))
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("ReadPalette =\n%+v\nwant\n%+v", got, tc.want)
			}
		})
	}
}

// TestPaletteMode is the doc's four mode rules, in their order.
func TestPaletteMode(t *testing.T) {
	cases := []struct {
		name      string
		colors    string
		lightMode bool
		want      string
	}{
		{"mode dark wins over the rest", "mode = \"dark\"\ntheme_type = \"light\"\nbackground = \"#ffffff\"\n", true, "dark"},
		{"mode light wins over a dark background", "mode = \"light\"\nbackground = \"#000000\"\n", false, "light"},
		{"theme_type light", "theme_type = \"light\"\nbackground = \"#000000\"\n", false, "light"},
		{"theme_type dark", "theme_type = \"dark\"\nbackground = \"#ffffff\"\n", false, "dark"},
		{"light.mode beside colors.toml", "background = \"#000000\"\n", true, "light"},
		{"a light background", "background = \"#ffffff\"\n", false, "light"},
		{"a dark background", "background = \"#1a1b26\"\n", false, "dark"},
		{"no background", "", false, "dark"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := testPaths(t, tc.colors, "Tokyo Night")
			if tc.lightMode {
				writeTestFile(t, filepath.Join(p.StateDir, "current", "theme", "light.mode"), "", 0o644)
			}
			pal, err := ReadPalette(p)
			if err != nil {
				t.Fatal(err)
			}
			if pal.Mode != tc.want {
				t.Errorf("mode = %q, want %q", pal.Mode, tc.want)
			}
		})
	}
}

// TestManifest is the manifest's shape, made in memory and read back as a
// theme pack: the name, the description, the seeds of the mode only, and
// the selection as the sidebar's active tab.
func TestManifest(t *testing.T) {
	p := testPaths(t, darkPalette, "Tokyo Night")
	pal, err := ReadPalette(p)
	if err != nil {
		t.Fatal(err)
	}
	m := Manifest(pal)
	if m.Name != Name || m.Description != "Omarchy theme Tokyo Night (generated by gorex)" {
		t.Errorf("the manifest is %q %q", m.Name, m.Description)
	}
	wantSeeds := map[theme.Seed]string{
		theme.BgTop: "#1a1b26", theme.Panel: "#24283b", theme.Text: "#c0caf5",
		theme.TextMuted: "#565f89", theme.TextFaint: "#565f89", theme.IconMuted: "#565f89",
		theme.Accent: "#7aa2f7", theme.Danger: "#7aa2f7", theme.Done: "#7aa2f7", theme.Attention: "#7aa2f7",
	}
	if got := m.Seeds[1]; len(got) != len(wantSeeds) {
		t.Errorf("dark seeds: %v", got)
	}
	for seed, want := range wantSeeds {
		if got := m.Seeds[1][seed]; got != mustHex(t, want) {
			t.Errorf("%s: %v, want %v", seed, got, want)
		}
	}
	if got := m.Seeds[0]; len(got) != 0 {
		t.Errorf("the light appearance has seeds: %v", got)
	}
	wantRoles := map[theme.Role]string{
		theme.TabActiveBackground:  "#283457",
		theme.SideBarBackground:    "#1a1b26",
		theme.PaneHeaderBackground: "#24283b",
		theme.PaneBorder:           "#283457",
	}
	for i := range m.Roles {
		for role, want := range wantRoles {
			if got := m.Roles[i][role]; got != mustHex(t, want) {
				t.Errorf("role %d %v: %v, want %v", i, role, got, want)
			}
		}
	}

	// A light palette seeds the light appearance.
	pal.Mode = "light"
	if got := Manifest(pal).Seeds[0][theme.BgTop]; got != mustHex(t, "#1a1b26") {
		t.Errorf("light background: %v", got)
	}
	if got := Manifest(pal).Seeds[1]; len(got) != 0 {
		t.Errorf("the dark appearance has seeds: %v", got)
	}

	// A key the palette does not resolve leaves the seed and the role out.
	sparse := Manifest(Palette{Name: "Sparse", Mode: "dark", Background: "#010203"})
	if len(sparse.Seeds[1]) != 1 || sparse.Seeds[1][theme.BgTop] != mustHex(t, "#010203") {
		t.Errorf("sparse seeds: %v", sparse.Seeds[1])
	}
	// Only the background resolves, so only its role is set.
	for i := range sparse.Roles {
		if len(sparse.Roles[i]) != 1 || sparse.Roles[i][theme.SideBarBackground] != mustHex(t, "#010203") {
			t.Errorf("sparse roles: %v %v", sparse.Roles[0], sparse.Roles[1])
		}
	}
}

func mustHex(t *testing.T, s string) ui.Color {
	t.Helper()
	c, ok := theme.Hex(s)
	if !ok {
		t.Fatalf("%q is not a color", s)
	}
	return c
}

func TestDetect(t *testing.T) {
	p := testPaths(t, darkPalette, "Tokyo Night")
	if err := Detect(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(p.OmarchyRoot, "bin", "omarchy-theme-current")); err != nil {
		t.Fatal(err)
	}
	if err := Detect(p); err == nil || !strings.Contains(err.Error(), "not an Omarchy system") {
		t.Fatalf("Detect = %v", err)
	}
}

// TestReadPaletteErrors refuses a palette with no name or no file.
func TestReadPaletteErrors(t *testing.T) {
	if _, err := ReadPalette(testPaths(t, darkPalette, " ")); err == nil {
		t.Error("a palette with no theme name reads")
	}
	p := testPaths(t, darkPalette, "Tokyo Night")
	if err := os.Remove(filepath.Join(p.CurrentDir(), "theme", "colors.toml")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPalette(p); err == nil {
		t.Error("a palette with no colors.toml reads")
	}
}

// TestPaletteAfterSwap follows the doc's watch case without a watch: a
// renamed theme directory resolves a different palette, and the same
// content resolves the same one.
func TestPaletteAfterSwap(t *testing.T) {
	p := testPaths(t, darkPalette, "Tokyo Night")
	first, err := ReadPalette(p)
	if err != nil {
		t.Fatal(err)
	}
	next := filepath.Join(p.CurrentDir(), "theme.next")
	writeTestFile(t, filepath.Join(next, "colors.toml"), `mode = "light"
background = "#f5f6f8"
foreground = "#1a1d21"
`, 0o644)
	writeTestFile(t, filepath.Join(next, "theme.name"), "Paper\n", 0o644)
	// Omarchy renders the next theme in a sibling directory and renames it
	// into place; the swap replaces the old theme's name too.
	if err := os.Rename(filepath.Join(p.CurrentDir(), "theme"), filepath.Join(p.CurrentDir(), "theme.old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, filepath.Join(p.CurrentDir(), "theme")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, p.themeNamePath(), "Paper\n", 0o644)
	second, err := ReadPalette(p)
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("the swap did not change the palette")
	}
	third, err := ReadPalette(p)
	if err != nil {
		t.Fatal(err)
	}
	if third != second {
		t.Errorf("the same content resolved another palette:\n%+v\n%+v", second, third)
	}
}

// TestBind inserts, replaces and removes the block and keeps every other
// line.
func TestBind(t *testing.T) {
	p := testPaths(t, darkPalette, "Tokyo Night")
	const user = "o.bind(\"SUPER + RETURN\", \"Terminal\", \"alacritty\")\no.bind(\"SUPER + B\", \"Browser\", \"firefox\")\n"
	writeTestFile(t, p.bindingPath(), user, 0o644)

	c, err := Bind(p, "SUPER + SHIFT + R")
	if err != nil || c.Action != ActionWrite {
		t.Fatalf("Bind = %+v %v", c, err)
	}
	want := user + BindingBegin + "\no.bind(\"SUPER + SHIFT + R\", \"GoRex\", \"gorex\")\n" + BindingEnd + "\n"
	if got := readTestFile(t, p.bindingPath()); got != want {
		t.Fatalf("insert:\n%s\nwant:\n%s", got, want)
	}
	if keys, ok := readBinding(p.bindingPath()); !ok || keys != "SUPER + SHIFT + R" {
		t.Errorf("binding = %q %v", keys, ok)
	}
	if c, err := Bind(p, "SUPER + SHIFT + R"); err != nil || c.Action != ActionUnchanged {
		t.Errorf("the second bind = %+v %v", c, err)
	}
	if _, err := Bind(p, "SUPER + G"); err != nil {
		t.Fatal(err)
	}
	got := readTestFile(t, p.bindingPath())
	if strings.Count(got, BindingBegin) != 1 || !strings.Contains(got, "SUPER + G") || !strings.HasPrefix(got, user) {
		t.Fatalf("replace:\n%s", got)
	}
	c, err = Unbind(p)
	if err != nil || c.Action != ActionRemove {
		t.Fatalf("Unbind = %+v %v", c, err)
	}
	if got := readTestFile(t, p.bindingPath()); got != user {
		t.Fatalf("remove:\n%q\nwant:\n%q", got, user)
	}
	if c, err := Unbind(p); err != nil || c.Action != ActionUnchanged {
		t.Errorf("the second unbind = %+v %v", c, err)
	}
	// A marker with no partner is not ours to rewrite.
	writeTestFile(t, p.bindingPath(), BindingBegin+"\n", 0o644)
	if _, err := Bind(p, "SUPER + G"); err == nil {
		t.Error("a lone begin marker is rewritten")
	}
	if _, err := Bind(p, "bad\"keys"); err == nil {
		t.Error("keys with a quote are written")
	}
	if _, err := Bind(p, ""); err == nil {
		t.Error("empty keys are written")
	}
}

// TestBindRefusesSymlink keeps a linked bindings file as it is.
func TestBindRefusesSymlink(t *testing.T) {
	p := testPaths(t, darkPalette, "Tokyo Night")
	real := filepath.Join(t.TempDir(), "bindings.lua")
	writeTestFile(t, real, "o.bind(\"SUPER + RETURN\", \"Terminal\", \"alacritty\")\n", 0o644)
	if err := os.MkdirAll(filepath.Dir(p.bindingPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, p.bindingPath()); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if _, err := Bind(p, DefaultBinding); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("Bind = %v", err)
	}
	if _, err := Unbind(p); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("Unbind = %v", err)
	}
	if got := readTestFile(t, real); strings.Contains(got, BindingBegin) {
		t.Errorf("the linked file was rewritten:\n%s", got)
	}
}

// TestStatus reports the desktop, the theme, the following and the binding,
// and follows none without a palette.
func TestStatus(t *testing.T) {
	p := testPaths(t, darkPalette, "Tokyo Night")
	r := Status(p, true)
	if !r.Omarchy || r.Theme != "Tokyo Night" || r.Mode != "dark" || !r.Following || r.BindingInstalled {
		t.Fatalf("status: %+v", r)
	}
	if _, err := Bind(p, DefaultBinding); err != nil {
		t.Fatal(err)
	}
	if r = Status(p, true); !r.BindingInstalled || r.Binding != DefaultBinding {
		t.Errorf("binding: %+v", r)
	}

	// A machine without Omarchy follows none, whatever the settings say.
	missing := p
	missing.OmarchyRoot = t.TempDir()
	if r = Status(missing, true); r.Omarchy || r.Following {
		t.Errorf("no Omarchy: %+v", r)
	}

	// A desktop whose palette does not read is a warning.
	broken := testPaths(t, darkPalette, "Tokyo Night")
	if err := os.Remove(filepath.Join(broken.CurrentDir(), "theme", "colors.toml")); err != nil {
		t.Fatal(err)
	}
	if r = Status(broken, true); r.Following || len(r.Warnings) == 0 {
		t.Errorf("palette that does not read: %+v", r)
	}
}

// TestSettings decides following: no appearance.theme, and
// appearance.omarchy not false.
func TestSettings(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"no file", "", true},
		{"empty object", "{}", true},
		{"an explicit theme", `{"appearance.theme": "slate"}`, false},
		{"following off", `{"appearance.omarchy": false}`, false},
		{"following on", `{"appearance.omarchy": true}`, true},
		{"a theme and following on", `{"appearance.theme": "slate", "appearance.omarchy": true}`, false},
		{"a file that does not decode", `{`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			if tc.body != "" {
				writeTestFile(t, path, tc.body, 0o600)
			}
			if got := ReadSettings(path).Follows(); got != tc.want {
				t.Errorf("Follows = %v, want %v", got, tc.want)
			}
		})
	}
}
