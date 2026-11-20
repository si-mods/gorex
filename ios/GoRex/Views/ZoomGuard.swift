import SwiftUI
import UIKit
import UIKit.UIGestureRecognizerSubclass

/// Keeps a zoom transition's interactive close (a drag down, a pinch) from
/// starting inside a view whose own drags and pinches mean something else:
/// the gallery's image (IOS-011). It begins at the first touch and holds
/// until the last lifts, and the pans and pinches of the views above wait
/// for it to fail: the edge swipe back and the scroll views above, as a
/// pager's, keep working; the view's own recognisers, and those under it,
/// are never held.
final class ZoomGuard: UIGestureRecognizer, UIGestureRecognizerDelegate {
    /// A guard that takes no touch from any other recogniser.
    static func make() -> ZoomGuard {
        let g = ZoomGuard(target: nil, action: nil)
        g.cancelsTouchesInView = false
        g.delaysTouchesBegan = false
        g.delaysTouchesEnded = false
        g.delegate = g
        return g
    }

    override func touchesBegan(_ touches: Set<UITouch>, with event: UIEvent) {
        super.touchesBegan(touches, with: event)
        if state == .possible { state = .began }
    }

    override func touchesEnded(_ touches: Set<UITouch>, with event: UIEvent) {
        super.touchesEnded(touches, with: event)
        endIfLifted(event)
    }

    override func touchesCancelled(_ touches: Set<UITouch>, with event: UIEvent) {
        super.touchesCancelled(touches, with: event)
        endIfLifted(event)
    }

    /// Ends once no finger is left down.
    private func endIfLifted(_ event: UIEvent) {
        let down = event.touches(for: self)?.filter { $0.phase != .ended && $0.phase != .cancelled } ?? []
        if down.isEmpty, state == .began || state == .changed { state = .ended }
    }

    func gestureRecognizer(_ g: UIGestureRecognizer, shouldRecognizeSimultaneouslyWith other: UIGestureRecognizer) -> Bool {
        true
    }

    /// A pan or a pinch above, the zoom's close among them, waits; not the
    /// edge swipe back, nor a scroll view's.
    func gestureRecognizer(_ g: UIGestureRecognizer, shouldBeRequiredToFailBy other: UIGestureRecognizer) -> Bool {
        guard let mine = view, let theirs = other.view, theirs !== mine, mine.isDescendant(of: theirs) else { return false }
        guard other is UIPanGestureRecognizer || other is UIPinchGestureRecognizer else { return false }
        return !(other is UIScreenEdgePanGestureRecognizer) && !(theirs is UIScrollView)
    }
}
