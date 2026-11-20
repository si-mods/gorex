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
        var calls: [String] = []
        var started: [TerminalActivity] = []
        var updates: [(state: TerminalActivity.ContentState, stale: Date?)] = []

        func start(_ attributes: TerminalActivity, _ state: TerminalActivity.ContentState) {
            calls.append("start \(attributes.block)")
            started.append(attributes)
            running = true
        }

        func update(_ state: TerminalActivity.ContentState, stale: Date?) {
            calls.append("update")
            updates.append((state, stale))
        }

        func end() {
            calls.append("end")
            running = false
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
    /// none. Done and error records seen don't count, but in the background
    /// those since the app left do, though the open screen marked them.
    @MainActor func testStateRanksOneBlock() {
        let rs = [Self.record("a", "working", at: 100, progress: 40), Self.record("b", "done", at: 200, title: "Wrote it"),
                  Self.record("c", "blocked", at: 50, title: "Edit main.go?", app: "claude-code")]
        var s = TerminalLive.state(title: "api", program: "zsh", records: rs, seen: nil, away: nil, connected: true)
        XCTAssertEqual(s.record?.state, "blocked")
        XCTAssertEqual(s.record?.title, "Edit main.go?")
        XCTAssertEqual(s.program, "claude-code", "a known program the record names gives the tile")
        XCTAssertEqual(s.title, "api")

        s = TerminalLive.state(title: "api", program: "zsh", records: Array(rs.prefix(2)), seen: nil, away: nil, connected: true)
        XCTAssertEqual(s.record?.state, "done")
        XCTAssertEqual(s.program, "zsh", "no app: the block's program")
        s = TerminalLive.state(title: "api", program: nil, records: Array(rs.prefix(2)), seen: Date(timeIntervalSince1970: 200), away: nil, connected: true)
        XCTAssertEqual(s.record?.state, "working", "the done record was seen")
        XCTAssertEqual(s.record?.progress, 40)
        s = TerminalLive.state(title: "api", program: nil, records: Array(rs.prefix(2)), seen: Date(timeIntervalSince1970: 200),
                               away: Date(timeIntervalSince1970: 150), connected: true)
        XCTAssertEqual(s.record?.state, "done", "done after the app left: not seen")
        s = TerminalLive.state(title: "api", program: nil, records: Array(rs.prefix(2)), seen: Date(timeIntervalSince1970: 200),
                               away: Date(timeIntervalSince1970: 250), connected: true)
        XCTAssertEqual(s.record?.state, "working", "done before the app left, and seen")

        let two = [Self.record("x", "working", at: 10), Self.record("y", "working", at: 20, title: "later")]
        XCTAssertEqual(TerminalLive.state(title: "", program: nil, records: two, seen: nil, away: nil, connected: true).record?.title, "later")
        XCTAssertNil(TerminalLive.state(title: "", program: nil, records: [Self.record("i", "idle", at: 1)], seen: nil, away: nil, connected: true).record)
        XCTAssertNil(TerminalLive.state(title: "", program: nil, records: [], seen: nil, away: nil, connected: false).record)
    }

    /// The line is the screen's last that is not blank, its trailing spaces
    /// gone, cut to 80 characters, whole characters.
    func testLastLine() {
        XCTAssertEqual(TerminalActivity.lastLine("one\ntwo  \n   \n\n"), "two")
        XCTAssertEqual(TerminalActivity.lastLine("  indented\n"), "  indented")
        XCTAssertEqual(TerminalActivity.lastLine(""), "")
        XCTAssertEqual(TerminalActivity.lastLine("\n \n"), "")
        XCTAssertEqual(TerminalActivity.lastLine("a\r\nb\r\n"), "b")
        let long = String(repeating: "界", count: 100)
        XCTAssertEqual(TerminalActivity.lastLine("x\n" + long), String(repeating: "界", count: 80))
        let emoji = String(repeating: "👩‍💻", count: 90)
        XCTAssertEqual(TerminalActivity.lastLine(emoji).count, 80)
    }

    /// Attributes and state together stay under ActivityKit's 4 KB with the
    /// longest of each field: titles and names cut to 200 characters of
    /// three bytes each, the line to 80 of the widest characters.
    @MainActor func testSizeUnderLimit() throws {
        let wide = String(repeating: "界", count: 1000)
        let attributes = TerminalActivity(host: UUID(), hostName: TerminalActivity.cut(wide), session: String(repeating: "s", count: 64),
                                          block: String(repeating: "b", count: 64))
        var r = Self.record(String(repeating: "i", count: 64), "blocked", at: 1_800_000_000, title: wide, app: String(repeating: "a", count: 64), progress: 100)
        r.kind = "permission"
        let s = TerminalLive.state(title: wide, program: String(repeating: "p", count: 64), records: [r], seen: nil, away: nil,
                                   line: String(repeating: "👩‍💻", count: 200), connected: true)
        XCTAssertEqual(s.title.count, 200)
        XCTAssertEqual(s.record?.title?.count, 200)
        XCTAssertEqual(s.line.count, 80)
        let size = try JSONEncoder().encode(attributes).count + JSONEncoder().encode(s).count
        XCTAssertLessThan(size, 4096, "\(size) bytes")
    }

    @MainActor private func source(block: String = "b1", state: @escaping (Date?) -> TerminalActivity.ContentState,
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
        l.show(source(state: { _ in .init(title: m.title, line: "", connected: m.connected) }, lines: { done in
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
        l.show(source(state: { _ in .init(title: "api", line: "", connected: true) }), owner: UUID())
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
        l.show(source(state: { _ in .init(title: m.title, line: "", connected: true) }), owner: UUID())
        var suspends = 0
        l.background { suspends += 1 }
        XCTAssertEqual(w.all.map(\.wait), [25])
        clock += 25
        w.run()
        XCTAssertEqual(suspends, 1)
        XCTAssertEqual(t.ended, [1])
        XCTAssertTrue(l.paused)
        XCTAssertEqual(d.updates.last?.state.connected, false)
        XCTAssertEqual(d.updates.last?.stale, clock)
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
        l.show(source(state: { _ in .init(title: "api", line: "", connected: true) }), owner: UUID())
        var suspends = 0
        l.background { suspends += 1 }
        t.expirations[1]?()
        XCTAssertEqual(suspends, 1)
        XCTAssertEqual(t.ended, [1])
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
        XCTAssertEqual(t.ended, [1, 2])
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
        let state: (Date?) -> TerminalActivity.ContentState = { _ in .init(title: "api", line: "", connected: true) }
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
