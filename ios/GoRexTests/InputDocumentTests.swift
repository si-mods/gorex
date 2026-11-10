import Foundation
import XCTest

/// The keyboard's text through the sequences UIKit sends: pinyin, Japanese
/// and Korean composition, dictation and its corrections, and the
/// spacebar's trackpad.
nonisolated final class InputDocumentTests: XCTestCase {
    private func at(_ n: Int) -> NSRange { NSRange(location: n, length: 0) }
    /// The anchor, before what was typed.
    private let z = "\u{200B}"

    /// Pinyin: the letters compose and show at the cursor; the candidate
    /// goes once, in their place, with no modifiers.
    @MainActor func testPinyin() {
        var d = InputDocument()
        XCTAssertEqual(d.insert("a"), [.type("a")])
        XCTAssertEqual(d.mark("n", selected: at(1)), [.preedit("n")])
        XCTAssertEqual(d.mark("ni h", selected: at(4)), [.preedit("ni h")])
        XCTAssertEqual(d.markedRange, NSRange(location: 2, length: 4))
        XCTAssertEqual(d.selection, at(6))
        XCTAssertEqual(d.mark("ni hao", selected: at(6)), [.preedit("ni hao")])
        XCTAssertEqual(d.insert("你好"), [.preedit(nil), .text("你好")])
        XCTAssertNil(d.markedRange)
        XCTAssertEqual(d.text, z + "a你好")
        XCTAssertEqual(d.count, 4)
        // Nothing more goes out when the keyboard unmarks after.
        XCTAssertEqual(d.unmark(), [])
    }

    /// Some input methods set the candidate as the composition and unmark.
    @MainActor func testCommitByUnmark() {
        var d = InputDocument()
        _ = d.mark("かな", selected: at(2))
        _ = d.mark("仮名", selected: at(2))
        XCTAssertEqual(d.unmark(), [.preedit(nil), .text("仮名")])
        XCTAssertEqual(d.text, z + "仮名")
    }

    /// Korean: each syllable composes, and the next one's first letter
    /// commits it.
    @MainActor func testKorean() {
        var d = InputDocument()
        _ = d.mark("ㅎ", selected: at(1))
        _ = d.mark("하", selected: at(1))
        XCTAssertEqual(d.mark("한", selected: at(1)), [.preedit("한")])
        XCTAssertEqual(d.unmark(), [.preedit(nil), .text("한")])
        XCTAssertEqual(d.mark("ㄱ", selected: at(1)), [.preedit("ㄱ")])
        XCTAssertEqual(d.markedRange, NSRange(location: 2, length: 1))
    }

    /// Backspace edits the composition and goes to the program only past
    /// it, where the anchor stays for the next.
    @MainActor func testBackspace() {
        var d = InputDocument()
        _ = d.insert("x")
        _ = d.mark("ni", selected: at(2))
        XCTAssertEqual(d.markedRange, NSRange(location: 2, length: 2))
        XCTAssertFalse(d.erasesUnseen(chord: false))
        XCTAssertEqual(d.deleteBackward(), [.preedit("n")])
        XCTAssertEqual(d.deleteBackward(), [.preedit(nil)])
        XCTAssertNil(d.markedRange)
        XCTAssertFalse(d.erasesUnseen(chord: false))
        XCTAssertTrue(d.erasesUnseen(chord: true))
        XCTAssertEqual(d.deleteBackward(), [.backspace])
        XCTAssertEqual(d.text, z)
        XCTAssertTrue(d.erasesUnseen(chord: false))
        XCTAssertEqual(d.deleteBackward(), [.backspace])
        XCTAssertEqual(d.text, z)
    }

    /// Held on the empty line: every Backspace goes to the program, and
    /// the anchor stays, so the keyboard always has one to erase.
    @MainActor func testDeleteRepeat() {
        var d = InputDocument()
        for _ in 0..<5 {
            XCTAssertEqual(d.count, 1)
            XCTAssertEqual(d.selection, at(1))
            XCTAssertEqual(d.deleteBackward(), [.backspace])
        }
        XCTAssertEqual(d.text, z)
    }

    /// Return and Tab are keys; the line after them is new.
    @MainActor func testReturn() {
        var d = InputDocument()
        _ = d.insert("ls")
        XCTAssertEqual(d.insert("\n"), [.type("\n")])
        XCTAssertEqual(d.text, z)
        _ = d.insert("ab")
        d.reset()
        XCTAssertEqual(d.count, 1)
    }

    /// Dictation types a phrase, then corrects its last word: Backspaces
    /// back to the word, and the word again.
    @MainActor func testDictation() {
        var d = InputDocument()
        XCTAssertEqual(d.insert("echo hello word"), [.text("echo hello word")])
        XCTAssertEqual(d.replace(NSRange(location: 12, length: 4), with: "world"), [.erase(4), .text("world")])
        XCTAssertEqual(d.text, z + "echo hello world")
        // A word in the middle: the rest is typed again after it.
        XCTAssertEqual(d.replace(NSRange(location: 6, length: 5), with: "hi"), [.erase(11), .text("hi world")])
        XCTAssertEqual(d.text, z + "echo hi world")
        // At the end, a replace is an insert.
        XCTAssertEqual(d.replace(at(14), with: "!"), [.type("!")])
        // Out of the text, or on the anchor: nothing.
        XCTAssertEqual(d.replace(NSRange(location: 11, length: 9), with: "x"), [])
        XCTAssertEqual(d.replace(NSRange(location: 0, length: 2), with: "x"), [])
        XCTAssertEqual(d.replace(at(0), with: "x"), [])
        XCTAssertEqual(d.text, z + "echo hi world!")
    }

    /// On the empty line, text at the caret, after the anchor, is typed.
    @MainActor func testReplaceEmpty() {
        var d = InputDocument()
        XCTAssertEqual(d.replace(at(1), with: "x"), [.type("x")])
        XCTAssertEqual(d.text, z + "x")
    }

    /// Live dictation as composition: the hypotheses show, the result goes.
    @MainActor func testDictationHypotheses() {
        var d = InputDocument()
        _ = d.mark("你", selected: at(1))
        _ = d.mark("你好世", selected: at(3))
        XCTAssertEqual(d.replace(NSRange(location: 1, length: 3), with: "你好世界"), [.preedit(nil), .text("你好世界")])
        XCTAssertEqual(d.text, z + "你好世界")
    }

    /// A correction inside the composition commits the whole of it, the
    /// correction in place, not the corrected part alone.
    @MainActor func testReplaceInComposition() {
        var d = InputDocument()
        _ = d.insert("a")
        _ = d.mark("ni hao", selected: at(6))
        XCTAssertEqual(d.replace(NSRange(location: 5, length: 3), with: "好"), [.preedit(nil), .text("ni 好")])
        XCTAssertNil(d.markedRange)
        XCTAssertEqual(d.text, z + "ani 好")
        // Past the composition: nothing.
        _ = d.mark("x", selected: at(1))
        XCTAssertEqual(d.replace(NSRange(location: 7, length: 2), with: "y"), [])
    }

    /// A key with Ctrl or Alt held sends a control sequence, not its
    /// letter: the line is no longer what was typed, so a later correction
    /// erases only what follows.
    @MainActor func testChord() {
        var d = InputDocument()
        _ = d.insert("ab")
        XCTAssertEqual(d.insert("c", chord: true), [.type("c")])
        XCTAssertEqual(d.text, z)
        _ = d.insert("xy")
        XCTAssertEqual(d.deleteBackward(chord: true), [.backspace])
        XCTAssertEqual(d.text, z)
        // Composed text is text, chord or not.
        _ = d.mark("ni", selected: at(2))
        XCTAssertEqual(d.insert("你", chord: true), [.preedit(nil), .text("你")])
        XCTAssertEqual(d.text, z + "你")
    }

    /// Wide and composed characters count as UIKit counts them.
    @MainActor func testUTF16() {
        var d = InputDocument()
        _ = d.insert("e\u{301}😀")
        XCTAssertEqual(d.text, z + "é😀")
        XCTAssertEqual(d.count, 4)
        XCTAssertEqual(d.replace(NSRange(location: 2, length: 2), with: "x"), [.erase(1), .text("x")])
        XCTAssertEqual(d.replace(NSRange(location: 2, length: 1), with: "y"), [.erase(1), .text("y")])
        XCTAssertEqual(d.text, z + "éy")
    }

    /// Composed text is sent as text, each character a key of no US key.
    @MainActor func testCommittedKeys() {
        XCTAssertEqual(KeyEvent.typing("你好"), [
            KeyEvent(code: KeyEvent.unidentified, text: "你"), KeyEvent(code: KeyEvent.unidentified, text: "好"),
        ])
    }

    /// One arrow per cell the finger crosses, either way, both axes.
    @MainActor func testTrackpad() {
        var t = Trackpad()
        let cell = CGSize(width: 7, height: 15)
        XCTAssertEqual(t.update(to: .zero, cell: cell), [])
        t.begin(at: CGPoint(x: 100, y: 100))
        XCTAssertEqual(t.update(to: CGPoint(x: 106, y: 100), cell: cell), [])
        XCTAssertEqual(t.update(to: CGPoint(x: 79, y: 100), cell: cell), ["arrow_left", "arrow_left", "arrow_left"])
        XCTAssertEqual(t.update(to: CGPoint(x: 86, y: 131), cell: cell), ["arrow_right", "arrow_down", "arrow_down"])
        XCTAssertEqual(t.update(to: CGPoint(x: 86, y: 99), cell: cell), ["arrow_up", "arrow_up"])
        t.end()
        XCTAssertEqual(t.update(to: CGPoint(x: 0, y: 0), cell: cell), [])
    }
}
