import CoreText
import GhosttyVt
import UIKit

/// Draws a TerminalSession's screen with Core Text and types into it.
/// The soft keyboard comes with a bar of the keys it lacks (Esc, Tab, Ctrl,
/// Alt, Shift, arrows), after rootshell's KeyboardAccessoryView; the
/// modifiers hold for the next key. A vertical drag scrolls back, or turns
/// the wheel of a full-screen program.
final class TerminalView: UIView, UIKeyInput {
    let session: TerminalSession
    /// The font size chosen in Settings.
    static var fontSize: CGFloat {
        let v = UserDefaults.standard.double(forKey: "fontSize")
        return v > 0 ? v : 12
    }
    private let font = UIFont.monospacedSystemFont(ofSize: TerminalView.fontSize, weight: .regular)
    private lazy var fonts: [CTFont] = [UIFont.Weight.regular, .bold].flatMap { w -> [CTFont] in
        let f = UIFont.monospacedSystemFont(ofSize: font.pointSize, weight: w)
        let italic = f.fontDescriptor.withSymbolicTraits(.traitItalic).map { UIFont(descriptor: $0, size: 0) } ?? f
        return [f as CTFont, italic as CTFont]
    }
    let cell: CGSize
    private var lines: [LineKey: CTLine] = [:]
    var ctrl = false { didSet { bar.ctrl.isSelected = ctrl } }
    var alt = false { didSet { bar.alt.isSelected = alt } }
    var shift = false { didSet { bar.shift.isSelected = shift } }
    private var dragRows: CGFloat = 0
    private lazy var bar = KeyBar(target: self)

    init(session: TerminalSession) {
        self.session = session
        let scale = UIScreen.main.scale
        let advance = ("M" as NSString).size(withAttributes: [.font: font]).width
        cell = CGSize(width: (advance * scale).rounded() / scale, height: (font.lineHeight * scale).rounded(.up) / scale)
        super.init(frame: .zero)
        isOpaque = true
        contentMode = .redraw
        backgroundColor = UIColor(cgColor: (session.background ?? .black).cgColor)
        addGestureRecognizer(UITapGestureRecognizer(target: self, action: #selector(tap)))
        addGestureRecognizer(UIPanGestureRecognizer(target: self, action: #selector(drag)))
        isAccessibilityElement = true
        accessibilityIdentifier = "terminal"
    }

    required init?(coder: NSCoder) { fatalError() }

    // MARK: Size

    override func layoutSubviews() {
        super.layoutSubviews()
        let scale = window?.screen.scale ?? UIScreen.main.scale
        session.fit(cols: Int(bounds.width / cell.width), rows: Int(bounds.height / cell.height),
                    cellWidth: Int(cell.width * scale), cellHeight: Int(cell.height * scale))
    }

    // MARK: Drawing

    private struct LineKey: Hashable {
        var text: String
        var fg: Int
        var font: Int
    }

    override func draw(_ rect: CGRect) {
        guard let ctx = UIGraphicsGetCurrentContext() else { return }
        guard let t = session.terminal else {
            ctx.setFillColor((session.background ?? .black).cgColor)
            ctx.fill(bounds)
            return
        }
        var rows: [(Int, [VTerminal.Cell])] = []
        let s = t.draw { rows.append(($0, $1)) }
        ctx.setFillColor(s.bg.cgColor)
        ctx.fill(bounds)
        let ascent = CTFontGetAscent(fonts[0])
        ctx.textMatrix = CGAffineTransform(scaleX: 1, y: -1)
        if lines.count > 4096 { lines = [:] }
        for (y, cells) in rows {
            let top = CGFloat(y) * cell.height
            for c in cells {
                let x = CGFloat(c.x) * cell.width
                if let bg = c.bg {
                    ctx.setFillColor(bg.cgColor)
                    ctx.fill(CGRect(x: x, y: top, width: cell.width, height: cell.height))
                }
                if c.text.isEmpty || c.text == " " { continue }
                let fg = c.faint ? c.fg.mixed(with: s.bg) : c.fg
                let key = LineKey(text: c.text, fg: fg.packed, font: (c.bold ? 2 : 0) + (c.italic ? 1 : 0))
                let line = lines[key] ?? {
                    let a = NSAttributedString(string: c.text, attributes: [
                        .font: fonts[key.font], .foregroundColor: fg.cgColor,
                    ])
                    let l = CTLineCreateWithAttributedString(a)
                    lines[key] = l
                    return l
                }()
                ctx.textPosition = CGPoint(x: x, y: top + ascent)
                CTLineDraw(line, ctx)
                if c.underline || c.strike {
                    ctx.setFillColor(fg.cgColor)
                    let ly = c.underline ? top + ascent + 1.5 : top + cell.height / 2
                    ctx.fill(CGRect(x: x, y: ly, width: cell.width, height: 1))
                }
            }
        }
        if let cur = s.cursor {
            let r = CGRect(x: CGFloat(cur.x) * cell.width, y: CGFloat(cur.y) * cell.height, width: cell.width, height: cell.height)
            ctx.setFillColor((s.cursorColor ?? s.fg).cgColor.copy(alpha: 0.7)!)
            switch cur.style {
            case GHOSTTY_RENDER_STATE_CURSOR_VISUAL_STYLE_BAR: ctx.fill(r.divided(atDistance: 2, from: .minXEdge).slice)
            case GHOSTTY_RENDER_STATE_CURSOR_VISUAL_STYLE_UNDERLINE: ctx.fill(r.divided(atDistance: 2, from: .maxYEdge).slice)
            case GHOSTTY_RENDER_STATE_CURSOR_VISUAL_STYLE_BLOCK_HOLLOW:
                ctx.setStrokeColor((s.cursorColor ?? s.fg).cgColor)
                ctx.stroke(r.insetBy(dx: 0.5, dy: 0.5))
            default: ctx.fill(r)
            }
        }
        backgroundColor = UIColor(cgColor: s.bg.cgColor)
        session.drew(background: s.bg)
    }

    // MARK: Touch

    @objc private func tap() {
        if !isFirstResponder { becomeFirstResponder() }
    }

    @objc private func drag(_ g: UIPanGestureRecognizer) {
        if g.state == .began { dragRows = 0 }
        dragRows += g.translation(in: self).y / cell.height
        g.setTranslation(.zero, in: self)
        let n = Int(dragRows)
        guard n != 0, let t = session.terminal else { return }
        dragRows -= CGFloat(n)
        if t.wantsWheel {
            let p = g.location(in: self)
            for _ in 0..<abs(n) {
                session.wheel(up: n > 0, col: Int(p.x / cell.width), row: Int(p.y / cell.height))
            }
        } else {
            session.scrollLocal(-n)
        }
    }

    // MARK: Keyboard

    override var canBecomeFirstResponder: Bool { true }
    override var inputAccessoryView: UIView? { bar }

    var hasText: Bool { true }
    var autocorrectionType: UITextAutocorrectionType = .no
    var autocapitalizationType: UITextAutocapitalizationType = .none
    var spellCheckingType: UITextSpellCheckingType = .no
    var smartQuotesType: UITextSmartQuotesType = .no
    var smartDashesType: UITextSmartDashesType = .no
    var smartInsertDeleteType: UITextSmartInsertDeleteType = .no
    var inlinePredictionType: UITextInlinePredictionType = .no
    var keyboardType: UIKeyboardType = .asciiCapable
    var keyboardAppearance: UIKeyboardAppearance = .dark

    func insertText(_ text: String) {
        session.send(KeyEvent.typing(text, ctrl: ctrl, alt: alt, shift: shift))
        release()
    }

    func deleteBackward() { send("backspace") }

    /// Sends a key of the bar, with its held modifiers.
    func send(_ key: String) {
        session.send([.named(key, shift: shift, ctrl: ctrl, alt: alt)])
        release()
    }

    private func release() {
        ctrl = false
        alt = false
        shift = false
    }

    override func canPerformAction(_ action: Selector, withSender sender: Any?) -> Bool {
        action == #selector(paste(_:)) ? UIPasteboard.general.hasStrings : super.canPerformAction(action, withSender: sender)
    }

    override func paste(_ sender: Any?) {
        if let s = UIPasteboard.general.string { session.paste(s) }
    }

    /// Hardware keys that type no text, and Ctrl chords; the rest arrive as
    /// insertText.
    override func pressesBegan(_ presses: Set<UIPress>, with event: UIPressesEvent?) {
        var rest = Set<UIPress>()
        for p in presses {
            guard let k = p.key else { rest.insert(p); continue }
            let names: [UIKeyboardHIDUsage: String] = [
                .keyboardEscape: "escape", .keyboardUpArrow: "arrow_up", .keyboardDownArrow: "arrow_down",
                .keyboardLeftArrow: "arrow_left", .keyboardRightArrow: "arrow_right", .keyboardHome: "home",
                .keyboardEnd: "end", .keyboardPageUp: "page_up", .keyboardPageDown: "page_down",
                .keyboardDeleteForward: "delete",
            ]
            if let name = names[k.keyCode] {
                let f = k.modifierFlags
                session.send([.named(name, shift: f.contains(.shift), ctrl: f.contains(.control), alt: f.contains(.alternate))])
            } else if k.modifierFlags.contains(.control), !k.charactersIgnoringModifiers.isEmpty {
                session.send(KeyEvent.typing(k.charactersIgnoringModifiers, ctrl: true))
            } else {
                rest.insert(p)
            }
        }
        if !rest.isEmpty { super.pressesBegan(rest, with: event) }
    }
}

/// The bar over the soft keyboard: three capsules of Liquid Glass, dark as
/// the keyboard is. ⌘ opens a menu of the rarer keys; the middle one pages
/// from Esc, the modifiers and Tab to the punctuation a shell wants and
/// Backspace; the last holds a stick that sends arrows the way it is
/// dragged, and the key that hides the keyboard.
final class KeyBar: UIView {
    let ctrl = KeyBar.button("control", String(localized: "Control"))
    let alt = KeyBar.button("option", String(localized: "Option"))
    let shift = KeyBar.button("shift", String(localized: "Shift"))
    private weak var target: TerminalView?
    /// Repeats the arrow the stick points to.
    private var repeating: Task<Void, Never>?
    private var arrow: String? { didSet { pad.arrow = arrow } }
    /// What the stick turns into while held.
    private let pad = StickPad()
    private let tick = UIImpactFeedbackGenerator(style: .light)

    init(target: TerminalView) {
        self.target = target
        super.init(frame: CGRect(x: 0, y: 0, width: 0, height: 52))
        autoresizingMask = .flexibleHeight
        overrideUserInterfaceStyle = .dark
        func key(_ face: String, _ label: String?, _ name: String) -> UIButton {
            let b = Self.button(face, label)
            b.addAction(UIAction { [weak target] _ in target?.send(name) }, for: .touchUpInside)
            return b
        }
        func text(_ t: String) -> UIButton {
            let b = Self.button(t)
            b.addAction(UIAction { [weak target] _ in target?.insertText(t) }, for: .touchUpInside)
            return b
        }
        ctrl.addAction(UIAction { [weak target] _ in target?.ctrl.toggle() }, for: .touchUpInside)
        alt.addAction(UIAction { [weak target] _ in target?.alt.toggle() }, for: .touchUpInside)
        shift.addAction(UIAction { [weak target] _ in target?.shift.toggle() }, for: .touchUpInside)

        let more = Self.button("command", String(localized: "More Keys"))
        func item(_ title: String, _ name: String) -> UIAction {
            UIAction(title: title) { [weak target] _ in target?.send(name) }
        }
        more.menu = UIMenu(children: [
            item(String(localized: "Esc"), "escape"),
            UIAction(title: String(localized: "Paste"), image: UIImage(systemName: "doc.on.clipboard")) { [weak target] _ in
                target?.paste(nil)
            },
            UIMenu(options: .displayInline, children: [
                item(String(localized: "Home"), "home"), item(String(localized: "End"), "end"),
                item(String(localized: "Page Up"), "page_up"), item(String(localized: "Page Down"), "page_down"),
            ]),
        ])
        more.showsMenuAsPrimaryAction = true

        // Two pages of five keys in even slots, each page as wide as the capsule.
        let pages = UIStackView(arrangedSubviews: [
            [key("escape", String(localized: "Esc"), "escape"), ctrl, alt, shift,
             key("arrow.right.to.line", String(localized: "Tab"), "tab")],
            [text("~"), text("|"), text("/"), text("-"), key("delete.left", String(localized: "Delete"), "backspace")],
        ].map { keys in
            let page = UIStackView(arrangedSubviews: keys)
            page.distribution = .fillEqually
            return page
        })
        let scroll = UIScrollView()
        scroll.isPagingEnabled = true
        scroll.showsHorizontalScrollIndicator = false
        scroll.addSubview(pages)
        pages.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            pages.leadingAnchor.constraint(equalTo: scroll.contentLayoutGuide.leadingAnchor),
            pages.trailingAnchor.constraint(equalTo: scroll.contentLayoutGuide.trailingAnchor),
            pages.topAnchor.constraint(equalTo: scroll.contentLayoutGuide.topAnchor),
            pages.bottomAnchor.constraint(equalTo: scroll.contentLayoutGuide.bottomAnchor),
            pages.heightAnchor.constraint(equalTo: scroll.frameLayoutGuide.heightAnchor),
        ] + pages.arrangedSubviews.map { $0.widthAnchor.constraint(equalTo: scroll.frameLayoutGuide.widthAnchor) })

        let stick = Self.button("stick", String(localized: "Arrow Keys"))
        let drag = UILongPressGestureRecognizer(target: self, action: #selector(steer))
        drag.minimumPressDuration = 0
        drag.allowableMovement = .greatestFiniteMagnitude
        stick.addGestureRecognizer(drag)
        let hide = Self.button("keyboard.chevron.compact.down", String(localized: "Hide Keyboard"))
        hide.addAction(UIAction { [weak target] _ in target?.resignFirstResponder() }, for: .touchUpInside)

        let middle = Self.capsule(scroll)
        // The middle capsule takes the room the others leave.
        middle.setContentHuggingPriority(.defaultLow, for: .horizontal)
        let right = UIStackView(arrangedSubviews: [stick, hide])
        right.distribution = .fillEqually
        let left = Self.capsule(more), last = Self.capsule(right)
        // As in the bar this one copies: ⌘ 56pt wide, the stick and Hide 100.
        left.widthAnchor.constraint(equalToConstant: 56).isActive = true
        last.widthAnchor.constraint(equalToConstant: 100).isActive = true
        let row = UIStackView(arrangedSubviews: [left, middle, last])
        row.spacing = 8
        row.translatesAutoresizingMaskIntoConstraints = false
        addSubview(row)
        addSubview(pad)
        NSLayoutConstraint.activate([
            row.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 16),
            row.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -16),
            row.topAnchor.constraint(equalTo: topAnchor, constant: 4),
            row.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -4),
        ])
    }

    required init?(coder: NSCoder) { fatalError() }

    override var intrinsicContentSize: CGSize { CGSize(width: UIView.noIntrinsicMetric, height: 52) }

    /// Points the stick: past a dead zone, the arrow of the drag's main
    /// axis goes at once, then repeats while held, as a held key does. Each
    /// arrow taps the hand: firmly as the stick turns, lightly as it repeats.
    @objc private func steer(_ g: UILongPressGestureRecognizer) {
        var next: String?
        if g.state == .began, let v = g.view {
            tick.prepare()
            pad.center = v.convert(CGPoint(x: v.bounds.midX, y: v.bounds.midY), to: self)
            pad.transform = CGAffineTransform(scaleX: 0.5, y: 0.5)
            pad.isHidden = false
            UIView.animate(withDuration: 0.18, delay: 0, usingSpringWithDamping: 0.75, initialSpringVelocity: 0) { self.pad.transform = .identity }
        }
        if g.state == .ended || g.state == .cancelled || g.state == .failed { pad.isHidden = true }
        if g.state == .began || g.state == .changed, let v = g.view {
            let p = g.location(in: v)
            let dx = p.x - v.bounds.midX, dy = p.y - v.bounds.midY
            if max(abs(dx), abs(dy)) > 12 {
                next = abs(dx) > abs(dy) ? (dx > 0 ? "arrow_right" : "arrow_left") : (dy > 0 ? "arrow_down" : "arrow_up")
            }
        }
        guard next != arrow else { return }
        arrow = next
        repeating?.cancel()
        guard let next else { return }
        repeating = Task { [weak target, tick] in
            var delay = 0.4
            repeat {
                target?.send(next)
                tick.impactOccurred(intensity: delay == 0.4 ? 1 : 0.5)
                try? await Task.sleep(for: .seconds(delay))
                delay = 0.08
            } while !Task.isCancelled
        }
    }

    /// The stick, which no SF Symbol draws: a ring with four arrowheads
    /// inside it and a dot at its centre.
    private static let stick = UIGraphicsImageRenderer(size: CGSize(width: 24, height: 24)).image { _ in
        let c = CGPoint(x: 12, y: 12)
        let ring = UIBezierPath(arcCenter: c, radius: 10.6, startAngle: 0, endAngle: 2 * .pi, clockwise: true)
        ring.lineWidth = 1.4
        ring.stroke()
        UIBezierPath(arcCenter: c, radius: 4.2, startAngle: 0, endAngle: 2 * .pi, clockwise: true).fill()
        for i in 0..<4 {
            let head = UIBezierPath()
            head.move(to: CGPoint(x: 0, y: -8.7))
            head.addLine(to: CGPoint(x: 2.4, y: -6.3))
            head.addLine(to: CGPoint(x: -2.4, y: -6.3))
            head.close()
            head.apply(CGAffineTransform(rotationAngle: CGFloat(i) * .pi / 2).concatenating(CGAffineTransform(translationX: c.x, y: c.y)))
            head.fill()
        }
    }.withRenderingMode(.alwaysTemplate)

    /// A capsule of glass around a view.
    private static func capsule(_ content: UIView) -> UIView {
        let glass = UIVisualEffectView(effect: UIGlassEffect(style: .regular))
        glass.cornerConfiguration = .capsule()
        glass.clipsToBounds = true
        glass.contentView.addSubview(content)
        content.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            content.leadingAnchor.constraint(equalTo: glass.contentView.leadingAnchor),
            content.trailingAnchor.constraint(equalTo: glass.contentView.trailingAnchor),
            content.topAnchor.constraint(equalTo: glass.contentView.topAnchor, constant: 4),
            content.bottomAnchor.constraint(equalTo: glass.contentView.bottomAnchor, constant: -4),
        ])
        return glass
    }

    /// A key that shows an SF Symbol, read out by its label; without a
    /// label, the key shows its face as text.
    private static func button(_ face: String, _ label: String? = nil) -> UIButton {
        let b = UIButton(configuration: config(face, text: label == nil, held: false))
        b.accessibilityLabel = label
        b.changesSelectionAsPrimaryAction = false
                // A held modifier is prominent glass, in the accent: white here.
        b.configurationUpdateHandler = { b in b.configuration = config(face, text: label == nil, held: b.isSelected) }
        return b
    }

    private static func config(_ face: String, text: Bool, held: Bool) -> UIButton.Configuration {
        var c = held ? UIButton.Configuration.prominentGlass() : .plain()
        if text {
            c.title = face
            c.titleTextAttributesTransformer = UIConfigurationTextAttributesTransformer {
                var a = $0
                a.font = .systemFont(ofSize: 19, weight: .light)
                return a
            }
        } else {
            c.image = face == "stick" ? stick : UIImage(systemName: face)
            c.preferredSymbolConfigurationForImage = UIImage.SymbolConfiguration(pointSize: 17, weight: .regular)
        }
        c.baseForegroundColor = held ? .black : .label
        c.cornerStyle = .capsule
        c.contentInsets = NSDirectionalEdgeInsets(top: 4, leading: 0, bottom: 4, trailing: 0)
        return c
    }
}

/// The stick while held: a disc over its capsule, the quarter it points to
/// lit, and a knob at its centre.
final class StickPad: UIView {
    var arrow: String? { didSet { setNeedsDisplay() } }

    init() {
        super.init(frame: CGRect(x: 0, y: 0, width: 56, height: 56))
        isOpaque = false
        isHidden = true
        isUserInteractionEnabled = false
    }

    required init?(coder: NSCoder) { fatalError() }

    override func draw(_ rect: CGRect) {
        let c = CGPoint(x: bounds.midX, y: bounds.midY), r = bounds.width / 2 - 0.5
        let disc = UIBezierPath(ovalIn: bounds.insetBy(dx: 0.5, dy: 0.5))
        UIColor(white: 0.3, alpha: 1).setFill()
        disc.fill()
        let angles = ["arrow_right": 0.0, "arrow_down": 0.5, "arrow_left": 1.0, "arrow_up": 1.5]
        if let a = arrow.flatMap({ angles[$0] }).map({ CGFloat($0) * .pi }) {
            let wedge = UIBezierPath()
            wedge.move(to: c)
            wedge.addArc(withCenter: c, radius: r, startAngle: a - .pi / 4, endAngle: a + .pi / 4, clockwise: true)
            wedge.close()
            UIColor(red: 0.31, green: 0.45, blue: 0.42, alpha: 1).setFill()
            wedge.fill()
        }
        UIColor(white: 1, alpha: 0.25).setStroke()
        disc.lineWidth = 1
        disc.stroke()
        UIColor(white: 0.13, alpha: 1).setFill()
        UIBezierPath(arcCenter: c, radius: 8, startAngle: 0, endAngle: 2 * .pi, clockwise: true).fill()
    }
}

extension VTerminal.RGB {
    var cgColor: CGColor {
        CGColor(srgbRed: CGFloat(r) / 255, green: CGFloat(g) / 255, blue: CGFloat(b) / 255, alpha: 1)
    }

    static let black = Self(0)

    var packed: Int { Int(r) << 16 | Int(g) << 8 | Int(b) }

    init(_ packed: Int) { self.init(r: UInt8(packed >> 16 & 0xff), g: UInt8(packed >> 8 & 0xff), b: UInt8(packed & 0xff)) }

    /// Halfway to another color, for faint text.
    func mixed(with o: Self) -> Self {
        Self(r: UInt8((Int(r) + Int(o.r)) / 2), g: UInt8((Int(g) + Int(o.g)) / 2), b: UInt8((Int(b) + Int(o.b)) / 2))
    }
}
