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

    /// What is typed while the host is offline is written once it's back.
    @MainActor func testTypingOfflineWritesOnReconnect() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        rex.respond = { method, _ in method == "session.bootstrap" ? ["attach_all": ["sessions": []]] : [:] }
        let h = Host(HostConfig(name: "test", url: try await rex.url()))
        let s = TerminalSession(host: h, session: "s", block: "b")
        s.type(Data("ls".utf8))
        s.type(Data("\r".utf8))
        h.connect()
        await until("written") { self.written(rex).joined() == "ls\r" }
        s.close()
        h.stop()
    }

    /// A paste larger than the server takes in one message goes in parts,
    /// each well under it and ending on a whole character.
    @MainActor func testLargeWriteGoesInParts() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        let h = try await online(rex)
        let s = TerminalSession(host: h, session: "s", block: "b")
        let text = String(repeating: "中\u{1}ab", count: 300_000)
        s.type(Data(text.utf8))
        await until("written", timeout: 20) { self.written(rex).joined() == text }
        XCTAssertGreaterThan(written(rex).count, 1)
        XCTAssertNotNil(h.control)
        s.close()
        h.stop()
    }

    /// A data connection the server closes for policy, after its WELCOME,
    /// fails the terminal instead of reconnecting forever.
    @MainActor func testPolicyCloseFails() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        let h = try await online(rex)
        rex.respond = { method, _ in method == "data.connect" ? ["ticket": "uH4mJKwzMarlTNT-UiyMog"] : [:] }
        var handshakes = 0
        rex.data = { c, _ in
            handshakes += 1
            FakeRex.send(c, FakeRex.welcome)
            FakeRex.close(c, .protocolCode(.policyViolation))
        }
        let s = TerminalSession(host: h, session: "s", block: "b")
        s.start()
        await until("failed") { if case .failed = s.state { true } else { false } }
        try await Task.sleep(for: .milliseconds(500))
        XCTAssertEqual(handshakes, 1)
        h.stop()
    }

    /// A data connection that keeps dropping is tried again ever later,
    /// not at once.
    @MainActor func testDropsBackOff() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        let h = try await online(rex)
        rex.respond = { method, _ in method == "data.connect" ? ["ticket": "uH4mJKwzMarlTNT-UiyMog"] : [:] }
        var handshakes = 0
        rex.data = { c, _ in
            handshakes += 1
            FakeRex.send(c, FakeRex.welcome)
            FakeRex.close(c, .protocolCode(.goingAway))
        }
        let s = TerminalSession(host: h, session: "s", block: "b")
        s.start()
        try await Task.sleep(for: .seconds(2))
        // At once, then after 0.5 s and 1 s; the next waits 2 s.
        XCTAssertLessThanOrEqual(handshakes, 4)
        XCTAssertGreaterThanOrEqual(handshakes, 2)
        s.close()
        h.stop()
    }

    @MainActor func testBackoff() {
        XCTAssertEqual(TerminalSession.backoff(after: 1), .milliseconds(500))
        XCTAssertEqual(TerminalSession.backoff(after: 2), .seconds(1))
        XCTAssertEqual(TerminalSession.backoff(after: 5), .seconds(8))
        XCTAssertEqual(TerminalSession.backoff(after: 6), .seconds(10))
        XCTAssertNil(TerminalSession.backoff(after: 7))
    }

    /// A terminal closed as its screen went away opens again when it comes back.
    @MainActor func testStartsAgainAfterClose() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        let h = try await online(rex)
        rex.respond = { method, _ in method == "data.connect" ? ["ticket": "uH4mJKwzMarlTNT-UiyMog"] : [:] }
        let s = TerminalSession(host: h, session: "s", block: "b")
        s.start()
        await until("connect") { rex.count("data.connect") == 1 }
        s.close()
        s.start()
        await until("connect again") { rex.count("data.connect") == 2 }
        h.stop()
    }
}
