import Accelerate
import AVFoundation
import GameController
import GhosttyKit
import UIKit

/// A TerminalSession's screen, drawn by a Ghostty surface with its Metal
/// renderer, after rootshell's TerminalView. The session's bytes go into
/// the surface's terminal; what Ghostty encodes for the keys, the pastes
/// and the wheel comes back out of it, for the session to write to the
/// program. A phone's terminal has a bar of the keys the soft keyboard
/// lacks (Esc, Tab, Ctrl, Alt, Shift, arrows), after rootshell's
/// KeyboardAccessoryView, at the foot of the screen or on the keyboard; the
/// modifiers hold for the next key. An iPad's keyboard has its own bar
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
    static var fontFamily: String { family(UserDefaults.standard.string(forKey: "fontFamily") ?? "") }
    /// A stored family as it is used: Maple Mono, which each device
    /// downloads, falls back to Ghostty's own where it is not installed.
    static func family(_ stored: String) -> String {
        stored == Maple.family && !Maple.shared.installed ? "" : stored
    }
    /// Whether the font's ligatures draw, chosen in Settings: pairs like !=
    /// as one glyph, where the font has the shapes. On where it was never set.
    static var fontLigatures: Bool {
        UserDefaults.standard.object(forKey: "fontLigatures") == nil || UserDefaults.standard.bool(forKey: "fontLigatures")
    }
    /// The least contrast of text with its background chosen in Settings,
    /// as Ghostty's minimum-contrast: 1, unset, corrects nothing. Ghostty
    /// turns text short of it black or white, not a shade of its hue as
    /// the desktop does, so it is off until chosen: on by default, a light
    /// theme's green and cyan would turn black as well.
    static var minimumContrast: Double {
        let v = UserDefaults.standard.double(forKey: "minimumContrast")
        return v > 0 ? min(max(v, 1), 21) : 1
    }
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
    /// terminal; whether a slide waits for the output to be drawn, whether
    /// more came meanwhile, and whether any was drawn.
    var inset: CGFloat = 0 { didSet { if inset != oldValue { setNeedsLayout() } } }
    /// How far the pane bar reaches over the terminal: the cursor and the
    /// lines under it stay above it, as they do above the key bar.
    var paneInset: CGFloat = 0 { didSet { if paneInset != oldValue { setNeedsLayout(); slide(duration: 0) } } }
    var slideWaits = false
    var slideAgain = false
    var drawn = false
    /// How many writes came, and the slide owed once they stop: the output
    /// slides the terminal up as far as it needs, and down only where the
    /// need stays, so no frame in it can set it shaking.
    var wrote = 0
    var settling: DispatchWorkItem?
    /// How many ticks in a row the output asked to come down.
    var wantsDown = 0
    /// The ticks an ask to come down takes to count as what the program means
    /// to draw, rather than a frame passing through.
    static let downTicks = 4
    /// Opens a new window in the session, and closes this one, for ⌘T and ⌘W.
    var newTab: (() -> Void)?
    var closeTab: (() -> Void)?
    /// Splits the pane open, for ⌘D, as the desktop's Split Right.
    var newSplit: (() -> Void)?
    /// Whether the terminal is on the page: one kept open off it draws
    /// nothing, and gives up the keyboard.
    var onPage = true {
        didSet {
            guard onPage != oldValue else { return }
            if !onPage, isFirstResponder { resignFirstResponder() }
            if UIApplication.shared.applicationState != .background { show() }
        }
    }

    /// The scroll view whose physics the drag has, and where it last was as
    /// the wheel.
    let scroller = UIScrollView()
    var wheel = false
    var last: CGFloat = 0
    /// Accumulated upward wheel scroll distance (points) in alternate/mouse mode.
    var wheelOffset: CGFloat = 0
    /// Set while the content is moved to match Ghostty, not by the finger.
    var syncing = false
    /// Whether the viewport is scrolled up from the bottom, and whether new
    /// output arrived while scrolled up.
    var scrolledUp = false { didSet { if scrolledUp != oldValue { onScrollState?(scrolledUp, hasUnreadOutput) } } }
    var hasUnreadOutput = false { didSet { if hasUnreadOutput != oldValue { onScrollState?(scrolledUp, hasUnreadOutput) } } }
    var onScrollState: ((_ scrolledUp: Bool, _ unread: Bool) -> Void)?

    /// What Ghostty's actions owe the view, done on the next frame
    /// (TerminalView+Scroll's payOwed).
    lazy var owed = FrameWork { [weak self] in self?.nextFrame.arm() }
    lazy var nextFrame = NextFrame { [weak self] in self?.payOwed() }
    /// The geometry Ghostty was last given: a layout of the same one gives
    /// it nothing again.
    private var applied: (size: CGSize, scale: CGFloat, top: CGFloat, bottom: CGFloat)?

    /// The keyboard's height over the terminal.
    private var keys: CGFloat = 0
    /// How far the keyboard reaches over the grid, past the band under it:
    /// how far the terminal slides up to keep its cursor clear. The bars
    /// ride the keyboard, and the grid ends above them.
    var covered: CGFloat {
        guard band > 0 else { return keys }
        return max(0, keys - (superview?.safeAreaInsets.bottom ?? 0))
    }

    /// How tall the phone's bar is, and the pane bar over it.
    private var barCover: CGFloat {
        guard let host = superview, bar.superview === host, !bar.isHidden else { return paneInset }
        return bar.intrinsicContentSize.height + paneInset
    }

    /// The band at the bottom the grid stays out of: the home indicator and
    /// the bars over it, so they never cover a row. Ghostty keeps it blank
    /// at rest and, while the scrollback is scrolled back, draws the lines
    /// after the screen into it, which go on under the bars' fade. Without
    /// bars it is nothing, as on an iPad with one pane.
    var band: CGFloat {
        let bars = barCover
        guard bars > 0 else { return 0 }
        return (superview?.safeAreaInsets.bottom ?? 0) + bars
    }
    /// Whether the mouse is over a link, as Ghostty last told.
    var overLink = false
    /// Whether the keyboard was up as the last tap that opened a link
    /// began: a file's preview gives it back as it closes.
    var typingBeforeLink = false

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
    /// The lines the config was made of, which a theme message repeats,
    /// the last applied theme, and the minimum contrast it was made with.
    private var configured: String?
    private var theme: [String: Any] = [:]
    private var contrast: Double?
    /// The surface's terminal takes the program's output here, in order on
    /// the queue: a write waits while Ghostty catches up.
    private var output: Int32 = -1
    private let writes = DispatchQueue(label: "dev.gorex.ios.terminal.output")
    /// Reads what Ghostty encodes for the program; left once it stopped.
    private var input: DispatchSourceRead?
    private let reading = DispatchGroup()
    /// The hardware key held, and its repeat.
    private var held: (code: UIKeyboardHIDUsage, task: Task<Void, Never>)?
    /// The microphone while the mic is on, or starts, what listens to it,
    /// what is told what is said, what tells it, and what shows its level
    /// (dictate).
    private var dictating: Dictating?
    private typealias Dictating = (recording: Recording, listening: Listening, said: (Dictation.Said?) -> Void, told: Task<Void, Never>,
                                   level: Task<Void, Never>)
    /// The dictation being finished (recognising its last phrase or polishing),
    /// so stopDictating can cancel it too.
    private var finishing: (task: Task<Void, Never>, listening: Listening, said: (Dictation.Said?) -> Void)?

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
        NotificationCenter.default.addObserver(self, selector: #selector(contrastChanged), name: .minimumContrast, object: nil)
        NotificationCenter.default.addObserver(self, selector: #selector(fontChanged), name: .terminalFont, object: nil)
        for name in [Notification.Name.GCKeyboardDidConnect, .GCKeyboardDidDisconnect] {
            NotificationCenter.default.addObserver(self, selector: #selector(showBar), name: name, object: nil)
        }
        // An iPad's bar of shortcuts has the keys from the start.
        if UIDevice.current.userInterfaceIdiom == .pad { _ = bar }
    }

    required init?(coder: NSCoder) { fatalError() }

    // MARK: Surface

    override func didMoveToWindow() {
        super.didMoveToWindow()
        if window != nil, surface == nil { open() }
        if window != nil { showBar() }
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
        // A surface is born visible, its display link running: one made
        // while the draw latch is armed starts occluded instead, or it
        // presents while the app is out of the foreground.
        c.initially_visible = onPage && !Ghostty.secureDrawProhibited
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
        input = FDReader.start(fd, reading) { [weak self] got in
            DispatchQueue.main.async { MainActor.assumeIsolated { self?.session.type(got) } }
        }
    }

    /// Frees the surface, once its last output is written.
    func close() {
        held?.task.cancel()
        settling?.cancel()
        stopDictating()
        nextFrame.stop()
        _ = owed.take()
        applied = nil
        guard let s = surface else { return }
        surface = nil
        output = -1
        Ghostty.unregister(s)
        input?.cancel()
        Self.free(s, config, of: self, after: reading, writes)
        config = nil
    }

    /// Frees a surface and its config, after its reader stopped and the
    /// writes queued before, off the main thread, as rootshell does: the
    /// renderer, as it stops, sends a pause of its display link to the main
    /// thread, which a free there runs after the link is gone; a free
    /// elsewhere waits for the main thread to run it first. The surface
    /// holds its view unretained, which lives until then and goes on the
    /// main thread.
    nonisolated private static func free(_ s: ghostty_surface_t, _ c: ghostty_config_t?, of view: TerminalView, after reading: DispatchGroup, _ writes: DispatchQueue) {
        nonisolated(unsafe) let (surface, config, view) = (s, c, Unmanaged.passRetained(view))
        reading.notify(queue: writes) {
            ghostty_surface_free(surface)
            if let config { ghostty_config_free(config) }
            DispatchQueue.main.async { view.release() }
        }
    }

    func write(_ bytes: Data) {
        let fd = output
        guard fd >= 0, !bytes.isEmpty else { return }
        wrote += 1
        if scrolledUp { hasUnreadOutput = true }
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
        self.theme = theme
        func hex(_ c: VTerminal.RGB) -> String { String(format: "#%06x", c.packed) }
        var lines = ["font-size = \(Self.fontSize)"]
        if !Self.fontFamily.isEmpty { lines.append("font-family = \"\(Self.fontFamily)\"") }
        if !Self.fontLigatures {
            for f in ["liga", "clig", "dlig", "hlig", "calt"] { lines.append("font-feature = -\(f)") }
        }
        for (key, name) in [("background", "background"), ("foreground", "foreground"), ("cursor", "cursor-color")] {
            if let c = (theme[key] as? String).flatMap(VTerminal.RGB.init(hex:)) { lines.append("\(name) = \(hex(c))") }
        }
        for entry in theme["palette"] as? [String] ?? [] {
            let parts = entry.split(separator: "=", maxSplits: 1)
            if parts.count == 2, let i = Int(parts[0]), (0..<256).contains(i), let c = VTerminal.RGB(hex: String(parts[1])) {
                lines.append("palette = \(i)=\(hex(c))")
            }
        }
        configure(lines.joined(separator: "\n"))
    }

    /// Makes the config of the lines and the minimum contrast chosen in
    /// Settings current, unless it is already.
    private func configure(_ text: String) {
        guard let s = surface else { return }
        let ratio = Self.minimumContrast
        guard text != configured || ratio != contrast, let c = Ghostty.config(text + "\nminimum-contrast = \(ratio)") else { return }
        (configured, contrast) = (text, ratio)
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

    /// Settings changed the minimum contrast: the same lines again, so the
    /// font stays as it is, with the new contrast. A terminal not yet
    /// configured takes it with its theme.
    @objc private func contrastChanged() {
        if let configured { configure(configured) }
    }

    /// Settings changed the font family, size or ligatures: rebuilds the
    /// Ghostty config.
    @objc private func fontChanged() {
        setTheme(theme)
    }

    @objc private func hide() { if let surface { ghostty_surface_set_occlusion(surface, false) } }
    @objc private func show() { if let surface { ghostty_surface_set_occlusion(surface, onPage) } }

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
        let bottom = band
        if applied.map({ $0.size != bounds.size || $0.scale != scale || $0.top != inset || $0.bottom != bottom }) ?? true {
            applied = (bounds.size, scale, inset, bottom)
            ghostty_surface_set_content_scale(s, scale, scale)
            // The grid starts below the bars, on either screen, and the lines
            // above it in the scrollback fill the band under them. The terminal
            // takes the two as one resize: Ghostty's IO thread waits 25 ms for
            // the last. The grid ends above the band at the bottom, the bars'.
            ghostty_surface_set_bottom_inset(s, bottom * scale)
            ghostty_surface_set_top_inset(s, inset * scale)
            ghostty_surface_set_size(s, UInt32(bounds.width * scale), UInt32(bounds.height * scale))
        }
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
        keys = max(0, superview.bounds.maxY - window.screen.coordinateSpace.convert(end, to: superview).minY)
        bar.keyboard.setNeedsUpdateConfiguration()
        slide(duration: duration, options: UIView.AnimationOptions(rawValue: curve << 16))
    }

    /// Slides the terminal up over the keyboard, no farther than it is tall,
    /// as far as the cursor's row and the lines drawn under it, as an
    /// agent's box and status, need. The grid ends above the bars, which
    /// ride the keyboard: with the keyboard away nothing is over it, and it
    /// rests at 0 and reads nothing.
    func slide(duration: Double, options: UIView.AnimationOptions = .curveEaseOut) {
        guard surface != nil, drawn else { return }
        slide(to: needed(), duration: duration, options: options)
    }

    /// Where the terminal goes: up as far as the cursor's row and the lines
    /// drawn under it need to show above the keyboard, and no farther up than
    /// the keyboard. With nothing over the terminal, it is 0, and the screen
    /// is not read.
    func needed() -> CGFloat {
        let covered = self.covered
        guard surface != nil, covered > 0 else { return 0 }
        let c = cursor(width: nil)
        let room = bounds.height - band - covered - drawnBottom(under: c)
        return min(0, max(room, inset - c.minY, -covered))
    }

    /// Slides the terminal there, the scroll indicator's insets with it, and
    /// leaves it alone if it is there already.
    func slide(to y: CGFloat, duration: Double, options: UIView.AnimationOptions = .curveEaseOut) {
        wantsDown = 0
        scroller.verticalScrollIndicatorInsets = UIEdgeInsets(top: inset - y, left: 0, bottom: band + covered + y, right: 0)
        guard transform.ty != y else { return }
        UIView.animate(withDuration: duration, delay: 0, options: [options, .beginFromCurrentState]) {
            self.transform = CGAffineTransform(translationX: 0, y: y)
        }
    }

    /// The bottom of the last line of the screen with text under the
    /// cursor's, else the cursor's. Lines count from the cursor's: Ghostty
    /// reads text at points of another origin than the cursor's. A few
    /// reads of runs of lines find it (LastDrawn), not one read a line.
    private func drawnBottom(under c: CGRect) -> CGFloat {
        guard let s = surface else { return c.maxY }
        let z = ghostty_surface_size(s), h = cell.height
        guard z.rows > 0, z.columns > 0, h > 0 else { return c.maxY }
        let at = Int(((c.minY - inset) / h).rounded()), last = Int(z.rows) - 1
        guard at < last else { return c.maxY }
        let point = { (x: Int, y: Int) in ghostty_point_s(tag: GHOSTTY_POINT_ACTIVE, coord: GHOSTTY_POINT_COORD_EXACT, x: UInt32(x), y: UInt32(y)) }
        let row = LastDrawn.row(in: max(at + 1, 0)...last) { run in
            var t = ghostty_text_s()
            guard ghostty_surface_read_text(s, ghostty_selection_s(top_left: point(0, run.lowerBound), bottom_right: point(Int(z.columns) - 1, run.upperBound), rectangle: false), &t) else { return false }
            defer { ghostty_surface_free_text(s, &t) }
            guard let p = t.text else { return false }
            return LastDrawn.hasText(UnsafeRawBufferPointer(start: p, count: Int(t.text_len)))
        }
        return row.map { c.maxY + CGFloat($0 - at) * h } ?? c.maxY
    }

    /// The text on the screen, as shown.
    private var screen: String {
        guard let s = surface else { return "" }
        let corner = { (c: ghostty_point_coord_e) in ghostty_point_s(tag: GHOSTTY_POINT_VIEWPORT, coord: c, x: 0, y: 0) }
        var t = ghostty_text_s()
        guard ghostty_surface_read_text(s, ghostty_selection_s(top_left: corner(GHOSTTY_POINT_COORD_TOP_LEFT),
                                                               bottom_right: corner(GHOSTTY_POINT_COORD_BOTTOM_RIGHT), rectangle: false), &t) else { return "" }
        defer { ghostty_surface_free_text(s, &t) }
        return t.text.map { String(decoding: UnsafeRawBufferPointer(start: $0, count: Int(t.text_len)), as: UTF8.self) } ?? ""
    }

    /// Reads the screen's text off the main thread, after the writes queued
    /// before it, which the surface's free waits for: the screen and as
    /// much scrollback above it as the gallery reads, else the screen
    /// alone, as a point in the scrollback is no further than a screen's
    /// rows from its top (Ghostty clamps it).
    func readScreen(_ done: @escaping @Sendable (_ text: String, _ columns: Int) -> Void) {
        guard let s = surface else { return }
        let z = ghostty_surface_size(s), rows = Int(z.rows), columns = Int(z.columns)
        guard rows > 0, columns > 0 else { return }
        nonisolated(unsafe) let surface = s
        writes.async {
            let total = Int(ghostty_surface_total_rows(surface))
            let whole = total - rows <= GalleryScanner.maxScrollback && total <= GalleryScanner.maxScreenRows
            let top = whole ? GHOSTTY_POINT_SCREEN : GHOSTTY_POINT_ACTIVE
            let sel = ghostty_selection_s(top_left: ghostty_point_s(tag: top, coord: GHOSTTY_POINT_COORD_TOP_LEFT, x: 0, y: 0),
                                          bottom_right: ghostty_point_s(tag: GHOSTTY_POINT_ACTIVE, coord: GHOSTTY_POINT_COORD_BOTTOM_RIGHT, x: 0, y: 0),
                                          rectangle: false)
            var t = ghostty_text_s()
            guard ghostty_surface_read_text(surface, sel, &t) else { return }
            defer { ghostty_surface_free_text(surface, &t) }
            done(t.text.map { String(decoding: UnsafeRawBufferPointer(start: $0, count: Int(t.text_len)), as: UTF8.self) } ?? "", columns)
        }
    }

    /// Reads the screen's rows off the main thread, after the writes queued
    /// before it, a read a row: Ghostty joins soft-wrapped rows in one read,
    /// which would not tell which row is the cursor's. The cursor's row is
    /// read now, as drawnBottom reads it; output that moves it before the
    /// rows are read marks the line changed again.
    func readRows(_ done: @escaping @Sendable (_ rows: [String], _ cursor: Int?) -> Void) {
        guard let s = surface else { return }
        let z = ghostty_surface_size(s), rows = Int(z.rows), columns = Int(z.columns), h = cell.height
        guard rows > 0, columns > 0 else { return }
        let at = h > 0 ? Int(((cursor(width: nil).minY - inset) / h).rounded()) : -1
        let row = (0..<rows).contains(at) ? at : nil
        nonisolated(unsafe) let surface = s
        writes.async {
            let point = { (x: Int, y: Int) in ghostty_point_s(tag: GHOSTTY_POINT_ACTIVE, coord: GHOSTTY_POINT_COORD_EXACT, x: UInt32(x), y: UInt32(y)) }
            let lines = (0..<rows).map { y -> String in
                var t = ghostty_text_s()
                guard ghostty_surface_read_text(surface, ghostty_selection_s(top_left: point(0, y), bottom_right: point(columns - 1, y), rectangle: false), &t) else { return "" }
                defer { ghostty_surface_free_text(surface, &t) }
                return t.text.map { String(decoding: UnsafeRawBufferPointer(start: $0, count: Int(t.text_len)), as: UTF8.self) } ?? ""
            }
            done(lines, row)
        }
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

    /// Whether a two-finger swipe may switch panes: not while a selection
    /// is shown, not in a full-screen program that has the mouse, and not
    /// while a pinch sizes the font, whose fingers can drift as a swipe's.
    var paneSwipeAllowed: Bool {
        guard let s = surface else { return false }
        let pinching = gestureRecognizers?.contains { $0 is UIPinchGestureRecognizer && ($0.state == .began || $0.state == .changed) } ?? false
        return !showsSelection && !pinching && !ghostty_surface_mouse_captured(s)
    }

    /// A tap that clears no selection clicks for a program that has the
    /// mouse, else opens the link under it, and shows the keyboard. A tap on
    /// a link leaves the keyboard as it is: the link may open a preview
    /// that takes it away, and gives it back as it closes.
    @objc private func tap(_ g: UITapGestureRecognizer) {
        let p = g.location(in: self)
        if deselect(at: p) { return }
        if let s = surface, ghostty_surface_mouse_captured(s) {
            click(at: p)
        } else {
            let typing = isFirstResponder
            if openLink(at: p) {
                typingBeforeLink = typing
                return
            }
        }
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
    private func openLink(at p: CGPoint) -> Bool {
        guard let s = surface else { return false }
        overLink = false
        ghostty_surface_mouse_pos(s, -1, -1, GHOSTTY_MODS_NONE)
        ghostty_surface_mouse_pos(s, p.x, p.y, GHOSTTY_MODS_SUPER)
        // Read before the mouse leaves, which tells Ghostty's no link.
        let hit = overLink
        if hit { click(at: p, mods: GHOSTTY_MODS_SUPER) }
        ghostty_surface_mouse_pos(s, -1, -1, GHOSTTY_MODS_NONE)
        return hit
    }

    // MARK: Keyboard

    override var canBecomeFirstResponder: Bool { true }

    /// Whether the terminal shows the bar: it is for the soft keyboard,
    /// which a hardware one keeps away, and for a phone's: an iPad's keys
    /// are in its bar of shortcuts.
    static var hasBar: Bool {
        GCKeyboard.coalesced == nil && UIDevice.current.userInterfaceIdiom != .pad
    }

    /// Shows the bar where the terminal has it, as a hardware keyboard
    /// comes and goes.
    @objc private func showBar() {
        bar.isHidden = !Self.hasBar
        (superview as? TerminalHost)?.placeBottomEdge()
        setNeedsLayout()
        slide(duration: 0)
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
        guard key != "backspace" else { return changing { apply(document.deleteBackward(chord: ctrl || alt)) } }
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
        trace("forget", [])
    }

    /// Tells the keyboard of a change it did not make, or not as it
    /// predicted.
    func changing(_ change: () -> Void) {
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

    /// Turns the mic on, and tells whether it starts: what the microphone
    /// hears is recognised as it comes, and said told what is said so far,
    /// from the moment it is on, and nil as the mic goes off, by
    /// typeDictation or stopDictating. The microphone starts off the main
    /// thread; a stop meanwhile wins, and one that can't start says why. A
    /// first tap asks for the microphone, and does no more.
    func dictate(said: @escaping (Dictation.Said?) -> Void) -> Bool {
        guard dictating == nil else { return false }
        switch AVAudioApplication.shared.recordPermission {
        case .undetermined:
            AVAudioApplication.requestRecordPermission { @Sendable _ in }
            return false
        case .denied:
            alert(String(localized: "No Access to the Microphone"),
                  String(localized: "Allow Rex Go to use the microphone in Settings."))
            return false
        default:
            break
        }
        let listening = Dictation.shared.listen()
        let recording = Recording()
        let meter = Meter()
        dictating = (recording, listening, said, Task {
            for await s in listening.said { said(s) }
            // Ended with the mic on, it failed.
            if dictating?.listening === listening, mute() != nil {
                if case .failure(let e) = await listening.heard.result {
                    alert(String(localized: "Can't Recognise Speech"), e.localizedDescription)
                }
                said(nil)
            }
        }, Dictation.shared.show(meter))
        recording.start({ s in
            listening.add(s)
            meter.set(vDSP.rootMeanSquare(s))
        }, done: { [weak self] r in self?.started(listening, r) })
        return true
    }

    /// The microphone started, or not, for a listening: on, if the mic
    /// still is, it shows; one that can't record goes off and says why.
    private func started(_ listening: Listening, _ r: Result<Bool, any Error>) {
        guard let d = dictating, d.listening === listening else { return }
        switch r {
        case .success:
            d.said(Dictation.Said())
        case .failure(let e):
            _ = mute()
            listening.cancel()
            alert(String(localized: "Can't Record"), e.localizedDescription)
        }
    }

    /// Turns the mic off, and types what was said once its last phrase is
    /// recognised, polished first with a service set (Polish), as a paste,
    /// not sent.
    func typeDictation() async {
        guard let d = mute() else { return }
        let task = Task { @MainActor in
            do {
                var text = try await d.listening.finish()
                try Task.checkCancellation()
                await d.told.value
                if !text.isEmpty, let polish = Polish.saved {
                    d.said(Dictation.Said(phrases: [text], polishing: true))
                    text = await polish.run(text, vocabulary: Polish.words(in: screen, program: session.host.blocks[session.block]?.program))
                }
                try Task.checkCancellation()
                if !text.isEmpty { insert(text) }
            } catch is CancellationError {
            } catch {
                alert(String(localized: "Can't Recognise Speech"), error.localizedDescription)
            }
            await d.told.value
            d.said(nil)
        }
        finishing = (task, d.listening, d.said)
        await task.value
        finishing = nil
    }

    /// Turns the mic off, typing nothing; cancels an in-flight finish or
    /// polish too.
    func stopDictating() {
        if let f = finishing {
            finishing = nil
            f.task.cancel()
            f.listening.cancel()
            f.said(nil)
            return
        }
        guard let d = mute() else { return }
        d.listening.cancel()
        Task {
            await d.told.value
            d.said(nil)
        }
    }

    private func mute() -> Dictating? {
        guard let d = dictating else { return nil }
        dictating = nil
        d.recording.stop()
        d.level.cancel()
        Dictation.shared.levels = Dictation.shared.levels.map { _ in 0 }
        return d
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
/// omp does for a moment each time it redraws for a new size. A phone's
/// key bar is over it, not on the terminal, which slides under it: on the
/// keyboard, else above the home indicator.
/// A non-interactive gradient blur and background-color fade along a vertical
/// edge: top under the status bar, or bottom behind the key bar.
final class EdgeFadeView: UIView {
    enum Edge { case top, bottom }

    let blurView = UIVisualEffectView(effect: UIBlurEffect(style: .systemUltraThinMaterial))
    private let maskLayer = CAGradientLayer()
    private let colorLayer = CAGradientLayer()
    private var color: UIColor
    /// How tall the fade to clear is, from the inner edge; nil fades over
    /// 65% of the view, with a 0.75 tint where it starts. With a length the
    /// rest is solid: the bottom edge covers the key bar's lower half and
    /// the home indicator, and fades over the bar's upper half only.
    private let fade: CGFloat?

    init(edge: Edge, color: UIColor, fade: CGFloat? = nil) {
        self.color = color
        self.fade = fade
        super.init(frame: .zero)
        isUserInteractionEnabled = false
        backgroundColor = .clear

        let start = CGPoint(x: 0.5, y: edge == .top ? 0 : 1)
        let end = CGPoint(x: 0.5, y: edge == .top ? 1 : 0)

        maskLayer.colors = [UIColor.black.cgColor, UIColor.black.cgColor, UIColor.clear.cgColor]
        maskLayer.locations = [0, 0.35, 1]
        maskLayer.startPoint = start
        maskLayer.endPoint = end

        blurView.isUserInteractionEnabled = false
        blurView.layer.mask = maskLayer
        addSubview(blurView)

        colorLayer.locations = [0, 0.35, 1]
        colorLayer.startPoint = start
        colorLayer.endPoint = end
        layer.addSublayer(colorLayer)

        applyColor()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError() }

    override func layoutSubviews() {
        super.layoutSubviews()
        blurView.frame = bounds
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        maskLayer.frame = blurView.bounds
        colorLayer.frame = bounds
        if let fade, bounds.height > 0 {
            let solid = NSNumber(value: Double(max(0, 1 - fade / bounds.height)))
            maskLayer.locations = [0, solid, 1]
            colorLayer.locations = [0, solid, 1]
        }
        CATransaction.commit()
    }

    func setColor(_ c: UIColor) {
        guard c != color else { return }
        color = c
        applyColor()
    }

    private func applyColor() {
        colorLayer.colors = [
            color.cgColor,
            color.withAlphaComponent(fade == nil ? 0.75 : 1).cgColor,
            color.withAlphaComponent(0).cgColor,
        ]
    }
}

final class TerminalHost: UIView, UIGestureRecognizerDelegate {
    let terminal: TerminalView
    private let scrollDown = UIVisualEffectView(effect: UIGlassEffect(style: .regular))
    private let unreadDot = UIView()
    /// Follows a two-finger drag between panes: its state, and how far and
    /// how fast the fingers went. Nil on a host whose screen has no pane
    /// switching.
    var onPaneDrag: ((UIGestureRecognizer.State, _ translation: CGPoint, _ velocity: CGPoint) -> Void)?
    /// Whether a drag may switch now: no selection, no full-screen program
    /// with the mouse, no pinch, no other switch under way.
    var canSwipe: (() -> Bool)?
    /// The two-finger drag between panes.
    private let paneDrag = UIPanGestureRecognizer()
    /// While a tab switch animates, layout passes place the terminal, its
    /// inset and the key bar at once, so they don't animate from .zero under
    /// the switch's spring.
    var snaps = false
    /// Whether its pane is the one shown, as the screen last said.
    var current = true
    /// The fade behind the bars at the bottom, as the top edge's under the
    /// status bar: solid from the bottom up, clear at the top bar's top, so
    /// the row the cursor rests on above the bars is never covered; it
    /// changes no inset. It covers the phone's key bar and what sits above
    /// it, and goes when neither is there.
    private let bottomEdge = EdgeFadeView(edge: .bottom, color: .black, fade: KeyBar.height / 2)
    private var bottomEdgeTop: NSLayoutConstraint?
    /// How tall the bars above the key bar reach, such as the pane bar,
    /// counted from the key bar's top, or from the keyboard's (or the home
    /// indicator's) without it. The bottom edge reaches over them too.
    var aboveBar: CGFloat = 0 { didSet { if aboveBar != oldValue { placeBottomEdge() } } }

    init(_ terminal: TerminalView) {
        self.terminal = terminal
        super.init(frame: .zero)
        addSubview(terminal)
        // Two fingers, so one keeps scrolling, selecting and clicking.
        paneDrag.addTarget(self, action: #selector(dragged))
        paneDrag.minimumNumberOfTouches = 2
        paneDrag.maximumNumberOfTouches = 2
        paneDrag.delegate = self
        paneDrag.cancelsTouchesInView = false
        // Off until it feels right on a device (si-mods/gorex#676); the pane
        // bar switches panes.
        paneDrag.isEnabled = false
        addGestureRecognizer(paneDrag)
        let edge = bottomEdge
        edge.translatesAutoresizingMaskIntoConstraints = false
        addSubview(edge)
        let edgeTop = edge.topAnchor.constraint(equalTo: keyboardLayoutGuide.topAnchor)
        bottomEdgeTop = edgeTop
        NSLayoutConstraint.activate([
            edge.leadingAnchor.constraint(equalTo: leadingAnchor),
            edge.trailingAnchor.constraint(equalTo: trailingAnchor),
            edgeTop,
            edge.bottomAnchor.constraint(equalTo: bottomAnchor),
        ])
        let anchor: NSLayoutYAxisAnchor
        if UIDevice.current.userInterfaceIdiom != .pad {
            let bar = terminal.bar
            bar.translatesAutoresizingMaskIntoConstraints = false
            addSubview(bar)
            NSLayoutConstraint.activate([
                bar.leadingAnchor.constraint(equalTo: leadingAnchor),
                bar.trailingAnchor.constraint(equalTo: trailingAnchor),
                bar.bottomAnchor.constraint(equalTo: keyboardLayoutGuide.topAnchor),
            ])
            anchor = bar.topAnchor
        } else {
            anchor = keyboardLayoutGuide.topAnchor
        }

        scrollDown.cornerConfiguration = .capsule()
        scrollDown.clipsToBounds = true
        scrollDown.alpha = 0
        scrollDown.isHidden = true
        scrollDown.translatesAutoresizingMaskIntoConstraints = false
        var cfg = UIButton.Configuration.plain()
        cfg.image = UIImage(systemName: "arrow.down")
        cfg.preferredSymbolConfigurationForImage = UIImage.SymbolConfiguration(pointSize: 15, weight: .semibold)
        cfg.baseForegroundColor = .label
        let btn = UIButton(configuration: cfg, primaryAction: UIAction { [weak terminal] _ in terminal?.scrollToBottom() })
        btn.accessibilityLabel = String(localized: "Scroll to Bottom")
        btn.translatesAutoresizingMaskIntoConstraints = false
        scrollDown.contentView.addSubview(btn)

        unreadDot.backgroundColor = .systemOrange
        unreadDot.layer.cornerRadius = 3.5
        unreadDot.isHidden = true
        unreadDot.isUserInteractionEnabled = false
        unreadDot.translatesAutoresizingMaskIntoConstraints = false
        scrollDown.contentView.addSubview(unreadDot)

        addSubview(scrollDown)
        NSLayoutConstraint.activate([
            btn.leadingAnchor.constraint(equalTo: scrollDown.contentView.leadingAnchor),
            btn.trailingAnchor.constraint(equalTo: scrollDown.contentView.trailingAnchor),
            btn.topAnchor.constraint(equalTo: scrollDown.contentView.topAnchor),
            btn.bottomAnchor.constraint(equalTo: scrollDown.contentView.bottomAnchor),
            unreadDot.widthAnchor.constraint(equalToConstant: 7),
            unreadDot.heightAnchor.constraint(equalToConstant: 7),
            unreadDot.topAnchor.constraint(equalTo: scrollDown.contentView.topAnchor, constant: 7),
            unreadDot.trailingAnchor.constraint(equalTo: scrollDown.contentView.trailingAnchor, constant: -7),
            scrollDown.widthAnchor.constraint(equalToConstant: 40),
            scrollDown.heightAnchor.constraint(equalToConstant: 40),
            scrollDown.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -16),
            scrollDown.bottomAnchor.constraint(equalTo: anchor, constant: -12),
        ])

        placeBottomEdge()
        terminal.onScrollState = { [weak self] up, unread in
            self?.updateScrollDown(visible: up, unread: unread)
        }
    }

    private func updateScrollDown(visible: Bool, unread: Bool) {
        unreadDot.isHidden = !unread
        guard visible == scrollDown.isHidden else { return }
        if visible {
            scrollDown.isHidden = false
            UIView.animate(withDuration: 0.2) { self.scrollDown.alpha = 1 }
        } else {
            UIView.animate(withDuration: 0.2) { self.scrollDown.alpha = 0 } completion: { _ in
                if !self.terminal.scrolledUp { self.scrollDown.isHidden = true }
            }
        }
    }

    /// A two-finger drag moves the panes with the fingers, unless a switch
    /// is under way or the terminal must keep the touch (a selection, a
    /// full-screen program with the mouse). One that can no longer switch
    /// midway, as a pinch starts, is cancelled. The key bar and the
    /// scroll-to-bottom button keep their own touches.
    @objc private func dragged(_ g: UIPanGestureRecognizer) {
        if g.state == .changed, canSwipe?() == false {
            onPaneDrag?(.cancelled, g.translation(in: self), .zero)
            // Off and on again ends it, as cancelled.
            g.isEnabled = false
            g.isEnabled = true
            return
        }
        onPaneDrag?(g.state, g.translation(in: self), g.velocity(in: self))
    }

    override func gestureRecognizerShouldBegin(_ g: UIGestureRecognizer) -> Bool {
        guard g === paneDrag else { return true }
        let p = g.location(in: self)
        if terminal.bar.superview != nil, terminal.bar.frame.contains(p) { return false }
        if scrollDown.superview != nil, !scrollDown.isHidden, scrollDown.frame.contains(p) { return false }
        return canSwipe?() != false
    }

    /// The terminal's own drag, tap, long press and pinch keep working with
    /// the pane drag.
    func gestureRecognizer(_ g: UIGestureRecognizer, shouldRecognizeSimultaneouslyWith other: UIGestureRecognizer) -> Bool {
        true
    }

    required init?(coder: NSCoder) { fatalError() }

    override func layoutSubviews() {
        guard snaps else {
            super.layoutSubviews()
            place()
            return
        }
        UIView.performWithoutAnimation {
            super.layoutSubviews()
            place()
            terminal.layoutIfNeeded()
        }
    }

    private func place() {
        terminal.bounds = CGRect(origin: .zero, size: bounds.size)
        terminal.center = CGPoint(x: bounds.midX, y: bounds.midY)
        terminal.inset = safeAreaInsets.top
    }

    override func safeAreaInsetsDidChange() {
        super.safeAreaInsetsDidChange()
        setNeedsLayout()
        // The terminal's band holds the home indicator's height.
        terminal.setNeedsLayout()
    }

    func setBottomEdgeColor(_ color: UIColor) {
        bottomEdge.setColor(color)
    }

    /// Reaches the bottom edge up over the key bar, when it shows, and the
    /// bars above it; with neither it goes.
    func placeBottomEdge() {
        let bar = terminal.bar
        let keyBar = bar.superview === self && !bar.isHidden ? KeyBar.height : 0
        let height = keyBar + aboveBar
        bottomEdge.isHidden = height == 0
        bottomEdgeTop?.constant = -height
    }
}

/// The bar over the soft keyboard, which stays at the bottom while the
/// keyboard is hidden: three capsules of Liquid Glass, dark as the keyboard
/// is. ⌘ opens a menu of Paste, Add Photo and Add File, and spins while a
/// file goes up; the middle one pages from Esc, the modifiers and Tab to
/// the punctuation a shell wants and Backspace; the last holds a stick that
/// sends arrows the way it is dragged, the screen's mic, and the key that
/// hides or shows the keyboard. On an iPad the keys go in the keyboard's
/// bar of shortcuts, which shows with a hardware keyboard too: ⌘, Esc, the
/// modifiers and Tab before its middle, the punctuation and four arrows
/// after it. Its keyboard has keys of its own to hide it and for Backspace.
final class KeyBar: UIView {
    let ctrl = KeyBar.button("control", String(localized: "Control"), size: KeyBar.small)
    let alt = KeyBar.button("option", String(localized: "Option"), size: KeyBar.small)
    let shift = KeyBar.button("shift", String(localized: "Shift"), size: KeyBar.small)
    /// Where the screen puts its mic, and its voice capsule while dictating.
    let mic = UIView()
    let voice = UIView()
    private var keysRow: UIView?
    private var voiceCapsule: UIView?
    private var voiceActive = false
    /// Hides the keyboard, or shows it while it is hidden.
    let keyboard = UIButton()
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
        super.init(frame: CGRect(x: 0, y: 0, width: 0, height: Self.height))
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

        let escape = key("escape", String(localized: "Esc"), "escape")
        let tab = key("arrow.right.to.line", String(localized: "Tab"), "tab")
        let enter = key("return", String(localized: "Enter"), "enter")
        let tilde = text("~"), pipe = text("|"), slash = text("/"), dash = text("-")
        let first = [escape, ctrl, alt, shift, tab]
        let second = [tilde, pipe, slash, dash]
        // Every key taps the hand as it goes down, as the keyboard's do.
        // Backspace, the arrows and the stick tap on their own as they repeat.
        for b in [more, keyboard] + first + second + [enter] {
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

        // Three pages of four slots, the last one short, inset from the
        // capsule's ends, each page as wide as the capsule.
        let pages = UIStackView(arrangedSubviews: [[escape, ctrl, alt, enter], [shift, slash, dash, delete], [tilde, pipe, tab]].map { keys in
            let page = UIStackView(arrangedSubviews: keys + (keys.count..<4).map { _ in UIView() })
            page.distribution = .fillEqually
            page.isLayoutMarginsRelativeArrangement = true
            page.directionalLayoutMargins = NSDirectionalEdgeInsets(top: 0, leading: 8, bottom: 0, trailing: 8)
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
        keyboard.addAction(UIAction { [weak target] _ in
            guard let target else { return }
            _ = target.isFirstResponder ? target.resignFirstResponder() : target.becomeFirstResponder()
        }, for: .touchUpInside)
        keyboard.configurationUpdateHandler = { [weak target] b in
            let hidden = target?.isFirstResponder != true
            b.configuration = Self.config(hidden ? "keyboard" : "keyboard.chevron.compact.down", text: false, size: 17, held: false)
            b.accessibilityLabel = hidden ? String(localized: "Show Keyboard") : String(localized: "Hide Keyboard")
        }

        // The mic taps the hand as the keys do, as the finger goes down.
        // What the tap does is the screen's, so it never keeps the touch.
        let micDown = PressWatcher(target: self, action: #selector(pressMic))
        micDown.minimumPressDuration = 0
        micDown.cancelsTouchesInView = false
        mic.addGestureRecognizer(micDown)

        let middle = Self.capsule(scroll)
        // The middle capsule takes the room the others leave.
        middle.setContentHuggingPriority(.defaultLow, for: .horizontal)
        let right = UIStackView(arrangedSubviews: [stick, mic, keyboard])
        right.distribution = .fillEqually
        let left = Self.capsule(more), last = Self.capsule(right)
        // As in the bar this one copies: ⌘ 56pt wide, and 50 for each key
        // of the last.
        left.widthAnchor.constraint(equalToConstant: 56).isActive = true
        last.widthAnchor.constraint(equalToConstant: 150).isActive = true
        let keysRow = UIStackView(arrangedSubviews: [middle, last])
        keysRow.spacing = 8
        let voiceCapsule = Self.capsule(voice)
        voiceCapsule.alpha = 0
        voiceCapsule.isHidden = true
        let rightArea = UIView()
        rightArea.setContentHuggingPriority(.defaultLow, for: .horizontal)
        for sub in [keysRow, voiceCapsule] {
            sub.translatesAutoresizingMaskIntoConstraints = false
            rightArea.addSubview(sub)
            NSLayoutConstraint.activate([
                sub.leadingAnchor.constraint(equalTo: rightArea.leadingAnchor),
                sub.trailingAnchor.constraint(equalTo: rightArea.trailingAnchor),
                sub.topAnchor.constraint(equalTo: rightArea.topAnchor),
                sub.bottomAnchor.constraint(equalTo: rightArea.bottomAnchor),
            ])
        }
        self.keysRow = keysRow
        self.voiceCapsule = voiceCapsule

        let row = UIStackView(arrangedSubviews: [left, rightArea])
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

    /// How tall a phone's bar is, over the keyboard or the home indicator.
    static let height: CGFloat = 52

    override var intrinsicContentSize: CGSize { CGSize(width: UIView.noIntrinsicMetric, height: Self.height) }

    /// Transitions the middle and right capsules into a single horizontal
    /// voice capsule while dictating, or back to the key capsules when done.
    func setVoice(active: Bool) {
        guard active != voiceActive, let keysRow, let voiceCapsule else { return }
        voiceActive = active
        if active {
            voiceCapsule.isHidden = false
            voiceCapsule.transform = CGAffineTransform(scaleX: 0.94, y: 0.94)
            keysRow.isUserInteractionEnabled = false
            UIView.animate(withDuration: 0.28, delay: 0, usingSpringWithDamping: 0.86, initialSpringVelocity: 0) {
                keysRow.alpha = 0
                keysRow.transform = CGAffineTransform(scaleX: 0.96, y: 0.96)
                voiceCapsule.alpha = 1
                voiceCapsule.transform = .identity
            }
        } else {
            keysRow.isUserInteractionEnabled = true
            UIView.animate(withDuration: 0.24, delay: 0, usingSpringWithDamping: 0.9, initialSpringVelocity: 0) {
                keysRow.alpha = 1
                keysRow.transform = .identity
                voiceCapsule.alpha = 0
                voiceCapsule.transform = CGAffineTransform(scaleX: 0.94, y: 0.94)
            } completion: { _ in
                if !self.voiceActive { voiceCapsule.isHidden = true }
            }
        }
    }

    /// The screen's mic, as it goes down: a light tap of the hand, as the
    /// keys give.
    @objc private func pressMic(_ g: UILongPressGestureRecognizer) {
        guard g.state == .began else { return }
        tick.impactOccurred()
    }

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

/// A touch, watched as it goes down: it never keeps the touch from the
/// view, so what the view does with the tap still happens.
final class PressWatcher: UILongPressGestureRecognizer {
    override func canPrevent(_ other: UIGestureRecognizer) -> Bool { false }
    override func canBePrevented(by other: UIGestureRecognizer) -> Bool { false }
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
