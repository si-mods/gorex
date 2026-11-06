import CoreText
import GhosttyVt
import UIKit

/// Draws a TerminalSession's screen with Core Text and types into it.
/// The soft keyboard comes with a bar of the keys it lacks (Esc, Tab, Ctrl,
/// Alt, arrows), after rootshell's KeyboardAccessoryView; Ctrl and Alt hold
/// for the next key. A vertical drag scrolls back, or turns the wheel of a
/// full-screen program.
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
    private var ctrl = false { didSet { bar.ctrl.isSelected = ctrl } }
    private var alt = false { didSet { bar.alt.isSelected = alt } }
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
        session.send(KeyEvent.typing(text, ctrl: ctrl, alt: alt))
        ctrl = false
        alt = false
    }

    func deleteBackward() {
        session.send([.named("backspace", ctrl: ctrl)])
        ctrl = false
    }

    /// Sends a key of the bar, with its held modifiers.
    func send(_ key: String) {
        var k = KeyEvent.named(key, ctrl: ctrl)
        if alt { k.mods.append("alt") }
        session.send([k])
        ctrl = false
        alt = false
    }

    func toggle(ctrl c: Bool) {
        if c { ctrl.toggle() } else { alt.toggle() }
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
                var e = KeyEvent.named(name)
                if k.modifierFlags.contains(.shift) { e.mods.append("shift") }
                if k.modifierFlags.contains(.control) { e.mods.append("ctrl") }
                if k.modifierFlags.contains(.alternate) { e.mods.append("alt") }
                session.send([e])
            } else if k.modifierFlags.contains(.control), !k.charactersIgnoringModifiers.isEmpty {
                session.send(KeyEvent.typing(k.charactersIgnoringModifiers, ctrl: true))
            } else {
                rest.insert(p)
            }
        }
        if !rest.isEmpty { super.pressesBegan(rest, with: event) }
    }
}

/// The bar over the soft keyboard: a capsule of Liquid Glass, dark as the
/// keyboard is, that scrolls its keys.
final class KeyBar: UIView {
    let ctrl = KeyBar.button("ctrl")
    let alt = KeyBar.button("alt")

    init(target: TerminalView) {
        super.init(frame: CGRect(x: 0, y: 0, width: 0, height: 56))
        autoresizingMask = .flexibleHeight
        overrideUserInterfaceStyle = .dark
        func key(_ title: String, _ name: String) -> UIButton {
            let b = Self.button(title)
            b.addAction(UIAction { [weak target] _ in target?.send(name) }, for: .touchUpInside)
            return b
        }
        func text(_ t: String) -> UIButton {
            let b = Self.button(t)
            b.addAction(UIAction { [weak target] _ in target?.insertText(t) }, for: .touchUpInside)
            return b
        }
        ctrl.addAction(UIAction { [weak target] _ in target?.toggle(ctrl: true) }, for: .touchUpInside)
        alt.addAction(UIAction { [weak target] _ in target?.toggle(ctrl: false) }, for: .touchUpInside)
        var buttons = [
            key("esc", "escape"), ctrl, alt, key("tab", "tab"),
            key("←", "arrow_left"), key("↓", "arrow_down"), key("↑", "arrow_up"), key("→", "arrow_right"),
            text("~"), text("|"), text("/"), text("-"),
            key("home", "home"), key("end", "end"), key("pgup", "page_up"), key("pgdn", "page_down"),
        ]
        let paste = Self.button(nil, symbol: "doc.on.clipboard")
        paste.accessibilityLabel = String(localized: "Paste")
        paste.addAction(UIAction { [weak target] _ in target?.paste(nil) }, for: .touchUpInside)
        let hide = Self.button(nil, symbol: "keyboard.chevron.compact.down")
        hide.accessibilityLabel = String(localized: "Hide Keyboard")
        hide.addAction(UIAction { [weak target] _ in target?.resignFirstResponder() }, for: .touchUpInside)
        buttons += [paste, hide]

        let stack = UIStackView(arrangedSubviews: buttons)
        stack.spacing = 6
        let scroll = UIScrollView()
        scroll.showsHorizontalScrollIndicator = false
        scroll.contentInset = UIEdgeInsets(top: 0, left: 6, bottom: 0, right: 6)
        scroll.addSubview(stack)
        let glass = UIVisualEffectView(effect: UIGlassEffect(style: .regular))
        glass.cornerConfiguration = .capsule()
        glass.clipsToBounds = true
        glass.contentView.addSubview(scroll)
        addSubview(glass)
        for v in [stack, scroll, glass] as [UIView] { v.translatesAutoresizingMaskIntoConstraints = false }
        NSLayoutConstraint.activate([
            glass.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 8),
            glass.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -8),
            glass.topAnchor.constraint(equalTo: topAnchor, constant: 4),
            glass.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -4),
            scroll.leadingAnchor.constraint(equalTo: glass.contentView.leadingAnchor),
            scroll.trailingAnchor.constraint(equalTo: glass.contentView.trailingAnchor),
            scroll.topAnchor.constraint(equalTo: glass.contentView.topAnchor),
            scroll.bottomAnchor.constraint(equalTo: glass.contentView.bottomAnchor),
            stack.leadingAnchor.constraint(equalTo: scroll.contentLayoutGuide.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: scroll.contentLayoutGuide.trailingAnchor),
            stack.topAnchor.constraint(equalTo: scroll.contentLayoutGuide.topAnchor, constant: 6),
            stack.bottomAnchor.constraint(equalTo: scroll.contentLayoutGuide.bottomAnchor, constant: -6),
            stack.heightAnchor.constraint(equalTo: scroll.frameLayoutGuide.heightAnchor, constant: -12),
        ])
    }

    required init?(coder: NSCoder) { fatalError() }

    override var intrinsicContentSize: CGSize { CGSize(width: UIView.noIntrinsicMetric, height: 56) }

    private static func button(_ title: String?, symbol: String? = nil) -> UIButton {
        let b = UIButton(configuration: config(title, symbol, held: false))
        b.changesSelectionAsPrimaryAction = false
        b.setContentHuggingPriority(.required, for: .horizontal)
        b.setContentCompressionResistancePriority(.required, for: .horizontal)
        // A held Ctrl or Alt is prominent glass, in the accent: white here.
        b.configurationUpdateHandler = { b in b.configuration = config(title, symbol, held: b.isSelected) }
        return b
    }

    private static func config(_ title: String?, _ symbol: String?, held: Bool) -> UIButton.Configuration {
        var c = held ? UIButton.Configuration.prominentGlass() : .glass()
        c.title = title
        c.image = symbol.map { UIImage(systemName: $0) } ?? nil
        c.baseForegroundColor = held ? .black : .label
        c.contentInsets = NSDirectionalEdgeInsets(top: 4, leading: 12, bottom: 4, trailing: 12)
        c.titleTextAttributesTransformer = UIConfigurationTextAttributesTransformer {
            var a = $0
            a.font = .monospacedSystemFont(ofSize: 15, weight: .medium)
            return a
        }
        return c
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
