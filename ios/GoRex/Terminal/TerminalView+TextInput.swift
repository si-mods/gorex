import GhosttyKit
import UIKit

/// The terminal as UIKit's text input, after rootshell's
/// TerminalView+TextInput: input methods compose in it, dictation types and
/// corrects in it, and the spacebar's trackpad moves the cursor. The text is
/// the view's InputDocument; Ghostty shows the composition at the cursor and
/// says where the cursor is, for the candidates.
extension TerminalView: UITextInput {
    var hasText: Bool { true }

    func insertText(_ text: String) {
        let chord = ctrl || alt, e = document.insert(text, chord: chord)
        trace("insertText \(InputTrace.quote(text)) chord=\(chord)", e)
        apply(e)
    }

    /// The keyboard predicts its own edits, and a Backspace past the typed
    /// text breaks the prediction: the anchor it believes erased stays. It
    /// is told, or it takes the line for empty and stops a held Backspace.
    func deleteBackward() {
        let chord = ctrl || alt, unseen = document.erasesUnseen(chord: chord)
        let delete = {
            let e = self.document.deleteBackward(chord: chord)
            self.trace("deleteBackward chord=\(chord) unseen=\(unseen)", e)
            self.apply(e)
        }
        if unseen { changing(delete) } else { delete() }
    }

    func setMarkedText(_ markedText: String?, selectedRange: NSRange) {
        let e = document.mark(markedText, selected: selectedRange)
        trace("setMarkedText \(InputTrace.quote(markedText)) selected=\(InputTrace.range(selectedRange))", e)
        apply(e)
    }

    func unmarkText() {
        let e = document.unmark()
        trace("unmarkText", e)
        apply(e)
    }

    func replace(_ range: UITextRange, withText text: String) {
        guard let r = range as? TextRange else { return trace("replace \(traced(range)) with \(InputTrace.quote(text)) ignored") }
        let e = document.replace(r.range, with: text)
        trace("replace \(InputTrace.range(r.range)) with \(InputTrace.quote(text))", e)
        apply(e)
    }

    func insertDictationResult(_ dictationResult: [UIDictationPhrase]) {
        trace("insertDictationResult \(dictationResult.map { InputTrace.quote($0.text) }.joined(separator: " "))")
        insertText(dictationResult.map(\.text).joined())
    }

    func text(in range: UITextRange) -> String? {
        let t = (range as? TextRange).flatMap { Range($0.range, in: document.text) }.map { String(document.text[$0]) }
        trace("text(in: \(traced(range))) -> \(InputTrace.quote(t))")
        return t
    }

    var markedTextRange: UITextRange? { document.markedRange.map(TextRange.init) }

    /// The terminal's cursor is the program's: the keyboard cannot move it.
    var selectedTextRange: UITextRange? {
        get { TextRange(document.selection) }
        set { trace("set selectedTextRange \(traced(newValue)) ignored") }
    }

    /// Notes a keyboard call for InputTrace, after the keyboard in use, and
    /// with its edits, the document after them.
    func trace(_ call: @autoclosure () -> String, _ edits: [InputDocument.Edit]? = nil) {
        guard InputTrace.isOn else { return }
        InputTrace.keyboard(textInputMode.map { "\($0.primaryLanguage ?? "nil") \(NSStringFromClass(Swift.type(of: $0)))" } ?? "nil")
        InputTrace.note(call() + (edits.map { " -> \(InputTrace.edits($0)) \(document.traced)" } ?? ""))
    }

    /// A range as a trace line shows it: {location,length}, or the class of
    /// one not the view's.
    private func traced(_ range: UITextRange?) -> String {
        guard let range else { return "nil" }
        return (range as? TextRange).map { InputTrace.range($0.range) } ?? "\(Swift.type(of: range))"
    }

    // MARK: Positions

    var beginningOfDocument: UITextPosition { TextPosition(0) }
    var endOfDocument: UITextPosition { TextPosition(document.count) }

    func textRange(from fromPosition: UITextPosition, to toPosition: UITextPosition) -> UITextRange? {
        guard let a = fromPosition as? TextPosition, let b = toPosition as? TextPosition else { return nil }
        return TextRange(NSRange(location: min(a.offset, b.offset), length: abs(b.offset - a.offset)))
    }

    func position(from position: UITextPosition, offset: Int) -> UITextPosition? {
        guard let p = position as? TextPosition, case let o = p.offset + offset, (0...document.count).contains(o) else { return nil }
        return TextPosition(o)
    }

    func position(from position: UITextPosition, in direction: UITextLayoutDirection, offset: Int) -> UITextPosition? {
        self.position(from: position, offset: direction == .left || direction == .up ? -offset : offset)
    }

    func compare(_ position: UITextPosition, to other: UITextPosition) -> ComparisonResult {
        let a = (position as? TextPosition)?.offset ?? 0, b = (other as? TextPosition)?.offset ?? 0
        return a < b ? .orderedAscending : a > b ? .orderedDescending : .orderedSame
    }

    func offset(from: UITextPosition, to toPosition: UITextPosition) -> Int {
        ((toPosition as? TextPosition)?.offset ?? 0) - ((from as? TextPosition)?.offset ?? 0)
    }

    func position(within range: UITextRange, farthestIn direction: UITextLayoutDirection) -> UITextPosition? {
        direction == .left || direction == .up ? range.start : range.end
    }

    func characterRange(byExtending position: UITextPosition, in direction: UITextLayoutDirection) -> UITextRange? {
        guard let p = position as? TextPosition else { return nil }
        let back = direction == .left || direction == .up
        return textRange(from: p, to: self.position(from: p, offset: back ? -1 : 1) ?? p)
    }

    func baseWritingDirection(for position: UITextPosition, in direction: UITextStorageDirection) -> NSWritingDirection { .leftToRight }

    func setBaseWritingDirection(_ writingDirection: NSWritingDirection, for range: UITextRange) {}

    // MARK: Geometry

    /// The composition's box at the cursor, a cell wide when empty: the
    /// candidates go by it.
    func firstRect(for range: UITextRange) -> CGRect { cursor(width: nil) }

    func caretRect(for position: UITextPosition) -> CGRect { cursor(width: 2) }

    func selectionRects(for range: UITextRange) -> [UITextSelectionRect] { [] }

    func closestPosition(to point: CGPoint) -> UITextPosition? { endOfDocument }

    func closestPosition(to point: CGPoint, within range: UITextRange) -> UITextPosition? { range.end }

    func characterRange(at point: CGPoint) -> UITextRange? { TextRange(NSRange(location: document.count, length: 0)) }

    /// A cell's size, in points.
    var cell: CGSize {
        guard let s = surface else { return .zero }
        let z = ghostty_surface_size(s), scale = traitCollection.displayScale
        return CGSize(width: CGFloat(z.cell_width_px) / scale, height: CGFloat(z.cell_height_px) / scale)
    }

    /// Ghostty gives the cursor cell's middle and bottom, and its height, in
    /// points, and the composition's width in pixels.
    func cursor(width: CGFloat?) -> CGRect {
        guard let s = surface else { return CGRect(x: 0, y: 0, width: 2, height: 20) }
        var x = 0.0, y = 0.0, w = 0.0, h = 0.0
        ghostty_surface_ime_point(s, &x, &y, &w, &h)
        let c = cell
        return CGRect(x: x - c.width / 2, y: y - h, width: width ?? max(w / traitCollection.displayScale, c.width), height: h)
    }

    // MARK: Trackpad

    /// Holding the spacebar moves the cursor with arrows, which Ghostty
    /// encodes as the program's cursor keys ask.
    func beginFloatingCursor(at point: CGPoint) {
        trace("beginFloatingCursor")
        trackpad.begin(at: point)
    }

    func updateFloatingCursor(at point: CGPoint) {
        let arrows = trackpad.update(to: point, cell: cell)
        guard !arrows.isEmpty else { return }
        forget()
        press(arrows.map { .named($0) })
    }

    func endFloatingCursor() {
        trace("endFloatingCursor")
        trackpad.end()
    }
}

/// A place in the InputDocument, in UTF-16 units.
nonisolated final class TextPosition: UITextPosition {
    let offset: Int
    init(_ offset: Int) { self.offset = offset }
}

nonisolated final class TextRange: UITextRange {
    let range: NSRange
    init(_ range: NSRange) { self.range = range }
    override var start: UITextPosition { TextPosition(range.location) }
    override var end: UITextPosition { TextPosition(range.location + range.length) }
    override var isEmpty: Bool { range.length == 0 }
}
