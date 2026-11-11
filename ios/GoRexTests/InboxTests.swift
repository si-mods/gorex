import XCTest

/// How the inbox picks a window's record, groups the windows, and drops
/// what the user saw, as the desktop does.
nonisolated final class InboxTests: XCTestCase {
    @MainActor private func record(_ id: String, _ state: String, at t: TimeInterval? = nil) -> Host.ProgramStatus {
        var j: [String: Any] = ["id": id, "state": state]
        if let t { j["updated_at"] = Date(timeIntervalSince1970: t).formatted(.iso8601) }
        return Host.ProgramStatus(json: j)!
    }

    /// updated_at reads as the server writes it; Go's zero time and none
    /// are nil.
    @MainActor func testUpdatedParses() {
        let r = Host.ProgramStatus(json: ["state": "done", "updated_at": "2026-11-07T10:20:30Z"])
        XCTAssertEqual(r?.updated, Date(timeIntervalSince1970: 1_794_046_830))
        XCTAssertNil(Host.ProgramStatus(json: ["state": "done", "updated_at": "0001-01-01T00:00:00Z"])?.updated)
        XCTAssertNil(Host.ProgramStatus(json: ["state": "done", "updated_at": ""])?.updated)
        XCTAssertNil(Host.ProgramStatus(json: ["state": "done"])?.updated)
    }

    /// The most urgent record wins, the latest of those alike; done and
    /// error records the user saw don't count, others do.
    @MainActor func testMostUrgent() {
        let rs = [record("a", "working", at: 10), record("b", "done", at: 20), record("c", "done", at: 30), record("d", "idle", at: 40)]
        XCTAssertEqual(Host.ProgramStatus.mostUrgent(rs, seen: [])?.id, "c")
        XCTAssertEqual(Host.ProgramStatus.mostUrgent(rs, seen: [rs[2].seenKey])?.id, "b")
        XCTAssertEqual(Host.ProgramStatus.mostUrgent(rs, seen: [rs[1].seenKey, rs[2].seenKey])?.id, "a")
        let blocked = record("e", "blocked", at: 1)
        XCTAssertEqual(Host.ProgramStatus.mostUrgent(rs + [blocked], seen: [blocked.seenKey])?.id, "e")
        XCTAssertNil(Host.ProgramStatus.mostUrgent([], seen: []))
    }

    /// A record seen working, then done in the same second, is news: the
    /// time alone, of whole seconds, would hide it.
    @MainActor func testSameSecondDoneIsNew() {
        let working = record("x", "working", at: 100), done = record("x", "done", at: 100)
        XCTAssertEqual(Host.ProgramStatus.mostUrgent([done], seen: [working.seenKey])?.state, "done")
        XCTAssertNil(Host.ProgramStatus.mostUrgent([done], seen: [done.seenKey]))
    }

    /// A done record with no time goes once seen.
    @MainActor func testUntimedDoneGoesOnceSeen() {
        let done = record("x", "done")
        XCTAssertEqual(Host.ProgramStatus.mostUrgent([done], seen: [])?.id, "x")
        XCTAssertNil(Host.ProgramStatus.mostUrgent([done], seen: [done.seenKey]))
    }

    /// How long ago, in the locale's narrow units.
    @MainActor func testAgo() {
        let now = Date(timeIntervalSince1970: 1_000_000)
        func ago(_ s: TimeInterval, _ l: String) -> String { Inbox.ago(now - s, now: now, locale: Locale(identifier: l)) }
        XCTAssertEqual(ago(59, "en"), "now")
        XCTAssertEqual(ago(5 * 60 + 59, "en"), "5m")
        XCTAssertEqual(ago(3 * 3600 + 3599, "en"), "3h")
        XCTAssertEqual(ago(2 * 86400 + 5, "en"), "2d")
        print("ago zh-Hans:", ago(300, "zh-Hans"), ago(3 * 3600, "zh-Hans"), ago(2 * 86400, "zh-Hans"))
        XCTAssertNotEqual(ago(300, "zh-Hans"), "5m")
    }

    /// Groups go blocked, error, done, working, the newest first in each.
    @MainActor func testGroups() {
        func item(_ b: String, _ r: Host.ProgramStatus) -> InboxItem {
            InboxItem(host: UUID(), session: "s", block: b, title: b, program: nil, status: r)
        }
        let items = [item("w1", record("x", "working", at: 1)), item("d1", record("x", "done", at: 1)),
                     item("b1", record("x", "blocked", at: 1)), item("b2", record("x", "blocked", at: 2)),
                     item("e1", record("x", "error", at: 1))]
        let g = Inbox.groups(items)
        XCTAssertEqual(g.map(\.state), ["blocked", "error", "done", "working"])
        XCTAssertEqual(g[0].items.map(\.block), ["b2", "b1"])
    }

    /// The lines and the search, as the desktop's palette tells a record.
    @MainActor func testItemText() {
        var r = record("x", "blocked")
        r.msg = "Allow?"
        let a = InboxItem(host: UUID(), session: "s", block: "b", title: "zsh", program: nil, status: r)
        XCTAssertEqual(a.heading, "zsh")
        XCTAssertEqual(a.detail, "Allow?")
        r.msg = nil
        r.title = "Edit"
        let b = InboxItem(host: UUID(), session: "s", block: "b", title: "zsh", program: nil, status: r)
        XCTAssertEqual(b.heading, "Edit")
        XCTAssertEqual(b.detail, "zsh")
        let c = InboxItem(host: UUID(), session: "s", block: "b", title: "Edit", program: nil, status: r)
        XCTAssertEqual(c.detail, "")
        XCTAssertTrue(a.matches("allow") && a.matches("ZSH") && a.matches(""))
        XCTAssertFalse(a.matches("edit"))
        // A record done without a message shows the last one it carried.
        var d = record("x", "done")
        d.lastMsg = "Testing"
        let e = InboxItem(host: UUID(), session: "s", block: "b", title: "zsh", program: nil, status: d)
        XCTAssertEqual(e.detail, "Testing")
        XCTAssertTrue(e.matches("test"))
        d.msg = "Uploading"
        let f = InboxItem(host: UUID(), session: "s", block: "b", title: "zsh", program: nil, status: d)
        XCTAssertEqual(f.detail, "Uploading")
        XCTAssertFalse(f.matches("test"))
        // Working without a message: its last one is of an earlier step.
        var g = record("x", "working")
        g.lastMsg = "Testing"
        XCTAssertEqual(InboxItem(host: UUID(), session: "s", block: "b", title: "zsh", program: nil, status: g).detail, "")
    }

    /// A host lists its windows by their tiled blocks' records, leaves out
    /// idle ones, opens the block whose record it shows, and drops a done
    /// one once the user saw that block.
    @MainActor func testHostInbox() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        let view: [String: Any] = ["revision": 1, "session_id": "s", "label": "s", "windows": [
            ["window_id": "w1", "label": "One", "layers": [["blocks": [["block_id": "a"], ["block_id": "b"]]]]],
            ["window_id": "w2", "label": "Two", "layers": [["blocks": [["block_id": "c"]]]]],
        ]]
        let records: [String: [[String: Any]]] = [
            "a": [["id": "omp", "state": "working", "updated_at": "2026-11-07T10:00:00Z"]],
            "b": [["id": "claude", "state": "done", "msg": "Fixed", "updated_at": "2026-11-07T10:05:00Z"]],
            "c": [["id": "", "state": "idle", "updated_at": "2026-11-07T10:06:00Z"]],
        ]
        rex.respond = { m, p in
            switch m {
            case "session.bootstrap":
                ["attach_all": ["sessions": [["session_id": "s", "label": "s"]]], "selected_session": ["view": view]]
            case "com.superlogical.terminal.program_status":
                ["records": records[(p["block_id"] as? String) ?? ""] ?? []]
            default: [:]
            }
        }
        let h = Host(HostConfig(name: "test", url: try await rex.url()))
        h.connect()
        defer { h.stop() }
        await until("read") { h.status == .online && h.blocks.values.filter { $0.statuses != nil }.count == 3 }
        XCTAssertEqual(h.inbox.map(\.title), ["One"])
        XCTAssertEqual(h.inbox.first?.status.state, "done")
        XCTAssertEqual(h.inbox.first?.block, "b")
        h.see("a")
        XCTAssertEqual(h.inbox.first?.status.state, "done")
        h.see("b")
        XCTAssertEqual(h.inbox.first?.status.state, "working")
        XCTAssertEqual(h.inbox.first?.block, "a")
    }

    /// A block on screen sees its records as they come.
    @MainActor func testShownBlockSeesNews() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        let view: [String: Any] = ["revision": 1, "session_id": "s", "label": "s", "windows": [
            ["window_id": "w", "label": "W", "layers": [["blocks": [["block_id": "b"]]]]],
        ]]
        rex.respond = { m, _ in
            switch m {
            case "session.bootstrap":
                ["attach_all": ["sessions": [["session_id": "s", "label": "s"]]], "selected_session": ["view": view]]
            case "com.superlogical.terminal.program_status":
                ["records": [["id": "x", "state": "working"]]]
            default: [:]
            }
        }
        var shown = true
        Host.shown = { _, b in shown && b == "b" }
        defer { Host.shown = nil }
        let h = Host(HostConfig(name: "test", url: try await rex.url()))
        h.connect()
        defer { h.stop() }
        await until("read") { h.status == .online && h.blocks["b"]?.statuses != nil }
        func done(_ at: String) {
            rex.event(["type": "session_event", "payload": ["session_id": "s", "event": [
                "type": "block_event", "payload": ["block_id": "b", "name": "program_status_changed", "payload": [
                    "records": [["id": "x", "state": "done", "updated_at": at]]]],
            ]]])
        }
        done("2026-11-07T10:00:00Z")
        await until("done") { h.blocks["b"]?.statuses?.first?.state == "done" }
        XCTAssertTrue(h.inbox.isEmpty)
        shown = false
        done("2026-11-07T10:01:00Z")
        await until("done again") { h.blocks["b"]?.statuses?.first?.updated == Date(timeIntervalSince1970: 1_794_045_660) }
        XCTAssertEqual(h.inbox.first?.status.state, "done")
    }
}
