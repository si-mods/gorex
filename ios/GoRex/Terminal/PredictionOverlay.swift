import UIKit

/// Draws the guesses over the surface, underlined: a guess is never
/// written into the terminal, so a wrong one corrupts nothing.
final class PredictionOverlay: UIView {
    private weak var terminal: TerminalView?

    init(in terminal: TerminalView) {
        self.terminal = terminal
        super.init(frame: .zero)
        isUserInteractionEnabled = false
        isOpaque = false
        autoresizingMask = [.flexibleWidth, .flexibleHeight]
    }

    required init?(coder: NSCoder) { fatalError("not used") }

    /// Draws each guess at its cell: the glyph underlined, or a blank in
    /// the background's color where a Delete took a character.
    override func draw(_ rect: CGRect) {
        guard let t = terminal, !t.predictor.open.isEmpty else { return }
        let c = t.cell, inset = t.inset
        guard c.width > 0, c.height > 0 else { return }
        let font = UIFont.monospacedSystemFont(ofSize: TerminalView.fontSize, weight: .regular)
        let attrs: [NSAttributedString.Key: Any] = [.font: font,
                                                  .foregroundColor: UIColor.label,
                                                  .underlineStyle: NSUnderlineStyle.single.rawValue]
        for g in t.predictor.open {
            let r = CGRect(x: CGFloat(g.col) * c.width, y: inset + CGFloat(g.row) * c.height, width: c.width, height: c.height)
            switch g.cell {
            case .glyph(let w):
                String(w).draw(at: CGPoint(x: r.minX, y: r.minY + max(0, (c.height - font.lineHeight) / 2)), withAttributes: attrs)
            case .erase:
                (t.backgroundColor ?? .black).setFill()
                UIRectFill(r)
            }
        }
    }
}
