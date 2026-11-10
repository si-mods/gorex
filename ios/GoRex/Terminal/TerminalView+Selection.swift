import GhosttyKit
import UIKit

/// Selecting the screen's text, after rootshell's selection gestures. A long
/// press selects the word under the finger, as a double click does, with the
/// system's loupe; the drag that follows extends it a word at a time, and
/// past the top or bottom edge Ghostty scrolls on. A pointer's click and
/// drag selects as a mouse's does. Two handles move the ends,
/// and a menu copies. Ghostty selects as for a mouse, with Shift while a
/// program has the mouse: Ghostty then keeps the clicks from the program.
/// None of this is the keyboard's InputDocument, which never sees the screen.
extension TerminalView: UIEditMenuInteractionDelegate {
    /// Shift while a program has the mouse, so that Ghostty selects.
    private var mods: ghostty_input_mods_e {
        guard let s = surface, ghostty_surface_mouse_captured(s) else { return GHOSTTY_MODS_NONE }
        return GHOSTTY_MODS_SHIFT
    }

    private func click(_ s: ghostty_surface_t, _ states: [ghostty_input_mouse_state_e], at p: CGPoint) {
        let m = mods
        ghostty_surface_mouse_pos(s, p.x, p.y, m)
        for state in states { _ = ghostty_surface_mouse_button(s, state, GHOSTTY_MOUSE_LEFT, m) }
    }

    @objc func hold(_ g: UILongPressGestureRecognizer) {
        guard let s = surface else { return }
        let p = g.location(in: self)
        switch g.state {
        case .began:
            hideSelection()
            // A double click selects the word, and stays down for the drag.
            click(s, [GHOSTTY_MOUSE_PRESS, GHOSTTY_MOUSE_RELEASE, GHOSTTY_MOUSE_PRESS], at: p)
            UIImpactFeedbackGenerator(style: .light, view: self).impactOccurred()
            loupe = UITextLoupeSession.begin(at: p, fromSelectionWidgetView: nil, in: self)
        case .changed:
            ghostty_surface_mouse_pos(s, p.x, p.y, mods)
            loupe?.move(to: p, withCaretRect: .null, trackingCaret: false)
        default:
            click(s, [GHOSTTY_MOUSE_RELEASE], at: p)
            showSelection()
        }
    }

    /// A pointer's drag selects from where it went down.
    @objc func drag(_ g: UIPanGestureRecognizer) {
        guard let s = surface else { return }
        let p = g.location(in: self)
        switch g.state {
        case .began:
            hideSelection()
            let t = g.translation(in: self)
            click(s, [GHOSTTY_MOUSE_PRESS], at: CGPoint(x: p.x - t.x, y: p.y - t.y))
            ghostty_surface_mouse_pos(s, p.x, p.y, mods)
        case .changed:
            ghostty_surface_mouse_pos(s, p.x, p.y, mods)
        default:
            click(s, [GHOSTTY_MOUSE_RELEASE], at: p)
            showSelection()
        }
    }

    /// Moves the end of the selection its handle stands for, from the other.
    /// The end follows the finger as the stem's middle was from it, so that
    /// a finger on the knob does not take the next line.
    @objc func steer(_ g: UIPanGestureRecognizer) {
        guard let s = surface, let h = g.view as? SelectionHandle else { return }
        let f = g.location(in: self)
        if g.state == .began {
            // From where the finger came down, before the drag began.
            let t = g.translation(in: self)
            h.grab = CGVector(dx: f.x - t.x - h.stem.x, dy: f.y - t.y - h.stem.y)
        }
        let p = CGPoint(x: f.x - h.grab.dx, y: f.y - h.grab.dy)
        switch g.state {
        case .began:
            editMenu.dismissMenu()
            _ = ghostty_surface_selection_handle_drag_begin(s, h.start)
            ghostty_surface_mouse_pos(s, p.x, p.y, mods)
            loupe = UITextLoupeSession.begin(at: p, fromSelectionWidgetView: h, in: self)
        case .changed:
            ghostty_surface_mouse_pos(s, p.x, p.y, mods)
            loupe?.move(to: p, withCaretRect: .null, trackingCaret: false)
            placeHandles()
        default:
            click(s, [GHOSTTY_MOUSE_RELEASE], at: p)
            showSelection()
        }
    }

    /// Shows the handles and the menu at the selection just made, if any.
    private func showSelection() {
        loupe?.invalidate()
        loupe = nil
        guard let s = surface, ghostty_surface_has_selection(s) else { return hideSelection() }
        showsSelection = true
        placeHandles()
        let box = ends()?.box ?? bounds
        editMenu.presentEditMenu(with: UIEditMenuConfiguration(identifier: nil, sourcePoint: CGPoint(x: box.midX, y: box.minY)))
    }

    /// The menu goes by the part of the selection in view, its knobs too.
    func editMenuInteraction(_ interaction: UIEditMenuInteraction, targetRectFor configuration: UIEditMenuConfiguration) -> CGRect {
        ends()?.box.insetBy(dx: 0, dy: -SelectionHandle.knob) ?? CGRect(origin: configuration.sourcePoint, size: .zero)
    }

    /// The system's menu, and first in it the link the selection is, if
    /// that is all it is: a tap opens a link, a selection has to say so.
    func editMenuInteraction(_ interaction: UIEditMenuInteraction, menuFor configuration: UIEditMenuConfiguration, suggestedActions: [UIMenuElement]) -> UIMenu? {
        guard let t = selectedText, let url = WebLink.selected(t) else { return nil }
        let open = UIAction(title: String(localized: "Open in Browser"), image: UIImage(systemName: "safari")) { _ in
            UIApplication.shared.open(url)
        }
        return UIMenu(children: [open] + suggestedActions)
    }

    /// Takes the handles and the menu away; the selection stays in Ghostty.
    func hideSelection() {
        showsSelection = false
        handles.start.isHidden = true
        handles.end.isHidden = true
        editMenu.dismissMenu()
    }

    /// The selection changed, or the screen under it scrolled.
    func selectionMoved() {
        guard showsSelection, let s = surface else { return }
        guard ghostty_surface_has_selection(s) else { return hideSelection() }
        placeHandles()
    }

    /// Clears the selection with a click, as Ghostty clears it. False when
    /// there was none.
    func deselect(at p: CGPoint) -> Bool {
        guard let s = surface, ghostty_surface_has_selection(s) else { return false }
        hideSelection()
        // With Shift the click would extend it: a program's mouse reporting
        // is off for this one click instead.
        let captured = ghostty_surface_mouse_captured(s)
        func toggle() { _ = ghostty_surface_binding_action(s, "toggle_mouse_reporting", UInt("toggle_mouse_reporting".utf8.count)) }
        if captured { toggle() }
        click(at: p)
        if captured { toggle() }
        return true
    }

    /// Puts each handle at its end of the selection, hidden when its end
    /// scrolled out of view.
    private func placeHandles() {
        guard let e = ends() else { return }
        for (handle, at, show) in [(handles.start, e.first, e.shown.start), (handles.end, e.last, e.shown.end)] {
            if handle.superview == nil { addSubview(handle) }
            handle.place(at: at, height: cell.height)
            handle.isHidden = !show
        }
    }

    /// The selection's ends: the first cell's top left and the last cell's
    /// top right, whether each is in view, and the box of the lines between
    /// them in view, else the middle of the view.
    private func ends() -> (first: CGPoint, last: CGPoint, shown: (start: Bool, end: Bool), box: CGRect)? {
        guard let s = surface else { return nil }
        var t = ghostty_text_s()
        guard ghostty_surface_read_selection(s, &t) else { return nil }
        defer { ghostty_surface_free_text(s, &t) }
        var shown = (start: true, end: true)
        _ = ghostty_surface_selection_viewport_visibility(s, &shown.start, &shown.end)
        let cols = Int(ghostty_surface_size(s).columns), c = cell
        guard cols > 0, c.width > 0, c.height > 0, t.tl_px_x.isFinite else { return nil }
        // The first cell, and the last: Ghostty counts cells from the
        // viewport's top left, and the length to the last. It gives the
        // first cell's left in points, whence the grid's, but the baseline
        // of its text, not its top: the grid's top is the cursor's, a whole
        // number of lines up, below the band under the bars.
        let a = Int(t.offset_start)
        let top = inset + (cursor(width: nil).minY - inset).truncatingRemainder(dividingBy: c.height)
        let origin = CGPoint(x: t.tl_px_x - CGFloat(a % cols) * c.width, y: top)
        func corner(_ i: Int, right: Bool) -> CGPoint {
            CGPoint(x: origin.x + CGFloat(i % cols + (right ? 1 : 0)) * c.width, y: origin.y + CGFloat(i / cols) * c.height)
        }
        let first = corner(a, right: false), last = corner(a + Int(t.offset_len), right: true)
        let box = first.y == last.y
            ? CGRect(x: first.x, y: first.y, width: last.x - first.x, height: c.height)
            : CGRect(x: 0, y: first.y, width: bounds.width, height: last.y + c.height - first.y)
        let seen = box.intersection(bounds)
        return (first, last, shown, seen.isNull ? CGRect(x: bounds.midX, y: bounds.midY, width: 0, height: 0) : seen)
    }

    /// The selected text, as Ghostty reads it: wide characters whole, and
    /// wrapped lines joined.
    var selectedText: String? {
        guard let s = surface else { return nil }
        var t = ghostty_text_s()
        guard ghostty_surface_read_selection(s, &t) else { return nil }
        defer { ghostty_surface_free_text(s, &t) }
        guard let p = t.text, t.text_len > 0 else { return nil }
        return String(decoding: UnsafeRawBufferPointer(start: p, count: Int(t.text_len)), as: UTF8.self)
    }

    override func copy(_ sender: Any?) {
        if let t = selectedText { UIPasteboard.general.string = t }
    }

    override func selectAll(_ sender: Any?) {
        guard let s = surface else { return }
        _ = ghostty_surface_binding_action(s, "select_all", UInt("select_all".utf8.count))
        showSelection()
    }
}

/// One end of the selection, after rootshell's SelectionHandleView: a stem
/// the height of the line at the outer edge of the end cell, with a knob
/// above the first or below the last. Touches near it are its own, not the
/// terminal's.
final class SelectionHandle: UIView {
    let start: Bool
    static let knob: CGFloat = 10
    /// Where the finger took it, from the stem's middle.
    var grab = CGVector.zero
    /// The stem's middle, in the terminal.
    var stem: CGPoint { CGPoint(x: frame.midX, y: start ? (frame.minY + Self.knob + frame.maxY) / 2 : (frame.minY + frame.maxY - Self.knob) / 2) }

    init(start: Bool) {
        self.start = start
        super.init(frame: .zero)
        isOpaque = false
        isHidden = true
        accessibilityIdentifier = start ? "selection-start" : "selection-end"
    }

    required init?(coder: NSCoder) { fatalError() }

    /// Stands the stem at a cell's top corner, a line high.
    func place(at p: CGPoint, height: CGFloat) {
        let k = Self.knob
        frame = CGRect(x: p.x - k, y: start ? p.y - k : p.y, width: 2 * k, height: height + k)
        setNeedsDisplay()
    }

    override func draw(_ rect: CGRect) {
        let k = Self.knob
        tintColor.setFill()
        UIRectFill(CGRect(x: bounds.midX - 1, y: start ? k / 2 : 0, width: 2, height: bounds.height - k / 2))
        UIBezierPath(ovalIn: CGRect(x: bounds.midX - k / 2, y: start ? 0 : bounds.height - k, width: k, height: k)).fill()
    }

    override func point(inside point: CGPoint, with event: UIEvent?) -> Bool {
        bounds.insetBy(dx: -16, dy: -16).contains(point)
    }

    override func gestureRecognizerShouldBegin(_ g: UIGestureRecognizer) -> Bool { g.view === self }
}
