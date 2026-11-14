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
    }

    /// Slides again once Ghostty has drawn the output that came, which moves
    /// the cursor, a tick later: at most once a tick while output streams,
    /// and once more a tick after the last of it, which Ghostty may not have
    /// drawn yet when the tick before came.
    ///
    /// The output slides the terminal up as far as it needs, at once, and
    /// down only where the need stays: a program draws the lines after the
    /// cursor in an order of its own, so they pass, between two writes,
    /// through rows no frame ends on, and a slide that followed them down
    /// again would shake the terminal by a line for as long as an agent's
    /// spinner works. A need to come down that four ticks in a row ask for is
    /// where the program means to draw, and comes down then.
    func laterSlide() {
        guard !slideWaits else { return slideAgain = true }
        slideWaits = true
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.05) { [weak self] in
            guard let self else { return }
            let again = slideAgain
            slideWaits = false
            slideAgain = false
            guard surface != nil else { return }
            drawn = true
            let y = needed()
            if y < transform.ty {
                slide(to: y, duration: 0.15)
            } else if y > transform.ty {
                wantsDown += 1
                if wantsDown >= Self.downTicks {
                    wantsDown = 0
                    slide(to: y, duration: 0.15)
                }
            } else {
                wantsDown = 0
            }
            if again { laterSlide() } else { settle() }
        }
    }

    /// Slides once the output stops, where the last frame left the cursor and
    /// the lines under it, so the terminal comes down again as far as they
    /// let. Output that came meanwhile arms a settle of its own.
    private func settle() {
        settling?.cancel()
        let wrote = self.wrote
        let work = DispatchWorkItem { [weak self] in
            guard let self, wrote == self.wrote, surface != nil else { return }
            settling = nil
            slide(duration: 0.2)
        }
        settling = work
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.35, execute: work)
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
