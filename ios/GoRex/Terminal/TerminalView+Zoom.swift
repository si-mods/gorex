import GhosttyKit
import UIKit

/// Pinching steps the font a point for each 15% of the pinch, with a tap of
/// the hand at each step, after rootshell's pinch. The size is the one in
/// Settings, so it sticks. A label shows the grid it comes to; the block
/// takes the grid once it holds, as for any resize.
extension TerminalView {
    @objc func pinch(_ g: UIPinchGestureRecognizer) {
        switch g.state {
        case .began:
            // A long press under the first finger gives way.
            longPress.isEnabled = false
            longPress.isEnabled = true
            sizeLabel.layer.removeAllAnimations()
        case .changed:
            let step = g.scale >= 1.15 ? 1 : g.scale <= 1 / 1.15 ? -1 : 0
            guard step != 0 else { return }
            g.scale = 1
            zoom(by: step)
        default:
            UIView.animate(withDuration: 0.3, delay: 1) { self.sizeLabel.alpha = 0 }
        }
    }

    /// Makes the font a point larger or smaller, within Settings' range.
    private func zoom(by step: Int) {
        guard let s = surface else { return }
        let size = min(max(Self.fontSize + CGFloat(step), 8), 24)
        guard size != Self.fontSize else { return }
        UserDefaults.standard.set(Double(size), forKey: "fontSize")
        let action = step > 0 ? "increase_font_size:1" : "decrease_font_size:1"
        _ = ghostty_surface_binding_action(s, action, UInt(action.utf8.count))
        UIImpactFeedbackGenerator(style: .light, view: self).impactOccurred()
        showSize()
    }

    /// Shows the grid's columns and rows over the middle of the screen.
    func showSize() {
        guard let s = surface else { return }
        let z = ghostty_surface_size(s)
        sizeLabel.text = "\(z.columns) × \(z.rows)"
        sizeLabel.sizeToFit()
        sizeLabel.bounds.size.width += 32
        sizeLabel.bounds.size.height += 16
        sizeLabel.layer.cornerRadius = sizeLabel.bounds.height / 2
        sizeLabel.center = CGPoint(x: bounds.midX, y: bounds.midY)
        if sizeLabel.superview == nil { addSubview(sizeLabel) }
        bringSubviewToFront(sizeLabel)
        sizeLabel.alpha = 1
    }
}
