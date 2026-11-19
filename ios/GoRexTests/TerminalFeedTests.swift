import Foundation
import Network
import XCTest

/// The terminal stream a real Rex server sent (ProtocolTests): a GHOSTSNP
/// snapshot of an 80x24 shell, then the first resize message.
private let stream = try! Data(contentsOf: Bundle(for: TerminalFeedTests.self).url(forResource: "rex-terminal-stream", withExtension: "bin")!)
private let snapshotLength = 1227
private let streamNonce: UInt64 = 17389683449275939001

/// The events a feed delivered, in order, on the main actor.
@MainActor private final class Delivered {
    var events: [TerminalFeed.Event] = []
    var dropped: Bool { events.contains { if case .drop = $0 { true } else { false } } }

    /// The output of the events, joined.
    var output: Data {
        events.reduce(into: Data()) { if case .output(let o) = $1 { $0 += o } }
    }
}

/// What a session's view was given to draw, in order.
@MainActor private final class Screen: TerminalOutput {
    var writes: [Data] = []
    /// Empty writes left out, as of the bytes pending when the view came.
    func write(_ bytes: Data) { if !bytes.isEmpty { writes.append(bytes) } }
    func setTheme(_ theme: [String: Any]) {}
}

/// The terminal stream framed and its snapshot restored off the main actor
/// (#844): the bytes reach the terminal in the order they came, after the
/// screen of the snapshot before them.
nonisolated final class TerminalFeedTests: XCTestCase {
    /// The scan of a snapshot that arrives in parts goes on where it
    /// stopped: the records it walked are not read again.
    @MainActor func testSnapshotScanGoesOnWhereItStopped() {
        var at = 10
        XCTAssertNil(TerminalStream.snapshotLength(stream.prefix(snapshotLength - 1), from: &at))
        // Past every whole record before FINISH, whose header is cut.
        XCTAssertEqual(at, snapshotLength - 10)
        // Records walked are not read again: spoiled, the scan still ends
        // where the snapshot does.
        var spoiled = stream.prefix(snapshotLength)
        spoiled.replaceSubrange(10..<at, with: Data(repeating: 0xff, count: at - 10))
        XCTAssertNil(TerminalStream.snapshotLength(spoiled))
        XCTAssertEqual(TerminalStream.snapshotLength(spoiled, from: &at), snapshotLength)
    }

    /// Fed a byte at a time, a stream still gives its snapshot whole.
    @MainActor func testSnapshotFedByteByByte() {
        var s = TerminalStream(nonce: streamNonce)
        var snapshots: [Data] = []
        for b in stream.prefix(snapshotLength) {
            for case .snapshot(let d) in s.feed(Data([b])) { snapshots.append(d) }
        }
        XCTAssertEqual(snapshots, [stream.prefix(snapshotLength)])
    }

    /// Output fed in many small parts comes out whole and in order, after
    /// the restored snapshot, and a drop comes after all of it.
    @MainActor func testFeedKeepsOrder() async {
        let got = Delivered()
        let done = expectation(description: "drop")
        let feed = TerminalFeed { events in
            got.events += events
            if got.dropped { done.fulfill() }
        }
        feed.start(nonce: streamNonce)
        let output = Data((0..<2000).map { "line \($0)\r\n" }.joined().utf8)
        let message = Data("\u{1b}_rex1;\(streamNonce);{\"type\":\"theme\",\"background\":\"101010\"}\u{1b}\\".utf8)
        let all = stream.prefix(snapshotLength) + output.prefix(5000) + message + output.dropFirst(5000)
        var at = all.startIndex
        var n = 1
        while at < all.endIndex {
            // Parts of changing sizes, cutting the snapshot and the message.
            let end = min(at + n, all.endIndex)
            feed.feed(all[at..<end], size: .init(cols: 40, rows: 10, cellWidth: 8, cellHeight: 16))
            at = end
            n = n % 97 + 1
        }
        feed.drop(1000)
        await fulfillment(of: [done], timeout: 30)

        guard case .snapshot(let restored)? = got.events.first, let restored else {
            return XCTFail("the first event is not a restored snapshot: \(got.events.first.map { "\($0)" } ?? "none")")
        }
        XCTAssertTrue(restored.screen.starts(with: Data("\u{1b}c".utf8)))
        XCTAssertEqual(restored.screen.dropFirst(2), restored.replay)
        XCTAssertTrue(String(decoding: restored.replay, as: UTF8.self).contains("hello"))
        XCTAssertEqual(got.events.filter { if case .snapshot = $0 { true } else { false } }.count, 1)
        guard case .drop(1000)? = got.events.last else { return XCTFail("the last event is not the drop") }
        XCTAssertEqual(got.output, output)
        // The message between the output it came between.
        var before = Data()
        for e in got.events {
            if case .output(let o) = e { before += o }
            if case .message(let m) = e {
                XCTAssertEqual(m["background"] as? String, "101010")
                XCTAssertEqual(before, output.prefix(5000))
            }
        }
    }

    /// A snapshot that cannot be read is told as such, in its place.
    @MainActor func testBadSnapshotInItsPlace() async {
        let got = Delivered()
        let done = expectation(description: "drop")
        let feed = TerminalFeed { events in
            got.events += events
            if got.dropped { done.fulfill() }
        }
        var bad = stream.prefix(snapshotLength)
        bad[bad.startIndex] = 0 // no GHOSTSNP envelope
        feed.feed(bad + Data("after".utf8), size: nil)
        feed.drop(1006)
        await fulfillment(of: [done], timeout: 30)
        guard case .snapshot(nil)? = got.events.first else { return XCTFail("no unreadable snapshot first") }
        XCTAssertEqual(got.output, Data("after".utf8))
    }

    /// A session draws the snapshot's screen before the output that follows
    /// it, and that output in the order the server sent it, over a real
    /// data connection to a fake server.
    @MainActor func testSessionWritesSnapshotThenOutputInOrder() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        rex.respond = { method, _ in method == "session.bootstrap" ? ["attach_all": ["sessions": []]] : [:] }
        let h = Host(HostConfig(name: "test", url: try await rex.url()))
        h.connect()
        await until("online") { h.status == .online }
        rex.respond = { method, _ in method == "data.connect" ? ["ticket": "uH4mJKwzMarlTNT-UiyMog"] : [:] }
        let output = Data((0..<500).map { "row \($0)\r\n" }.joined().utf8)
        rex.data = { c, _ in
            FakeRex.send(c, FakeRex.welcome)
            // The snapshot cut in two, its end in the frame of the first output.
            FakeRex.send(c, stream.prefix(600))
            FakeRex.send(c, stream[600..<snapshotLength] + output.prefix(100))
            var at = 100
            while at < output.count {
                FakeRex.send(c, output[at..<min(at + 333, output.count)])
                at += 333
            }
        }
        let screen = Screen()
        let s = TerminalSession(host: h, session: "s", block: "b")
        s.output = screen
        s.start()
        await until("output", timeout: 10) { screen.writes.reduce(0) { $0 + $1.count } >= output.count }
        let first = try XCTUnwrap(screen.writes.first)
        XCTAssertTrue(first.starts(with: Data("\u{1b}c".utf8)), "the snapshot's screen is not written first")
        XCTAssertTrue(String(decoding: first, as: UTF8.self).contains("hello"))
        XCTAssertEqual(screen.writes.dropFirst().reduce(Data(), +), output)
        XCTAssertEqual(s.state, .live)
        s.close()
        h.stop()
    }
}
