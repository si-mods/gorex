import Foundation
import XCTest

/// The keyboard input log: off, it records nothing; its lines show the
/// characters a keyboard's text hides.
nonisolated final class InputTraceTests: XCTestCase {
    @MainActor func testQuote() {
        XCTAssertEqual(InputTrace.quote(nil), "nil")
        XCTAssertEqual(InputTrace.quote("\u{200B}我准备\r\n\t\u{7F}\u{1B}\"\\"), #""\u{200B}我准备\r\n\t\u{7F}\u{1B}\"\\""#)
        XCTAssertEqual(InputTrace.edits([.erase(2), .text("a"), .preedit(nil), .backspace]), #"[erase(2), text("a"), preedit(nil), backspace]"#)
    }

    @MainActor func testTraced() {
        var d = InputDocument()
        _ = d.insert("ab")
        _ = d.mark("ni", selected: NSRange(location: 2, length: 0))
        XCTAssertEqual(d.traced, #"typed="ab" marked="ni" sel={5,0}"#)
    }

    @MainActor func testOff() {
        let was = UserDefaults.standard.object(forKey: InputTrace.key)
        defer { UserDefaults.standard.set(was, forKey: InputTrace.key) }
        InputTrace.clear()
        UserDefaults.standard.set(false, forKey: InputTrace.key)
        InputTrace.note("a")
        XCTAssertEqual(InputTrace.lines, [])
        UserDefaults.standard.set(true, forKey: InputTrace.key)
        InputTrace.note("b")
        XCTAssertEqual(InputTrace.lines.count, 1)
        XCTAssertTrue(InputTrace.lines[0].hasSuffix(" b"))
        InputTrace.clear()
        XCTAssertEqual(InputTrace.lines, [])
    }
}
