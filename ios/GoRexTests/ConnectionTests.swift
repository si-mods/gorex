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

    /// A gateway that refuses the caller, as one on the tailnet refuses
    /// another Tailscale user, is told from a connection that failed.
    @MainActor func testRefused() async throws {
        let http = try FakeHTTP()
        defer { http.stop() }
        http.respond = { _ in (403, ["error": "this server lets in only the Tailscale user who signed it in"]) }
        let c = ControlConnection(base: try await http.url())
        do {
            try await c.open(token: nil)
            XCTFail("opened")
        } catch let e as RexError {
            XCTAssertEqual(e.code, "refused")
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
        await until("waiting") { s.waitingToSend }
        h.connect()
        await until("written") { self.written(rex).joined() == "ls\r" }
        await until("sent") { !s.waitingToSend }
        s.close()
        h.stop()
    }

    /// A write the server may have run, but never answered, is not
    /// written again: a command would run twice.
    @MainActor func testUnansweredWriteIsNotWrittenTwice() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        let h = try await online(rex)
        h.control?.callWait = .milliseconds(300)
        rex.respond = { method, _ in method == "com.superlogical.terminal.write" ? nil : [:] }
        let s = TerminalSession(host: h, session: "s", block: "b")
        s.type(Data("rm -r build\r".utf8))
        try await Task.sleep(for: .seconds(2))
        XCTAssertEqual(written(rex), ["rm -r build\r"])
        s.close()
        h.stop()
    }

    /// A last byte that is no UTF-8, as an X10 mouse report's coordinate,
    /// is written in a moment, not held until the next key.
    @MainActor func testMouseReportIsNotHeldBack() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        let h = try await online(rex)
        let s = TerminalSession(host: h, session: "s", block: "b")
        let report = Data([0x1B, 0x5B, 0x4D, 0x20, 0x21, 0xC2])
        s.type(report)
        await until("written") { self.written(rex) == [report.text] }
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

    /// A block that could not be owned leaves its stream as it is: the
    /// terminal works, at another size.
    @MainActor func testFailedClaimKeepsTheStream() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        let h = try await online(rex)
        h.control?.callWait = .milliseconds(300)
        rex.respond = { method, _ in
            switch method {
            case "data.connect": ["ticket": "uH4mJKwzMarlTNT-UiyMog"]
            case "session.own_block": nil
            default: [:]
            }
        }
        var conns: [NWConnection] = []
        rex.data = { c, _ in
            conns.append(c)
            FakeRex.send(c, FakeRex.welcome)
        }
        let s = TerminalSession(host: h, session: "s", block: "b")
        s.start()
        await until("claim") { rex.count("session.own_block") == 1 }
        try await Task.sleep(for: .milliseconds(600))
        XCTAssertEqual(s.state, .connecting)
        s.close()
        conns.forEach { $0.cancel() }
        h.stop()
    }

    /// Coming back to a terminal whose window closed keeps telling so,
    /// instead of connecting again.
    @MainActor func testResumeAfterWindowClosedKeepsTheReason() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        let h = try await online(rex)
        rex.respond = { method, _ in method == "data.connect" ? ["ticket": "uH4mJKwzMarlTNT-UiyMog"] : [:] }
        var handshakes = 0
        rex.data = { c, _ in
            handshakes += 1
            if handshakes == 1 {
                FakeRex.send(c, FakeRex.welcome)
                FakeRex.close(c, .privateCode(4000))
            } else {
                c.cancel()
            }
        }
        let closed = TerminalSession.State.failed(String(localized: "The window closed."))
        let s = TerminalSession(host: h, session: "s", block: "b")
        s.start()
        await until("window closed") { s.state == closed }
        s.suspend()
        s.resume()
        try await Task.sleep(for: .seconds(1))
        XCTAssertEqual(s.state, closed)
        XCTAssertEqual(handshakes, 1)
        s.close()
        h.stop()
    }

    /// A data handshake the server refuses closes its socket.
    @MainActor func testRefusedHandshakeClosesTheSocket() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        let h = try await online(rex)
        rex.respond = { method, _ in method == "data.connect" ? ["ticket": "uH4mJKwzMarlTNT-UiyMog"] : [:] }
        var conns: [NWConnection] = []
        var ended = false
        rex.data = { c, _ in
            conns.append(c)
            FakeRex.send(c, Data([0x82, 1]))
            c.receiveMessage { _, _, _, _ in MainActor.assumeIsolated { ended = true } }
        }
        let s = TerminalSession(host: h, session: "s", block: "b")
        s.start()
        await until("failed") { if case .failed = s.state { true } else { false } }
        await until("socket closed") { ended }
        conns.forEach { $0.cancel() }
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

    /// A data connection the server drops shows the terminal reconnecting,
    /// and it connects again.
    @MainActor func testDropShowsReconnecting() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        let h = try await online(rex)
        rex.respond = { method, _ in method == "data.connect" ? ["ticket": "uH4mJKwzMarlTNT-UiyMog"] : [:] }
        var handshakes = 0
        var conns: [NWConnection] = []
        rex.data = { c, _ in
            handshakes += 1
            conns.append(c)
            FakeRex.send(c, FakeRex.welcome)
            if handshakes == 1 { FakeRex.close(c, .protocolCode(.normalClosure)) }
        }
        let s = TerminalSession(host: h, session: "s", block: "b")
        s.start()
        XCTAssertFalse(s.reconnecting)
        await until("reconnecting") { s.reconnecting }
        // Without a snapshot it stays so; it connected again.
        await until("again") { handshakes == 2 }
        s.close()
        conns.forEach { $0.cancel() }
        h.stop()
    }

    /// A server that answers data.connect with an error is not asked
    /// again on its own: Retry asks.
    @MainActor func testServerErrorDoesNotStartAgain() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        let h = try await online(rex)
        rex.respond = { method, _ in
            method == "data.connect" ? ["error": ["code": "unavailable", "message": "no"]] : [:]
        }
        let s = TerminalSession(host: h, session: "s", block: "b")
        s.start()
        await until("failed") { if case .failed = s.state { true } else { false } }
        try await Task.sleep(for: .seconds(3))
        XCTAssertEqual(rex.count("data.connect"), 1)
        s.close()
        h.stop()
    }

    /// A terminal whose data connection keeps failing asks again on its
    /// own, however many times, as a window's pane does: it ends only as
    /// its session does.
    @MainActor func testLostConnectionKeepsConnectingAgain() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        let h = try await online(rex)
        rex.respond = { method, _ in method == "data.connect" ? ["ticket": "uH4mJKwzMarlTNT-UiyMog"] : [:] }
        rex.data = { c, _ in c.cancel() }
        let s = TerminalSession(host: h, session: "s", block: "b")
        s.start()
        // Seven attempts: the waits of a stream that keeps going, at most
        // two seconds each.
        await until("seven attempts", timeout: 30) { rex.count("data.connect") >= 7 }
        if case .failed = s.state { XCTFail("the terminal ended: \(s.state)") }
        XCTAssertTrue(s.reconnecting)
        // It goes on asking: a stream lost for good is not the end of it.
        let asked = rex.count("data.connect")
        await until("another attempt", timeout: 10) { rex.count("data.connect") > asked }
        s.close()
        h.stop()
    }

    @MainActor func testBackoff() {
        XCTAssertEqual(TerminalSession.backoff(after: 1), .milliseconds(250))
        XCTAssertEqual(TerminalSession.backoff(after: 2), .milliseconds(500))
        XCTAssertEqual(TerminalSession.backoff(after: 3), .seconds(1))
        XCTAssertEqual(TerminalSession.backoff(after: 4), .seconds(2))
        XCTAssertEqual(TerminalSession.backoff(after: 7), .seconds(2))
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

    /// Creating a session with a space label forwards `label`, and
    /// `resolveSession(forBlock:fallback:)` resolves the new session ID
    /// when `gorex.spaces_apply` moved the block to another session.
    @MainActor func testNewSessionWithLabelAndResolveMovedBlock() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        rex.respond = { method, payload in
            switch method {
            case "session.bootstrap":
                return ["attach_all": ["sessions": []]]
            case "session.create":
                XCTAssertEqual(payload["label"] as? String, "Work")
                return [
                    "session_id": "session:temp",
                    "initial_windows": [["window_id": "window:1", "block_ids": ["block:1"]]],
                ]
            case "gorex.spaces_apply":
                return ["applied": 1]
            case "target.resolve":
                return ["results": [["query": "block:1", "status": "ok", "target": ["session_id": "session:work"]]]]
            default:
                return [:]
            }
        }
        let h = Host(HostConfig(name: "test", url: try await rex.url()))
        h.connect()
        await until("online") { h.status == .online }
        defer { h.stop() }

        let made = try await h.newSession(label: "Work")
        XCTAssertEqual(made?.0, "session:temp")
        XCTAssertEqual(made?.1, "block:1")

        let resolved = await h.resolveSession(forBlock: "block:1", fallback: "session:temp")
        XCTAssertEqual(resolved, "session:work")
    }
}
