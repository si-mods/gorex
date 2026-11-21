import Foundation

/// A key's glyph the host has not answered yet, drawn over the terminal as
/// an underlined guess that never writes into its state
/// (docs/IOS-018-predictive-echo.md).
struct Guess: Equatable {
    /// What the answer puts in the cell: a glyph, or nothing where a
    /// Delete clears one.
    enum Cell: Equatable {
        case glyph(Character)
        case erase
    }
    var col = 0
    var row = 0
    var cell = Cell.erase
    var deadline = ContinuousClock.now
}

/// The guesses of one terminal, after mosh's: a guess lives until output
/// shows it, covers it or outlives its deadline, and nothing of it reaches
/// the terminal's own state. The main actor alone uses it.
final class Predictor {
    /// What a key needs of the surface: the cursor's cell and the grid's,
    /// whether the mouse or the alternate screen belongs to a program, and
    /// whether the cursor's line asks for a secret.
    struct Context {
        var col = 0, row = 0, cols = 0, rows = 0
        var guarded = false
        var secret = false
        var rtt = Duration.zero
    }

    /// The guesses not resolved yet, in the order they were made.
    private(set) var open: [Guess] = []
    /// Where the real cursor sat when the first guess was made; the open
    /// guesses live only while it stays or moves toward the shadow's.
    private(set) var primed: (col: Int, row: Int)?
    /// Where the open guesses put the cursor: the terminal's shadow.
    private(set) var shadow = (col: 0, row: 0)

    /// The setting's key and its default, on: a guess is made only while
    /// it is on (docs/IOS-018-predictive-echo.md, the design).
    static let key = "predictiveEcho"
    static var isOn: Bool { UserDefaults.standard.object(forKey: key) as? Bool ?? true }

    /// How long a guess waits for its answer: two round trips, at least
    /// half a second, at most five.
    static func until(rtt: Duration) -> Duration {
        min(max(rtt * 2, .milliseconds(500)), .seconds(5))
    }

    /// A key's bytes, as Ghostty encoded them: a short run of printable
    /// characters, Return and Delete guesses; anything else — an escape
    /// sequence, a control key, a paste — closes the open guesses. A
    /// guarded screen or a secret's line guesses nothing.
    func type(_ bytes: Data, context c: Context, now: ContinuousClock.Instant) {
        guard Self.isOn, !c.guarded, !c.secret, c.cols > 0, c.rows > 0,
              !bytes.isEmpty, bytes.count <= 8 else {
            clear()
            return
        }
        if primed == nil {
            primed = (c.col, c.row)
            shadow = (c.col, c.row)
        }
        var at = shadow
        var made: [Guess] = []
        let deadline = now + Self.until(rtt: c.rtt)
        for b in bytes {
            switch b {
            case 0x20...0x7E:
                made.append(Guess(col: at.col, row: at.row, cell: .glyph(Character(UnicodeScalar(b))), deadline: deadline))
                at.col += 1
                if at.col >= c.cols { at.col = 0; at.row += 1 }
            case 0x0D:
                at.col = 0
                at.row += 1
            case 0x7F where at.col > 0:
                at.col -= 1
                made.append(Guess(col: at.col, row: at.row, cell: .erase, deadline: deadline))
            default:
                clear()
                return
            }
            guard at.row < c.rows else { clear(); return }
        }
        open += made
        shadow = at
    }

    /// Output may have reached the screen: real is the cursor now, lines
    /// the text of the guesses' rows. A guess ends shown, overwritten or
    /// expired; the rest fall where the cursor retreated or ran past the
    /// shadow's place.
    func resolve(real: (col: Int, row: Int), lines: [Int: String], now: ContinuousClock.Instant) {
        open.removeAll { g in
            if g.deadline <= now { return true }
            guard let l = lines[g.row] else { return false }
            let c = Self.char(l, at: g.col)
            switch g.cell {
            case .glyph(let w): return c == w || (c != nil && c != " ")
            case .erase: return c == nil || c == " "
            }
        }
        guard !open.isEmpty, let p = primed else { primed = nil; return }
        if real == shadow
            || real.row < p.row || (real.row == p.row && real.col < p.col)
            || real.row > shadow.row || (real.row == shadow.row && real.col > shadow.col) {
            clear()
        }
    }

    /// Drops every guess, shown or not.
    func clear() {
        open = []
        primed = nil
    }

    /// The character shown at a cell of a line, else nil past its end or
    /// inside a wide one; a wide character takes two cells.
    static func char(_ line: String, at col: Int) -> Character? {
        var at = 0
        for c in line {
            if at == col { return c }
            if at > col { return nil }
            at += width(c)
        }
        return nil
    }

    /// How many cells a character takes: a plain one one, a wide or
    /// fullwidth one two, as the terminals' own wide ranges name them.
    static func width(_ c: Character) -> Int {
        c.unicodeScalars.contains { s in Self.wide.contains { $0.contains(s.value) } } ? 2 : 1
    }

    /// The wide and fullwidth ranges, after wcwidth: CJK, Hangul and the
    /// fullwidth forms, emoji's own blocks among them.
    private static let wide: [ClosedRange<UInt32>] = [
        0x1100 ... 0x115F,  // Hangul Jamo
        0x2329 ... 0x232A,  // angle brackets
        0x2E80 ... 0xA4CF,  // CJK Radicals through Yi
        0xAC00 ... 0xD7A3,  // Hangul Syllables
        0xF900 ... 0xFAFF,  // CJK Compatibility Ideographs
        0xFE30 ... 0xFE6F,  // CJK Compatibility Forms
        0xFF00 ... 0xFF60,  // Fullwidth Forms
        0xFFE0 ... 0xFFE6,  // fullwidth signs
        0x1F300 ... 0x1FAFF,  // emoji
        0x20000 ... 0x3FFFD,  // CJK Extension B and later
    ]
}
