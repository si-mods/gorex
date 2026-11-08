import GameController
import GhosttyKit
import UIKit

/// A TerminalSession's screen, drawn by a Ghostty surface with its Metal
/// renderer, after rootshell's TerminalView. The session's bytes go into
/// the surface's terminal; what Ghostty encodes for the keys, the pastes
/// and the wheel comes back out of it, for the session to write to the
/// program. The soft keyboard comes with a bar of the keys it lacks (Esc,
/// Tab, Ctrl, Alt, Shift, arrows), after rootshell's KeyboardAccessoryView;
/// the modifiers hold for the next key. An iPad's keyboard has its own bar
/// of shortcuts, which takes the keys instead. Input methods compose at the
/// cursor (TerminalView+TextInput). A vertical drag scrolls back, or turns a
/// full-screen program's wheel, with UIKit's physics (TerminalView+Scroll);
/// a pinch sizes the font (TerminalView+Zoom). A long press selects
/// (TerminalView+Selection); a tap clears the selection, else clicks for a
/// program that has the mouse, and brings up the keyboard. With a hardware
/// keyboard the bar stays away, and ⌘ keys work the tab and the font
/// (TerminalView+Commands); a pointer's click and drag selects.
final class TerminalView: UIView, TerminalOutput {
    let session: TerminalSession
    /// The font size chosen in Settings.
    static var fontSize: CGFloat {
        let v = UserDefaults.standard.double(forKey: "fontSize")
        return v > 0 ? v : 12
    }
    /// The font family chosen in Settings; "" is Ghostty's own, JetBrains Mono.
    static var fontFamily: String { UserDefaults.standard.string(forKey: "fontFamily") ?? "" }
    var ctrl = false { didSet { bar.ctrl.isSelected = ctrl } }
    var alt = false { didSet { bar.alt.isSelected = alt } }
    var shift = false { didSet { bar.shift.isSelected = shift } }
    lazy var bar = KeyBar(target: self)

    /// What the keyboard believes it edits.
    var document = InputDocument()
    var trackpad = Trackpad()
    weak var inputDelegate: UITextInputDelegate?
    var markedTextStyle: [NSAttributedString.Key: Any]?
    lazy var tokenizer: UITextInputTokenizer = UITextInputStringTokenizer(textInput: self)

    /// The selection's handles, loupe and menu, while one is shown.
    let handles = (start: SelectionHandle(start: true), end: SelectionHandle(start: false))
    var loupe: UITextLoupeSession?
    lazy var editMenu = UIEditMenuInteraction(delegate: self)
    var showsSelection = false
    lazy var longPress = UILongPressGestureRecognizer(target: self, action: #selector(hold))
    /// How far down the status bar and the clear bar reach over the
    /// terminal; whether a slide waits for the output to be drawn, and
    /// whether any was.
    var inset: CGFloat = 0 { didSet { if inset != oldValue { setNeedsLayout() } } }
    var slideWaits = false
    var drawn = false
    /// Opens a new window in the session, and closes this one, for ⌘T and ⌘W.
    var newTab: (() -> Void)?
    var closeTab: (() -> Void)?

    /// The scroll view whose physics the drag has, and where it last was as
    /// the wheel.
    let scroller = UIScrollView()
    var wheel = false
    var last: CGFloat = 0
    /// Set while the content is moved to match Ghostty, not by the finger.
    var syncing = false

    /// The keyboard's height over the terminal.
    private var covered: CGFloat = 0
    /// Whether the mouse is over a link, as Ghostty last told.
    var overLink = false

    /// The grid's size, shown while pinching.
    lazy var sizeLabel: UILabel = {
        let l = UILabel()
        l.font = .monospacedDigitSystemFont(ofSize: 17, weight: .medium)
        l.textColor = .white
        l.textAlignment = .center
        l.backgroundColor = UIColor(white: 0, alpha: 0.6)
        l.clipsToBounds = true
        l.alpha = 0
        l.isUserInteractionEnabled = false
        l.accessibilityIdentifier = "grid-size"
        return l
    }()

    private(set) var surface: ghostty_surface_t?
    private var config: ghostty_config_t?
    /// The lines the config was made of, which a theme message repeats.
    private var configured: String?
    /// The surface's terminal takes the program's output here, in order on
    /// the queue: a write waits while Ghostty catches up.
    private var output: Int32 = -1
    private let writes = DispatchQueue(label: "dev.gorex.ios.terminal.output")
    /// Reads what Ghostty encodes for the program; left once it stopped.
    private var input: DispatchSourceRead?
    private let reading = DispatchGroup()
    /// The hardware key held, and its repeat.
    private var held: (code: UIKeyboardHIDUsage, task: Task<Void, Never>)?

    init(session: TerminalSession) {
        self.session = session
        super.init(frame: .zero)
        backgroundColor = UIColor(cgColor: (session.background ?? .black).cgColor)
        addGestureRecognizer(UITapGestureRecognizer(target: self, action: #selector(tap)))
        addGestureRecognizer(longPress)
        addGestureRecognizer(UIPinchGestureRecognizer(target: self, action: #selector(pinch)))
        let drag = UIPanGestureRecognizer(target: self, action: #selector(self.drag))
        drag.allowedTouchTypes = [UITouch.TouchType.indirectPointer.rawValue as NSNumber]
        addGestureRecognizer(drag)
        for h in [handles.start, handles.end] { h.addGestureRecognizer(UIPanGestureRecognizer(target: self, action: #selector(steer))) }
        addInteraction(editMenu)
        isAccessibilityElement = true
        accessibilityIdentifier = "terminal"
        // The renderer draws nothing in the background, which iOS forbids.
        NotificationCenter.default.addObserver(self, selector: #selector(hide), name: UIApplication.didEnterBackgroundNotification, object: nil)
        NotificationCenter.default.addObserver(self, selector: #selector(show), name: UIApplication.willEnterForegroundNotification, object: nil)
        NotificationCenter.default.addObserver(self, selector: #selector(keyboard), name: UIResponder.keyboardWillChangeFrameNotification, object: nil)
        for name in [Notification.Name.GCKeyboardDidConnect, .GCKeyboardDidDisconnect] {
            NotificationCenter.default.addObserver(self, selector: #selector(reloadInputViews), name: name, object: nil)
        }
        // An iPad's bar of shortcuts has the keys from the start.
        if UIDevice.current.userInterfaceIdiom == .pad { _ = bar }
    }

    required init?(coder: NSCoder) { fatalError() }

    // MARK: Surface

    override func didMoveToWindow() {
        super.didMoveToWindow()
        if window != nil, surface == nil { open() }
    }

    /// Makes the surface, its terminal fed from outside, and shows the
    /// session in it.
    private func open() {
        guard let app = Ghostty.app else { return }
        var c = ghostty_surface_config_new()
        c.platform_tag = GHOSTTY_PLATFORM_IOS
        c.platform = ghostty_platform_u(ios: ghostty_platform_ios_s(uiview: Unmanaged.passUnretained(self).toOpaque()))
        c.userdata = Unmanaged.passUnretained(self).toOpaque()
        c.scale_factor = traitCollection.displayScale
        c.font_size = Float(Self.fontSize)
        c.use_external_io = true
        c.initially_visible = true
        guard let s = ghostty_surface_new(app, &c) else { return }
        surface = s
        Ghostty.register(self, for: s)
        output = ghostty_surface_get_slave_fd(s)
        _ = fcntl(output, F_SETFL, fcntl(output, F_GETFL) & ~O_NONBLOCK)
        read(ghostty_surface_response_read_fd(s))
        session.output = self
        addScroller()
        setNeedsLayout()
    }

    /// Passes what Ghostty encodes to the session, as it comes.
    private func read(_ fd: Int32) {
        guard fd >= 0 else { return }
        input = Self.reader(fd, reading) { [weak self] got in
            DispatchQueue.main.async { MainActor.assumeIsolated { self?.session.type(got) } }
        }
    }

    /// Reads fd on a queue of its own, not the main actor's.
    /// The reader is in group until it stops, by close or at the end of fd.
    nonisolated private static func reader(_ fd: Int32, _ group: DispatchGroup, _ deliver: @escaping @Sendable (Data) -> Void) -> DispatchSourceRead {
        _ = fcntl(fd, F_SETFL, fcntl(fd, F_GETFL) | O_NONBLOCK)
        let source = DispatchSource.makeReadSource(fileDescriptor: fd, queue: DispatchQueue(label: "dev.gorex.ios.terminal.input"))
        group.enter()
        source.setCancelHandler { group.leave() }
        source.setEventHandler {
            var buf = [UInt8](repeating: 0, count: 16 << 10)
            var got = Data()
            while true {
                let n = Darwin.read(fd, &buf, buf.count)
                if n > 0 { got.append(buf, count: n); continue }
                if n == 0 { source.cancel() }
                break
            }
            if !got.isEmpty { deliver(got) }
        }
        source.resume()
        return source
    }

    /// Frees the surface, once its last output is written.
    func close() {
        held?.task.cancel()
        guard let s = surface else { return }
        surface = nil
        output = -1
        Ghostty.unregister(s)
        input?.cancel()
        Self.free(s, config, of: self, after: reading, writes)
        config = nil
    }

    /// Frees a surface and its config on the main thread, after its reader
    /// stopped and the writes queued before. The surface holds its view
    /// unretained, which lives until then.
    nonisolated private static func free(_ s: ghostty_surface_t, _ c: ghostty_config_t?, of view: TerminalView, after reading: DispatchGroup, _ writes: DispatchQueue) {
        nonisolated(unsafe) let (surface, config, view) = (s, c, view)
        reading.notify(queue: writes) {
            DispatchQueue.main.async {
                ghostty_surface_free(surface)
                if let config { ghostty_config_free(config) }
                withExtendedLifetime(view) {}
            }
        }
    }

    func write(_ bytes: Data) {
        let fd = output
        guard fd >= 0, !bytes.isEmpty else { return }
        laterSlide()
        writes.async {
            bytes.withUnsafeBytes { p in
                var at = 0
                while at < p.count {
                    let n = Darwin.write(fd, p.baseAddress! + at, p.count - at)
                    if n > 0 { at += n } else if errno != EINTR { return }
                }
            }
        }
    }

    /// Sets the default colors, and the font, through Ghostty's config.
    func setTheme(_ theme: [String: Any]) {
        guard let s = surface else { return }
        func hex(_ c: VTerminal.RGB) -> String { String(format: "#%06x", c.packed) }
        var lines = ["font-size = \(Self.fontSize)"]
        if !Self.fontFamily.isEmpty { lines.append("font-family = \"\(Self.fontFamily)\"") }
        for (key, name) in [("background", "background"), ("foreground", "foreground"), ("cursor", "cursor-color")] {
            if let c = (theme[key] as? String).flatMap(VTerminal.RGB.init(hex:)) { lines.append("\(name) = \(hex(c))") }
        }
        for entry in theme["palette"] as? [String] ?? [] {
            let parts = entry.split(separator: "=", maxSplits: 1)
            if parts.count == 2, let i = Int(parts[0]), (0..<256).contains(i), let c = VTerminal.RGB(hex: String(parts[1])) {
                lines.append("palette = \(i)=\(hex(c))")
            }
        }
        let text = lines.joined(separator: "\n")
        guard text != configured, let c = Ghostty.config(text) else { return }
        configured = text
        ghostty_surface_update_config(s, c)
        if let config { ghostty_config_free(config) }
        config = c
        // The background it came to, Ghostty's own when the theme has none.
        var bg = ghostty_config_color_s()
        if ghostty_config_get(c, &bg, "background", UInt("background".utf8.count)) {
            let rgb = VTerminal.RGB(r: bg.r, g: bg.g, b: bg.b)
            backgroundColor = UIColor(cgColor: rgb.cgColor)
            session.drew(background: rgb)
        }
    }

    @objc private func hide() { if let surface { ghostty_surface_set_occlusion(surface, false) } }
    @objc private func show() { if let surface { ghostty_surface_set_occlusion(surface, true) } }

    // MARK: Size

    override func layoutSubviews() {
        super.layoutSubviews()
        guard let s = surface else { return }
        let scale = traitCollection.displayScale
        // The renderer's layer does not follow the view's size by itself.
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        for l in layer.sublayers ?? [] where !(l.delegate is UIView) {
            l.frame = bounds
            l.contentsScale = scale
        }
        CATransaction.commit()
        ghostty_surface_set_content_scale(s, scale, scale)
        // The grid starts below the bars, on either screen, and the lines
        // above it in the scrollback fill the band under them. The terminal
        // takes the two as one resize: Ghostty's IO thread waits 25 ms for
        // the last.
        ghostty_surface_set_top_inset(s, inset * scale)
        ghostty_surface_set_size(s, UInt32(bounds.width * scale), UInt32(bounds.height * scale))
        syncing = true
        scroller.frame = bounds
        syncing = false
        syncScroll()
        slide(duration: 0)
    }

    /// The keyboard never resizes the grid: a resize makes a program that
    /// draws in the scrollback, as an agent does, draw again, and what the
    /// terminal reflowed meanwhile leaves gaps. The terminal slides instead.
    @objc private func keyboard(_ n: Notification) {
        guard let window, let superview = superview as? TerminalHost, let end = n.userInfo?[UIResponder.keyboardFrameEndUserInfoKey] as? CGRect else { return }
        let duration = n.userInfo?[UIResponder.keyboardAnimationDurationUserInfoKey] as? Double ?? 0.25
        let curve = n.userInfo?[UIResponder.keyboardAnimationCurveUserInfoKey] as? UInt ?? 7
        // Measured on the host, which does not slide.
        covered = max(0, superview.bounds.maxY - window.screen.coordinateSpace.convert(end, to: superview).minY)
        slide(duration: duration, options: UIView.AnimationOptions(rawValue: curve << 16))
    }

    /// Slides the terminal up over the keyboard, no farther than it is tall,
    /// as far as the cursor's row and the lines drawn under it, as an
    /// agent's box and status, need, but not the cursor under the bar.
    func slide(duration: Double, options: UIView.AnimationOptions = .curveEaseOut) {
        guard surface != nil, drawn else { return }
        let c = cursor(width: nil)
        let room = bounds.height - covered - drawnBottom(under: c)
        let y = min(0, max(room, inset - c.minY, -covered))
        scroller.verticalScrollIndicatorInsets = UIEdgeInsets(top: inset - y, left: 0, bottom: covered + y, right: 0)
        guard transform.ty != y else { return }
        UIView.animate(withDuration: duration, delay: 0, options: [options, .beginFromCurrentState]) {
            self.transform = CGAffineTransform(translationX: 0, y: y)
        }
    }

    /// The bottom of the last line of the screen with text under the
    /// cursor's, else the cursor's. Lines count from the cursor's: Ghostty
    /// reads text at points of another origin than the cursor's.
    private func drawnBottom(under c: CGRect) -> CGFloat {
        guard let s = surface else { return c.maxY }
        let z = ghostty_surface_size(s), h = cell.height
        guard z.rows > 0, z.columns > 0, h > 0 else { return c.maxY }
        let at = Int(((c.minY - inset) / h).rounded())
        for row in stride(from: Int(z.rows) - 1, to: at, by: -1) {
            let point = { (x: Int) in ghostty_point_s(tag: GHOSTTY_POINT_ACTIVE, coord: GHOSTTY_POINT_COORD_EXACT, x: UInt32(x), y: UInt32(row)) }
            var t = ghostty_text_s()
            guard ghostty_surface_read_text(s, ghostty_selection_s(top_left: point(0), bottom_right: point(Int(z.columns) - 1), rectangle: false), &t) else { continue }
            defer { ghostty_surface_free_text(s, &t) }
            let text = t.text.map { String(decoding: UnsafeRawBufferPointer(start: $0, count: Int(t.text_len)), as: UTF8.self) } ?? ""
            if !text.allSatisfy(\.isWhitespace) { return c.maxY + CGFloat(row - at) * h }
        }
        return c.maxY
    }

    /// Ghostty settled on a grid for the view's size: the block takes it.
    func resized(cols: Int, rows: Int, width: Int, height: Int) {
        guard cols > 0, rows > 0 else { return }
        session.fit(cols: cols, rows: rows, cellWidth: width / cols, cellHeight: height / rows)
        if sizeLabel.alpha > 0 { showSize() }
        slide(duration: 0)
    }

    // MARK: Touch

    /// A tap, a long press or a pointer's click starts nowhere in the band
    /// under the bars: Ghostty takes a point there for the grid's first row,
    /// though the line drawn there is the scrollback's. On an iPhone the
    /// status bar and the bar take every touch there; any that gets through
    /// does nothing.
    override func gestureRecognizerShouldBegin(_ g: UIGestureRecognizer) -> Bool {
        guard g.view === self, g !== scroller.panGestureRecognizer, !(g is UIPinchGestureRecognizer) else { return true }
        return g.location(in: self).y >= inset
    }

    /// A tap that clears no selection clicks for a program that has the
    /// mouse, else opens the link under it, and keeps the keyboard.
    @objc private func tap(_ g: UITapGestureRecognizer) {
        let p = g.location(in: self)
        if deselect(at: p) { return }
        if let s = surface, ghostty_surface_mouse_captured(s) { click(at: p) } else { openLink(at: p) }
        if !isFirstResponder { becomeFirstResponder() }
    }

    /// A left click at a point, as a mouse makes it, plain unless keys are
    /// held.
    func click(at p: CGPoint, mods: ghostty_input_mods_e = GHOSTTY_MODS_NONE) {
        guard let s = surface else { return }
        ghostty_surface_mouse_pos(s, p.x, p.y, mods)
        for state in [GHOSTTY_MOUSE_PRESS, GHOSTTY_MOUSE_RELEASE] { _ = ghostty_surface_mouse_button(s, state, GHOSTTY_MOUSE_LEFT, mods) }
    }

    /// Clicks a link at a point with Super, with which Ghostty opens links,
    /// if the mouse over it with Super finds one: a click on no link would
    /// move a prompt's cursor. Ghostty looks again only where it has not
    /// looked, and keeps a link lit while the mouse stays on it, so the
    /// mouse leaves the view before and after.
    private func openLink(at p: CGPoint) {
        guard let s = surface else { return }
        overLink = false
        ghostty_surface_mouse_pos(s, -1, -1, GHOSTTY_MODS_NONE)
        ghostty_surface_mouse_pos(s, p.x, p.y, GHOSTTY_MODS_SUPER)
        if overLink { click(at: p, mods: GHOSTTY_MODS_SUPER) }
        ghostty_surface_mouse_pos(s, -1, -1, GHOSTTY_MODS_NONE)
    }

    // MARK: Keyboard

    override var canBecomeFirstResponder: Bool { true }

    /// Brings up the keyboard once the push that shows the view ends: one
    /// that comes up during the push stalls it.
    func focus() {
        let push = sequence(first: self as UIResponder, next: \.next).lazy.compactMap { $0 as? UIViewController }.first?.transitionCoordinator
        guard let push else { becomeFirstResponder(); return }
        push.animate(alongsideTransition: nil) { [weak self] c in
            if !c.isCancelled { self?.becomeFirstResponder() }
        }
    }

    /// The bar is for the soft keyboard, which a hardware one keeps away,
    /// and for a phone's: an iPad's keys are in its bar of shortcuts.
    override var inputAccessoryView: UIView? {
        GCKeyboard.coalesced == nil && UIDevice.current.userInterfaceIdiom != .pad ? bar : nil
    }

    var autocorrectionType: UITextAutocorrectionType = .no
    var autocapitalizationType: UITextAutocapitalizationType = .none
    var spellCheckingType: UITextSpellCheckingType = .no
    var smartQuotesType: UITextSmartQuotesType = .no
    var smartDashesType: UITextSmartDashesType = .no
    var smartInsertDeleteType: UITextSmartInsertDeleteType = .no
    var inlinePredictionType: UITextInlinePredictionType = .no
    var mathExpressionCompletionType: UITextMathExpressionCompletionType = .no
    var writingToolsBehavior: UIWritingToolsBehavior = .none
    var keyboardType: UIKeyboardType = .default
    var keyboardAppearance: UIKeyboardAppearance = .dark
    /// The keyboard comes back in the language it was left in.
    override var textInputContextIdentifier: String? { "terminal" }

    /// Sends a key of the bar, with its held modifiers, which it lets go
    /// unless they hold on, as for the stick's repeats. Backspace is the
    /// keyboard's, which edits the composition first.
    func send(_ key: String, holding: Bool = false) {
        guard key != "backspace" else { return changing { deleteBackward() } }
        forget()
        press([.named(key, shift: shift, ctrl: ctrl, alt: alt)])
        if !holding { release() }
    }

    /// Types a key of the bar that types text, as the keyboard would.
    func type(_ text: String) {
        changing { insertText(text) }
    }

    /// Carries out the document's edits.
    func apply(_ edits: [InputDocument.Edit]) {
        for e in edits {
            switch e {
            case .type(let t):
                press(KeyEvent.typing(t, ctrl: ctrl, alt: alt, shift: shift))
                release()
            case .text(let t):
                // Composed text goes as it is, and lets go of what the bar held.
                press(KeyEvent.typing(t))
                release()
            case .backspace:
                press([.named("backspace", shift: shift, ctrl: ctrl, alt: alt)])
                release()
            case .erase(let n):
                press(Array(repeating: .named("backspace"), count: n))
            case .preedit(let t):
                guard let s = surface else { continue }
                if let t {
                    t.withCString { ghostty_surface_preedit(s, $0, UInt(t.utf8.count)) }
                } else {
                    ghostty_surface_preedit(s, nil, 0)
                }
                ghostty_surface_refresh(s)
            }
        }
    }

    /// The line moved on without the keyboard: what it typed is gone.
    func forget() {
        guard !document.typed.isEmpty else { return }
        changing { document.reset() }
    }

    /// Tells the keyboard of a change it did not make.
    private func changing(_ change: () -> Void) {
        inputDelegate?.textWillChange(self)
        inputDelegate?.selectionWillChange(self)
        change()
        inputDelegate?.selectionDidChange(self)
        inputDelegate?.textDidChange(self)
    }

    func release() {
        ctrl = false
        alt = false
        shift = false
    }

    /// Has Ghostty encode keys, each pressed and let go, for the program's
    /// keyboard modes.
    func press(_ keys: [KeyEvent]) {
        guard let s = surface else { return }
        stopScrolling()
        for k in keys {
            for action in [GHOSTTY_ACTION_PRESS, GHOSTTY_ACTION_RELEASE] {
                k.text.withCString { text in
                    var e = ghostty_input_key_s()
                    e.action = action
                    e.mods = ghostty_input_mods_e(k.mods.rawValue)
                    e.consumed_mods = ghostty_input_mods_e(k.consumed.rawValue)
                    e.keycode = k.code
                    e.text = action == GHOSTTY_ACTION_PRESS && !k.text.isEmpty ? text : nil
                    e.unshifted_codepoint = k.unshifted?.unicodeScalars.first?.value ?? 0
                    _ = ghostty_surface_key(s, e)
                }
            }
        }
    }

    override func canPerformAction(_ action: Selector, withSender sender: Any?) -> Bool {
        switch action {
        case #selector(paste(_:)): UIPasteboard.general.hasStrings
        case #selector(copy(_:)): surface.map(ghostty_surface_has_selection) ?? false
        case #selector(selectAll(_:)): surface != nil
        default: super.canPerformAction(action, withSender: sender)
        }
    }

    override func paste(_ sender: Any?) {
        if let t = UIPasteboard.general.string { insert(t) }
    }

    /// Pastes text as a terminal does: bracketed when the program asked for it.
    func insert(_ t: String) {
        guard let s = surface else { return }
        forget()
        stopScrolling()
        t.withCString { ghostty_surface_text(s, $0, UInt(t.utf8.count)) }
    }

    /// Hardware keys that type no text, Enter, Tab and Backspace with a
    /// modifier, and Ctrl and Alt chords: Ghostty encodes them, Alt as Meta,
    /// and they repeat while held. The rest arrive as insertText and
    /// deleteBackward, as all keys do but Ctrl chords while an input method
    /// composes.
    override func pressesBegan(_ presses: Set<UIPress>, with event: UIPressesEvent?) {
        var rest = Set<UIPress>()
        for p in presses {
            guard let k = p.key, let keys = Self.keys(for: k, composing: document.markedRange != nil) else {
                rest.insert(p)
                continue
            }
            forget()
            pressHeld(keys, for: k.keyCode)
        }
        if !rest.isEmpty { super.pressesBegan(rest, with: event) }
    }

    override func pressesEnded(_ presses: Set<UIPress>, with event: UIPressesEvent?) {
        letGo(presses)
        super.pressesEnded(presses, with: event)
    }

    override func pressesCancelled(_ presses: Set<UIPress>, with event: UIPressesEvent?) {
        letGo(presses)
        super.pressesCancelled(presses, with: event)
    }

    /// The keys Ghostty encodes for a hardware key, or nil for UIKit's text input.
    private static func keys(for k: UIKey, composing: Bool) -> [KeyEvent]? {
        let f = k.modifierFlags
        let shift = f.contains(.shift), ctrl = f.contains(.control), alt = f.contains(.alternate)
        if composing && !ctrl { return nil }
        if let name = names[k.keyCode] { return [.named(name, shift: shift, ctrl: ctrl, alt: alt)] }
        guard shift || ctrl || alt, !f.contains(.command) else { return nil }
        if let name = modified[k.keyCode] { return [.named(name, shift: shift, ctrl: ctrl, alt: alt)] }
        guard ctrl || alt, !k.charactersIgnoringModifiers.isEmpty else { return nil }
        return KeyEvent.typing(k.charactersIgnoringModifiers, ctrl: ctrl, alt: alt, shift: shift)
    }

    /// Presses keys, and again while their hardware key is held, as macOS
    /// repeats at its fastest: after 225 ms, every 30 ms.
    private func pressHeld(_ keys: [KeyEvent], for code: UIKeyboardHIDUsage) {
        press(keys)
        held?.task.cancel()
        held = (code, Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(225))
            while !Task.isCancelled, let v = self {
                v.press(keys)
                try? await Task.sleep(for: .milliseconds(30))
            }
        })
    }

    /// Stops the repeat when its key is let go.
    private func letGo(_ presses: Set<UIPress>) {
        guard let h = held, presses.contains(where: { $0.key?.keyCode == h.code }) else { return }
        h.task.cancel()
        held = nil
    }

    /// Keys that type text, or edit it, unless a modifier is held.
    private static let modified: [UIKeyboardHIDUsage: String] = [
        .keyboardReturnOrEnter: "enter", .keyboardTab: "tab", .keyboardDeleteOrBackspace: "backspace",
    ]

    private static let names: [UIKeyboardHIDUsage: String] = [
        .keyboardEscape: "escape", .keyboardUpArrow: "arrow_up", .keyboardDownArrow: "arrow_down",
        .keyboardLeftArrow: "arrow_left", .keyboardRightArrow: "arrow_right", .keyboardHome: "home",
        .keyboardEnd: "end", .keyboardPageUp: "page_up", .keyboardPageDown: "page_down",
        .keyboardDeleteForward: "delete",
        .keyboardF1: "f1", .keyboardF2: "f2", .keyboardF3: "f3", .keyboardF4: "f4", .keyboardF5: "f5",
        .keyboardF6: "f6", .keyboardF7: "f7", .keyboardF8: "f8", .keyboardF9: "f9", .keyboardF10: "f10",
        .keyboardF11: "f11", .keyboardF12: "f12",
    ]
}

/// Holds a TerminalView for SwiftUI, which sets its views' frames and so
/// would undo the slide: the host sets the terminal's bounds and center,
/// which keep it. The terminal fills it, under the bars, which are its
/// inset: its grid starts below them and the scrollback fills the band. The
/// grid never changes as a program takes the alternate screen, which would
/// resize the main screen at the phone only, behind the session's back, as
/// omp does for a moment each time it redraws for a new size.
final class TerminalHost: UIView {
    let terminal: TerminalView

    init(_ terminal: TerminalView) {
        self.terminal = terminal
        super.init(frame: .zero)
        addSubview(terminal)
    }

    required init?(coder: NSCoder) { fatalError() }

    override func layoutSubviews() {
        super.layoutSubviews()
        terminal.bounds = CGRect(origin: .zero, size: bounds.size)
        terminal.center = CGPoint(x: bounds.midX, y: bounds.midY)
        terminal.inset = safeAreaInsets.top
    }

    override func safeAreaInsetsDidChange() {
        super.safeAreaInsetsDidChange()
        setNeedsLayout()
    }
}

/// The bar over the soft keyboard: three capsules of Liquid Glass, dark as
/// the keyboard is. ⌘ opens a menu of Paste, Add Photo and Add File, and
/// spins while a file goes up; the middle one pages from Esc, the modifiers
/// and Tab to the punctuation a shell wants and Backspace; the last holds a
/// stick that sends arrows the way it is dragged, and the key that hides
/// the keyboard. On an iPad the keys go in the keyboard's bar of shortcuts,
/// which shows with a hardware keyboard too: ⌘, Esc, the modifiers and Tab
/// before its middle, the punctuation and four arrows after it. Its
/// keyboard has keys of its own to hide it and for Backspace.
final class KeyBar: UIView {
    let ctrl = KeyBar.button("control", String(localized: "Control"), size: KeyBar.small)
    let alt = KeyBar.button("option", String(localized: "Option"), size: KeyBar.small)
    let shift = KeyBar.button("shift", String(localized: "Shift"), size: KeyBar.small)
    private weak var target: TerminalView?
    private let more = KeyBar.button("command", String(localized: "More"))
    /// The files going up; ⌘ spins while there are any.
    var uploading = 0 { didSet { more.setNeedsUpdateConfiguration() } }
    /// Repeats the arrow the stick points to, with the modifiers held
    /// until the stick is let go.
    private var repeating: Task<Void, Never>?
    /// Repeats Backspace, or an iPad's arrow, while it is held.
    private var pressing: Task<Void, Never>?
    private var steered = false
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
            let b = Self.button(face, label, size: Self.small)
            b.addAction(UIAction { [weak target] _ in target?.send(name) }, for: .touchUpInside)
            return b
        }
        func text(_ t: String) -> UIButton {
            let b = Self.button(t, size: Self.small)
            b.addAction(UIAction { [weak target] _ in target?.type(t) }, for: .touchUpInside)
            return b
        }
        // Backspace and an iPad's arrows go on touch down and repeat while
        // held, as the keyboard's Backspace does: after 400 ms, every 50 ms.
        // The modifiers hold until the key is let go.
        func repeating(_ face: String, _ label: String, _ name: String) -> UIButton {
            let b = Self.button(face, label, size: Self.small)
            b.addAction(UIAction { [weak self, weak target, tick] _ in
                self?.pressing?.cancel()
                self?.pressing = Task {
                    var delay = 0.4
                    repeat {
                        target?.send(name, holding: true)
                        tick.impactOccurred(intensity: 0.5)
                        try? await Task.sleep(for: .seconds(delay))
                        delay = 0.05
                    } while !Task.isCancelled
                }
            }, for: .touchDown)
            b.addAction(UIAction { [weak self, weak target] _ in
                self?.pressing?.cancel()
                target?.release()
            }, for: [.touchUpInside, .touchUpOutside, .touchCancel])
            return b
        }
        ctrl.addAction(UIAction { [weak target] _ in target?.ctrl.toggle() }, for: .touchUpInside)
        alt.addAction(UIAction { [weak target] _ in target?.alt.toggle() }, for: .touchUpInside)
        shift.addAction(UIAction { [weak target] _ in target?.shift.toggle() }, for: .touchUpInside)

        // Add Photo and Add File are for a server that takes uploads, as
        // the menu opens.
        more.menu = UIMenu(children: [
            UIAction(title: String(localized: "Paste"), image: UIImage(systemName: "doc.on.clipboard")) { [weak target] _ in target?.paste(nil) },
            UIDeferredMenuElement.uncached { [weak target] done in
                let off: UIMenuElement.Attributes = target?.session.host.canUpload == true ? [] : .disabled
                done([
                    UIAction(title: String(localized: "Add Photo"), image: UIImage(systemName: "photo"), attributes: off) { _ in target?.addPhoto() },
                    UIAction(title: String(localized: "Add File"), image: UIImage(systemName: "folder"), attributes: off) { _ in target?.addFile() },
                ])
            },
        ])
        more.showsMenuAsPrimaryAction = true
        more.configurationUpdateHandler = { [weak self] b in
            b.configuration = Self.config("command", text: false, size: 17, held: false)
            b.configuration?.showsActivityIndicator = (self?.uploading ?? 0) > 0
        }

        let first = [key("escape", String(localized: "Esc"), "escape"), ctrl, alt, shift,
                     key("arrow.right.to.line", String(localized: "Tab"), "tab")]
        let second = [text("~"), text("|"), text("/"), text("-")]
        // Every key taps the hand as it goes down, as the keyboard's do.
        // Backspace, the arrows and the stick tap on their own as they repeat.
        for b in [more] + first + second {
            b.addAction(UIAction { [tick] _ in tick.impactOccurred() }, for: .touchDown)
        }

        if UIDevice.current.userInterfaceIdiom == .pad {
            let arrows = [
                repeating("arrow.left", String(localized: "Left Arrow"), "arrow_left"),
                repeating("arrow.up", String(localized: "Up Arrow"), "arrow_up"),
                repeating("arrow.down", String(localized: "Down Arrow"), "arrow_down"),
                repeating("arrow.right", String(localized: "Right Arrow"), "arrow_right"),
            ]
            // Each key as wide as the bar's own, which its face alone is not.
            func group(_ keys: [UIButton]) -> UIBarButtonItemGroup {
                UIBarButtonItemGroup(barButtonItems: keys.map { k in
                    let item = UIBarButtonItem(customView: k)
                    item.width = 36
                    return item
                }, representativeItem: nil)
            }
            target.inputAssistantItem.leadingBarButtonGroups = [group([more] + first)]
            target.inputAssistantItem.trailingBarButtonGroups = [group(second + arrows)]
            return
        }
        let delete = repeating("delete.left", String(localized: "Delete"), "backspace")

        // Two pages of five keys in even slots, inset from the capsule's
        // ends, each page as wide as the capsule.
        let pages = UIStackView(arrangedSubviews: [first, second + [delete]].map { keys in
            let page = UIStackView(arrangedSubviews: keys)
            page.distribution = .fillEqually
            page.isLayoutMarginsRelativeArrangement = true
            page.directionalLayoutMargins = NSDirectionalEdgeInsets(top: 0, leading: 14, bottom: 0, trailing: 14)
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
        hide.addAction(UIAction { [tick] _ in tick.impactOccurred() }, for: .touchDown)

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
        // The width of a phone's bar at most, in the middle of a wider one.
        let fill = row.widthAnchor.constraint(equalTo: widthAnchor, constant: -32)
        fill.priority = .defaultHigh
        NSLayoutConstraint.activate([
            fill,
            row.widthAnchor.constraint(lessThanOrEqualToConstant: 440),
            row.centerXAnchor.constraint(equalTo: centerXAnchor),
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
        if g.state == .ended || g.state == .cancelled || g.state == .failed {
            pad.isHidden = true
            if steered { target?.release() }
            steered = false
        }
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
        steered = true
        repeating = Task { [weak target, tick] in
            var delay = 0.4
            repeat {
                target?.send(next, holding: true)
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
    /// The size of the middle capsule's keys, a little under the others'.
    private static let small: CGFloat = 14

    private static func button(_ face: String, _ label: String? = nil, size: CGFloat = 17) -> UIButton {
        let b = UIButton(configuration: config(face, text: label == nil, size: size, held: false))
        b.accessibilityLabel = label
        b.changesSelectionAsPrimaryAction = false
                // A held modifier is prominent glass, in the accent: white here.
        b.configurationUpdateHandler = { b in b.configuration = config(face, text: label == nil, size: size, held: b.isSelected) }
        return b
    }

    private static func config(_ face: String, text: Bool, size: CGFloat, held: Bool) -> UIButton.Configuration {
        var c = held ? UIButton.Configuration.prominentGlass() : .plain()
        if text {
            c.title = face
            c.titleTextAttributesTransformer = UIConfigurationTextAttributesTransformer {
                var a = $0
                a.font = .systemFont(ofSize: size + 2, weight: .light)
                return a
            }
        } else {
            c.image = face == "stick" ? stick : UIImage(systemName: face)
            c.preferredSymbolConfigurationForImage = UIImage.SymbolConfiguration(pointSize: size, weight: .regular)
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
