---
name: gorex-ui-testing
description: Test GoRex's UI and session server without a human clicking — headless MyGo view tests (ui.NewTester), screenshots of the real window via the GOREX_DEBUG hook, macOS window captures with traffic lights, and driving the session server over its Unix socket. Use when adding or changing UI in GoRex (tabs, sidebar, panes, settings, palette, menus), when asked to "verify the UI", "take screenshots", "UI 自动化测试", "截图验证", "compare with Rex", or to check session-server behavior end to end (upgrade, attach, output continuity).
---

# GoRex UI testing

Four layers, cheapest first. Use the lowest layer that can prove the behavior; screenshots are for what only eyes can judge (alignment, spacing, colors, clipping).

| Layer | Proves | Runs in CI |
|---|---|---|
| 1. Headless view tests (`ui.NewTester`) | Layout, hit targets, clicks/drags/keys/menus, state changes | yes |
| 2. `GOREX_DEBUG` hook screenshots | Real rendering of the window's content, light/dark | no |
| 3. macOS window capture | Window chrome: traffic lights, title bar, full window | no |
| 4. Session server over its socket | Sessions, attach, input/output, upgrade, PIDs | partly |

## Safety rules (always)

- **Never touch the user's real data dir or server.** The built app uses `~/Library/Application Support/GoRex`, `mygo dev` uses `GoRex Dev`. For any manual run set `GOREX_DIR` to a fresh short path, e.g. `/tmp/gxt` (Unix socket paths have a length limit, so keep it short).
- Before ending anything, check what it is: `pgrep -fl GoRex`, and `ps eww -p <pid> | grep GOREX_DIR=` to tell your instance from the user's. Shut a test server down through its own socket (`op: shutdown`), never `pkill GoRex`.
- Delete the temp dir when done. Screenshots go to the session scratchpad, not the repo.

## 1. Headless view tests (`ui.NewTester`)

MyGo renders the view in memory, with no window. Use `newTestApp(t)` in `app_test.go`: it starts a session server in a temp `GOREX_DIR`, connects, opens a tab, and returns `(*App, *ui.Tester)` at 1000×620. Settings live in the package global `prefs`: save it and restore it with `t.Cleanup`.

Tester API (MyGo `ui/headless.go`; read it for the full list):

- Frames: `tt.Frame()` lays out and draws once. Call it after every state change or input step. `tt.Advance(d)` moves the fake clock for animations and timers.
- Find: `tt.Find(label)` returns the `Rect` of an element by its text or accessibility `Label`. `tt.Texts()` and `tt.HasText(s)` list what shows. Give new interactive elements a `.Label(...)` (and a `.Role(...)`) so tests can find them.
- Pointer: `tt.Click(label)`, `tt.ClickAt(x, y)`, `tt.RightClick(label)`, `tt.Press`, `tt.Move`, `tt.Release` (call `Frame()` between drag steps), `tt.Scroll`, `tt.Wheel`, `tt.DropFiles`.
- Context menus: `tt.RightClick(...)`, then `tt.Menu()` and `tt.ChooseMenuItem("Rename…")`.
- Keys and text: `tt.Key(mods, key)`, `tt.Type(s)`, `tt.Compose(text, caret)` for IME marked text, `tt.Command(name)`.
- Environment: `tt.SetSize(w, h)`, `tt.SetDark(true)`, `tt.SetScale(2)`, `tt.SetLocale("zh-Hans")`, `tt.SetTitleBar(...)`.
- Other: `tt.Focused(label)`, `tt.Cursor()`, `tt.Clipboard()`, `tt.Announcements()`, and `tt.Image()`, the rendered RGBA, for pixel checks.

Pattern, from `TestSidebar` on `feat/vertical-tabs`:

```go
a, tt := newTestApp(t)
old := prefs
t.Cleanup(func() { prefs = old })
prefs = settings{TabPlacement: "left"}
a.newTab(os.TempDir())
tt.Frame()
list, ok := tt.Find("Tabs")
if !ok { t.Fatalf("no sidebar; texts %q", tt.Texts()) }
x, y := list.X+60, list.Y+sideRowH*1.5+2 // drag the 2nd row over the 1st
tt.Press(x, y); tt.Frame()
for _, dy := range []float32{4, 10, 20, sideRowH + 2} { tt.Move(x, y-dy); tt.Frame() }
tt.Release(x, y-sideRowH-2); tt.Frame()
```

To prove a test guards a fix, revert the fix temporarily, check that the test fails, then restore the fix.

Run `go test -count=1 ./...` and `go vet ./...`, plus `GOOS=windows go build ./...` (and `GOOS=linux`) for code that runs on other platforms too.

## 2. Screenshots through the `GOREX_DEBUG` hook

`debug.go`: when `GOREX_DEBUG=<dir>` is set, the app reads `<dir>/do` every 150 ms and runs each line, then deletes the file. **Read `debug.go` for the current verbs**; on `main` these are:

- `shot [name.png]`: capture the main window's content (`Window.CapturePage`, no traffic lights). `sshot name.png` captures the settings window.
- `appearance light|dark|system`, `size W H`, `sleep 300ms`, `type text`, where `\r` is Enter and `\e` is Esc.
- `tab N`, `focus N`, `name TEXT`, `zoom`, `close`.
- Any command title with spaces replaced by `-`, case-insensitive: `new-tab`, `split-right`, `settings`.
- `section NAME` opens a settings section. `remote …` drives the Remote page.
- `feat/vertical-tabs` adds `tabs above|left|right` and `headers show|hide`.
- Windows and sessions: one hook drives every window. `win N` makes the next lines target the Nth window by place (1 is StateKey `main`, 2 `main-2`…), `win 0` the one focused last (the default); an N out of range is logged and skipped. `shot`, `type`, `size`, `tab`, `close` and the command verbs (`new-window`, `new-session`, `next-session`, `move-tab-to-new-window`, `close-session`…) act on the target window.
- `focus-win N` counts window N as focused last without taking the real focus. `session NAME` shows a session in the target window (or fronts the window showing it), `rename-session NAME` renames its session, `confirm` / `cancel` answer Close Session's question.
- `state FILE` writes, atomically and from the UI thread, `{"pid", "windows": [{"index", "stateKey", "session": {"id", "name"}, "tabs": [labels], "activeTab" (from 1), "focused", "minimized"}], "sessions": [{"id", "name", "tabs", "window" (index, 0 when hidden)}]}`.
- `panes FILE` writes, atomically, the target window's panes in tab order: `{"panes": [{"tab" (from 1), "pane" (its ID), "session" (the id `state`'s `sessions[].id` joins with), "tabTitle", "cols", "rows", "text", "screenCols", "screenRows", "screen", "captureError"}]}`, the terminal's text and size beside the screen the session server's snapshot renders, to tell a duplication in a pane from one of the server. FILE defaults to `panes.json`.

Add a verb to `debug.go` when a state can't be reached otherwise, with a one-line comment in the existing style.

Run it, with `scripts/debug-shots.sh` as a template:

```sh
D=/tmp/gxt; rm -rf $D; mkdir -p $D
echo '{"tabPlacement":"left"}' > $D/settings.json   # optional starting settings
GOREX_DIR=$D GOREX_DEBUG=$D go tool mygo dev &       # or the built app's binary
printf 'appearance dark\nshot left-dark.png\n' > $D/do
```

GoRex runs as a single instance, but a process with `GOREX_DIR` set takes no lock, so the run above never meets the user's app. To test the single-instance hand-over itself, leave `GOREX_DIR` unset and isolate the rest instead (on Windows `APPDATA`, `LOCALAPPDATA`, `TEMP` and `TMP`: the lock's socket is in the temp directory).

Wait until the PNG exists, then read it with the Read tool and check it. Look at both light and dark, every placement or state the change touches, and a narrow window (`size 560 400`).

## 3. macOS window capture (chrome included)

`CapturePage` doesn't draw the title bar or the traffic lights. For those, capture the real window:

```sh
W=$(swift .agents/skills/gorex-ui-testing/scripts/winid.swift GoRex | head -1 | cut -d' ' -f1)
screencapture -x -o -l "$W" "$OUT/real.png"
sips -Z 1400 "$OUT/real.png" --out "$OUT/real-small.png"   # make it small enough to read
```

`winid.swift OWNER` prints `id width height` for the on-screen layer-0 windows whose owner is exactly OWNER, the largest first (pass `"Rex Beta"` for Rex). When the user's own GoRex is open too, pick your window by its size or capture it before the user's comes to the front. Compare against the reference Rex screenshots when matching Rex's design. `sips -c` crops from the center, not from an offset, so downscale the whole image with `-Z` instead.

## 4. Session server over its socket

The server speaks JSON lines on `$GOREX_DIR/server.sock` (`internal/rex/proto.go`). `scripts/rexctl.py` is a small client:

```sh
python3 -I .agents/skills/gorex-ui-testing/scripts/rexctl.py --sock /tmp/gxt/server.sock hello    # pid exe exeTime upgrade
python3 -I …/rexctl.py --sock … list                     # id pid program exited
python3 -I …/rexctl.py --sock … type <sid> 'echo hi\r'   # attach, send input
python3 -I …/rexctl.py --sock … screen <sid>             # attach, print the snapshot
python3 -I …/rexctl.py --sock … shutdown
```

For output continuity across restarts or upgrades, run `while :; do date +%s | tee -a ticks.txt; sleep 1; done` in a session, then check `ticks.txt` for gaps with awk. A one-second gap can come from load (`sleep 1` drifts), so map each gap to wall-clock time and compare it with the server log before blaming the change.

Server-side behavior that needs a real process, such as exec, PIDs and fds, belongs in `internal/rex` tests that spawn the test binary as the server (see `TestMain`/`TestUpgrade` on `feat/server-live-upgrade`).

## Limits, so you say them in the report

- Native AppKit menus in the real menu bar (item enabled state, for example) and real hover can't be checked headlessly or with `CapturePage`. Say they were checked through code only, or use layer 3 and look.
- Windows and Linux: compile and vet only, unless a machine is available.
- Screenshots are judged by eye. There is no golden-image diff, so say what you looked at.
