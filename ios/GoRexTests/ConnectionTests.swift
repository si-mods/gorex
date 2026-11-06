import Foundation
import Network
import XCTest

/// What the app does with its connections when the server misbehaves, the
/// path dies or the host is offline, against a fake server.
nonisolated final class ConnectionTests: XCTestCase {
    /// A host online at a fake server that has no sessions.
    @MainActor private func online(_ rex: FakeRex) async throws -> Host {
        rex.respond = { method, _ in method == "session.bootstrap" ? ["attach_all": ["sessions": []]] : [:] }
        let h = Host(HostConfig(name: "test", url: try await rex.url()))
        h.connect()
        await until("online") { h.status == .online }
        return h
    }

    /// The text of the terminal writes the server got, joined.
    @MainActor private func written(_ rex: FakeRex) -> [String] {
        zip(rex.calls, rex.payloads).compactMap { m, p in
            m == "com.superlogical.terminal.write" ? (p["args"] as? [String: Any])?["data"] as? String : nil
        }
    }

    /// A view announced for a session the host doesn't have is read once,
    /// not again and again.
    @MainActor func testViewOfUnknownSessionIsNotReadForever() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        let h = try await online(rex)
        rex.respond = { method, _ in
            method == "session.view" ? ["revision": 5, "session_id": "gone", "windows": []] : [:]
        }
        rex.event(["type": "session_event", "payload": ["session_id": "gone",
                   "event": ["type": "session_view_changed", "payload": ["revision": 5]]]])
        try await Task.sleep(for: .milliseconds(500))
        XCTAssertLessThanOrEqual(rex.count("session.view"), 1)
        h.stop()
    }

    /// A server that stops answering pings is a dead connection.
    @MainActor func testNoPongClosesTheConnection() async throws {
        let rex = try FakeRex(pongs: false)
        defer { rex.stop() }
        let c = ControlConnection(base: try await rex.url())
        c.pingEvery = .milliseconds(200)
        c.pongWait = .milliseconds(300)
        var closed = false
        c.onClose = { _ in closed = true }
        try await c.open(token: nil)
        await until("close") { closed }
    }

    /// A call the server never answers fails after a while.
    @MainActor func testUnansweredCallTimesOut() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        let c = ControlConnection(base: try await rex.url())
        c.callWait = .milliseconds(300)
        try await c.open(token: nil)
        rex.respond = { _, _ in nil }
        do {
            try await c.call("session.bootstrap")
            XCTFail("the call returned")
        } catch {
            XCTAssertEqual((error as? RexError)?.code, "timeout")
        }
        c.close()
    }
}
