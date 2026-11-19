import XCTest

/// The control calls a host makes on its own, as after a reconnect: each
/// block read once, a few reads at a time, and what waits for the host
/// let go when it is cancelled.
nonisolated final class HostRPCTests: XCTestCase {
    /// A window's blocks, each once: the focused one first, then the tiled
    /// ones in layout order.
    @MainActor func testWindowBlockIDsOnce() throws {
        func window(focused: String?) throws -> SessionView.Window {
            var w: [String: Any] = ["window_id": "w", "layers": [["blocks": [["block_id": "b"], ["block_id": "c"]]]]]
            if let focused { w["focused_block_id"] = focused }
            let json: [String: Any] = ["payload": ["revision": 1, "session_id": "s", "windows": [w]]]
            let v = try SessionView(response: try JSONSerialization.data(withJSONObject: json))
            return try XCTUnwrap(v.windows?.first)
        }
        XCTAssertEqual(try window(focused: "b").blockIDs, ["b", "c"])
        XCTAssertEqual(try window(focused: "c").blockIDs, ["c", "b"])
        XCTAssertEqual(try window(focused: nil).blockIDs, ["b", "c"])
    }

    /// A connect and a reconnect read each block once, the focused one too,
    /// which its window lists twice.
    @MainActor func testReconnectDescribesEachBlockOnce() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        let view: [String: Any] = ["revision": 1, "session_id": "s", "label": "s", "windows": [
            ["window_id": "w", "focused_block_id": "b", "layers": [["blocks": [["block_id": "b"], ["block_id": "c"]]]]],
        ]]
        rex.respond = { m, _ in
            switch m {
            case "session.bootstrap":
                ["attach_all": ["sessions": [["session_id": "s", "label": "s"]]], "selected_session": ["view": view]]
            case "com.superlogical.terminal.title":
                ["title": "t"]
            default: [:]
            }
        }
        let h = Host(HostConfig(name: "test", url: try await rex.url()))
        h.connect()
        defer { h.stop() }
        func idle() -> Bool { h.describes.runningCount == 0 && h.describes.waitingCount == 0 }
        await until("read") { rex.count("com.superlogical.terminal.program_status") == 2 && idle() }
        XCTAssertEqual(rex.count("com.superlogical.terminal.title"), 2)

        FakeRex.close(rex.controls[0], .protocolCode(.goingAway))
        await until("offline") { h.control == nil }
        h.reconnectIfNeeded()
        await until("read again") { rex.count("com.superlogical.terminal.program_status") == 4 && idle() }
        XCTAssertEqual(rex.count("com.superlogical.terminal.title"), 4)
    }

    /// Many blocks are read a few at a time, and a disconnect drops the
    /// reads: the reconnect reads every block again.
    @MainActor func testDescribesBoundedAndCancelled() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        let blocks = (0..<10).map { "b\($0)" }
        let view: [String: Any] = ["revision": 1, "session_id": "s", "label": "s", "windows": [
            ["window_id": "w", "layers": [["blocks": blocks.map { ["block_id": $0] }]]],
        ]]
        var answer = false
        rex.respond = { m, _ in
            switch m {
            case "session.bootstrap":
                ["attach_all": ["sessions": [["session_id": "s", "label": "s"]]], "selected_session": ["view": view]]
            case "com.superlogical.terminal.title":
                answer ? ["title": "t"] : nil
            default: [:]
            }
        }
        let h = Host(HostConfig(name: "test", url: try await rex.url()))
        h.connect()
        defer { h.stop() }
        let limit = h.describes.limit
        await until("first reads") { rex.count("com.superlogical.terminal.title") == limit }
        XCTAssertEqual(h.describes.runningCount, limit)
        XCTAssertEqual(h.describes.waitingCount, blocks.count - limit)

        answer = true
        FakeRex.close(rex.controls[0], .protocolCode(.goingAway))
        await until("offline") { h.control == nil }
        XCTAssertEqual(h.describes.runningCount, 0)
        XCTAssertEqual(h.describes.waitingCount, 0)

        h.reconnectIfNeeded()
        await until("all read") { blocks.allSatisfy { h.blocks[$0]?.title == "t" } }
        XCTAssertLessThanOrEqual(rex.count("com.superlogical.terminal.title"), limit + blocks.count)
    }

    /// A cancelled wait for the host to be online ends at once with
    /// CancellationError, and the host lets it go.
    @MainActor func testCancelledOnlineWaiterLeaves() async {
        let h = Host(HostConfig(name: "test", url: URL(string: "http://127.0.0.1:1")!))
        h.connect()
        defer { h.stop() }
        let waiting = Task { try await h.online() }
        await until("waiting") { h.waitingCount == 1 }
        waiting.cancel()
        do {
            _ = try await waiting.value
            XCTFail("online")
        } catch {
            XCTAssertTrue(error is CancellationError)
        }
        XCTAssertEqual(h.waitingCount, 0)
    }
}
