import XCTest

/// Grouping windows by the desktop's spaces across hosts (Spaces.swift).
nonisolated final class SpacesTests: XCTestCase {
    /// A session's view of windows, each with its tiled blocks and, after
    /// a colon, a floating one.
    @MainActor private func view(_ session: String, _ windows: [(String, [String], String?)]) throws -> SessionView {
        try SessionView(json: [
            "revision": 1, "session_id": session,
            "windows": windows.map { id, tiled, float in
                var layers: [[String: Any]] = [["blocks": tiled.map { ["block_id": $0] }]]
                if let float { layers.append(["blocks": [["block_id": float]]]) }
                return ["window_id": id, "layers": layers] as [String: Any]
            },
        ])
    }

    @MainActor private func member(_ machine: String, _ block: String) -> Spaces.Member {
        try! JSONDecoder().decode(Spaces.Member.self, from: Data(#"{"machine":"\#(machine)","block_id":"\#(block)"}"#.utf8))
    }

    private let mac = UUID(), studio = UUID(), other = UUID()

    /// The desktop "mac" (machine M) with space "work": its own window w1,
    /// and panes of machine S (one in a floating layer), of a machine the
    /// app doesn't reach, and of a block S doesn't show. S is "studio".
    @MainActor private func sources() throws -> [CrossHost.Source] {
        [
            CrossHost.Source(host: mac, machine: "M", sessions: [
                .init(id: "work", view: try view("work", [("w1", ["b1"], nil)]), remote: [
                    member("S", "s1"), member("S", "s3"), member("X", "x1"), member("S", "gone"),
                ]),
            ]),
            CrossHost.Source(host: studio, machine: "S", sessions: [
                .init(id: "desk", view: try view("desk", [("d1", ["s1"], nil), ("d2", ["s2"], nil), ("d3", ["s4"], "s3")]), remote: []),
            ]),
        ]
    }

    /// /api/spaces's answer reads as the gateway writes it.
    @MainActor func testAnswerDecodes() throws {
        let a = try Spaces.Answer(json: Data(#"""
        {"machine_id":"9c63","sessions":[{"session_id":"session:a","revision":4,
          "remote_blocks":[{"machine":"f4b7","block_id":"block:b"}]}]}
        """#.utf8))
        XCTAssertEqual(a.machine_id, "9c63")
        XCTAssertEqual(a.sessions.first?.session_id, "session:a")
        XCTAssertEqual(a.sessions.first?.remote_blocks, [member("f4b7", "block:b")])
    }

    /// The setting is desktop spaces until set, and stays a known value.
    @MainActor func testGroupingSetting() {
        XCTAssertEqual(Grouping.standard, .spaces)
        XCTAssertEqual(Grouping(stored: nil), .spaces)
        XCTAssertEqual(Grouping(stored: "hosts"), .hosts)
        XCTAssertEqual(Grouping(stored: "spaces"), .spaces)
        XCTAssertEqual(Grouping(stored: "bogus"), .spaces)
    }

    /// A space shows the windows of another host that hold its panes, in
    /// its order, a floating one among them; they leave their host's
    /// list. Panes of a machine not reached, or not shown there, are left
    /// out; the other host's other windows stay.
    @MainActor func testSpaceTakesOtherHostsWindows() throws {
        let c = CrossHost(try sources())
        XCTAssertEqual(c.rows(mac, "work"), [
            .init(host: studio, session: "desk", window: "d1"),
            .init(host: studio, session: "desk", window: "d3"),
        ])
        XCTAssertTrue(c.isMoved(studio, "desk", "d1"))
        XCTAssertTrue(c.isMoved(studio, "desk", "d3"))
        XCTAssertFalse(c.isMoved(studio, "desk", "d2"))
        XCTAssertFalse(c.isMoved(mac, "work", "w1"))
        XCTAssertEqual(c.rows(studio, "desk"), [])
    }

    /// A session is no space when every window of it is in another
    /// host's space; one with a window left, one with none, or one that
    /// holds another host's windows stays.
    @MainActor func testSessionGone() throws {
        let c = CrossHost(try sources())
        XCTAssertTrue(c.isGone(studio, "desk", windows: ["d1", "d3"]))
        XCTAssertFalse(c.isGone(studio, "desk", windows: ["d1", "d2", "d3"]))
        XCTAssertFalse(c.isGone(studio, "empty", windows: []))
        XCTAssertFalse(c.isGone(mac, "work", windows: ["w1"]))
        // Its own window in another host's space, but it holds studio's.
        var s = try sources()
        s.append(CrossHost.Source(host: other, machine: "O", sessions: [
            .init(id: "o", view: try view("o", []), remote: [member("M", "b1")]),
        ]))
        let moved = CrossHost(s)
        XCTAssertTrue(moved.isMoved(mac, "work", "w1"))
        XCTAssertFalse(moved.isGone(mac, "work", windows: ["w1"]))
    }

    /// Without a host the app added and reaches, nothing of it shows, and
    /// nothing moves: the desktop's answer alone opens nothing.
    @MainActor func testOnlyHostsTheAppReaches() throws {
        let c = CrossHost(Array(try sources().prefix(1)))
        XCTAssertEqual(c.rows(mac, "work"), [])
        XCTAssertTrue(c.moved.isEmpty)
        // A host the app reaches but that names no machine, as an older
        // server, matches nothing either.
        var s = try sources()
        s[1] = CrossHost.Source(host: studio, machine: nil, sessions: s[1].sessions)
        XCTAssertTrue(CrossHost(s).joined.isEmpty)
    }

    /// A window shows once: the first space to hold it has it, and a
    /// space's pane of its own host adds nothing.
    @MainActor func testEachWindowOnce() throws {
        var s = try sources()
        s.append(CrossHost.Source(host: other, machine: "O", sessions: [
            .init(id: "copy", view: try view("copy", []), remote: [member("S", "s1"), member("O", "o1")]),
        ]))
        let c = CrossHost(s)
        XCTAssertEqual(c.rows(mac, "work").map(\.window), ["d1", "d3"])
        XCTAssertEqual(c.rows(other, "copy"), [])
    }

    /// A machine the app reaches by two addresses is matched by the first.
    @MainActor func testMachineAddedTwice() throws {
        var s = try sources()
        s.insert(CrossHost.Source(host: other, machine: "S", sessions: [
            .init(id: "lan", view: try view("lan", [("l1", ["s1"], nil)]), remote: []),
        ]), at: 1)
        XCTAssertEqual(CrossHost(s).rows(mac, "work"), [.init(host: other, session: "lan", window: "l1")])
    }

    /// A host keeps what /api/spaces answered, and forgets it for a server
    /// that serves none.
    @MainActor func testHostTakesSpaces() throws {
        let h = Host(HostConfig(name: "mac", url: URL(string: "http://127.0.0.1:1")!))
        h.take(try Spaces.Answer(json: Data(#"""
        {"machine_id":"M","sessions":[{"session_id":"work","revision":1,"remote_blocks":[{"machine":"S","block_id":"s1"}]}]}
        """#.utf8)))
        XCTAssertEqual(h.machine, "M")
        XCTAssertEqual(h.remote["work"], [member("S", "s1")])
        XCTAssertEqual(h.crossHost?.machine, "M")
        h.take(nil)
        XCTAssertNil(h.machine)
        XCTAssertTrue(h.remote.isEmpty)
    }

    @MainActor private func answer(_ machine: String) throws -> Spaces.Answer {
        try Spaces.Answer(json: Data(#"{"machine_id":"\#(machine)","sessions":[]}"#.utf8))
    }

    /// A read under way as the connection drops does not take the new
    /// connection's read with it: its answer is dropped, and the new
    /// connection is read again.
    @MainActor func testReadFollowsNewConnection() async throws {
        let r = try FakeRex()
        defer { r.stop() }
        r.features = [Spaces.feature]
        let h = Host(HostConfig(name: "mac", url: try await r.url()))
        defer { h.stop() }
        var calls = 0
        var first: CheckedContinuation<Void, Never>?
        h.spacesReader = { _, _ in
            calls += 1
            if calls == 1 {
                await withCheckedContinuation { first = $0 }
                return try self.answer("OLD")
            }
            return try self.answer("NEW")
        }
        h.connect()
        await until("the first read") { first != nil }
        h.reconnect()
        XCTAssertNil(h.machine)
        await until("online again") { h.status == .online }
        first?.resume()
        await until("the new connection's spaces") { h.machine == "NEW" }
        XCTAssertEqual(calls, 2)
    }

    /// A connection that closes takes what it said of the spaces with it.
    @MainActor func testCloseForgetsSpaces() async throws {
        let r = try FakeRex()
        r.features = [Spaces.feature]
        let h = Host(HostConfig(name: "mac", url: try await r.url()))
        defer { h.stop() }
        h.spacesReader = { _, _ in try self.answer("M") }
        h.connect()
        await until("the spaces") { h.machine == "M" }
        r.stop()
        await until("the close") { h.control == nil }
        XCTAssertNil(h.machine)
        XCTAssertNil(h.crossHost)
    }
}
