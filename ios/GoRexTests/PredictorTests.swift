import XCTest

/// The guesses' life (docs/IOS-018-predictive-echo.md, the model): a plain
/// key makes one, an answer that shows, overwrites or outlives it ends it,
/// and an unguessable key, a guarded screen or a secret's line makes none.
final class PredictorTests: XCTestCase {
    private let now = ContinuousClock.now

    private func context(_ f: (inout Predictor.Context) -> Void = { _ in }) -> Predictor.Context {
        var c = Predictor.Context(col: 3, row: 2, cols: 80, rows: 24)
        f(&c)
        return c
    }

    /// A printable key guesses its glyph and moves the shadow right.
    func testPrintable() {
        let p = Predictor()
        p.type(Data("ab".utf8), context: context(), now: now)
        XCTAssertEqual(p.open.count, 2)
        XCTAssertEqual(p.open.first?.cell, .glyph("a"))
        XCTAssertEqual(p.open.last?.col, 4)
        XCTAssertEqual(p.shadow, (col: 5, row: 2))
    }

    /// Output that shows the guess ends it; what it did not reach waits.
    func testResolveConfirms() {
        let p = Predictor()
        p.type(Data("ab".utf8), context: context(), now: now)
        p.resolve(real: (col: 4, row: 2), lines: [2: "xyza"], now: now)
        XCTAssertEqual(p.open.map(\.col), [4])
        p.resolve(real: (col: 5, row: 2), lines: [2: "xyzab"], now: now)
        XCTAssertTrue(p.open.isEmpty)
    }

    /// An answer that put another glyph over the guess ends it: the wrong
    /// guess must not keep standing.
    func testOverwritten() {
        let p = Predictor()
        p.type(Data("a".utf8), context: context(), now: now)
        p.resolve(real: (col: 4, row: 2), lines: [2: "xyz*"], now: now)
        XCTAssertTrue(p.open.isEmpty)
    }

    /// A cursor that retreated or ran past the shadow drops what it did
    /// not show; at the primed place, nothing arrived and the guess stays.
    func testCursorClears() {
        let p = Predictor()
        p.type(Data("a".utf8), context: context(), now: now)
        p.resolve(real: (col: 0, row: 1), lines: [:], now: now)
        XCTAssertTrue(p.open.isEmpty)

        p.type(Data("a".utf8), context: context(), now: now)
        p.resolve(real: (col: 3, row: 2), lines: [:], now: now)
        XCTAssertEqual(p.open.count, 1)
    }

    /// A guess not answered by its deadline goes.
    func testDeadline() {
        let p = Predictor()
        p.type(Data("a".utf8), context: context(), now: now)
        p.resolve(real: (col: 3, row: 2), lines: [:], now: now + .seconds(1))
        XCTAssertTrue(p.open.isEmpty)
    }

    /// Two round trips, at least half a second, at most five.
    func testUntil() {
        XCTAssertEqual(Predictor.until(rtt: .milliseconds(40)), .milliseconds(500))
        XCTAssertEqual(Predictor.until(rtt: .milliseconds(400)), .milliseconds(800))
        XCTAssertEqual(Predictor.until(rtt: .seconds(9)), .seconds(5))
    }

    /// An escape sequence, a control key or a long paste closes the open
    /// guesses instead of adding to them.
    func testUnpredictableCloses() {
        let p = Predictor()
        p.type(Data("a".utf8), context: context(), now: now)
        p.type(Data([0x1B, 0x5B, 0x44]), context: context(), now: now)
        XCTAssertTrue(p.open.isEmpty)

        p.type(Data(String(repeating: "x", count: 64).utf8), context: context(), now: now)
        XCTAssertTrue(p.open.isEmpty)
    }

    /// A guarded screen and a secret's line make no guess.
    func testGuarding() {
        let p = Predictor()
        p.type(Data("a".utf8), context: context { $0.guarded = true }, now: now)
        XCTAssertTrue(p.open.isEmpty)
        p.type(Data("a".utf8), context: context { $0.secret = true }, now: now)
        XCTAssertTrue(p.open.isEmpty)
    }

    /// Delete guesses the removal of the glyph before the cursor.
    func testDelete() {
        let p = Predictor()
        p.type(Data([0x7F]), context: context(), now: now)
        XCTAssertEqual(p.open.first?.cell, .erase)
        XCTAssertEqual(p.open.first?.col, 2)
        XCTAssertEqual(p.shadow.col, 2)
    }

    /// The setting off makes no guess.
    func testOff() {
        let d = UserDefaults.standard
        let had = d.object(forKey: Predictor.key)
        defer { if let had { d.set(had, forKey: Predictor.key) } else { d.removeObject(forKey: Predictor.key) } }
        d.set(false, forKey: Predictor.key)
        let p = Predictor()
        p.type(Data("a".utf8), context: context(), now: now)
        XCTAssertTrue(p.open.isEmpty)
    }

    /// A character's cell: plain ones one cell, wide ones two.
    func testChar() {
        XCTAssertEqual(Predictor.char("xyza", at: 3), "a")
        XCTAssertEqual(Predictor.char("ab文d", at: 2), "文")
        XCTAssertNil(Predictor.char("ab文d", at: 3))
        XCTAssertNil(Predictor.char("ab", at: 5))
    }
}
