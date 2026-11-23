import AppIntents
import Foundation
import XCTest

/// The Live Activity's Approve and Deny (docs/IOS-006-live-activities.md,
/// Actions): what they send, when they show, the intents' parameters and
/// the send itself over a control connection.
nonisolated final class ActivityActionsTests: XCTestCase {
    /// Approve is Return, which takes the prompt's default choice; deny
    /// is Escape.
    func testActionBytes() {
        XCTAssertEqual(TerminalAction.approve.bytes, "\r")
        XCTAssertEqual(TerminalAction.deny.bytes, "\u{1B}")
    }

    /// The buttons answer a blocked record of kind permission or auth —
    /// a question takes text they cannot hold — and nothing else.
    func testAnswerable() {
        XCTAssertTrue(TerminalActivity.Record.answerable(state: "blocked", kind: "permission"))
        XCTAssertTrue(TerminalActivity.Record.answerable(state: "blocked", kind: "auth"))
        XCTAssertFalse(TerminalActivity.Record.answerable(state: "blocked", kind: "question"))
        XCTAssertFalse(TerminalActivity.Record.answerable(state: "blocked", kind: nil))
        XCTAssertFalse(TerminalActivity.Record.answerable(state: "working", kind: "permission"))
        XCTAssertFalse(TerminalActivity.Record.answerable(state: "error", kind: "auth"))
    }

    /// The state shows the buttons while its top record is answerable.
    func testShowsActions() {
        func state(_ r: TerminalActivity.Record?) -> TerminalActivity.ContentState {
            .init(title: "w", program: nil, record: r, line: "", connected: true)
        }
        XCTAssertTrue(state(.init(state: "blocked", kind: "auth", progress: nil, title: nil, updated: nil)).showsActions)
        XCTAssertTrue(state(.init(state: "blocked", kind: "permission", progress: nil, title: nil, updated: nil)).showsActions)
        XCTAssertFalse(state(.init(state: "blocked", kind: "question", progress: nil, title: nil, updated: nil)).showsActions)
        XCTAssertFalse(state(.init(state: "done", kind: nil, progress: nil, title: nil, updated: nil)).showsActions)
        XCTAssertFalse(state(nil).showsActions)
    }

    /// The sends the stand-in records.
    private nonisolated final class Sent: @unchecked Sendable {
        var calls: [(host: UUID, session: String, block: String, action: TerminalAction)] = []
    }

    /// The intents' parameters: each sends its action with the activity's
    /// host, session and block; a bad target fails before a send.
    func testIntentParameters() async throws {
        let sent = Sent()
        let was = ActivityAnswerer.send
        ActivityAnswerer.send = { sent.calls.append((host: $0, session: $1, block: $2, action: $3)) }
        defer { ActivityAnswerer.send = was }
        let id = UUID()

        _ = try await ApproveIntent(host: id, session: "s1", block: "b1").perform()
        _ = try await DenyIntent(host: id, session: "s1", block: "b1").perform()
        XCTAssertEqual(sent.calls.map(\.host), [id, id])
        XCTAssertEqual(sent.calls.map(\.action), [.approve, .deny])
        XCTAssertEqual(sent.calls.first?.session, "s1")
        XCTAssertEqual(sent.calls.first?.block, "b1")

        sent.calls = []
        do {
            _ = try await ApproveIntent(host: "not-a-host", session: "s1", block: "b1").perform()
            XCTFail("a host id that is no UUID fails")
        } catch ActivityAnswerError.badTarget {}
        do {
            _ = try await DenyIntent(host: id, session: "", block: "b1").perform()
            XCTFail("an empty session fails")
        } catch ActivityAnswerError.badTarget {}
        XCTAssertTrue(sent.calls.isEmpty, "a bad target sends nothing")
    }

    /// An answer that outruns the intent's seconds fails the intent.
    func testAnswerTimeout() async throws {
        let wasSend = ActivityAnswerer.send, wasBudget = ActivityAnswerer.budget
        ActivityAnswerer.send = { _, _, _, _ in try await Task.sleep(for: .seconds(10)) }
        ActivityAnswerer.budget = .milliseconds(50)
        defer { ActivityAnswerer.send = wasSend; ActivityAnswerer.budget = wasBudget }
        do {
            try await ActivityAnswerer.answer(host: UUID().uuidString, session: "s", block: "b", .approve)
            XCTFail("the intent's budget ran out")
        } catch ActivityAnswerError.timeout {}
    }

    /// A host the store does not hold fails the answer.
    @MainActor func testUnknownHost() async throws {
        let store = HostStore(tokens: MemoryLANTokens())
        do {
            try await store.answer(UUID(), session: "s", block: "b", .approve)
            XCTFail("no such host")
        } catch let e as RexError {
            XCTAssertEqual(e.code, "unknown_host")
        }
    }

    /// Over a control connection: the record is read again, still
    /// blocked the answer's bytes go to the block's stdin through the
    /// input path; resolved meanwhile, nothing goes — the second tap on
    /// a resolved record is a no-op.
    @MainActor func testAnswerOverControl() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        var records: [Any] = [["id": "r1", "state": "blocked", "kind": "permission"]]
        rex.respond = { m, _ in
            switch m {
            case "session.bootstrap": ["attach_all": ["sessions": []]]
            case "com.superlogical.terminal.program_status": ["records": records]
            default: [:]
            }
        }
        let h = Host(HostConfig(name: "test", url: try await rex.url()))
        h.connect()
        defer { h.stop() }

        try await h.answerBlock(.approve, session: "s1", block: "b1")
        let w = try XCTUnwrap(rex.payload("com.superlogical.terminal.write"))
        XCTAssertEqual(w["session_id"] as? String, "s1")
        XCTAssertEqual(w["block_id"] as? String, "b1")
        XCTAssertEqual((w["args"] as? [String: Any])?["data"] as? String, "\r")

        // A question kind takes no key either.
        records = [["id": "r2", "state": "blocked", "kind": "question"]]
        try await h.answerBlock(.deny, session: "s1", block: "b1")
        XCTAssertEqual(rex.count("com.superlogical.terminal.write"), 1)

        // Resolved meanwhile: nothing goes.
        records = []
        try await h.answerBlock(.deny, session: "s1", block: "b1")
        XCTAssertEqual(rex.count("com.superlogical.terminal.write"), 1)

        // Still blocked: deny is Escape.
        records = [["id": "r3", "state": "blocked", "kind": "auth"]]
        try await h.answerBlock(.deny, session: "s1", block: "b1")
        XCTAssertEqual(rex.count("com.superlogical.terminal.write"), 2)
        XCTAssertEqual((rex.payload("com.superlogical.terminal.write")?["args"] as? [String: Any])?["data"] as? String, "\u{1B}")
    }
}
