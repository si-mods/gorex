package main

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"gorex/locales"
)

// catalogKeys returns the message keys of a catalog of locales, without
// its descriptions (@key) and its locale (@@locale).
func catalogKeys(t *testing.T, file string) []string {
	t.Helper()
	b, err := os.ReadFile("locales/" + file)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range m {
		if !strings.HasPrefix(k, "@") {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}

// TestNoThreeDots checks that the catalogs write the ellipsis as …, never as
// three dots.
func TestNoThreeDots(t *testing.T) {
	for _, file := range []string{"en.json", "zh-Hans.json"} {
		b, err := os.ReadFile("locales/" + file)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		for k, v := range m {
			if strings.Contains(string(v), "...") {
				t.Errorf("%s: %s has three dots: %s", file, k, v)
			}
		}
	}
}

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

// TestCatalogsAgree checks that Chinese says every message English does.
func TestCatalogsAgree(t *testing.T) {
	en, zh := catalogKeys(t, "en.json"), catalogKeys(t, "zh-Hans.json")
	if len(en) == 0 {
		t.Fatal("no messages")
	}
	for _, k := range en {
		if !slices.Contains(zh, k) {
			t.Errorf("zh-Hans lacks %s", k)
		}
	}
	for _, k := range zh {
		if !slices.Contains(en, k) {
			t.Errorf("zh-Hans has %s, which en lacks", k)
		}
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

// TestSpaceChipInChinese checks that the chip above the tabs names spaces in
// Chinese.
func TestSpaceChipInChinese(t *testing.T) {
	isolateSettings(t, "")
	a, tt := newTestApp(t)
	inChinese(t, tt)
	if _, ok := tt.Find(locales.SessionLabel(a.ws.Name)); !ok {
		t.Fatalf("no space chip in Chinese; texts %q", tt.Texts())
	}
}

func TestChineseSettings(t *testing.T) {
	isolateSettings(t, "")
	a := &App{}
	settingsUI.section, settingsUI.themes = "appearance", map[string]*terminal.Theme{}
	tt := ui.NewTester(a.settingsView, 780, 540)
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
	tt := ui.NewTester(a.settingsView, 780, 540)
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
