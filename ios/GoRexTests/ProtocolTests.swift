import Foundation
import XCTest

/// The terminal stream a real Rex server sent: a GHOSTSNP snapshot of an
/// 80x24 shell, then the first resize message.
private let fixture = try! Data(contentsOf: Bundle(for: HandshakeTests.self).url(forResource: "rex-terminal-stream", withExtension: "bin")!)
private let snapshotBytes = 1227
private let nonce: UInt64 = 17389683449275939001

nonisolated final class HandshakeTests: XCTestCase {
    @MainActor func testHello() throws {
        let h = try XCTUnwrap(DataHandshake.hello(ticket: "uH4mJKwzMarlTNT-UiyMog"))
        XCTAssertEqual(h.count, 17)
        XCTAssertEqual(h.first, 0x01)
        XCTAssertEqual(h.dropFirst().base64EncodedString(), "uH4mJKwzMarlTNT+UiyMog==")
        XCTAssertNil(DataHandshake.hello(ticket: "short"))
    }

    @MainActor func testResume() {
        let token = Data((1...16).map { UInt8($0) })
        let r = DataHandshake.resume(token: token, received: 0x0102_0304_0506_0708)
        XCTAssertEqual(r.count, 25)
        XCTAssertEqual([UInt8](r.prefix(17)), [0x02] + (1...16).map { UInt8($0) })
        XCTAssertEqual([UInt8](r.suffix(8)), [8, 7, 6, 5, 4, 3, 2, 1])
    }

    @MainActor func testWelcomeAndReject() {
        var b: [UInt8] = [0x81] + (1...16).map { UInt8($0) } + [0, 0, 0]
        b += [0x60, 0xEA, 0, 0] // 60000 ms
        b += withUnsafeBytes(of: nonce.littleEndian, Array.init)
        XCTAssertEqual(DataHandshake(Data(b)), .welcome(token: Data((1...16).map { UInt8($0) }), keepMillis: 60000, nonce: nonce))
        XCTAssertEqual(DataHandshake(Data([0x82, 0x02])), .reject(reason: 2))
        XCTAssertNil(DataHandshake(Data([0x81, 0x00])))
    }
}

nonisolated final class TerminalStreamTests: XCTestCase {
    /// The snapshot and messages of items, with the output joined.
    @MainActor private func split(_ items: [TerminalStream.Item]) -> (snapshot: Data?, output: Data, messages: [[String: Any]]) {
        var r: (snapshot: Data?, output: Data, messages: [[String: Any]]) = (nil, Data(), [])
        for i in items {
            switch i {
            case .snapshot(let s): r.snapshot = s
            case .output(let o): r.output += o
            case .message(let m): r.messages.append(m)
            }
        }
        return r
    }

    @MainActor func testFixtureAtOnce() throws {
        var s = TerminalStream(nonce: nonce)
        let r = split(s.feed(fixture))
        XCTAssertEqual(r.snapshot?.count, snapshotBytes)
        XCTAssertTrue(r.output.isEmpty)
        let m = try XCTUnwrap(r.messages.first)
        XCTAssertEqual(m["type"] as? String, "resize")
        XCTAssertEqual(m["cols"] as? Int, 80)
        XCTAssertEqual(m["rows"] as? Int, 24)
    }

    @MainActor func testFixtureByteByByte() {
        var s = TerminalStream(nonce: nonce)
        var items: [TerminalStream.Item] = []
        for b in fixture { items += s.feed(Data([b])) }
        let r = split(items)
        XCTAssertEqual(r.snapshot, fixture.prefix(snapshotBytes))
        XCTAssertEqual(r.messages.count, 1)
        XCTAssertTrue(r.output.isEmpty)
    }

    @MainActor func testOutputAroundMessages() {
        let stream = fixture.prefix(snapshotBytes)
            + Data("ab\u{1b}[1mc".utf8)
            + Data("\u{1b}_rex1;1;{\"type\":\"theme\"}\u{1b}\\".utf8) // a program's: another nonce
            + Data("d\u{1b}_rex1;\(nonce);{\"type\":\"child_exited\",\"exit_code\":3}\u{1b}\\e".utf8)
        for chunk in [1, 7, 1000] {
            var t = TerminalStream(nonce: nonce)
            var items: [TerminalStream.Item] = []
            var at = stream.startIndex
            while at < stream.endIndex {
                let end = min(at + chunk, stream.endIndex)
                items += t.feed(stream[at..<end])
                at = end
            }
            let r = split(items)
            XCTAssertEqual(String(decoding: r.output, as: UTF8.self), "ab\u{1b}[1mcde", "chunks of \(chunk)")
            XCTAssertEqual(r.messages.count, 1)
            XCTAssertEqual(r.messages.first?["exit_code"] as? Int, 3)
        }
    }

    @MainActor func testSnapshotLength() {
        XCTAssertEqual(TerminalStream.snapshotLength(fixture), snapshotBytes)
        XCTAssertNil(TerminalStream.snapshotLength(fixture.prefix(snapshotBytes - 1)))
        XCTAssertNil(TerminalStream.snapshotLength(fixture.prefix(5)))
    }

    /// libghostty-vt restores the fixture's screen, and turns it into the
    /// escape sequences that draw it again.
    @MainActor func testSnapshotReplays() throws {
        let t = try XCTUnwrap(VTerminal(snapshot: fixture.prefix(snapshotBytes)))
        XCTAssertEqual(t.size.cols, 80)
        XCTAssertEqual(t.size.rows, 24)
        let lines = t.text.components(separatedBy: "\n")
        XCTAssertEqual(lines.first?.trimmingCharacters(in: .whitespaces), "hello world")
        XCTAssertTrue(lines.count > 1 && lines[1].hasPrefix("sh-3.2$"), t.text)
        t.write(Data("\r\nhello \u{1b}[31mred".utf8))
        XCTAssertTrue(t.text.contains("hello red"), t.text)
        // Styled: the fixture's bold red "hello", and the red written.
        let replay = String(decoding: t.replay(), as: UTF8.self)
        XCTAssertTrue(replay.contains("\u{1b}[1m\u{1b}[38;5;1mhello"), replay)
        XCTAssertTrue(replay.contains("\u{1b}[38;5;1mred"), replay)
        // The fixture's terminal has no default colors of its own.
        XCTAssertNil(t.theme["foreground"])
        XCTAssertEqual((t.theme["palette"] as? [String])?.count, 256)
        t.resize(cols: 40, rows: 10, cellWidth: 8, cellHeight: 16)
        XCTAssertEqual(t.size.cols, 40)
    }

    /// A full-screen program's screen comes back on the alternate screen,
    /// with the modes it set: here application cursor keys.
    @MainActor func testReplayKeepsModes() throws {
        let t = try XCTUnwrap(VTerminal(snapshot: fixture.prefix(snapshotBytes)))
        t.write(Data("\u{1b}[?1049h\u{1b}[?1h\u{1b}[Hfull screen".utf8))
        let replay = String(decoding: t.replay(), as: UTF8.self)
        XCTAssertTrue(replay.contains("\u{1b}[?1049h") || replay.contains("\u{1b}[?1047h"), replay)
        XCTAssertTrue(replay.contains("\u{1b}[?1h"), replay)
        XCTAssertTrue(replay.contains("full screen"), replay)
        XCTAssertFalse(replay.contains("hello world"), replay)
    }
}

nonisolated final class EventTests: XCTestCase {
    @MainActor private func event(_ seq: Int, _ type: String = "session_view_changed") -> RexEvent {
        RexEvent(message: [
            "type": "event", "seq": seq,
            "payload": ["type": "session_event", "payload": [
                "session_id": "session:1", "event": ["type": type, "payload": ["revision": seq]],
            ]],
        ])!
    }

    @MainActor func testUnwrapsSessionEvents() {
        let e = event(4, "window_created")
        XCTAssertEqual(e.type, "window_created")
        XCTAssertEqual(e.sessionID, "session:1")
        XCTAssertEqual(e.payload["revision"] as? Int, 4)
        XCTAssertNil(RexEvent(message: ["type": "response", "id": "1"]))
    }

    @MainActor func testAppliesBySeq() {
        var g = EventGate()
        XCTAssertTrue(g.receive(event(1)).isEmpty)
        XCTAssertTrue(g.receive(event(2)).isEmpty)
        XCTAssertTrue(g.receive(event(3)).isEmpty)
        // The response's seq is 2: its result already includes events 1 and 2.
        XCTAssertEqual(g.open(at: 2).map(\.seq), [3])
        XCTAssertEqual(g.receive(event(4)).map(\.seq), [4])
        XCTAssertTrue(g.receive(event(2)).isEmpty)
    }

    @MainActor func testSessionView() throws {
        let v = try SessionView(json: [
            "revision": 3, "session_id": "session:1", "label": "work",
            "windows": [["window_id": "window:1", "label": "Window 1", "layers": [
                ["layer_id": "layer:1", "kind": "tiled", "blocks": [["block_id": "block:7"]]],
            ]]],
        ])
        XCTAssertEqual(v.windows?.first?.block, "block:7")
    }

    @MainActor func testWindowName() throws {
        let v = try SessionView(json: [
            "revision": 1, "session_id": "session:1",
            "windows": [
                ["window_id": "window:0123abcd4567ef89", "label": "Window 4567ef89"],
                ["window_id": "window:89", "label": "build"],
            ],
        ])
        XCTAssertEqual(v.windows?.map(\.name), [nil, "build"])
    }
}

nonisolated final class KeyTests: XCTestCase {
    @MainActor func testTyping() {
        let k = KeyEvent.typing("aZ1!é")
        XCTAssertEqual(k.map(\.code), [0x00, 0x06, 0x12, 0x12, KeyEvent.unidentified])
        XCTAssertEqual(k[1].mods, .shift)
        XCTAssertEqual(k[1].consumed, .shift)
        XCTAssertEqual(k[1].text, "Z")
        XCTAssertEqual(k[1].unshifted, "z")
        XCTAssertEqual(k[3].unshifted, "1")
        XCTAssertEqual(k[4].text, "é")
        XCTAssertEqual(KeyEvent.typing("你好").map(\.text), ["你", "好"])
    }

    @MainActor func testWholeUTF8() {
        let d = Data("a中".utf8)
        XCTAssertEqual(d.wholeUTF8, 4)
        XCTAssertEqual(d.prefix(3).wholeUTF8, 1)
        XCTAssertEqual(d.prefix(2).wholeUTF8, 1)
        XCTAssertEqual(Data("ab\u{1b}".utf8).wholeUTF8, 3)
    }

    @MainActor func testCtrlAndAlt() {
        XCTAssertEqual(KeyEvent.typing("c", ctrl: true), [KeyEvent(code: 0x08, text: "c", mods: .ctrl, unshifted: "c")])
        XCTAssertEqual(KeyEvent.typing("x", alt: true).first?.mods, .alt)
        XCTAssertEqual(KeyEvent.typing("\n").first, KeyEvent.named("enter"))
        XCTAssertEqual(KeyEvent.named("enter").code, 0x24)
    }

    @MainActor func testShift() {
        XCTAssertEqual(KeyEvent.typing("a", shift: true),
                       [KeyEvent(code: 0x00, text: "A", mods: .shift, consumed: .shift, unshifted: "a")])
        XCTAssertEqual(KeyEvent.typing("c", ctrl: true, shift: true).first?.mods, [.shift, .ctrl])
        XCTAssertEqual(KeyEvent.typing("\n", shift: true), [KeyEvent(code: 0x24, mods: .shift)])
        XCTAssertEqual(KeyEvent.typing("1", shift: true).first?.text, "1")
        XCTAssertEqual(KeyEvent.named("tab", shift: true), KeyEvent(code: 0x30, mods: .shift))
        XCTAssertEqual(KeyEvent.named("arrow_up", shift: true, ctrl: true, alt: true).mods, [.shift, .ctrl, .alt])
    }
}
