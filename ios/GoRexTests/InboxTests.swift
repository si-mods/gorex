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
    /// error records of before seen don't count, others do.
    @MainActor func testMostUrgent() {
        let rs = [record("a", "working", at: 10), record("b", "done", at: 20), record("c", "done", at: 30), record("d", "idle", at: 40)]
        XCTAssertEqual(Host.ProgramStatus.mostUrgent(rs, seen: nil)?.id, "c")
        XCTAssertEqual(Host.ProgramStatus.mostUrgent(rs, seen: Date(timeIntervalSince1970: 25))?.id, "c")
        XCTAssertEqual(Host.ProgramStatus.mostUrgent(rs, seen: Date(timeIntervalSince1970: 30))?.id, "a")
        XCTAssertEqual(Host.ProgramStatus.mostUrgent(rs + [record("e", "blocked", at: 1)], seen: .now)?.id, "e")
        XCTAssertEqual(Host.ProgramStatus.mostUrgent([record("f", "error", at: 5)], seen: .now)?.id, nil)
        XCTAssertNil(Host.ProgramStatus.mostUrgent([], seen: nil))
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
    }

    /// A host lists its windows by their tiled blocks' records, leaves out
    /// idle ones, and drops a done one once the user saw its window.
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
        await until("read") { h.status == .online && h.statuses.count == 3 }
        XCTAssertEqual(h.inbox.map(\.title), ["One"])
        XCTAssertEqual(h.inbox.first?.status.state, "done")
        XCTAssertEqual(h.inbox.first?.block, "a")
        h.see("a")
        XCTAssertEqual(h.inbox.first?.status.state, "working")
    }
}
