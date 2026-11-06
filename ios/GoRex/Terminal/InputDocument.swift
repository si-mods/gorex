import CoreGraphics
import Foundation

/// The text the keyboard believes it edits, for UITextInput, after
/// rootshell's TerminalCorrectionContext: what was typed since the last key
/// that types no text, then the text an input method composes. Each change
/// gives the edits that make the program's line follow: keys to type, text
/// to send as it is, Backspaces, and the composed text Ghostty shows at the
/// cursor.
struct InputDocument {
    enum Edit: Equatable {
        /// A key typed on the keyboard: the held modifiers apply.
        case type(String)
        /// Composed or dictated text, sent as it is.
        case text(String)
        /// Backspace, pressed on the keyboard: the held modifiers apply.
        case backspace
        /// Backspaces that take back typed text.
        case erase(Int)
        /// The text being composed, shown at the cursor; nil hides it.
        case preedit(String?)
    }

    /// The typed text since the last key that types no text.
    private(set) var typed = ""
    /// The text being composed, and the selection in it.
    private(set) var marked: String?
    private(set) var selected = NSRange(location: 0, length: 0)

    /// The whole text, in UTF-16 units as UIKit counts them.
    var count: Int { typed.utf16.count + (marked?.utf16.count ?? 0) }
    var text: String { typed + (marked ?? "") }
    /// The composed text's range in the whole.
    var markedRange: NSRange? { marked.map { NSRange(location: typed.utf16.count, length: $0.utf16.count) } }
    /// The caret: in the composed text where its selection is, else at the end.
    var selection: NSRange {
        guard let m = marked else { return NSRange(location: count, length: 0) }
        let at = min(selected.location, m.utf16.count)
        return NSRange(location: typed.utf16.count + at, length: min(selected.length, m.utf16.count - at))
    }

    /// An input method composes: show the text at the cursor.
    mutating func mark(_ text: String?, selected range: NSRange) -> [Edit] {
        let t = text?.precomposedStringWithCanonicalMapping ?? ""
        marked = t.isEmpty ? nil : t
        selected = range.location == NSNotFound ? NSRange(location: t.utf16.count, length: 0) : range
        return [.preedit(marked)]
    }

    /// The composition ends as it stands: some input methods commit so.
    mutating func unmark() -> [Edit] {
        guard let m = marked else { return [] }
        marked = nil
        append(m)
        return [.preedit(nil), .text(m)]
    }

    /// Text in place of the composition, if any, else a key or text typed.
    /// A key typed as a chord, with Ctrl or Alt held, sends no text: the
    /// line is no longer what was typed.
    mutating func insert(_ text: String, chord: Bool = false) -> [Edit] {
        let t = text.precomposedStringWithCanonicalMapping
        if marked != nil {
            marked = nil
            append(t)
            return t.isEmpty ? [.preedit(nil)] : [.preedit(nil), .text(t)]
        }
        guard !t.isEmpty else { return [] }
        let key = t.count == 1
        if key, chord { reset() } else { append(t) }
        return [key ? .type(t) : .text(t)]
    }

    /// Backspace edits the composition, if any, else goes to the program;
    /// as a chord it may erase more than a character.
    mutating func deleteBackward(chord: Bool = false) -> [Edit] {
        if var m = marked {
            m.removeLast()
            marked = m.isEmpty ? nil : m
            selected = NSRange(location: m.utf16.count, length: 0)
            return [.preedit(marked)]
        }
        if chord { reset() } else if !typed.isEmpty { typed.removeLast() }
        return [.backspace]
    }

    /// Text in place of a range, as dictation corrects what it typed: a
    /// range in the composition commits the composition so corrected, and
    /// a range in the typed text is erased back to its start and typed
    /// again. A range outside the text changes nothing.
    mutating func replace(_ range: NSRange, with text: String) -> [Edit] {
        let n = typed.utf16.count
        if let m = marked, range.location >= n {
            guard let r = Range(NSRange(location: range.location - n, length: range.length), in: m) else { return [] }
            return insert(m.replacingCharacters(in: r, with: text))
        }
        guard marked == nil, range.length >= 0, range.location >= 0, range.location + range.length <= n,
              let r = Range(range, in: typed) else { return [] }
        if range.location == n { return insert(text) }
        let t = text.precomposedStringWithCanonicalMapping
        let tail = String(typed[r.upperBound...])
        let erased = typed[r.lowerBound...].count
        typed = String(typed[..<r.lowerBound])
        append(t + tail)
        return t + tail == "" ? [.erase(erased)] : [.erase(erased), .text(t + tail)]
    }

    /// The program's line moved on: a key typed no text, or the cursor went.
    mutating func reset() { typed = "" }

    /// Adds to the typed text; past a control key, such as Return or Tab,
    /// the line is no longer what was typed.
    private mutating func append(_ t: String) {
        if let c = t.lastIndex(where: { $0.unicodeScalars.contains { CharacterSet.controlCharacters.contains($0) } }) {
            typed = String(t[t.index(after: c)...])
        } else {
            typed += t
        }
    }
}

/// The spacebar as a trackpad: the arrows that take the cursor one cell for
/// each cell the finger went, across and down.
struct Trackpad {
    private var start: CGPoint?
    private var col = 0, row = 0

    mutating func begin(at p: CGPoint) {
        start = p
        col = 0
        row = 0
    }

    /// The arrow keys, by name, for the cells crossed since the last update.
    mutating func update(to p: CGPoint, cell: CGSize) -> [String] {
        guard let start, cell.width > 0, cell.height > 0 else { return [] }
        let c = Int(((p.x - start.x) / cell.width).rounded(.towardZero))
        let r = Int(((p.y - start.y) / cell.height).rounded(.towardZero))
        defer { (col, row) = (c, r) }
        return Array(repeating: c > col ? "arrow_right" : "arrow_left", count: abs(c - col))
            + Array(repeating: r > row ? "arrow_down" : "arrow_up", count: abs(r - row))
    }

    mutating func end() { start = nil }
}
