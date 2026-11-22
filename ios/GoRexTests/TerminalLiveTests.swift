import SwiftUI
import XCTest

/// The terminal's Live Activity, apart from ActivityKit: its content, its
/// updates, the grace on leaving the app, and when it starts and ends
/// (docs/IOS-006-live-activities.md, Terminal activity). The clock and the
/// waits are the test's: no time passes by itself.
nonisolated final class TerminalLiveTests: XCTestCase {
    /// What the controller asked of ActivityKit.
    @MainActor final class FakeDriver: TerminalLive.Driver {
        var enabled = true
        var running = false
        var dismissed = false
        /// The done of each update that has one, for the test to call.
        var dones: [() -> Void] = []
        var calls: [String] = []
        var started: [TerminalActivity] = []
        var updates: [(state: TerminalActivity.ContentState, stale: Date?)] = []

        func start(_ attributes: TerminalActivity, _ state: TerminalActivity.ContentState) {
            calls.append("start \(attributes.block)")
            started.append(attributes)
            running = true
        }

        func update(_ state: TerminalActivity.ContentState, stale: Date?, done: (() -> Void)?) {
            calls.append(done == nil ? "update" : "update in grace")
            updates.append((state, stale))
            if let done { dones.append(done) }
        }

        func end() {
            calls.append("end")
            running = false
            dismissed = false
        }
    }

    /// The waits asked for, run when the test says.
    @MainActor final class Waits {
        var all: [(wait: TimeInterval, work: @MainActor () -> Void)] = []
        func schedule(_ w: TimeInterval, _ f: @escaping @MainActor () -> Void) { all.append((w, f)) }
        /// Runs the waits asked so far; those they ask wait for the next run.
        func run() {
            let now = all
            all = []
            for w in now { w.work() }
        }
    }

    /// Background tasks: begun, ended, and the expiration handlers.
    @MainActor final class Tasks {
        var gives = true
        var begun = 0
        var ended: [Int] = []
        var expirations: [Int: @MainActor () -> Void] = [:]
        func begin(_ expired: @escaping @MainActor () -> Void) -> Int? {
            guard gives else { return nil }
            begun += 1
            expirations[begun] = expired
            return begun
        }
    }

    @MainActor private static func record(_ id: String, _ state: String, at t: TimeInterval, title: String? = nil, app: String? = nil, progress: Int? = nil) -> Host.ProgramStatus {
        .init(id: id, state: state, progress: progress, app: app, title: title, updated: Date(timeIntervalSince1970: t))
    }

    /// The block's most urgent record, as the inbox ranks them: blocked over
    /// error over done over working, the latest of those alike; idle shows
    /// none. Done and error records seen don't count.
    @MainActor func testStateRanksOneBlock() {
        let rs = [Self.record("a", "working", at: 100, progress: 40), Self.record("b", "done", at: 200, title: "Wrote it"),
                  Self.record("c", "blocked", at: 50, title: "Edit main.go?", app: "claude-code")]
        var s = TerminalLive.state(title: "api", program: "zsh", records: rs, seen: nil, connected: true)
        XCTAssertEqual(s.record?.state, "blocked")
        XCTAssertEqual(s.record?.title, "Edit main.go?")
        XCTAssertEqual(s.program, "claude-code", "a known program the record names gives the tile")
        XCTAssertEqual(s.title, "api")

        s = TerminalLive.state(title: "api", program: "zsh", records: Array(rs.prefix(2)), seen: nil, connected: true)
        XCTAssertEqual(s.record?.state, "done")
        XCTAssertEqual(s.program, "zsh", "no app: the block's program")
        s = TerminalLive.state(title: "api", program: nil, records: Array(rs.prefix(2)), seen: Date(timeIntervalSince1970: 200), connected: true)
        XCTAssertEqual(s.record?.state, "working", "the done record was seen")
        XCTAssertEqual(s.record?.progress, 40)
        let two = [Self.record("x", "working", at: 10), Self.record("y", "working", at: 20, title: "later")]
        XCTAssertEqual(TerminalLive.state(title: "", program: nil, records: two, seen: nil, connected: true).record?.title, "later")
        XCTAssertNil(TerminalLive.state(title: "", program: nil, records: [Self.record("i", "idle", at: 1)], seen: nil, connected: true).record)
        XCTAssertNil(TerminalLive.state(title: "", program: nil, records: [], seen: nil, connected: false).record)
    }

    /// The line is the screen's last that is not blank, its trailing spaces
    /// gone, cut to 80 characters and 240 bytes, whole characters.
    func testLastLine() {
        XCTAssertEqual(TerminalActivity.lastLine("one\ntwo  \n   \n\n"), "two")
        XCTAssertEqual(TerminalActivity.lastLine("  indented\n"), "  indented")
        XCTAssertEqual(TerminalActivity.lastLine(""), "")
        XCTAssertEqual(TerminalActivity.lastLine("\n \n"), "")
        XCTAssertEqual(TerminalActivity.lastLine("a\r\nb\r\n"), "b")
        let long = String(repeating: "界", count: 100)
        XCTAssertEqual(TerminalActivity.lastLine("x\n" + long), String(repeating: "界", count: 80))
        let emoji = String(repeating: "👩‍💻", count: 90)
        XCTAssertEqual(TerminalActivity.lastLine(emoji), String(repeating: "👩‍💻", count: 21), "11 bytes each")
        // Characters of many combining marks: what does not fit goes whole.
        let marks = "e" + String(repeating: "\u{301}", count: 30)
        XCTAssertEqual(TerminalActivity.lastLine(String(repeating: marks, count: 10)), String(repeating: marks, count: 3))
        XCTAssertEqual(TerminalActivity.lastLine("ab" + String(repeating: "\u{301}", count: 200)), "a")
        XCTAssertEqual(TerminalActivity.cut("short"), "short")
    }

    /// The activity's text and a notification's come without what a
    /// terminal's escapes leave, and without the characters iOS draws as
    /// nothing or as a box; CJK text and emoji stay, joiners and marks too.
    func testShownText() {
        XCTAssertEqual("plain ascii".shownText, "plain ascii")
        XCTAssertEqual("\u{1B}[31mred\u{1B}[0m".shownText, "red")
        XCTAssertEqual("\u{1B}]9;note\u{07}after".shownText, "after")
        XCTAssertEqual("\u{1B}]9;note\u{1B}\\after".shownText, "after")
        XCTAssertEqual("\u{1B}]9;note\u{9C}after".shownText, "after", "an 8-bit ST ends it")
        XCTAssertEqual("\u{9D}9;note\u{9C}after".shownText, "after", "an 8-bit OSC too")
        XCTAssertEqual("a\u{1B}\u{1B}[0mb".shownText, "ab", "a doubled ESC starts its own sequence")
        XCTAssertEqual("\u{1B}c\u{1B}(Bplain".shownText, "plain", "two-character escapes")
        XCTAssertEqual("\u{9B}31mred".shownText, "red", "an 8-bit CSI")
        XCTAssertEqual("a\u{07}b\u{08}c\u{7F}".shownText, "abc")
        XCTAssertEqual("\u{200E}left\u{200F}\u{200B} right".shownText, "left right")
        XCTAssertEqual("\u{E0B0} main \u{E0B2}".shownText, " main ")
        XCTAssertEqual("\u{10EEEE}x".shownText, "x", "a picture's placeholder")
        XCTAssertEqual("bad\u{FFFD}here".shownText, "badhere")
        let kept = "中文 🏴󠁧󠁢󠁳󠁣󠁴󠁿 👩‍💻 e\u{301}"
        XCTAssertEqual(kept.shownText, kept, "CJK, emoji, their joiners and marks stay")
    }

    /// The line and a record's text reach the activity filtered: a Nerd
    /// Font icon leaves no box, an escape sequence no text.
    @MainActor func testStateShowsFilteredText() {
        XCTAssertEqual(TerminalActivity.lastLine("\u{1B}[32m✓ done\u{1B}[0m  "), "✓ done")
        XCTAssertEqual(TerminalActivity.lastLine("main \u{E0B2}"), "main")
        let r = Host.ProgramStatus(id: "a", state: "blocked", title: "\u{E0B1} Edit?",
                                   msg: "\u{1B}[1mAllow\u{1B}[0m \u{1B}]8;;x\u{07}edits?")
        let s = TerminalLive.state(title: "\u{E0B0}api", program: nil, records: [r], seen: nil, connected: true)
        XCTAssertEqual(s.title, "api")
        XCTAssertEqual(s.record?.title, "Edit?")
        XCTAssertEqual(s.line, "Allow edits?")
    }

    /// The line the screen reads passes the same filter as a record's,
    /// and a rule alone is none: on the card it draws lines of nothing.
    @MainActor func testScreenLineShownFiltered() {
        let d = FakeDriver(), w = Waits(), t = Tasks()
        let clock = Date(timeIntervalSince1970: 1_800_000_000)
        let l = live(d, w, t) { clock }
        var records = [Host.ProgramStatus]()
        var screen = "main \u{E0B2}"
        l.show(source(state: { TerminalLive.state(title: "api", program: nil, records: records, seen: nil, connected: true) },
                      lines: { done in done(screen) }), owner: UUID())
        w.run()
        XCTAssertEqual(l.sent?.line, "main", "the screen's line is filtered as a record's is")

        // A box's edge caught mid-draw, or a separator a command printed.
        screen = String(repeating: "─", count: 100)
        l.outputChanged(); w.run(); w.run()
        XCTAssertEqual(l.sent?.line, "", "a rule alone is no line")

        // A message of rules is none either: the screen's line is there.
        screen = "building it"
        records = [.init(id: "a", state: "working", msg: "──────", updated: clock)]
        l.outputChanged(); w.run(); w.run()
        XCTAssertEqual(l.sent?.line, "building it")
    }

    /// A screen's rows, and the cursor's row: the one marked with a "|".
    private static func screen(_ text: String) -> (rows: [String], cursor: Int?) {
        var rows = text.components(separatedBy: "\n"), cursor: Int?
        for (i, r) in rows.enumerated() where r.contains("|") {
            cursor = i
            rows[i] = r.replacingOccurrences(of: "|", with: " ")
        }
        return (rows, cursor)
    }

    private static func line(_ text: String) -> String {
        let s = screen(text)
        return TerminalActivity.screenLine(s.rows, cursor: s.cursor)
    }

    /// Claude Code: its spinner over the input between rules, or in a
    /// box, with its mode and status lines under it.
    func testScreenLineClaudeCode() {
        XCTAssertEqual(Self.line("""
        ● Read 3 files (ctrl+o to expand)

        ● Update(internal/api/server.go)
          ⎿  Updated internal/api/server.go with 4 additions

        ✻ Refactoring… (1m 15s · esc to interrupt)

        ────────────────────────────────────────────────────────────
        > |
        ────────────────────────────────────────────────────────────
          ⏵⏵ bypass permissions on (shift+tab to cycle)
                                                     ◯ IDE disconnected
        """), "✻ Refactoring… (1m 15s · esc to interrupt)")

        XCTAssertEqual(Self.line("""
        ✶ Thinking… (12s · ↑ 1.2k tokens · esc to interrupt)

        ╭──────────────────────────────────────────────────────╮
        │ > fix the flaky test in store_test.go|                │
        ╰──────────────────────────────────────────────────────╯
          ? for shortcuts
        """), "✶ Thinking… (12s · ↑ 1.2k tokens · esc to interrupt)")

        // An input of two lines, the cursor on the second.
        XCTAssertEqual(Self.line("""
        ● Done. All 42 tests pass.

        ────────────────────────────────────────
        > now update the changelog
          and the docs|
        ────────────────────────────────────────
          ⏵⏵ accept edits on (shift+tab to cycle)
        """), "● Done. All 42 tests pass.")

        // Text in a box above the input is skipped too.
        XCTAssertEqual(Self.line("""
        ✻ Compacting conversation… (40s)
        ╭────────────────────────╮
        │ Tip: press # to note │
        ╰────────────────────────╯
        ────────────────────────────
        > |
        ────────────────────────────
          ? for shortcuts
        """), "✻ Compacting conversation… (40s)")
    }

    /// Codex: its progress line, its input after a prompt, its context line.
    func testScreenLineCodex() {
        XCTAssertEqual(Self.line("""
        • Explored
          └ Read main.go, handler.go

        • Working (23s • esc to interrupt)

        › Summarize recent commits|

          100% context left · ? for shortcuts
        """), "• Working (23s • esc to interrupt)")
    }

    /// Devin: its status bar under the input, which today's line showed.
    func testScreenLineDevin() {
        XCTAssertEqual(Self.line("""
          Edited src/app.ts (+12 -3)

        ⠹ Running tests… (8s)

        ▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔
        ❯ |
        ▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁
        SWE-2 High · bypass    ctrl+o details · opt+t thinking
        """), "⠹ Running tests… (8s)")
    }

    /// Pi: its working line over an editor between rules, a footer of two.
    func testScreenLinePi() {
        XCTAssertEqual(Self.line("""
         Reading src/index.ts

        ⠙ Working... (esc to interrupt)
        ─────────────────────────────────────────
         |
        ─────────────────────────────────────────
        ~/project (main)
        ↑12k ↓3.4k $0.042  23.1%/200k  model-name
        """), "⠙ Working... (esc to interrupt)")
    }

    /// A shell at its prompt shows the prompt's line; a command printing,
    /// its last output; blank rows under the cursor don't count.
    func testScreenLineShell() {
        XCTAssertEqual(Self.line("""
        alice@alice-laptop ~ % ls
        Makefile  README.md  src
        alice@alice-laptop ~ % |


        """), "alice@alice-laptop ~ %")
        XCTAssertEqual(Self.line("""
        $ go test ./...
        ok   example.com/app/store   0.412s
        ok   example.com/app/api     1.208s
        |

        """), "ok   example.com/app/api     1.208s")
        // A rule a command printed is a line like another.
        XCTAssertEqual(Self.line("""
        $ ./build.sh
        ────────────
        |
        """), "────────────")
    }

    /// No cursor, a cursor out of the rows, or nothing above the input:
    /// the screen's last line that is not blank, as before.
    func testScreenLineFallback() {
        XCTAssertEqual(TerminalActivity.screenLine([], cursor: nil), "")
        XCTAssertEqual(TerminalActivity.screenLine(["", "  ", ""], cursor: 1), "")
        XCTAssertEqual(TerminalActivity.screenLine(["one", "two", ""], cursor: nil), "two")
        XCTAssertEqual(TerminalActivity.screenLine(["one", "two"], cursor: 5), "two")
        XCTAssertEqual(Self.line("""
        ──────
        > |
        ──────
        status bar
        """), "status bar")
        let long = String(repeating: "界", count: 100)
        XCTAssertEqual(TerminalActivity.screenLine([long, "|", "bar"], cursor: 1), String(repeating: "界", count: 80))
    }

    /// A screen of 40 rows with text on the rows given, as captured.
    private static func rows(_ text: [Int: String]) -> [String] {
        (0..<40).map { text[$0] ?? "" }
    }

    private static let rule = String(repeating: "─", count: 100)

    /// Claude Code idle, as captured in a 100×40 terminal, names made up:
    /// its cursor on the input's row (2, 36), shown. Working, it may hide
    /// the cursor and leave it under its last line: the input still finds
    /// the line.
    func testScreenLineClaudeCodeCaptured() {
        var screen = Self.rows([
            1: " ▐▛███▜▌   Claude Code v2.1.0", 2: "▝▜█████▛▘  Model · Example Org", 3: "  ▘▘ ▝▝    ~/project",
            34: String(repeating: " ", count: 60) + "✔ Update installed · Restart to update",
            35: Self.rule, 36: "❯ ", 37: Self.rule,
            38: "  🏢 Example Org  🖥 alice-laptop  📁 ~/project  🤖 Model  📟 v2.1.0",
            39: "  ⏵⏵ bypass permissions on (shift+tab to cycle)",
        ])
        XCTAssertEqual(TerminalActivity.screenLine(screen, cursor: 36), "✔ Update installed · Restart to update")
        screen[34] = "✻ Refactoring… (1m 15s · esc to interrupt)"
        XCTAssertEqual(TerminalActivity.screenLine(screen, cursor: 36), "✻ Refactoring… (1m 15s · esc to interrupt)")
        XCTAssertEqual(TerminalActivity.screenLine(screen, cursor: 39), "✻ Refactoring… (1m 15s · esc to interrupt)", "cursor on the last status line")
        XCTAssertEqual(TerminalActivity.screenLine(screen, cursor: nil), "✻ Refactoring… (1m 15s · esc to interrupt)")
    }

    /// Devin idle, as captured: drawn from the top, not full-screen, its
    /// cursor after its prompt ❭ (2, 10), shown, and its status bar under
    /// the input, which the line showed before.
    func testScreenLineDevinCaptured() {
        let screen = Self.rows([
            1: "⠀⣴⣾⣶⡄⠀⠀⠀⠀", 2: "⠀⠛⠿⠟⠻⣶⣾⣶⡄  Devin CLI", 3: "⠀⣤⣶⣦⣴⠿⢿⠿⠃  v3000.0", 4: "⠀⠻⢿⠿⠃⠀⠀⠀⠀",
            6: " Pro · 40% remaining (resets in 1d 19h)", 8: " ● ~/project",
            9: Self.rule, 10: "❭ Ask Devin to build features, fix bugs, or work on your code", 11: Self.rule,
            12: "SWE-2 High · bypass                                     Press Ctrl+O to view the full thinking trace",
        ])
        XCTAssertEqual(TerminalActivity.screenLine(screen, cursor: 10), "● ~/project")
        XCTAssertEqual(TerminalActivity.screenLine(screen, cursor: 13), "● ~/project", "cursor under the status bar")
    }

    /// Pi idle, as captured: an editor with no prompt between rules, the
    /// cursor in it (0, 35), hidden, a footer of three lines under it.
    func testScreenLinePiCaptured() {
        var screen = Self.rows([
            1: " ▀▀█  v1.3.0", 2: " █▀ █ escape interrupt · ctrl+c/ctrl+d clear/exit · / commands · ! bash · ctrl+o more",
            3: " Press ctrl+o to show full startup help and loaded resources.",
            5: " Pi can explain its own features and look up its docs. Ask it how to use or extend Pi.",
            7: "[Skills]", 8: "  example-skill", 10: "[Extensions]", 11: "  example-extension",
            14: Self.rule, 15: " Update Available", 16: " New version 1.6.0 is available. Run pi update", 17: " Changelog: https://pi.dev/changelog", 18: Self.rule,
            34: Self.rule, 36: Self.rule,
            37: "~/project", 38: "0.0%/1.0M (auto)                                                     auto/model-name • high", 39: "🖥alice-laptop",
        ])
        XCTAssertEqual(TerminalActivity.screenLine(screen, cursor: 35), "Changelog: https://pi.dev/changelog")
        screen[33] = " ⠙ Working... (esc to interrupt)"
        XCTAssertEqual(TerminalActivity.screenLine(screen, cursor: 35), "⠙ Working... (esc to interrupt)")
    }

    /// Codex asked to sign in, as captured, its cursor hidden at (1, 36):
    /// no input, so its last line.
    func testScreenLineCodexSignIn() {
        let screen = Self.rows([
            0: "  Welcome to Codex, OpenAI's command-line coding agent",
            2: "  Sign in with ChatGPT to use Codex as part of your paid plan", 3: "  or connect an API key for usage-based billing",
            5: "> 1. Sign in with ChatGPT", 6: "     Usage included with Plus, Pro, Business, and Enterprise plans",
            8: "  2. Sign in with Device Code", 9: "     Sign in from another device with a one-time code",
            11: "  3. Sign in another way", 12: "     Use an OpenAI API key", 14: "  Press enter to continue",
        ])
        XCTAssertEqual(TerminalActivity.screenLine(screen, cursor: 36), "  Press enter to continue")
    }

    /// A shell whose prompt is ❯ (Starship) is a shell: no rules round it;
    /// nor does a table a command printed make an input.
    func testScreenLineShellNotAnInput() {
        XCTAssertEqual(Self.line("""
        ~/project on main
        ❯ make
        ok
        ~/project on main
        ❯ |
        """), "❯")
        XCTAssertEqual(Self.line("""
        ❯ make
        built in 2s
        |
        """), "built in 2s")
        XCTAssertEqual(Self.line("""
        ┌──────┐
        │ a  b │
        └──────┘
        $ |
        """), "$")
    }

    /// The most urgent record's message is the line, in any state; done or
    /// failed, the last message it had; else the screen's. A blank one is
    /// none, and line breaks are spaces.
    @MainActor func testRecordMessageIsTheLine() {
        func line(_ r: [Host.ProgramStatus], seen: Date? = nil) -> String {
            TerminalLive.state(title: "api", program: nil, records: r, seen: seen, line: "status bar", connected: true).line
        }
        let at = Date(timeIntervalSince1970: 10)
        XCTAssertEqual(line([.init(id: "a", state: "blocked", app: "claude-code", title: "Permission", msg: "Allow Bash: rm -rf build?\nyes / no", updated: at)]),
                       "Allow Bash: rm -rf build? yes / no")
        XCTAssertEqual(line([.init(id: "a", state: "blocked", msg: nil, updated: at)]), "status bar")
        XCTAssertEqual(line([.init(id: "a", state: "working", msg: "Waiting for the dev build", lastMsg: "Older", updated: at)]), "Waiting for the dev build")
        XCTAssertEqual(line([.init(id: "a", state: "working", msg: " \n ", lastMsg: "Older", updated: at)]), "status bar", "lastMsg only once done")
        XCTAssertEqual(line([.init(id: "a", state: "idle", msg: "Ready", updated: at)]), "Ready")
        XCTAssertEqual(line([.init(id: "a", state: "done", lastMsg: "Correct the lint count", updated: at)]), "Correct the lint count")
        XCTAssertEqual(line([.init(id: "a", state: "error", msg: "Build failed", lastMsg: "Older", updated: at)]), "Build failed")
        XCTAssertEqual(line([.init(id: "a", state: "done", lastMsg: "Correct the lint count", updated: at)], seen: at), "status bar", "a seen done record does not count")
        // The most urgent record's own: the blocked one, not the working one's.
        XCTAssertEqual(line([.init(id: "w", state: "working", msg: "Reading files", updated: at),
                             .init(id: "b", state: "blocked", msg: "Allow edit?", updated: at)]), "Allow edit?")
        XCTAssertEqual(line([.init(id: "w", state: "working", msg: "Reading files", updated: at),
                             .init(id: "d", state: "done", lastMsg: "Wrote it", updated: at)], seen: at), "Reading files", "seen done skipped")
        XCTAssertEqual(line([]), "status bar")
        XCTAssertEqual(TerminalLive.state(title: "", program: nil, records: [], seen: nil, connected: true).line, "")
    }

    /// Through the activity: a record's message wins over the line the
    /// screen read; done, its last message; with none, the screen's.
    @MainActor func testRecordMessageWinsOverScreen() {
        let d = FakeDriver(), w = Waits(), t = Tasks()
        let clock = Date(timeIntervalSince1970: 1_800_000_000)
        let l = live(d, w, t) { clock }
        var records = [Host.ProgramStatus(id: "a", state: "working", app: "claude-code", msg: "Waiting for the dev build", updated: clock)]
        l.show(source(state: { TerminalLive.state(title: "api", program: nil, records: records, seen: nil, connected: true) },
                      lines: { done in done("SWE-2 High · bypass") }), owner: UUID())
        w.run()
        XCTAssertEqual(l.sent?.line, "Waiting for the dev build")

        func step(_ change: () -> Void) {
            change()
            l.changed()
            w.run()
        }
        step { records[0].msg = "   " }
        XCTAssertEqual(l.sent?.line, "SWE-2 High · bypass", "a blank message is none")
        step { records[0].state = "blocked"; records[0].msg = "Allow Bash: make test?" }
        XCTAssertEqual(l.sent?.line, "Allow Bash: make test?")
        step { records[0].state = "done"; records[0].msg = nil; records[0].lastMsg = "Correct the lint count" }
        XCTAssertEqual(l.sent?.line, "Correct the lint count")
        step { records = [] }
        XCTAssertEqual(l.sent?.line, "SWE-2 High · bypass", "no record: the screen's")
        XCTAssertEqual(d.updates.map(\.state.line),
                       ["SWE-2 High · bypass", "Allow Bash: make test?", "Correct the lint count", "SWE-2 High · bypass"])
    }

    /// Attributes and state together stay under ActivityKit's 4 KB with the
    /// longest of each field: titles and names cut to 200 characters and
    /// 400 bytes, the line to 80 characters and 240 bytes, whole characters
    /// of three bytes, of many combining marks or of a ZWJ sequence.
    @MainActor func testSizeUnderLimit() throws {
        let marks = "e" + String(repeating: "\u{301}", count: 30)
        for wide in [String(repeating: "界", count: 1000), String(repeating: marks, count: 100), String(repeating: "👩‍💻", count: 300)] {
            let attributes = TerminalActivity(host: UUID(), hostName: TerminalActivity.cut(wide), session: String(repeating: "s", count: 64),
                                              block: String(repeating: "b", count: 64))
            var r = Self.record(String(repeating: "i", count: 64), "blocked", at: 1_800_000_000, title: wide, app: String(repeating: "a", count: 64), progress: 100)
            r.kind = "permission"
            let s = TerminalLive.state(title: wide, program: String(repeating: "p", count: 64), records: [r], seen: nil, line: wide, connected: true)
            for (field, max) in [(s.title, 400), (s.record?.title ?? "", 400), (s.line, 240), (attributes.hostName, 400)] {
                XCTAssertLessThanOrEqual(field.utf8.count, max)
                XCTAssertGreaterThan(field.utf8.count, max - 61, "cut no shorter than one character needs")
                XCTAssertTrue(wide.hasPrefix(field), "whole characters")
            }
            let size = try JSONEncoder().encode(attributes).count + JSONEncoder().encode(s).count
            XCTAssertLessThan(size, 4096, "\(size) bytes")
        }
        XCTAssertEqual(TerminalLive.state(title: String(repeating: "x", count: 300), program: nil, records: [], seen: nil, connected: true).title.count, 200)
    }

    @MainActor private func source(block: String = "b1", state: @escaping () -> TerminalActivity.ContentState,
                                   lines: @escaping (@escaping @MainActor @Sendable (String) -> Void) -> Void = { _ in }) -> TerminalLive.Source {
        .init(attributes: TerminalActivity(host: UUID(uuidString: "00000000-0000-4000-8000-00000000a11c")!, hostName: "alice-laptop", session: "s1", block: block),
              state: state, readLine: lines)
    }

    @Observable @MainActor final class Model {
        var title = "api"
        var connected = true
    }

    @MainActor private func live(_ driver: FakeDriver, _ waits: Waits, _ tasks: Tasks, clock: @escaping () -> Date) -> TerminalLive {
        let grace = BackgroundGrace(limit: 25, begin: tasks.begin, end: { tasks.ended.append($0) }, now: clock, schedule: waits.schedule)
        return TerminalLive(driver: driver, interval: 2, now: clock, schedule: waits.schedule, grace: grace)
    }

    /// The app leaves the foreground: the screen registers its work, and
    /// the app starts the grace, as GoRexApp does.
    @MainActor private func leave(_ l: TerminalLive, _ suspend: @escaping () -> Void) {
        l.background(suspend: suspend)
        l.grace.start()
    }

    /// The app is in front again: the screen's active, and the app's cancel.
    @MainActor private func back(_ l: TerminalLive) -> Bool {
        let resume = l.active()
        l.grace.cancel()
        return resume
    }

    /// Updates come at most once per two seconds, the changes meanwhile
    /// joining one; one of the same state is not sent. A change of what the
    /// state reads, and output that changes the line, both update.
    @MainActor func testUpdatesCoalesce() {
        let d = FakeDriver(), w = Waits(), t = Tasks()
        var clock = Date(timeIntervalSince1970: 1_800_000_000)
        let l = live(d, w, t) { clock }
        let m = Model()
        var screen = "$ make"
        var reads = 0
        let owner = UUID()
        l.show(source(state: { .init(title: m.title, line: "", connected: m.connected) }, lines: { done in
            reads += 1
            done(screen)
        }), owner: owner)
        XCTAssertEqual(d.calls, ["start b1"])
        XCTAssertEqual(reads, 1)
        // The line read at the start came at once: its update waits two seconds.
        XCTAssertEqual(w.all.count, 1)
        XCTAssertEqual(w.all.first?.wait, 2)

        // Changes meanwhile join it.
        m.title = "api 2"
        l.outputChanged()
        l.changed()
        XCTAssertEqual(w.all.count, 1)
        clock += 2
        screen = "$ make test"
        w.run()
        XCTAssertEqual(d.calls, ["start b1", "update"], "one update for all of them")
        XCTAssertEqual(d.updates.last?.state.title, "api 2")
        XCTAssertNil(d.updates.last?.stale)

        // The output read a new line, which updates two seconds after the last.
        XCTAssertEqual(reads, 2)
        XCTAssertEqual(w.all.first?.wait, 2)
        clock += 0.5
        w.run()
        XCTAssertEqual(d.updates.count, 2)
        XCTAssertEqual(d.updates.last?.state.line, "$ make test")

        // Nothing changed: no update.
        clock += 5
        l.changed()
        XCTAssertEqual(w.all.first?.wait, 0, "the interval has passed")
        w.run()
        XCTAssertEqual(d.updates.count, 2)

        // A change the state observes asks for an update by itself.
        m.connected = false
        let changed = expectation(description: "observed")
        Task { @MainActor in
            while w.all.isEmpty { await Task.yield() }
            changed.fulfill()
        }
        wait(for: [changed], timeout: 5)
        w.run()
        XCTAssertEqual(d.updates.last?.state.connected, false)
        XCTAssertEqual(d.updates.count, 3)
    }

    /// Back within the grace, the streams were never suspended; the task
    /// ends once, and the grace's own end later does nothing.
    @MainActor func testGraceCancelledByReturn() {
        let d = FakeDriver(), w = Waits(), t = Tasks()
        var clock = Date(timeIntervalSince1970: 1_800_000_000)
        let l = live(d, w, t) { clock }
        l.show(source(state: { .init(title: "api", line: "", connected: true) }), owner: UUID())
        var suspends = 0
        leave(l) { suspends += 1 }
        XCTAssertTrue(l.grace.running)
        XCTAssertEqual(t.begun, 1)
        XCTAssertEqual(d.updates.last?.stale, clock + 30, "it goes stale should the app stop unannounced")
        XCTAssertNil(l.grace.awake(since: clock), "a grace keeps the app running")
        clock += 10
        XCTAssertFalse(back(l), "nothing to resume")
        XCTAssertEqual(t.ended, [1])
        XCTAssertNil(d.updates.last?.stale, "in front again, not stale")
        XCTAssertEqual(l.grace.awake(since: clock - 10), clock, "running till now")
        w.run()
        w.run()
        t.expirations[1]?()
        XCTAssertEqual(suspends, 0)
        XCTAssertEqual(t.ended, [1])
        XCTAssertFalse(l.paused)
    }

    /// The grace's 25 seconds end it: the streams suspend once, the
    /// activity says it is paused, stale now, and the task ends once, iOS's
    /// expiration coming later or not. Back in front, the streams resume
    /// and the activity is live again.
    @MainActor func testGraceExpires() {
        let d = FakeDriver(), w = Waits(), t = Tasks()
        var clock = Date(timeIntervalSince1970: 1_800_000_000)
        let l = live(d, w, t) { clock }
        let m = Model()
        l.show(source(state: { .init(title: m.title, line: "", connected: true) }), owner: UUID())
        var suspends = 0
        leave(l) { suspends += 1 }
        XCTAssertEqual(w.all.map(\.wait), [1], "the time left is read a second later")
        clock += 1
        w.run()
        XCTAssertEqual(w.all.map(\.wait), [25])
        clock += 25
        w.run()
        XCTAssertEqual(suspends, 1)
        XCTAssertTrue(l.paused)
        // The paused update goes within the grace's own task, no other.
        XCTAssertEqual(d.calls.last, "update in grace")
        XCTAssertEqual(d.updates.last?.state.connected, false)
        XCTAssertEqual(d.updates.last?.stale, clock)
        XCTAssertEqual(t.begun, 1)
        XCTAssertEqual(t.ended, [], "until ActivityKit has it")
        d.dones[0]()
        XCTAssertEqual(t.ended, [1])
        d.dones[0]()
        let updates = d.updates.count
        t.expirations[1]?()
        XCTAssertEqual(suspends, 1)
        XCTAssertEqual(t.ended, [1])
        // Paused, a change sends nothing.
        m.title = "other"
        l.changed()
        w.run()
        XCTAssertEqual(d.updates.count, updates)

        clock += 60
        XCTAssertEqual(l.grace.awake(since: clock - 86), clock - 60, "the app ran till the grace's end")
        XCTAssertTrue(back(l), "the screen resumes the streams")
        XCTAssertFalse(l.paused)
        XCTAssertEqual(d.updates.last?.state.connected, true)
        XCTAssertEqual(d.updates.last?.state.title, "other")
    }

    /// iOS takes the time back first: the work runs then, once, and the
    /// task ends in the handler; the 25 seconds' end does nothing after.
    /// Without background time, the streams suspend at once, as before.
    @MainActor func testGraceExpiredByIOSOrNotGiven() {
        let d = FakeDriver(), w = Waits(), t = Tasks()
        let clock = Date(timeIntervalSince1970: 1_800_000_000)
        let l = live(d, w, t) { clock }
        l.show(source(state: { .init(title: "api", line: "", connected: true) }), owner: UUID())
        var suspends = 0
        leave(l) { suspends += 1 }
        t.expirations[1]?()
        XCTAssertEqual(suspends, 1)
        XCTAssertEqual(t.ended, [1], "ended in the handler, not after the update")
        XCTAssertEqual(d.calls.last, "update in grace")
        d.dones.removeFirst()()
        w.run()
        w.run()
        XCTAssertEqual(suspends, 1)
        XCTAssertEqual(t.ended, [1])
        XCTAssertTrue(back(l))

        t.gives = false
        leave(l) { suspends += 1 }
        XCTAssertEqual(suspends, 2, "suspended at once")
        XCTAssertFalse(l.grace.running)
        XCTAssertEqual(t.ended, [1], "no task to end")
        XCTAssertTrue(back(l))

        // Leaving twice runs one grace, with the work registered last.
        t.gives = true
        leave(l) { suspends += 100 }
        leave(l) { suspends += 1 }
        XCTAssertEqual(t.begun, 2)
        w.run()
        w.run()
        XCTAssertEqual(suspends, 3)
        // iOS takes the time back while the paused update goes: the task
        // ends then, once.
        XCTAssertEqual(t.ended, [1])
        t.expirations[2]?()
        XCTAssertEqual(t.ended, [1, 2])
        d.dones.removeFirst()()
        XCTAssertEqual(t.ended, [1, 2])
        XCTAssertTrue(back(l))

        // A grace that starts while the last one's update still goes ends
        // the last one's task first.
        leave(l) { suspends += 1 }
        w.run()
        w.run()
        XCTAssertEqual(t.ended, [1, 2])
        XCTAssertTrue(back(l))
        leave(l) { suspends += 1 }
        XCTAssertEqual(t.ended, [1, 2, 3])
        d.dones.removeFirst()()
        XCTAssertEqual(t.ended, [1, 2, 3])
        XCTAssertTrue(l.grace.running)
    }

    /// One activity at a time: another terminal's ends it and starts its
    /// own; the close of a screen no longer shown does nothing; the close of
    /// the one shown ends it, and its work at the grace's end does not run,
    /// though the app's grace goes on. Off in Settings, none starts.
    /// One iOS ended, at 8 hours, starts anew in the foreground.
    @MainActor func testStartAndEnd() {
        let d = FakeDriver(), w = Waits(), t = Tasks()
        let clock = Date(timeIntervalSince1970: 1_800_000_000)
        let l = live(d, w, t) { clock }
        let a = UUID(), b = UUID()
        let state: () -> TerminalActivity.ContentState = { .init(title: "api", line: "", connected: true) }
        l.show(source(block: "b1", state: state), owner: a)
        l.show(source(block: "b1", state: state), owner: a)
        XCTAssertEqual(d.calls, ["start b1"], "the same terminal again only updates")
        // A pane switch: the same screen, another block.
        l.show(source(block: "b2", state: state), owner: a)
        XCTAssertEqual(d.calls, ["start b1", "end", "start b2"])
        // Another screen opens before the first one closes.
        l.show(source(block: "b3", state: state), owner: b)
        l.close(owner: a)
        XCTAssertEqual(d.calls, ["start b1", "end", "start b2", "end", "start b3"])
        XCTAssertEqual(l.owner, b)

        // iOS ended it: the next foreground starts it anew.
        d.running = false
        l.active()
        XCTAssertEqual(d.started.map(\.block), ["b1", "b2", "b3", "b3"])

        // Closed during a grace: the grace goes on, for the hosts, and ends
        // without suspending.
        var suspends = 0
        leave(l) { suspends += 1 }
        l.close(owner: b)
        XCTAssertEqual(d.calls.last, "end")
        XCTAssertTrue(l.grace.running)
        XCTAssertNil(l.owner)
        w.run()
        w.run()
        XCTAssertEqual(suspends, 0)
        XCTAssertEqual(t.ended, [1])
        XCTAssertFalse(back(l))

        // Live Activities off: no activity, but the grace still keeps the streams.
        d.enabled = false
        d.calls = []
        l.show(source(block: "b4", state: state), owner: a)
        l.changed()
        w.run()
        XCTAssertEqual(d.calls, [])
        leave(l) { suspends += 1 }
        XCTAssertTrue(l.grace.running)
        w.run()
        w.run()
        XCTAssertEqual(suspends, 1)
        XCTAssertEqual(d.calls, [])
    }

    /// One the user dismissed starts no more for its screen, as it comes
    /// back to the foreground or switches panes; another screen's starts.
    @MainActor func testDismissedStaysDismissed() {
        let d = FakeDriver(), w = Waits(), t = Tasks()
        let l = live(d, w, t) { Date(timeIntervalSince1970: 1_800_000_000) }
        let a = UUID(), b = UUID()
        let state: () -> TerminalActivity.ContentState = { .init(title: "api", line: "", connected: true) }
        l.show(source(block: "b1", state: state), owner: a)
        d.running = false
        d.dismissed = true
        l.active()
        l.show(source(block: "b1", state: state), owner: a)
        l.show(source(block: "b2", state: state), owner: a)
        XCTAssertEqual(d.started.map(\.block), ["b1"])
        l.show(source(block: "b3", state: state), owner: b)
        XCTAssertEqual(d.started.map(\.block), ["b1", "b3"])
    }

    /// The screen is read for the line only while an activity shows.
    @MainActor func testNoReadWithoutActivity() {
        let d = FakeDriver(), w = Waits(), t = Tasks()
        let l = live(d, w, t) { Date(timeIntervalSince1970: 1_800_000_000) }
        d.enabled = false
        var reads = 0
        l.show(source(state: { .init(title: "api", line: "", connected: true) }, lines: { _ in reads += 1 }), owner: UUID())
        l.outputChanged()
        w.run()
        XCTAssertEqual(reads, 0)
        d.enabled = true
        l.active()
        XCTAssertEqual(d.started.count, 1)
        XCTAssertEqual(reads, 1)
    }

    /// A program that finishes while the app is in the background is not
    /// seen, though its terminal is still open: not here, and not on the
    /// server, which would tell every client. Back in front, the screen
    /// sees it, as it does when it appears.
    @MainActor func testFinishedInBackgroundNotSeen() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        rex.features = [Host.seeFeature]
        rex.respond = { m, _ in m == "session.bootstrap" ? ["attach_all": ["sessions": [["session_id": "s", "label": "s"]]]] : [:] }
        let n = Notifier()
        Host.shown = { n.shows(host: $0.id, block: $1) }
        defer { Host.shown = nil }
        let h = Host(HostConfig(name: "alice-laptop", url: try await rex.url()))
        h.connect()
        defer { h.stop() }
        await until("online") { h.status == .online }
        n.onScreen = TerminalTarget(host: h.id, session: "s", block: "b", title: "api")
        func done(_ at: String) {
            rex.event(["type": "session_event", "payload": ["session_id": "s", "event": [
                "type": "block_event", "payload": ["block_id": "b", "name": "program_status_changed", "payload": [
                    "records": [["id": "claude", "state": "done", "updated_at": at]]]],
            ]]])
        }
        // In front: seen as it comes.
        done("2026-11-20T10:00:00Z")
        await until("seen") { rex.count("gorex.see_block") == 1 && h.status(["b"]) == nil }

        // In the background: not seen.
        n.inFront = false
        done("2026-11-20T10:01:00Z")
        await until("done") { h.blocks["b"]?.statuses?.first?.updated == Date(timeIntervalSince1970: 1_795_168_860) }
        XCTAssertEqual(h.status(["b"])?.state, "done")
        XCTAssertEqual(rex.count("gorex.see_block"), 1)
        XCTAssertEqual(TerminalLive.state(title: "api", program: nil, records: h.blocks["b"]?.statuses ?? [], seen: h.blocks["b"]?.seen,
                                          connected: true).record?.state, "done")

        // Back in front: the screen sees it.
        n.inFront = true
        h.see(session: "s", block: "b")
        await until("seen after") { rex.count("gorex.see_block") == 2 && h.status(["b"]) == nil }
    }

    /// sRGB to OKLCH and back, against Björn Ottosson's values; a color
    /// outside sRGB is clipped into it.
    @MainActor func testOKLCH() {
        let red = OKLCH(rgb: 0xff0000)
        XCTAssertEqual(red.l, 0.62796, accuracy: 1e-4)
        XCTAssertEqual(red.c, 0.25768, accuracy: 1e-4)
        XCTAssertEqual(red.h, 29.234, accuracy: 1e-2)
        let white = OKLCH(rgb: 0xffffff)
        XCTAssertEqual(white.l, 1, accuracy: 1e-4)
        XCTAssertEqual(white.c, 0, accuracy: 1e-4)
        XCTAssertEqual(OKLCH(rgb: 0x0000ff).h, 264.05, accuracy: 1e-1)
        for rgb: UInt32 in [0x000000, 0xffffff, 0xd97757, 0xf7f7f7, 0x111111, 0x1a73e8, 0x5fa04e, 0x7f52ff] {
            XCTAssertEqual(OKLCH(rgb: rgb).rgb, rgb, String(rgb, radix: 16))
        }
        let out = OKLCH(l: 0.7, c: 0.4, h: 140).srgb
        for v in [out.r, out.g, out.b] { XCTAssert((0...1).contains(v), "\(v)") }
    }

    /// The card's ground keeps the tile's hue; a neutral tile gets graphite.
    @MainActor func testCardGround() {
        let claude = Program.of("claude")
        let g = TerminalActivityCard.ground(claude)
        let k = OKLCH(rgb: claude.tileBg)
        XCTAssertEqual(g.top.l, 0.40)
        XCTAssertEqual(g.bottom.l, 0.20)
        XCTAssertEqual(g.top.h, k.h)
        XCTAssertEqual(g.top.c, min(k.c, 0.16) * 0.85, accuracy: 1e-9)
        XCTAssertEqual(g.bottom.c, min(k.c, 0.16) * 0.55, accuracy: 1e-9)
        // A strong tile's chroma is capped.
        XCTAssertEqual(TerminalActivityCard.ground(Program.of("kotlin")).top.c, 0.16 * 0.85, accuracy: 1e-9)
        for p in ["codex", "devin", "zsh", ""] {
            let n = TerminalActivityCard.ground(Program.of(p))
            XCTAssertEqual(n.top.h, 260, p)
            XCTAssertEqual(n.top.c, 0.012 * 0.85, accuracy: 1e-9, p)
        }
    }

    /// The Lock Screen card's states, as testCardHeight and testShots draw them.
    @MainActor static func cardStates(_ program: String, now: Date) -> [(String, TerminalActivity.ContentState)] {
        func record(_ state: String, progress: Int? = nil) -> TerminalActivity.Record {
            .init(state: state, kind: nil, progress: progress, title: "Refactoring the store", updated: now - 252)
        }
        return [
            ("working", .init(title: "api", program: program, record: record("working"), line: "Refactoring the session store", connected: true)),
            ("progress", .init(title: "build", program: program, record: record("working", progress: 64), line: "Running the tests, 77 of 120", connected: true)),
            ("blocked", .init(title: "api", program: program, record: record("blocked"),
                              line: "Do you want to make this edit to internal/session/store.go?", connected: true)),
            ("done", .init(title: "docs", program: program, record: record("done"), line: "Wrote the release notes", connected: true)),
            ("error", .init(title: "web", program: program, record: record("error"), line: "", connected: true)),
            ("paused", .init(title: "api", program: program, record: record("working"), line: "Refactoring the session store", connected: false)),
            ("no-record", .init(title: "api", program: program, record: nil, line: "", connected: true)),
            ("long", .init(title: String(repeating: "Refactor the session store ", count: 6), program: program, record: record("working"),
                           line: String(repeating: "W", count: 80), connected: true)),
        ]
    }

    /// The Lock Screen card is as tall in every state: its rows keep
    /// their heights, the hero two lines, and paused takes the last row.
    @MainActor func testCardHeight() throws {
        let a = TerminalActivity(host: UUID(), hostName: "alice-laptop", session: "s1", block: "b1")
        for program in ["claude", "codex", "devin", "zsh"] {
            var heights: [String: CGFloat] = [:]
            for (name, s) in Self.cardStates(program, now: Date()) {
                let r = ImageRenderer(content: TerminalActivityCard(attributes: a, state: s, stale: false).frame(width: 370))
                r.scale = 1
                heights[name] = try XCTUnwrap(r.uiImage, name).size.height
            }
            print("card heights", program, heights.sorted { $0.key < $1.key })
            XCTAssertEqual(Set(heights.values).count, 1, "\(program): \(heights)")
            XCTAssertLessThanOrEqual(heights.values.max() ?? 0, 160, program)
        }
    }

    /// Draws each face, connected and paused, light and dark, as PNGs in
    /// GOREX_ACTIVITY_SHOTS; skipped without it. Drawn apart from
    /// ActivityKit: the Lock Screen's wallpaper and the Dynamic Island's
    /// black are drawn here.
    @MainActor func testShots() throws {
        guard let dir = ProcessInfo.processInfo.environment["GOREX_ACTIVITY_SHOTS"] else { throw XCTSkip("GOREX_ACTIVITY_SHOTS not set") }
        try FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        let now = Date()
        let host = UUID(uuidString: "00000000-0000-4000-8000-00000000a11c")!
        let short = TerminalActivity(host: host, hostName: "alice-laptop", session: "s1", block: "b1")
        let long = TerminalActivity(host: host, hostName: "alice-laptop-with-a-very-long-host-name.example", session: "s1", block: "b1")
        func record(_ state: String, _ title: String?, progress: Int? = nil, ago: TimeInterval = 75) -> TerminalActivity.Record {
            .init(state: state, kind: nil, progress: progress, title: title, updated: now - ago)
        }
        let cases: [(String, TerminalActivity, TerminalActivity.ContentState)] = [
            ("blocked", short, .init(title: "api", program: "claude", record: record("blocked", "Edit main.go?"), line: "Do you want to make this edit to main.go?", connected: true)),
            ("working", short, .init(title: "api", program: "claude", record: record("working", "Refactoring the store"), line: "✻ Refactoring… (1m 15s · esc to interrupt)", connected: true)),
            ("progress", short, .init(title: "build", program: "cargo", record: record("working", nil, progress: 65), line: "   Compiling serde v1.0.210", connected: true)),
            ("done", short, .init(title: "docs", program: "codex", record: record("done", "Wrote the release notes"), line: "› ", connected: true)),
            ("error", short, .init(title: "web", program: "codex", record: record("error", "Tests failed"), line: "FAIL  3 of 120 tests", connected: true)),
            ("shell", short, .init(title: "~/src/gorex", program: "zsh", record: nil, line: "alice@alice-laptop gorex % git status", connected: true)),
            ("paused", short, .init(title: "api", program: "claude", record: record("working", "Refactoring the store"), line: "✻ Refactoring… (1m 15s · esc to interrupt)", connected: false)),
            ("msg", short, .init(title: "docs", program: "claude", record: record("working", nil), line: "Editing docs/MOD-002-projects.md", connected: true)),
            ("question", short, .init(title: "api", program: "claude", record: record("blocked", nil), line: "Should I also update the tests for the store?", connected: true)),
            ("two-lines", short, .init(title: "api", program: "claude", record: record("working", nil),
                                       line: "Reading internal/session/store.go to find where the scrollback is kept on reconnect", connected: true)),
            ("no-line", short, .init(title: "api", program: "claude", record: record("blocked", nil), line: "", connected: true)),
            ("shell-empty", short, .init(title: "~/src/gorex", program: "zsh", record: nil, line: "", connected: true)),
            ("worst", long, .init(title: String(repeating: "Refactor the session store so that every pane keeps its scrollback ", count: 3),
                                  program: "claude", record: record("blocked", "Allow edits to 14 files in internal/session and internal/rexapi?"),
                                  line: String(repeating: "W", count: 80), connected: true)),
            ("worst-paused", long, .init(title: String(repeating: "Refactor the session store so that every pane keeps its scrollback ", count: 3),
                                         program: "claude", record: record("blocked", "Allow edits to 14 files in internal/session and internal/rexapi?"),
                                         line: String(repeating: "W", count: 80), connected: false)),
            // What a program sends as it is, before shownText: an escape
            // sequence and Nerd Font icons, which drew as text and boxes.
            ("raw", short, .init(title: "\u{E0B0}api", program: "claude", record: record("blocked", "\u{E0B1} Edit main.go?"),
                                 line: "\u{1B}[1mAllow\u{1B}[0m \u{E0B2}edits?", connected: true)),
            ("filtered", short, TerminalLive.state(title: "\u{E0B0}api", program: "claude",
                records: [.init(id: "f", state: "blocked", title: "\u{E0B1} Edit main.go?",
                                msg: "\u{1B}[1mAllow\u{1B}[0m \u{E0B2}edits?")], seen: nil, connected: true)),
        ]
        func write(_ v: some View, _ name: String) throws {
            let r = ImageRenderer(content: v)
            r.scale = 3
            try XCTUnwrap(r.uiImage?.pngData(), name).write(to: URL(filePath: dir).appending(path: "activity-\(name).png"))
        }
        for (name, a, s) in cases {
            for scheme in [ColorScheme.light, .dark] {
                let suffix = scheme == .dark ? "dark" : "light"
                // The Lock Screen: a card over the wallpaper, 160 pt at most;
                // the card draws its own background.
                try write(TerminalActivityCard(attributes: a, state: s, stale: false)
                    .frame(width: 370)
                    .frame(maxHeight: 160)
                    .clipShape(RoundedRectangle(cornerRadius: 24, style: .continuous))
                    .padding(12)
                    .background(LinearGradient(colors: [Color(red: 0.2, green: 0.25, blue: 0.45), Color(red: 0.55, green: 0.35, blue: 0.5)], startPoint: .top, endPoint: .bottom))
                    .environment(\.colorScheme, scheme), "lock-\(name)-\(suffix)")
            }
            // The Dynamic Island is black in either scheme.
            let paused = s.paused(stale: false)
            try write(TerminalActivityFace(attributes: a, state: s, stale: false)
                .padding(.horizontal, 20).padding(.vertical, 16)
                .frame(width: 371)
                .background(.black, in: RoundedRectangle(cornerRadius: 44, style: .continuous))
                .padding(8).background(Color(white: 0.5))
                .environment(\.colorScheme, .dark), "expanded-\(name)")
            try write(HStack {
                TerminalActivityTile(state: s, scale: 1).opacity(paused ? 0.5 : 1)
                Spacer()
                TerminalActivityMark(state: s, paused: paused)
            }
            .padding(.horizontal, 14)
            .frame(width: 250, height: 37)
            .background(.black, in: Capsule())
            .padding(8).background(Color(white: 0.5))
            .environment(\.colorScheme, .dark), "compact-\(name)")
            try write(TerminalActivityTile(state: s, scale: 1).opacity(paused ? 0.5 : 1)
                .frame(width: 37, height: 37)
                .background(.black, in: Circle())
                .padding(8).background(Color(white: 0.5))
                .environment(\.colorScheme, .dark), "minimal-\(name)")
        }
        // The card alone, for each agent and state, as the Lock Screen
        // clips it.
        for program in ["claude", "codex", "devin"] {
            for (name, s) in Self.cardStates(program, now: now) {
                try write(TerminalActivityCard(attributes: short, state: s, stale: false)
                    .frame(width: 370)
                    .clipShape(RoundedRectangle(cornerRadius: 24, style: .continuous))
                    .environment(\.colorScheme, .dark), "card-\(program)-\(name)")
            }
        }
    }
}
