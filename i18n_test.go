package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"gorex/locales"
)

// TestServerPIDHasNoSeparator checks that a process id is not grouped.
func TestServerPIDHasNoSeparator(t *testing.T) {
	for _, lang := range []string{"en", "zh-Hans"} {
		if got := locales.In(lang).HostServerUp(12345, "3h"); !strings.Contains(got, "pid 12345") {
			t.Errorf("%s: %q groups the pid", lang, got)
		}
	}
}

// TestLanguageFromFile checks that a language of the settings file is the
// app's own spelling of the one it matches, and that "" is applied too.
func TestLanguageFromFile(t *testing.T) {
	for _, c := range []struct{ file, want string }{{"zh-hans", "zh-Hans"}, {"zh-CN", "zh-Hans"}, {"zh", "zh-Hans"}, {"en-GB", "en"}, {"de", ""}, {"", ""}} {
		isolateSettings(t, "")
		var applied []string
		setLocale = func(tag string) { applied = append(applied, tag) }
		os.WriteFile(settingsPath(), []byte(`{"language": "`+c.file+`"}`), 0o600)
		loadSettings()
		if prefs.Language != c.want || !slices.Equal(applied, []string{c.want}) {
			t.Errorf("file %q: language %q, applied %q, want %q", c.file, prefs.Language, applied, c.want)
		}
	}
}

// checkUnusedCatalogKeys reports any generated locales.Messages method in
// genSrc whose selector name never appears across prodSources.
func checkUnusedCatalogKeys(genSrc []byte, prodSources map[string][]byte) ([]string, error) {
	fset := token.NewFileSet()
	gen, err := parser.ParseFile(fset, "locales/catalog_gen.go", genSrc, 0)
	if err != nil {
		return nil, fmt.Errorf("parse locales/catalog_gen.go: %w", err)
	}
	var methods []struct{ method, key string }
	for _, decl := range gen.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Format" {
				return true
			}
			lit, ok := call.Args[1].(*ast.BasicLit)
			if ok && lit.Kind == token.STRING {
				methods = append(methods, struct{ method, key string }{fn.Name.Name, strings.Trim(lit.Value, `"`)})
			}
			return true
		})
	}
	if len(methods) == 0 {
		return nil, fmt.Errorf("no methods parsed from locales/catalog_gen.go")
	}

	used := map[string]bool{}
	for name, src := range prodSources {
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				used[sel.Sel.Name] = true
			}
			return true
		})
	}
	var errs []string
	for _, m := range methods {
		if !used[m.method] {
			errs = append(errs, fmt.Sprintf("unused locale key %q (locales.%s); remove from locales/*.json and run `go tool mygo generate`", m.key, m.method))
		}
	}
	return errs, nil
}

// TestCatalogNoUnusedKeys checks that every message in locales/catalog_gen.go
// is referenced by production code in the root package (tools/verify/arch.go
// forbids subpackages from importing gorex/locales).
func TestCatalogNoUnusedKeys(t *testing.T) {
	genSrc, err := os.ReadFile("locales/catalog_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	prod := map[string][]byte{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		prod[path] = b
	}
	errs, err := checkUnusedCatalogKeys(genSrc, prod)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range errs {
		t.Error(e)
	}
}

func TestCheckUnusedCatalogKeysViolations(t *testing.T) {
	genSrc := []byte(`package locales
func (msgs Messages) UsedKey() string { return Catalog.Format(msgs.locale, "usedKey", nil) }
func (msgs Messages) DeadKey() string { return Catalog.Format(msgs.locale, "deadKey", nil) }
`)
	prod := map[string][]byte{
		"app.go": []byte(`package main
import "gorex/locales"
func f() string { return locales.UsedKey() }
`),
	}
	errs, err := checkUnusedCatalogKeys(genSrc, prod)
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 1 || !strings.Contains(errs[0], `"deadKey"`) {
		t.Fatalf("expected unused deadKey violation, got %v", errs)
	}
}

// inChinese shows the app in Chinese: the app's language, which the
// messages follow, and the window's.
func inChinese(t *testing.T, tt *ui.Tester) {
	t.Helper()
	t.Setenv("MYGO_LOCALE", "zh-Hans")
	tt.SetLocale("zh-Hans")
	tt.Frame()
}

func TestChineseSurfaces(t *testing.T) {
	isolateSettings(t, "")
	a, tt := newTestApp(t)
	tt.Frame()
	if _, ok := tt.Find("New Tab"); !ok {
		t.Fatalf("no New Tab in English; texts %q", tt.Texts())
	}
	inChinese(t, tt)
	for _, label := range []string{"新建标签页", "命令面板", "标签页"} {
		if _, ok := tt.Find(label); !ok {
			t.Errorf("no %q in Chinese; texts %q", label, tt.Texts())
		}
	}
	if _, ok := tt.Find("New Tab"); ok {
		t.Error("New Tab stays in English")
	}

	// The palette lists commands in Chinese, and finds them by their English
	// titles too.
	a.openPalette()
	tt.Frame()
	if !tt.HasText("向右拆分") {
		t.Errorf("no 向右拆分 in the palette; texts %q", tt.Texts())
	}
	titles := func(query string) []string {
		a.paletteQuery = query
		var out []string
		for _, it := range a.paletteItems() {
			out = append(out, it.title)
		}
		return out
	}
	if got := titles("split right"); !slices.Contains(got, "向右拆分") {
		t.Errorf("'split right' finds %q, not 向右拆分", got)
	}
	if got := titles("向右"); !slices.Contains(got, "向右拆分") {
		t.Errorf("'向右' finds %q, not 向右拆分", got)
	}
	if got := titles("zzzz"); len(got) != 0 {
		t.Errorf("'zzzz' finds %q", got)
	}
	// Typing in the palette runs the command found by its English title.
	a.paletteQuery = ""
	tt.Frame()
	tt.Type("split right")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if n := len(a.tab().panes()); n != 2 {
		t.Errorf("%d panes after typing 'split right' in Chinese", n)
	}
	// The debug verbs name the commands in English.
	if cmdSplitRight.English() != "Split Right" || cmdSplitRight.Title() != "向右拆分" {
		t.Errorf("titles %q, %q", cmdSplitRight.English(), cmdSplitRight.Title())
	}
}

// TestSpaceChipInChinese checks that the space picker and the project
// picker above the tabs speak Chinese.
func TestSpaceChipInChinese(t *testing.T) {
	isolateSettings(t, "")
	_, tt := newTestApp(t)
	inChinese(t, tt)
	// Each text is the Chinese catalog's, and not the English one, so that a
	// missing translation fails and a change of copy does not.
	zh, en := locales.In("zh-Hans"), locales.In("en")
	for _, c := range []struct{ zh, en string }{
		{zh.SpacePickerLabel(zh.SpaceDefault()), en.SpacePickerLabel(en.SpaceDefault())},
		{zh.ProjectsNoProject(), en.ProjectsNoProject()},
	} {
		if c.zh == c.en {
			t.Errorf("%q is not translated", c.en)
		}
		if !tt.HasText(c.zh) {
			t.Errorf("no %q in Chinese; texts %q", c.zh, tt.Texts())
		}
	}
}

func TestChineseSettings(t *testing.T) {
	isolateSettings(t, "")
	a := &App{}
	settingsUI.section, settingsUI.themes = "appearance", map[string]*terminal.Theme{}
	tt := ui.NewTester(a.settingsView, 780, 680)
	inChinese(t, tt)
	for _, want := range []string{"设置", "外观", "终端", "主题", "键盘", "远程", "关于", "语言", "标签页位置", "文字大小", "跟随系统"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in Chinese; texts %q", want, tt.Texts())
		}
	}
	for _, section := range []string{"terminal", "theme", "keyboard", "about"} {
		settingsUI.section = section
		tt.Frame()
	}
	if !tt.HasText("设置文件") {
		t.Errorf("no 设置文件 in About; texts %q", tt.Texts())
	}
	settingsUI.section = "keyboard"
	tt.Frame()
	if !tt.HasText("新建标签页") || !tt.HasText("显示标签页 1…9") {
		t.Errorf("shortcuts not in Chinese; texts %q", tt.Texts())
	}
}

// TestLanguageSetting checks that the Language control chooses a language of
// the app, "" for the system's.
func TestLanguageSetting(t *testing.T) {
	isolateSettings(t, "")
	var chosen []string
	setLocale = func(tag string) { chosen = append(chosen, tag) }
	a := &App{}
	settingsUI.section, settingsUI.themes = "appearance", map[string]*terminal.Theme{}
	tt := ui.NewTester(a.settingsView, 780, 680)
	tt.Frame()
	if !tt.HasText("System") {
		t.Fatalf("no System choice; texts %q", tt.Texts())
	}
	// The control sits at the end of the Language row, whose title and hint
	// are two lines.
	row, ok := tt.Find("Language")
	if !ok {
		t.Fatalf("no Language setting; texts %q", tt.Texts())
	}
	tt.ClickAt(660, row.Y+row.H/2+8)
	tt.Frame()
	if err := tt.Click("简体中文"); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	tt.Frame()
	if prefs.Language != "zh-Hans" || !slices.Equal(chosen, []string{"zh-Hans"}) {
		t.Errorf("language %q, SetLocale calls %q", prefs.Language, chosen)
	}
	a.setLanguage("")
	if prefs.Language != "" || !slices.Equal(chosen, []string{"zh-Hans", ""}) {
		t.Errorf("language %q, SetLocale calls %q", prefs.Language, chosen)
	}
}

// TestErrorFollowsLanguage checks that a message of the catalog on the error
// banner is said in the language of the moment, not of when it was set.
func TestErrorFollowsLanguage(t *testing.T) {
	a := &App{errMsg: locales.Messages.ErrorConnectionLost}
	if got := a.errText(); got != "Lost the connection to the session server." {
		t.Errorf("in English: %q", got)
	}
	t.Setenv("MYGO_LOCALE", "zh-Hans")
	if got := a.errText(); got != "与会话服务器的连接已断开。" {
		t.Errorf("in Chinese: %q", got)
	}
}
