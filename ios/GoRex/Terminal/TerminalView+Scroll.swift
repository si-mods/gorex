import GhosttyKit
import UIKit

/// Scrolling with UIKit's own physics: a scroll view with no content and no
/// touches of its own, whose pan is the terminal's. The finger, the coast
/// after a flick, the bounce at the ends and the indicator are the scroll
/// view's; where it comes to drives Ghostty. In the scrollback its offset is
/// the viewport's top row and the part of a row past it, which Ghostty draws
/// smoothly, and past the ends Ghostty draws the bounce. A full-screen
/// program, or one that has the mouse, gets the movement as the wheel, the
/// coast too.
extension TerminalView: UIScrollViewDelegate {
    /// Puts the scroll view over the renderer's layer, for its indicator,
    /// and gives its pan to the terminal.
    func addScroller() {
        scroller.delegate = self
        scroller.isUserInteractionEnabled = false
        scroller.alwaysBounceVertical = true
        scroller.contentInsetAdjustmentBehavior = .never
        // The bar floats over the terminal clear, without a blur at its edge.
        scroller.topEdgeEffect.isHidden = true
        scroller.panGestureRecognizer.maximumNumberOfTouches = 1
        // A trackpad's two fingers scroll; a pointer's drag selects instead.
        scroller.panGestureRecognizer.allowedScrollTypesMask = .all
        scroller.panGestureRecognizer.allowedTouchTypes = [UITouch.TouchType.direct, .pencil].map { $0.rawValue as NSNumber }
        addSubview(scroller)
        addGestureRecognizer(scroller.panGestureRecognizer)
    }

    /// Ghostty's scrollback as the scroll view's content, a row a cell high,
    /// while neither the finger nor a coast moves it. A row moved under it
    /// keeps the part of a row it was scrolled past.
    func syncScroll() {
        guard let s = surface, !scroller.isTracking, !scroller.isDecelerating else { return }
        var b = ghostty_action_scrollbar_s()
        let h = cell.height
        guard h > 0, ghostty_surface_display_scrollbar(s, &b) else { return }
        wheel = false
        let end = CGFloat(b.total > b.len ? b.total - b.len : 0) * h
        let y = scroller.contentOffset.y, row = Int(y / h)
        syncing = true
        scroller.contentSize = CGSize(width: bounds.width, height: end + bounds.height)
        if row != Int(b.offset) || y > end { scroller.contentOffset.y = min(CGFloat(b.offset) * h + y - CGFloat(row) * h, end) }
        syncing = false
        checkCover()
    }

    /// Tells the session whether the cursor is under the bar, at the end of
    /// the scrollback, where the bar would hide what is typed; scrolled back,
    /// it is not.
    func checkCover() {
        guard let s = surface else { return }
        var b = ghostty_action_scrollbar_s()
        let live = !ghostty_surface_display_scrollbar(s, &b) || b.offset + b.len >= b.total
        let under = live && cover > 0 && cursor(width: nil).minY < cover
        if session.covered != under { session.covered = under }
    }

    /// Checks again once Ghostty has drawn the output that came, which moves
    /// the cursor: a tick later, once for a burst.
    func laterCheckCover() {
        guard !coverCheck else { return }
        coverCheck = true
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.05) { [weak self] in
            self?.coverCheck = false
            self?.checkCover()
        }
    }

    /// A program on the full screen, or with the mouse, takes the drag as
    /// the wheel: the content is then tall enough never to end.
    func scrollViewWillBeginDragging(_ v: UIScrollView) {
        guard let s = surface else { return }
        let p = v.panGestureRecognizer.location(in: self)
        ghostty_surface_mouse_pos(s, p.x, p.y, GHOSTTY_MODS_NONE)
        wheel = ghostty_surface_is_alternate_active(s) || ghostty_surface_mouse_captured(s)
        v.showsVerticalScrollIndicator = !wheel
        v.alwaysBounceVertical = !wheel
        guard wheel else { return }
        syncing = true
        v.contentSize = CGSize(width: bounds.width, height: 1e6)
        v.contentOffset.y = 5e5
        syncing = false
        last = v.contentOffset.y
    }

    func scrollViewDidScroll(_ v: UIScrollView) {
        guard !syncing, let s = surface else { return }
        let y = v.contentOffset.y, scale = traitCollection.displayScale
        if wheel {
            // The lines follow the finger: down scrolls back. The scroll
            // mods: precise, in pixels, and the coast's momentum phase.
            let momentum = v.isDecelerating ? GHOSTTY_MOUSE_MOMENTUM_CHANGED : GHOSTTY_MOUSE_MOMENTUM_NONE
            ghostty_surface_mouse_scroll(s, 0, (last - y) * scale, 1 | Int32(momentum.rawValue) << 1)
            last = y
            return
        }
        let h = cell.height, end = max(0, v.contentSize.height - v.bounds.height)
        guard h > 0 else { return }
        let at = min(max(y, 0), end)
        ghostty_surface_set_rubber_band_offset(s, (at - y) * scale)
        if at >= end {
            // The bottom, where the screen follows the output again.
            ghostty_surface_set_smooth_scroll_offset(s, 0)
            _ = ghostty_surface_binding_action(s, "scroll_to_bottom", UInt("scroll_to_bottom".utf8.count))
        } else {
            let row = Int(at / h)
            ghostty_surface_scroll_to_row_smooth(s, UInt(row), (at - CGFloat(row) * h) * scale)
        }
        checkCover()
    }

    func scrollViewDidEndDragging(_ v: UIScrollView, willDecelerate: Bool) {
        if !willDecelerate { syncScroll() }
    }

    func scrollViewDidEndDecelerating(_ v: UIScrollView) { syncScroll() }

    /// Stops a coast for input, which Ghostty scrolls to the bottom for,
    /// and drops the part of a row.
    func stopScrolling() {
        guard let s = surface else { return }
        if scroller.isDecelerating {
            syncing = true
            scroller.setContentOffset(scroller.contentOffset, animated: false)
            syncing = false
        }
        ghostty_surface_set_smooth_scroll_offset(s, 0)
        ghostty_surface_set_rubber_band_offset(s, 0)
    }
}
