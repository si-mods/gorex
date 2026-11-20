import SwiftUI
import XCTest

/// The terminal's Live Activity, apart from ActivityKit: its content, its
/// updates, the grace on leaving the app, and when it starts and ends
/// (docs/ios-live-activities.md, Terminal activity). The clock and the
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

    /// A blocked record's message is the line, in place of the screen's;
    /// a working one's, or a blocked one with none, leaves the screen's.
    @MainActor func testBlockedMessageIsTheLine() {
        let blocked = Host.ProgramStatus(id: "a", state: "blocked", app: "claude-code", title: "Permission",
                                         msg: "Allow Bash: rm -rf build?\nyes / no", updated: Date(timeIntervalSince1970: 10))
        var s = TerminalLive.state(title: "api", program: nil, records: [blocked], seen: nil, line: "status bar", connected: true)
        XCTAssertEqual(s.line, "Allow Bash: rm -rf build? yes / no")
        var none = blocked
        none.msg = nil
        s = TerminalLive.state(title: "api", program: nil, records: [none], seen: nil, line: "status bar", connected: true)
        XCTAssertEqual(s.line, "status bar")
        let working = Host.ProgramStatus(id: "b", state: "working", msg: "Reading files", updated: Date(timeIntervalSince1970: 20))
        s = TerminalLive.state(title: "api", program: nil, records: [working], seen: nil, line: "✻ Reading…", connected: true)
        XCTAssertEqual(s.line, "✻ Reading…")
        s = TerminalLive.state(title: "api", program: nil, records: [], seen: nil, line: "", connected: true)
        XCTAssertEqual(s.line, "")
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
        l.background { suspends += 1 }
        XCTAssertTrue(l.grace.running)
        XCTAssertEqual(t.begun, 1)
        XCTAssertEqual(d.updates.last?.stale, clock + 30, "it goes stale should the app stop unannounced")
        XCTAssertNil(l.grace.awake(since: clock), "a grace keeps the app running")
        clock += 10
        XCTAssertFalse(l.active(), "nothing to resume")
        XCTAssertEqual(t.ended, [1])
        XCTAssertNil(d.updates.last?.stale, "in front again, not stale")
        XCTAssertEqual(l.grace.awake(since: clock - 10), clock, "running till now")
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
        l.background { suspends += 1 }
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
        XCTAssertEqual(l.grace.awake(since: clock - 85), clock - 60, "the app ran till the grace's end")
        XCTAssertTrue(l.active(), "the screen resumes the streams")
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
        l.background { suspends += 1 }
        t.expirations[1]?()
        XCTAssertEqual(suspends, 1)
        XCTAssertEqual(t.ended, [1], "ended in the handler, not after the update")
        XCTAssertEqual(d.calls.last, "update in grace")
        d.dones.removeFirst()()
        w.run()
        XCTAssertEqual(suspends, 1)
        XCTAssertEqual(t.ended, [1])
        XCTAssertTrue(l.active())

        t.gives = false
        l.background { suspends += 1 }
        XCTAssertEqual(suspends, 2, "suspended at once")
        XCTAssertFalse(l.grace.running)
        XCTAssertEqual(t.ended, [1], "no task to end")
        XCTAssertTrue(l.active())

        // Leaving twice runs one grace.
        t.gives = true
        l.background { suspends += 1 }
        l.background { suspends += 100 }
        XCTAssertEqual(t.begun, 2)
        w.run()
        XCTAssertEqual(suspends, 3)
        // iOS takes the time back while the paused update goes: the task
        // ends then, once.
        XCTAssertEqual(t.ended, [1])
        t.expirations[2]?()
        XCTAssertEqual(t.ended, [1, 2])
        d.dones.removeFirst()()
        XCTAssertEqual(t.ended, [1, 2])
        XCTAssertTrue(l.active())

        // A grace that starts while the last one's update still goes ends
        // the last one's task first.
        l.background { suspends += 1 }
        w.run()
        XCTAssertEqual(t.ended, [1, 2])
        XCTAssertTrue(l.active())
        l.background { suspends += 1 }
        XCTAssertEqual(t.ended, [1, 2, 3])
        d.dones.removeFirst()()
        XCTAssertEqual(t.ended, [1, 2, 3])
        XCTAssertTrue(l.grace.running)
    }

    /// One activity at a time: another terminal's ends it and starts its
    /// own; the close of a screen no longer shown does nothing; the close of
    /// the one shown ends it and its grace. Off in Settings, none starts.
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

        // Closed during a grace: the grace ends, without suspending.
        var suspends = 0
        l.background { suspends += 1 }
        l.close(owner: b)
        XCTAssertEqual(d.calls.last, "end")
        XCTAssertEqual(t.ended, [1])
        XCTAssertNil(l.owner)
        w.run()
        XCTAssertEqual(suspends, 0)

        // Live Activities off: no activity, but the grace still keeps the streams.
        d.enabled = false
        d.calls = []
        l.show(source(block: "b4", state: state), owner: a)
        l.changed()
        w.run()
        XCTAssertEqual(d.calls, [])
        l.background { suspends += 1 }
        XCTAssertTrue(l.grace.running)
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

    /// Draws each face, connected and paused, light and dark, as PNGs in
    /// GOREX_ACTIVITY_SHOTS; skipped without it. Drawn apart from
    /// ActivityKit: the Lock Screen's card and the Dynamic Island's black
    /// are drawn here.
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
            ("worst", long, .init(title: String(repeating: "Refactor the session store so that every pane keeps its scrollback ", count: 3),
                                  program: "claude", record: record("blocked", "Allow edits to 14 files in internal/session and internal/rexapi?"),
                                  line: String(repeating: "W", count: 80), connected: true)),
            ("worst-paused", long, .init(title: String(repeating: "Refactor the session store so that every pane keeps its scrollback ", count: 3),
                                         program: "claude", record: record("blocked", "Allow edits to 14 files in internal/session and internal/rexapi?"),
                                         line: String(repeating: "W", count: 80), connected: false)),
        ]
        func write(_ v: some View, _ name: String) throws {
            let r = ImageRenderer(content: v)
            r.scale = 3
            try XCTUnwrap(r.uiImage?.pngData(), name).write(to: URL(filePath: dir).appending(path: "activity-\(name).png"))
        }
        for (name, a, s) in cases {
            for scheme in [ColorScheme.light, .dark] {
                let suffix = scheme == .dark ? "dark" : "light"
                // The Lock Screen: a card over the wallpaper, 160 pt at most.
                try write(TerminalActivityFace(attributes: a, state: s, stale: false)
                    .padding(16)
                    .frame(width: 370)
                    .frame(maxHeight: 160)
                    .background(scheme == .dark ? Color(white: 0.12).opacity(0.85) : Color(white: 0.97).opacity(0.85), in: RoundedRectangle(cornerRadius: 24, style: .continuous))
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
    }
}
