import XCTest

/// Dictation's phrases, joined as spoken.
nonisolated final class DictationTests: XCTestCase {
    /// English phrases take a space; Chinese and Japanese, which SenseVoice
    /// may space, take none between them, and keep one beside Latin words.
    func testPhrasesJoinAsWritten() {
        XCTAssertEqual(Dictation.joined(["Hello there.", "How are you?"]), "Hello there. How are you?")
        XCTAssertEqual(Dictation.joined(["今天天气很好。", "我们出去吧。"]), "今天天气很好。我们出去吧。")
        XCTAssertEqual(Dictation.joined(["こ ん に ち は"]), "こんにちは")
        XCTAssertEqual(Dictation.joined(["我在用 Claude Code 写代码。"]), "我在用 Claude Code 写代码。")
        XCTAssertEqual(Dictation.joined([]), "")
        XCTAssertEqual(Dictation.joined(["", " "]), "")
    }

    /// What is said so far: the phrases for good, then the guess at the one
    /// open, written as one; what is for good begins it.
    func testSaidShowsTheOpenPhraseAfterThoseForGood() {
        var said = Dictation.Said()
        XCTAssertEqual(said.text, "")
        said.open = "Hello"
        XCTAssertEqual(said.text, "Hello")
        XCTAssertEqual(said.settled, "")
        said.open = ""
        said.phrases = ["Hello there."]
        XCTAssertEqual(said.text, "Hello there.")
        said.open = "How are"
        XCTAssertEqual(said.text, "Hello there. How are")
        XCTAssertEqual(said.settled, "Hello there.")
        said = Dictation.Said(phrases: ["今天天气很好。"], open: "我们出")
        XCTAssertEqual(said.text, "今天天气很好。我们出")
        XCTAssertEqual(said.settled, "今天天气很好。")
        said = Dictation.Said(phrases: ["我在用"], open: "Claude Code")
        XCTAssertEqual(said.text, "我在用 Claude Code")
        for s in [Dictation.Said(phrases: ["A.", "今天"], open: "好"), Dictation.Said(phrases: ["こ ん"], open: "に ち は")] {
            XCTAssertTrue(s.text.hasPrefix(s.settled), "\(s)")
        }
    }

    /// The level shown: -50 dB and below is none, -10 dB and above full;
    /// it rises at once and falls by half.
    func testLevelRisesAtOnceAndFallsByHalf() {
        XCTAssertEqual(Dictation.level(of: 0, after: 0), 0)
        XCTAssertEqual(Dictation.level(of: 0.001, after: 0), 0, accuracy: 0.001)
        XCTAssertEqual(Dictation.level(of: 0.01, after: 0), 0.25, accuracy: 0.001)
        XCTAssertEqual(Dictation.level(of: 0.1, after: 0), 0.75, accuracy: 0.001)
        XCTAssertEqual(Dictation.level(of: 1, after: 0), 1)
        XCTAssertEqual(Dictation.level(of: 0, after: 0.8), 0.4, accuracy: 0.001)
        XCTAssertEqual(Dictation.level(of: 0.1, after: 0.5), 0.75, accuracy: 0.001)
    }
}
