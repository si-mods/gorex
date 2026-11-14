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

    /// The typed text since the last key that types no text, and any
    /// selection within it (nil means the caret is at the end).
    private(set) var typed = ""
    private var typedSelection: NSRange?
    /// The text being composed, and the selection in it.
    private(set) var marked: String?
    private(set) var selected = NSRange(location: 0, length: 0)

    /// The program's line before what was typed: always there, never seen,
    /// so Backspace always has something to erase. Without it, the keyboard
    /// predicts an empty line after a Backspace and stops a held one's
    /// repeat.
    private static let anchor = "\u{200B}"
    private static let a = anchor.utf16.count

    /// The whole text, in UTF-16 units as UIKit counts them.
    var count: Int { text.utf16.count }
    var text: String { Self.anchor + typed + (marked ?? "") }
    /// The composed text's range in the whole.
    var markedRange: NSRange? { marked.map { NSRange(location: Self.a + typed.utf16.count, length: $0.utf16.count) } }
    /// The caret: in the composed text where its selection is, else in the
    /// typed text if selected, else at the end.
    var selection: NSRange {
        if let m = marked {
            let at = min(selected.location, m.utf16.count)
            return NSRange(location: Self.a + typed.utf16.count + at, length: min(selected.length, m.utf16.count - at))
        }
        if let s = typedSelection {
            return NSRange(location: Self.a + s.location, length: s.length)
        }
        return NSRange(location: count, length: 0)
    }

    /// Whether a Backspace now erases what the keyboard does not see: the
    /// line before the typed text, or more, as a chord. The keyboard
    /// believes it erased the anchor, which stays.
    func erasesUnseen(chord: Bool) -> Bool {
        guard marked == nil else { return false }
        if chord { return true }
        if let s = typedSelection { return s.location == 0 && s.length == 0 }
        return typed.isEmpty
    }

    /// Moves or expands the selection inside the typed text or composition.
    /// Selections overlapping the anchor are clamped past it so the anchor stays.
    @discardableResult
    mutating func select(_ range: NSRange) -> Bool {
        guard range.location != NSNotFound, range.length >= 0, range.location >= 0,
              range.location + range.length <= count else { return false }
        if let m = marked {
            let mStart = Self.a + typed.utf16.count
            guard range.location >= mStart else { return false }
            selected = NSRange(location: range.location - mStart, length: range.length)
            return true
        }
        let end = range.location + range.length
        let loc = max(range.location, Self.a) - Self.a
        let len = max(end, Self.a) - Self.a - loc
        let r = NSRange(location: loc, length: len)
        guard Range(r, in: typed) != nil else { return false }
        typedSelection = (r.location == typed.utf16.count && r.length == 0) ? nil : r
        return true
    }

    /// An input method composes: show the text at the cursor.
    mutating func mark(_ text: String?, selected range: NSRange) -> [Edit] {
        typedSelection = nil
        let t = text?.precomposedStringWithCanonicalMapping ?? ""
        marked = t.isEmpty ? nil : t
        selected = range.location == NSNotFound ? NSRange(location: t.utf16.count, length: 0) : range
        return [.preedit(marked)]
    }

    /// The composition ends as it stands: some input methods commit so.
    mutating func unmark() -> [Edit] {
        guard let m = marked else { return [] }
        marked = nil
        typedSelection = nil
        _ = append(m)
        return [.preedit(nil), .text(m)]
    }

    /// Text in place of the composition, if any, else a key or text typed
    /// (replacing any active selection in the typed text).
    /// A key typed as a chord, with Ctrl or Alt held, sends no text: the
    /// line is no longer what was typed.
    mutating func insert(_ text: String, chord: Bool = false) -> [Edit] {
        let t = text.precomposedStringWithCanonicalMapping
        if marked != nil {
            marked = nil
            typedSelection = nil
            _ = append(t)
            return t.isEmpty ? [.preedit(nil)] : [.preedit(nil), .text(t)]
        }
        if let s = typedSelection {
            if t.count == 1, chord {
                reset()
                return [.type(t)]
            }
            if t.isEmpty && s.length == 0 { return [] }
            return replaceTyped(s, with: t, keepCaret: true)
        }
        guard !t.isEmpty else { return [] }
        let key = t.count == 1
        if key, chord { reset() } else { _ = append(t) }
        return [key ? .type(t) : .text(t)]
    }

    /// Backspace edits the composition, if any, else the selection in the
    /// typed text, else goes to the program; as a chord it may erase more
    /// than a character.
    mutating func deleteBackward(chord: Bool = false) -> [Edit] {
        if var m = marked {
            m.removeLast()
            marked = m.isEmpty ? nil : m
            selected = NSRange(location: m.utf16.count, length: 0)
            return [.preedit(marked)]
        }
        if chord {
            reset()
            return [.backspace]
        }
        if let s = typedSelection {
            if s.length > 0 {
                return replaceTyped(s, with: "", keepCaret: true)
            }
            guard s.location > 0, let r = Range(s, in: typed) else { return [.backspace] }
            let prev = typed.index(before: r.lowerBound)
            return replaceTyped(NSRange(prev..<r.lowerBound, in: typed), with: "", keepCaret: true)
        }
        if !typed.isEmpty { typed.removeLast() }
        return [.backspace]
    }

    /// Text in place of a range, as dictation corrects what it typed: a
    /// range in the composition commits the composition so corrected, and
    /// a range in the typed text is erased back to its start and typed
    /// again. A range outside the text, or on the anchor, changes nothing.
    mutating func replace(_ range: NSRange, with text: String) -> [Edit] {
        guard range.location >= Self.a else { return [] }
        let range = NSRange(location: range.location - Self.a, length: range.length)
        let n = typed.utf16.count
        if let m = marked, range.location >= n {
            guard let r = Range(NSRange(location: range.location - n, length: range.length), in: m) else { return [] }
            return insert(m.replacingCharacters(in: r, with: text))
        }
        guard marked == nil, range.length >= 0, range.location >= 0, range.location + range.length <= n,
              Range(range, in: typed) != nil else { return [] }
        if range.location == n {
            typedSelection = nil
            return insert(text)
        }
        return replaceTyped(range, with: text, keepCaret: false)
    }

    /// The program's line moved on: a key typed no text, or the cursor went.
    mutating func reset() {
        typed = ""
        typedSelection = nil
    }

    private mutating func replaceTyped(_ range: NSRange, with text: String, keepCaret: Bool) -> [Edit] {
        guard let r = Range(range, in: typed) else { return [] }
        let t = text.precomposedStringWithCanonicalMapping
        let tail = String(typed[r.upperBound...])
        let erased = typed[r.lowerBound...].count
        typed = String(typed[..<r.lowerBound])
        if append(t) {
            typedSelection = nil
            _ = append(tail)
        } else {
            let caret = typed.utf16.count
            let tailControl = append(tail)
            typedSelection = (keepCaret && !tail.isEmpty && !tailControl) ? NSRange(location: caret, length: 0) : nil
        }
        return t + tail == "" ? [.erase(erased)] : [.erase(erased), .text(t + tail)]
    }

    /// Adds to the typed text; past a control key, such as Return or Tab,
    /// the line is no longer what was typed. Returns true if a control key reset it.
    private mutating func append(_ t: String) -> Bool {
        if let c = t.lastIndex(where: { $0.unicodeScalars.contains { CharacterSet.controlCharacters.contains($0) } }) {
            typed = String(t[t.index(after: c)...])
            return true
        } else {
            typed += t
            return false
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
