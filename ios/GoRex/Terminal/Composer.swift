import UIKit
import UniformTypeIdentifiers

/// The compose bar (docs/IOS-025-composer.md, docs/IOS-030-composer-centric-bar.md),
/// up while Settings > Terminal's Composer Mode is on: an adaptive glass pill
/// pinned above the keyboard or home indicator. At rest or with a short single
/// line, `+`, the message field, the screen's mic, and Send share one 48 pt row;
/// as soon as the draft wraps or includes a newline, the field reflows in place
/// across the full width above a dedicated `[ +  …  Mic  Send ]` bottom action
/// row (Grok-style), growing smoothly up to `fieldMax` before scrolling. On
/// iPhone, swiping left on the bar (or choosing `More Keys` in `+`) unfurls the
/// inline 7-key strip across the bottom row, and swiping right returns to the
/// message field. Attachments stage in a temporary folder and upload as one
/// batch before pasting the text and sending Return.
final class ComposerBar: UIView, UITextViewDelegate, UIGestureRecognizerDelegate {
    /// A staged attachment: its file in the folder, and its thumbnail for
    /// the strip, nil for a file that is no image.
    private struct Attachment {
        let url: URL
        let thumb: UIImage?
    }

    /// The setting (Settings > Terminal) that pins the bar above the key
    /// bar and moves the mic into it.
    static let modeKey = "composerMode"

    private weak var terminal: TerminalView?
    /// The folder the attachments stage in, emptied as a send goes.
    private let dir: URL
    /// The next attachment's staging folder's number.
    private var staged = 0
    private var attachments: [Attachment] = []
    /// A send is up: Send spins and the attach menu holds still.
    private var sending = false
    /// The pickers' delegates, held while their picker is up.
    private var filePick: FilePick?
    private var shot: CameraShot?

    let field: UITextView = ComposeTextView()
    /// The placeholder, which names the block the message goes to.
    private let hint = UILabel()
    /// Right-edge fade mask for long placeholders so they fade out cleanly
    /// before the mic/send icons instead of clipping abruptly or overlapping.
    private let hintMask: CAGradientLayer = {
        let g = CAGradientLayer()
        g.colors = [UIColor.black.cgColor, UIColor.black.cgColor, UIColor.clear.cgColor]
        g.startPoint = CGPoint(x: 0, y: 0.5)
        g.endPoint = CGPoint(x: 1, y: 0.5)
        return g
    }()
    private let attach = QuickKeyButton()
    private let send = QuickKeyButton()
    /// Inline quick-keys pill on iPhone (IOS-030): unfurls full-width across
    /// the bottom 36 pt action row from `+` › `More Keys` so all 7 keys meet
    /// Apple HIG's 44 × 44 pt minimum tap target while keeping any multi-line
    /// draft visible above it.
    private let quickPill = QuickPillView()
    private let quickKeys = QuickStackView()
    private var quickKeysExpanded = false
    private let stickBtn = QuickKeyButton()
    private var stickDrag: UILongPressGestureRecognizer?
    private let keysMenuBtn = QuickKeyButton()
    private let pad = StickPad()
    private var repeating: Task<Void, Never>?
    private var pressing: Task<Void, Never>?
    private var steered = false
    private var arrow: String? { didSet { pad.arrow = arrow } }
    private let tick = UIImpactFeedbackGenerator(style: .light)
    /// Where the screen's mic lands, beside Send.
    let micHolder: UIView = QuickPillView()
    /// Where the screen's single-row voice controls land while dictating,
    /// cross-fading across the bottom 36 pt row inside the compose pill.
    let voiceHolder = UIView()
    private let fieldClip = UIView()
    private let scrollFadeMask: CAGradientLayer = {
        let g = CAGradientLayer()
        g.startPoint = CGPoint(x: 0.5, y: 0)
        g.endPoint = CGPoint(x: 0.5, y: 1)
        return g
    }()
    private var lastScrollFade: (w: CGFloat, h: CGFloat, top: Bool, bottom: Bool)?
    private let rowBox = RowBoxView()
    private var compactConstraints: [NSLayoutConstraint] = []
    private var expandedConstraints: [NSLayoutConstraint] = []
    private var fieldHeight: NSLayoutConstraint!
    private var multiLine = false
    private var lastRowWidth: CGFloat = 0
    private var lastEffectiveMax: CGFloat = 0
    private var voiceActive = false
    /// Press-and-hold anywhere on the compose bar to dictate (push-to-talk):
    /// holding starts voice recording, releasing converts speech to text.
    private lazy var holdVoice = UILongPressGestureRecognizer(target: self, action: #selector(heldForVoice))
    private var holdingVoice = false
    /// Downward drag or flick on the bar dismisses the keyboard first if up,
    /// and closes Composer Mode on the next swipe.
    private lazy var dismissPan = UIPanGestureRecognizer(target: self, action: #selector(draggedDown))
    /// Horizontal swipes on iPhone switch inline between the compose field and
    /// the 7-key strip without opening the `+` menu.
    private lazy var swipeLeft: UISwipeGestureRecognizer = {
        let g = UISwipeGestureRecognizer(target: self, action: #selector(swipedLeft))
        g.direction = .left
        g.cancelsTouchesInView = false
        g.delegate = self
        return g
    }()
    private lazy var swipeRight: UISwipeGestureRecognizer = {
        let g = UISwipeGestureRecognizer(target: self, action: #selector(swipedRight))
        g.direction = .right
        g.cancelsTouchesInView = false
        g.delegate = self
        return g
    }()
    private var isReduceMotion: Bool {
        UIAccessibility.isReduceMotionEnabled || UserDefaults.standard.bool(forKey: "reduceMotion")
    }
    /// Called when the user closes Composer Mode via swipe-down or menu.
    var onClose: (() -> Void)?
    /// Whether an upload or inline dictation is running and closing must wait.
    var isBusy: Bool { sending || voiceActive }
    var onHoldVoiceStart: ((_ stillHolding: @escaping () -> Bool) -> Void)?
    var onHoldVoiceFinish: (() -> Void)?
    var onHoldVoiceCancel: (() -> Void)?
    /// The attachments' thumbnails, over the input row while there are any.
    private let strip = UIScrollView()
    private let thumbs = UIStackView()
    /// Called when the bar's laid-out height changes (multi-line growth or
    /// attachments), so the host can slide the terminal and bottom edge.
    var onHeightChange: ((CGFloat) -> Void)?
    private var lastHeight: CGFloat = 0
    /// Single-line row height and concentric 6 pt padding inside the 24 pt
    /// radius pill (`18 + 6 = 24`), fixing the resting height at 48 pt.
    private static let fieldMin: CGFloat = 36
    private static let vPad: CGFloat = 6
    private static let hPad: CGFloat = 6
    private static let buttonGap: CGFloat = 8
    private static let rowGap: CGFloat = 8
    private static let compactInsets = UIEdgeInsets(top: 7.5, left: 4, bottom: 7.5, right: 4)
    private static let expandedInsets = UIEdgeInsets(top: 8, left: 10, bottom: 6, right: 10)
    /// Attachment strip geometry: 56 × 56 pt continuous-corner cards inset 12 pt
    /// inside the 24 pt glass pill (`vPad 6 + stripTopPad 6 = 12`, concentric
    /// `thumbRadius = 12`), with a 44 × 44 pt remove hit target and 20 × 20 pt
    /// high-contrast corner badge fully inside each thumbnail's bounds.
    private static let thumbSize: CGFloat = 56
    private static let thumbRadius: CGFloat = 12
    private static let removeHitSize: CGFloat = 44
    private static let removeBadgeSize: CGFloat = 20
    private static let removeBadgeInset: CGFloat = 4
    private static let stripTopPad: CGFloat = 6
    private static let stripSidePad: CGFloat = 6
    private static let stripHeight: CGFloat = stripTopPad + thumbSize
    /// Gap between the compose bar and the keyboard or safe area below it.
    static let gap: CGFloat = 6
    /// Resting height of the single-row pill with no attachments (48 pt).
    static let baseHeight: CGFloat = vPad + fieldMin + vPad
    /// Bottom inset reserved so the resting bar never covers the program's own
    /// input row (54 pt).
    static let baseInset: CGFloat = baseHeight + gap
    /// How tall the full-width field grows (~7 lines) before it scrolls instead.
    private static let fieldMax: CGFloat = 160

    init(terminal: TerminalView) {
        self.terminal = terminal
        dir = FileManager.default.temporaryDirectory.appending(path: "Composer").appending(path: UUID().uuidString)
        super.init(frame: .zero)
        accessibilityIdentifier = "composer"
        accessibilityContainerType = .semanticGroup

        let glass = UIVisualEffectView(effect: UIGlassEffect(style: .regular))
        glass.cornerConfiguration = .uniformCorners(radius: 24)
        glass.clipsToBounds = true
        glass.translatesAutoresizingMaskIntoConstraints = false
        addSubview(glass)
        NSLayoutConstraint.activate([
            glass.leadingAnchor.constraint(equalTo: leadingAnchor),
            glass.trailingAnchor.constraint(equalTo: trailingAnchor),
            glass.topAnchor.constraint(equalTo: topAnchor),
            glass.bottomAnchor.constraint(equalTo: bottomAnchor),
        ])

        var cfg = UIButton.Configuration.filled()
        cfg.image = UIImage(systemName: "plus")
        cfg.preferredSymbolConfigurationForImage = UIImage.SymbolConfiguration(pointSize: 17, weight: .medium)
        cfg.baseBackgroundColor = .tertiarySystemFill
        cfg.baseForegroundColor = .label
        cfg.background.cornerRadius = 18
        attach.configuration = cfg
        attach.accessibilityLabel = String(localized: "Add Attachment")
        var items: [UIMenuElement] = [
            UIAction(title: String(localized: "Paste"), image: UIImage(systemName: "doc.on.clipboard")) { [weak self] _ in self?.pasteClipboard() },
        ]
        if terminal.session.host.canUpload {
            items.append(UIAction(title: String(localized: "Photo"), image: UIImage(systemName: "photo")) { [weak self] _ in self?.addPhotos() })
            if UIImagePickerController.isSourceTypeAvailable(.camera) {
                items.append(UIAction(title: String(localized: "Camera"), image: UIImage(systemName: "camera")) { [weak self] _ in self?.takePhoto() })
            }
            items.append(UIAction(title: String(localized: "File"), image: UIImage(systemName: "doc")) { [weak self] _ in self?.addFiles() })
        }
        items.append(UIDeferredMenuElement.uncached { [weak self] done in
            var tail: [UIMenuElement] = []
            if UIDevice.current.userInterfaceIdiom != .pad {
                let keysAction = UIAction(title: String(localized: "More Keys"), image: UIImage(systemName: "command")) { [weak self] _ in
                    self?.tick.impactOccurred()
                    self?.setQuickKeysExpanded(true, animated: true)
                }
                keysAction.accessibilityIdentifier = "composer.keys.toggle"
                tail.append(keysAction)
                if self?.terminal?.keyboardUp == true {
                    tail.append(UIAction(title: String(localized: "Hide Keyboard"), image: UIImage(systemName: "keyboard.chevron.compact.down")) { [weak self] _ in
                        _ = self?.terminal?.resignFirstResponder()
                    })
                }
                tail.append(UIAction(title: String(localized: "Show Key Bar"), image: UIImage(systemName: "keyboard")) { [weak self] _ in
                    self?.requestClose()
                })
            } else {
                tail.append(UIAction(title: String(localized: "Hide Composer"), image: UIImage(systemName: "xmark.circle")) { [weak self] _ in
                    self?.requestClose()
                })
            }
            done([UIMenu(options: .displayInline, children: tail)])
        })
        attach.menu = UIMenu(children: items)
        attach.showsMenuAsPrimaryAction = true
        attach.addAction(UIAction { _ in Haptics.tapped() }, for: .touchDown)

        field.font = .systemFont(ofSize: 17)
        field.textColor = .label
        field.backgroundColor = .clear
        field.textContainerInset = Self.compactInsets
        field.textContainer.lineFragmentPadding = 0
        field.isScrollEnabled = false
        field.showsVerticalScrollIndicator = false
        field.alwaysBounceVertical = false
        field.delegate = self
        field.setContentHuggingPriority(.defaultLow, for: .horizontal)
        field.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        field.inputAssistantItem.leadingBarButtonGroups = []
        field.inputAssistantItem.trailingBarButtonGroups = []
        field.accessibilityLabel = String(localized: "Message")
        field.accessibilityIdentifier = "composer.message"

        let block = terminal.session.host.blocks[terminal.session.block]
        let name = block?.title.flatMap { $0.isEmpty ? nil : $0 } ?? block?.program.flatMap { $0.isEmpty ? nil : $0 }
        hint.text = name.map { String(format: String(localized: "Message to %@"), $0) } ?? String(localized: "Message")
        hint.font = field.font
        hint.textColor = .placeholderText
        hint.numberOfLines = 1
        hint.lineBreakMode = .byClipping
        hint.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        hint.isUserInteractionEnabled = false
        hint.translatesAutoresizingMaskIntoConstraints = false
        field.addSubview(hint)

        micHolder.backgroundColor = .tertiarySystemFill
        micHolder.layer.cornerRadius = 18

        let sendSym = UIImage.SymbolConfiguration(pointSize: 15, weight: .semibold)
        let sendActiveFg = UIColor { $0.userInterfaceStyle == .dark ? .black : .white }
        let sendActiveBg = UIColor { $0.userInterfaceStyle == .dark ? .white : .black }
        let sendIdleFg = UIColor.tertiaryLabel
        var s = UIButton.Configuration.filled()
        s.image = UIImage(systemName: "arrow.up", withConfiguration: sendSym)?
            .withTintColor(sendIdleFg, renderingMode: .alwaysOriginal)
        s.baseBackgroundColor = .tertiarySystemFill
        s.baseForegroundColor = sendIdleFg
        s.background.cornerRadius = 18
        send.configuration = s
        send.configurationUpdateHandler = { [weak self] btn in
            UIView.performWithoutAnimation {
                guard var cfg = btn.configuration else { return }
                let sending = self?.sending == true
                cfg.showsActivityIndicator = sending
                if sending {
                    cfg.image = nil
                    cfg.baseBackgroundColor = .tertiarySystemFill
                    cfg.baseForegroundColor = .secondaryLabel
                } else if btn.isEnabled {
                    cfg.image = UIImage(systemName: "arrow.up", withConfiguration: sendSym)?
                        .withTintColor(sendActiveFg, renderingMode: .alwaysOriginal)
                    cfg.baseBackgroundColor = sendActiveBg
                    cfg.baseForegroundColor = sendActiveFg
                } else {
                    cfg.image = UIImage(systemName: "arrow.up", withConfiguration: sendSym)?
                        .withTintColor(sendIdleFg, renderingMode: .alwaysOriginal)
                    cfg.baseBackgroundColor = .tertiarySystemFill
                    cfg.baseForegroundColor = sendIdleFg
                }
                btn.configuration = cfg
            }
        }
        send.accessibilityLabel = String(localized: "Send")
        send.isEnabled = false
        send.addAction(UIAction { _ in Haptics.tapped() }, for: .touchDown)
        send.addAction(UIAction { [weak self] _ in self?.sendNow() }, for: .touchUpInside)

        thumbs.axis = .horizontal
        thumbs.spacing = 10
        thumbs.alignment = .center
        thumbs.isLayoutMarginsRelativeArrangement = true
        thumbs.directionalLayoutMargins = NSDirectionalEdgeInsets(
            top: Self.stripTopPad,
            leading: Self.stripSidePad,
            bottom: 0,
            trailing: Self.stripSidePad
        )
        thumbs.translatesAutoresizingMaskIntoConstraints = false
        strip.addSubview(thumbs)
        strip.contentInsetAdjustmentBehavior = .never
        strip.delaysContentTouches = false
        strip.showsHorizontalScrollIndicator = false
        strip.isHidden = true

        // Inline 7-key strip (IOS-030), unfurled across the row from `+` › `More Keys`.
        func quickButton(image: UIImage?, label: String, id: String? = nil, tint: UIColor = .label) -> UIButton {
            var c = UIButton.Configuration.filled()
            c.image = image
            c.baseBackgroundColor = .clear
            c.baseForegroundColor = tint
            c.cornerStyle = .capsule
            c.contentInsets = .zero
            let b = QuickKeyButton(configuration: c)
            b.accessibilityLabel = label
            b.accessibilityIdentifier = id
            b.configurationUpdateHandler = { btn in
                UIView.performWithoutAnimation {
                    var cfg = btn.configuration ?? c
                    cfg.baseBackgroundColor = btn.isHighlighted ? .secondarySystemFill : .clear
                    btn.configuration = cfg
                }
            }
            b.translatesAutoresizingMaskIntoConstraints = false
            b.heightAnchor.constraint(equalToConstant: 32).isActive = true
            return b
        }
        let symCfg = UIImage.SymbolConfiguration(pointSize: 13.5, weight: .regular)
        let escBtn = quickButton(image: UIImage(systemName: "escape", withConfiguration: symCfg), label: String(localized: "Esc"))
        escBtn.addAction(UIAction { [weak self] _ in
            self?.tick.impactOccurred()
            self?.terminal?.send("escape")
        }, for: .touchUpInside)

        let tabBtn = quickButton(image: UIImage(systemName: "arrow.right.to.line", withConfiguration: symCfg), label: String(localized: "Tab"))
        tabBtn.addAction(UIAction { [weak self] _ in
            self?.tick.impactOccurred()
            self?.terminal?.send("tab")
        }, for: .touchUpInside)

        let enterBtn = quickButton(image: UIImage(systemName: "return", withConfiguration: symCfg), label: String(localized: "Enter"))
        enterBtn.addAction(UIAction { [weak self] _ in
            self?.tick.impactOccurred()
            self?.terminal?.send("enter")
        }, for: .touchUpInside)

        let deleteBtn = quickButton(image: UIImage(systemName: "delete.left", withConfiguration: symCfg), label: String(localized: "Delete"))
        deleteBtn.addAction(UIAction { [weak self] _ in
            guard let self else { return }
            self.pressing?.cancel()
            self.pressing = Task { [weak self] in
                var delay = 0.4
                repeat {
                    guard let self else { break }
                    self.terminal?.send("backspace", holding: true)
                    self.tick.impactOccurred(intensity: 0.5)
                    try? await Task.sleep(for: .seconds(delay))
                    delay = 0.05
                } while !Task.isCancelled
            }
        }, for: .touchDown)
        deleteBtn.addAction(UIAction { [weak self] _ in
            self?.pressing?.cancel()
            self?.terminal?.release()
        }, for: [.touchUpInside, .touchUpOutside, .touchCancel])

        var stickCfg = UIButton.Configuration.filled()
        stickCfg.image = KeyBar.stick
        stickCfg.baseBackgroundColor = .clear
        stickCfg.baseForegroundColor = .label
        stickCfg.cornerStyle = .capsule
        stickCfg.contentInsets = .zero
        stickBtn.configuration = stickCfg
        stickBtn.accessibilityLabel = String(localized: "Arrow Keys")
        stickBtn.translatesAutoresizingMaskIntoConstraints = false
        stickBtn.heightAnchor.constraint(equalToConstant: 32).isActive = true
        let drag = UILongPressGestureRecognizer(target: self, action: #selector(steer))
        drag.minimumPressDuration = 0
        drag.allowableMovement = .greatestFiniteMagnitude
        stickBtn.addGestureRecognizer(drag)
        stickDrag = drag

        var menuCfg = UIButton.Configuration.filled()
        menuCfg.image = UIImage(systemName: "ellipsis", withConfiguration: symCfg)
        menuCfg.baseBackgroundColor = .clear
        menuCfg.baseForegroundColor = .label
        menuCfg.cornerStyle = .capsule
        menuCfg.contentInsets = .zero
        keysMenuBtn.configuration = menuCfg
        keysMenuBtn.accessibilityLabel = String(localized: "More Keys")
        keysMenuBtn.accessibilityIdentifier = "composer.keys"
        keysMenuBtn.showsMenuAsPrimaryAction = true
        keysMenuBtn.addAction(UIAction { [weak self] _ in self?.tick.impactOccurred() }, for: .touchDown)
        keysMenuBtn.menu = UIMenu(children: [
            UIAction(title: String(localized: "Ctrl+C"), image: UIImage(systemName: "xmark.circle")) { [weak self] _ in
                self?.terminal?.sendCtrlC()
            },
            UIAction(title: String(localized: "Shift+Tab"), image: UIImage(systemName: "arrow.left.to.line")) { [weak self] _ in
                self?.terminal?.sendShiftTab()
            },
            UIDeferredMenuElement.uncached { [weak self] done in
                var tail: [UIMenuElement] = []
                if self?.terminal?.keyboardUp == true {
                    tail.append(UIAction(title: String(localized: "Hide Keyboard"), image: UIImage(systemName: "keyboard.chevron.compact.down")) { [weak self] _ in
                        _ = self?.terminal?.resignFirstResponder()
                    })
                }
                tail.append(UIAction(title: String(localized: "Show Key Bar"), image: UIImage(systemName: "keyboard")) { [weak self] _ in
                    self?.requestClose()
                })
                done([UIMenu(options: .displayInline, children: tail)])
            },
        ])
        keysMenuBtn.translatesAutoresizingMaskIntoConstraints = false
        keysMenuBtn.heightAnchor.constraint(equalToConstant: 32).isActive = true

        let closeCfg = UIImage.SymbolConfiguration(pointSize: 11, weight: .semibold)
        let collapseBtn = quickButton(image: UIImage(systemName: "xmark", withConfiguration: closeCfg),
                                      label: String(localized: "Hide"),
                                      id: "composer.keys.collapse",
                                      tint: .secondaryLabel)
        collapseBtn.addAction(UIAction { [weak self] _ in
            self?.tick.impactOccurred()
            self?.setQuickKeysExpanded(false, animated: true)
        }, for: .touchUpInside)

        quickKeys.axis = .horizontal
        quickKeys.distribution = .fillEqually
        quickKeys.alignment = .center
        quickKeys.spacing = 0
        quickKeys.transform = CGAffineTransform(scaleX: 0.94, y: 0.94)
        quickKeys.translatesAutoresizingMaskIntoConstraints = false
        for v in [escBtn, tabBtn, enterBtn, deleteBtn, stickBtn, keysMenuBtn, collapseBtn] { quickKeys.addArrangedSubview(v) }

        quickPill.backgroundColor = .tertiarySystemFill
        quickPill.layer.cornerRadius = 18
        quickPill.clipsToBounds = true
        quickPill.alpha = 0
        quickPill.isHidden = true
        quickPill.translatesAutoresizingMaskIntoConstraints = false
        quickPill.addSubview(quickKeys)

        voiceHolder.alpha = 0
        voiceHolder.isHidden = true
        voiceHolder.translatesAutoresizingMaskIntoConstraints = false

        field.translatesAutoresizingMaskIntoConstraints = false
        fieldClip.addSubview(field)
        rowBox.translatesAutoresizingMaskIntoConstraints = false
        for v in [fieldClip, attach, micHolder, send, quickPill, voiceHolder] {
            v.translatesAutoresizingMaskIntoConstraints = false
            rowBox.addSubview(v)
        }

        let body = QuickStackView(arrangedSubviews: [strip, rowBox])
        body.axis = .vertical
        body.spacing = Self.rowGap
        body.translatesAutoresizingMaskIntoConstraints = false
        glass.contentView.addSubview(body)

        let micDown = PressWatcher(target: self, action: #selector(pressMic))
        micDown.minimumPressDuration = 0
        micDown.cancelsTouchesInView = false
        micHolder.addGestureRecognizer(micDown)

        strip.translatesAutoresizingMaskIntoConstraints = false
        fieldHeight = fieldClip.heightAnchor.constraint(equalToConstant: Self.fieldMin)
        compactConstraints = [
            fieldClip.leadingAnchor.constraint(equalTo: attach.trailingAnchor, constant: Self.buttonGap),
            fieldClip.trailingAnchor.constraint(equalTo: micHolder.leadingAnchor, constant: -Self.buttonGap),
            fieldClip.bottomAnchor.constraint(equalTo: rowBox.bottomAnchor),
        ]
        expandedConstraints = [
            fieldClip.leadingAnchor.constraint(equalTo: rowBox.leadingAnchor),
            fieldClip.trailingAnchor.constraint(equalTo: rowBox.trailingAnchor),
            fieldClip.bottomAnchor.constraint(equalTo: attach.topAnchor, constant: -Self.rowGap),
        ]
        NSLayoutConstraint.activate([
            body.leadingAnchor.constraint(equalTo: glass.contentView.leadingAnchor, constant: Self.hPad),
            body.trailingAnchor.constraint(equalTo: glass.contentView.trailingAnchor, constant: -Self.hPad),
            body.topAnchor.constraint(equalTo: glass.contentView.topAnchor, constant: Self.vPad),
            body.bottomAnchor.constraint(equalTo: glass.contentView.bottomAnchor, constant: -Self.vPad),
            fieldClip.topAnchor.constraint(equalTo: rowBox.topAnchor),
            fieldHeight,
            field.leadingAnchor.constraint(equalTo: fieldClip.leadingAnchor),
            field.trailingAnchor.constraint(equalTo: fieldClip.trailingAnchor),
            field.topAnchor.constraint(equalTo: fieldClip.topAnchor),
            field.bottomAnchor.constraint(equalTo: fieldClip.bottomAnchor),
            attach.leadingAnchor.constraint(equalTo: rowBox.leadingAnchor),
            attach.bottomAnchor.constraint(equalTo: rowBox.bottomAnchor),
            attach.widthAnchor.constraint(equalToConstant: 36),
            attach.heightAnchor.constraint(equalToConstant: 36),
            send.trailingAnchor.constraint(equalTo: rowBox.trailingAnchor),
            send.bottomAnchor.constraint(equalTo: rowBox.bottomAnchor),
            send.widthAnchor.constraint(equalToConstant: 36),
            send.heightAnchor.constraint(equalToConstant: 36),
            micHolder.trailingAnchor.constraint(equalTo: send.leadingAnchor, constant: -Self.buttonGap),
            micHolder.bottomAnchor.constraint(equalTo: rowBox.bottomAnchor),
            micHolder.widthAnchor.constraint(equalToConstant: 36),
            micHolder.heightAnchor.constraint(equalToConstant: 36),
            quickPill.leadingAnchor.constraint(equalTo: rowBox.leadingAnchor),
            quickPill.trailingAnchor.constraint(equalTo: rowBox.trailingAnchor),
            quickPill.bottomAnchor.constraint(equalTo: rowBox.bottomAnchor),
            quickPill.heightAnchor.constraint(equalToConstant: Self.fieldMin),
            quickKeys.leadingAnchor.constraint(equalTo: quickPill.leadingAnchor, constant: 2),
            quickKeys.trailingAnchor.constraint(equalTo: quickPill.trailingAnchor, constant: -2),
            quickKeys.centerYAnchor.constraint(equalTo: quickPill.centerYAnchor),
            voiceHolder.leadingAnchor.constraint(equalTo: rowBox.leadingAnchor),
            voiceHolder.trailingAnchor.constraint(equalTo: rowBox.trailingAnchor),
            voiceHolder.bottomAnchor.constraint(equalTo: rowBox.bottomAnchor),
            voiceHolder.heightAnchor.constraint(equalToConstant: Self.fieldMin),
            thumbs.leadingAnchor.constraint(equalTo: strip.contentLayoutGuide.leadingAnchor),
            thumbs.trailingAnchor.constraint(equalTo: strip.contentLayoutGuide.trailingAnchor),
            thumbs.topAnchor.constraint(equalTo: strip.contentLayoutGuide.topAnchor),
            thumbs.bottomAnchor.constraint(equalTo: strip.contentLayoutGuide.bottomAnchor),
            thumbs.heightAnchor.constraint(equalTo: strip.frameLayoutGuide.heightAnchor),
            strip.heightAnchor.constraint(equalToConstant: Self.stripHeight),
            hint.leadingAnchor.constraint(equalTo: field.frameLayoutGuide.leadingAnchor, constant: 4),
            hint.centerYAnchor.constraint(equalTo: field.frameLayoutGuide.centerYAnchor),
            hint.trailingAnchor.constraint(equalTo: field.frameLayoutGuide.trailingAnchor, constant: -4),
        ] + compactConstraints)

        var actions = [
            UIAccessibilityCustomAction(name: String(localized: "Hide Composer")) { [weak self] _ in
                self?.requestClose()
                return true
            },
        ]
        if UIDevice.current.userInterfaceIdiom != .pad {
            actions.append(UIAccessibilityCustomAction(name: String(localized: "More Keys")) { [weak self] _ in
                guard let self else { return false }
                self.setQuickKeysExpanded(!self.quickKeysExpanded, animated: true)
                return true
            })
        }
        accessibilityCustomActions = actions

        // The bar's mic dictates into the field, not the terminal.
        terminal.dictationInsert = { [weak self] text in self?.hear(text) }

        holdVoice.minimumPressDuration = 0.25
        holdVoice.allowableMovement = .greatestFiniteMagnitude
        holdVoice.delegate = self
        addGestureRecognizer(holdVoice)

        dismissPan.delegate = self
        addGestureRecognizer(dismissPan)
        if UIDevice.current.userInterfaceIdiom != .pad {
            addGestureRecognizer(swipeLeft)
            addGestureRecognizer(swipeRight)
        }
        addSubview(pad)
        NotificationCenter.default.addObserver(self, selector: #selector(keyboardFrameChanged),
                                               name: UIResponder.keyboardDidChangeFrameNotification, object: nil)
    }

    @objc private func keyboardFrameChanged() {
        guard multiLine else { return }
        setNeedsLayout()
    }

    required init?(coder: NSCoder) { fatalError() }

    override func layoutSubviews() {
        super.layoutSubviews()
        let w = rowBox.bounds.width
        let effMax = effectiveFieldMax
        if w > 0, abs(w - lastRowWidth) > 0.5 || (multiLine && abs(effMax - lastEffectiveMax) > 0.5) {
            lastRowWidth = w
            lastEffectiveMax = effMax
            updateFieldLayout(animated: false)
            super.layoutSubviews()
        }
        field.layoutIfNeeded()
        updateScrollFade()
        if hint.bounds.width > 0, hint.intrinsicContentSize.width > hint.bounds.width {
            let fade = min(24, hint.bounds.width * 0.25) / hint.bounds.width
            CATransaction.begin()
            CATransaction.setDisableActions(true)
            hintMask.frame = hint.bounds
            hintMask.locations = [0, NSNumber(value: Double(1 - fade)), 1]
            CATransaction.commit()
            hint.layer.mask = hintMask
        } else {
            hint.layer.mask = nil
        }
        if bounds.height != lastHeight {
            lastHeight = bounds.height
            onHeightChange?(bounds.height)
        }
    }

    /// Caps the text view height in tight viewports (such as landscape with
    /// the software keyboard up) so the bar never overflows the screen top.
    private var effectiveFieldMax: CGFloat {
        guard let host = superview, host.bounds.height > 0 else { return Self.fieldMax }
        let bottomY = frame.maxY > 0 ? frame.maxY : host.bounds.height - host.safeAreaInsets.bottom
        let available = bottomY - host.safeAreaInsets.top - 56
        let chrome = Self.vPad * 2 + (multiLine ? Self.fieldMin + Self.rowGap : 0) + (attachments.isEmpty ? 0 : Self.stripHeight + Self.rowGap)
        return max(56, min(Self.fieldMax, available - chrome))
    }

    /// Syncs visibility of the bottom action controls and single-row message
    /// field when `More Keys` or inline voice dictation is active. In multi-line
    /// mode, `field` sits above the 36 pt bottom row and stays visible.
    private func syncChromeVisibility(still: Bool = true) {
        let hideActions = quickKeysExpanded || voiceActive
        let hideField = hideActions && !multiLine
        let hiddenScale = still ? CGAffineTransform.identity : CGAffineTransform(scaleX: 0.96, y: 0.96)
        for v in [attach, micHolder, send] {
            v.alpha = hideActions ? 0 : 1
            v.transform = hideActions ? hiddenScale : .identity
            v.isUserInteractionEnabled = !hideActions
        }
        fieldClip.alpha = hideField ? 0 : 1
        fieldClip.transform = hideField ? hiddenScale : .identity
        fieldClip.isUserInteractionEnabled = !hideField
    }

    /// Softens scrolled multi-line edges so lines never slice abruptly against
    /// the top or bottom of `fieldClip` when `field.isScrollEnabled` is active.
    private func updateScrollFade() {
        let w = fieldClip.bounds.width, h = fieldClip.bounds.height
        guard field.isScrollEnabled, w > 0, h > 24 else {
            if fieldClip.layer.mask != nil {
                fieldClip.layer.mask = nil
                lastScrollFade = nil
            }
            return
        }
        let maxY = max(0, field.contentSize.height - field.bounds.height)
        let fadeTop = field.contentOffset.y > 1
        let fadeBottom = field.contentOffset.y < maxY - 1
        guard fadeTop || fadeBottom else {
            fieldClip.layer.mask = nil
            lastScrollFade = nil
            return
        }
        if let prev = lastScrollFade, prev.w == w, prev.h == h, prev.top == fadeTop, prev.bottom == fadeBottom {
            return
        }
        lastScrollFade = (w, h, fadeTop, fadeBottom)
        let tFrac = min(0.18, 12 / h)
        let bFrac = min(0.15, 10 / h)
        let clear = UIColor.clear.cgColor
        let solid = UIColor.black.cgColor
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        scrollFadeMask.frame = fieldClip.bounds
        scrollFadeMask.colors = [fadeTop ? clear : solid, solid, solid, fadeBottom ? clear : solid]
        scrollFadeMask.locations = [0, NSNumber(value: Double(tFrac)), NSNumber(value: Double(1 - bFrac)), 1]
        fieldClip.layer.mask = scrollFadeMask
        CATransaction.commit()
    }

    /// Reflows `field` between the compact 48 pt single-row pill and the
    /// Grok-style full-width two-row card without re-parenting `field` (keeping
    /// keyboard focus, selection, and IME marked text intact), and updates
    /// `fieldHeight` + scroll state explicitly so `isScrollEnabled` never
    /// collapses `intrinsicContentSize`.
    private func updateFieldLayout(animated: Bool) {
        let totalW = rowBox.bounds.width > 0
            ? rowBox.bounds.width
            : max(220, (bounds.width > 0 ? bounds.width : 353) - Self.hPad * 2)
        let compactControlsW = Self.fieldMin * 3 + Self.buttonGap * 3
        let compactInnerW = max(60, totalW - compactControlsW - Self.compactInsets.left - Self.compactInsets.right)
        let text = field.text ?? ""
        let hasMarked = field.markedTextRange != nil

        let wantMulti: Bool
        if text.isEmpty && !hasMarked {
            wantMulti = false
        } else if text.contains("\n") {
            wantMulti = true
        } else {
            let probeW = compactInnerW + field.textContainerInset.left + field.textContainerInset.right
            let compactFitH = field.sizeThatFits(CGSize(width: probeW, height: .greatestFiniteMagnitude)).height
            let pureTextH = compactFitH - field.textContainerInset.top - field.textContainerInset.bottom
            let lineH = (field.font ?? .systemFont(ofSize: 17)).lineHeight
            if !multiLine {
                wantMulti = pureTextH > lineH * 1.45
            } else if hasMarked {
                // Keep layout steady while composing Pinyin/Shuangpin marked text.
                wantMulti = true
            } else {
                wantMulti = pureTextH > lineH * 1.3
            }
        }

        let modeChanged = wantMulti != multiLine
        if modeChanged {
            multiLine = wantMulti
            if wantMulti {
                NSLayoutConstraint.deactivate(compactConstraints)
                NSLayoutConstraint.activate(expandedConstraints)
                field.textContainerInset = Self.expandedInsets
            } else {
                NSLayoutConstraint.deactivate(expandedConstraints)
                NSLayoutConstraint.activate(compactConstraints)
                field.textContainerInset = Self.compactInsets
            }
            syncChromeVisibility(still: true)
        }

        let targetH: CGFloat
        let tall: Bool
        if multiLine {
            let maxH = effectiveFieldMax
            lastEffectiveMax = maxH
            let rawH = ceil(field.sizeThatFits(CGSize(width: totalW, height: .greatestFiniteMagnitude)).height)
            tall = rawH > maxH + 0.5
            targetH = min(max(Self.fieldMin, rawH), maxH)
        } else {
            tall = false
            targetH = Self.fieldMin
        }

        if field.isScrollEnabled != tall {
            field.isScrollEnabled = tall
            field.showsVerticalScrollIndicator = tall
            if !tall {
                field.contentOffset = .zero
            }
            updateScrollFade()
        }

        let heightChanged = abs(fieldHeight.constant - targetH) > 0.5
        if heightChanged {
            fieldHeight.constant = targetH
        }

        if modeChanged, animated, window != nil, !isReduceMotion {
            UIView.animate(withDuration: 0.22, delay: 0, usingSpringWithDamping: 0.92, initialSpringVelocity: 0,
                           options: [.beginFromCurrentState, .allowUserInteraction]) {
                (self.superview ?? self).layoutIfNeeded()
            }
        } else if modeChanged || heightChanged {
            setNeedsLayout()
        }
    }

    /// Scrolls the active caret (including a trailing newline) cleanly into view
    /// once the message field exceeds `effectiveFieldMax`.
    private func scrollCaretToVisible(_ t: UITextView) {
        guard let end = t.selectedTextRange?.end else { return }
        rowBox.layoutIfNeeded()
        t.layoutIfNeeded()
        let caret = t.caretRect(for: end)
        let nsLen = (t.text as NSString).length
        let atEnd = t.selectedRange.location == nsLen && t.selectedRange.length == 0 && t.markedTextRange == nil
        if atEnd, t.text.hasSuffix("\n") || !caret.origin.y.isFinite || caret.isNull || caret.isInfinite {
            let bottomY = max(0, t.contentSize.height - t.bounds.height)
            if abs(t.contentOffset.y - bottomY) > 0.5 {
                t.setContentOffset(CGPoint(x: 0, y: bottomY), animated: false)
            }
            return
        }
        guard caret.origin.x.isFinite, caret.origin.y.isFinite, !caret.isNull, !caret.isInfinite else { return }
        let padded = CGRect(
            x: caret.minX,
            y: max(0, caret.minY - t.textContainerInset.top),
            width: max(1, caret.width),
            height: caret.height + t.textContainerInset.top + t.textContainerInset.bottom
        )
        t.scrollRectToVisible(padded, animated: false)
    }

    /// Where an open lands: the field's keyboard.
    func focus() { field.becomeFirstResponder() }

    /// Cross-fades the bottom row into the inline single-row voice view while
    /// dictating, keeping the resting pill height steady (and keeping any
    /// multi-line draft visible above it).
    func setVoice(active: Bool) {
        guard active != voiceActive else { return }
        voiceActive = active
        if active, quickKeysExpanded {
            setQuickKeysExpanded(false, animated: false)
        }
        let still = isReduceMotion
        if active {
            voiceHolder.isHidden = false
            voiceHolder.transform = still ? .identity : CGAffineTransform(scaleX: 0.96, y: 0.96)
            let apply = {
                self.syncChromeVisibility(still: still)
                self.voiceHolder.alpha = 1
                self.voiceHolder.transform = .identity
            }
            if still {
                UIView.animate(withDuration: 0.15, animations: apply)
            } else {
                UIView.animate(withDuration: 0.26, delay: 0, usingSpringWithDamping: 0.88, initialSpringVelocity: 0, animations: apply)
            }
        } else {
            let apply = {
                self.syncChromeVisibility(still: still)
                self.voiceHolder.alpha = 0
                self.voiceHolder.transform = still ? .identity : CGAffineTransform(scaleX: 0.96, y: 0.96)
            }
            let done: (Bool) -> Void = { _ in
                if !self.voiceActive { self.voiceHolder.isHidden = true }
            }
            if still {
                UIView.animate(withDuration: 0.15, animations: apply, completion: done)
            } else {
                UIView.animate(withDuration: 0.22, delay: 0, usingSpringWithDamping: 0.9, initialSpringVelocity: 0, animations: apply, completion: done)
            }
        }
    }

    @objc private func pressMic(_ g: UILongPressGestureRecognizer) {
        guard g.state == .began else { return }
        Haptics.tapped()
    }

    @discardableResult
    private func requestClose() -> Bool {
        guard !sending, !voiceActive else { return false }
        Haptics.tapped()
        onClose?()
        return true
    }

    @objc private func draggedDown(_ g: UIPanGestureRecognizer) {
        switch g.state {
        case .began, .changed:
            if holdingVoice {
                holdVoice.isEnabled = false
                holdVoice.isEnabled = true
            }
            let dy = max(0, g.translation(in: self).y)
            transform = CGAffineTransform(translationX: 0, y: dy * 0.5)
        case .ended:
            let dy = g.translation(in: self).y
            let vy = g.velocity(in: self).y
            if dy > 28 || vy > 300 {
                // Two-stage downward swipe (IOS-030): dismiss the software
                // keyboard first if up, then a second swipe exits Composer Mode.
                if field.isFirstResponder {
                    Haptics.tapped()
                    field.resignFirstResponder()
                    UIView.animate(withDuration: 0.2, delay: 0, usingSpringWithDamping: 0.85, initialSpringVelocity: 0) {
                        self.transform = .identity
                    }
                } else if !requestClose() {
                    UIView.animate(withDuration: 0.2, delay: 0, usingSpringWithDamping: 0.85, initialSpringVelocity: 0) {
                        self.transform = .identity
                    }
                }
            } else {
                UIView.animate(withDuration: 0.2, delay: 0, usingSpringWithDamping: 0.85, initialSpringVelocity: 0) {
                    self.transform = .identity
                }
            }
        case .cancelled, .failed:
            UIView.animate(withDuration: 0.2) { self.transform = .identity }
        default:
            break
        }
    }

    /// Points the embedded arrow stick: past a dead zone, the arrow of the
    /// drag's main axis goes at once and repeats while held.
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
            if steered { terminal?.release() }
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
        repeating = Task { [weak terminal, tick] in
            var delay = 0.4
            repeat {
                terminal?.send(next, holding: true)
                tick.impactOccurred(intensity: delay == 0.4 ? 1 : 0.5)
                try? await Task.sleep(for: .seconds(delay))
                delay = 0.08
            } while !Task.isCancelled
        }
    }

    /// Smoothly expands or collapses the inline quick-keys pill with spring
    /// physics (or a plain fade under Reduce Motion).
    private func setQuickKeysExpanded(_ expanded: Bool, animated: Bool) {
        guard expanded != quickKeysExpanded else { return }
        quickKeysExpanded = expanded
        if expanded {
            quickPill.isHidden = false
            quickPill.isUserInteractionEnabled = false
        } else {
            pressing?.cancel()
            repeating?.cancel()
            quickPill.isUserInteractionEnabled = false
        }
        let still = isReduceMotion
        if expanded {
            quickKeys.transform = still ? .identity : CGAffineTransform(scaleX: 0.94, y: 0.94)
        }
        let apply = {
            self.syncChromeVisibility(still: still)
            self.quickPill.alpha = expanded ? 1 : 0
            self.quickKeys.transform = (expanded || still) ? .identity : CGAffineTransform(scaleX: 0.94, y: 0.94)
        }
        let done: (Bool) -> Void = { _ in
            if self.quickKeysExpanded {
                self.quickPill.isUserInteractionEnabled = true
            } else {
                self.quickPill.isHidden = true
            }
        }
        guard animated else {
            apply()
            done(true)
            return
        }
        if still {
            UIView.animate(withDuration: 0.15, animations: apply, completion: done)
        } else {
            UIView.animate(withDuration: 0.32, delay: 0, usingSpringWithDamping: 0.82, initialSpringVelocity: 0.2, animations: apply, completion: done)
        }
    }

    private func cancelTrackedControls(in root: UIView) {
        if let c = root as? UIControl { c.cancelTracking(with: nil) }
        for sub in root.subviews { cancelTrackedControls(in: sub) }
    }

    @objc private func swipedLeft(_ g: UISwipeGestureRecognizer) {
        guard g.state == .ended, !quickKeysExpanded else { return }
        for v in [attach, micHolder, send] { cancelTrackedControls(in: v) }
        tick.impactOccurred()
        setQuickKeysExpanded(true, animated: true)
    }

    @objc private func swipedRight(_ g: UISwipeGestureRecognizer) {
        guard g.state == .ended, quickKeysExpanded else { return }
        cancelTrackedControls(in: quickPill)
        tick.impactOccurred()
        setQuickKeysExpanded(false, animated: true)
        focus()
    }

    override func gestureRecognizerShouldBegin(_ g: UIGestureRecognizer) -> Bool {
        if g === swipeLeft || g === swipeRight {
            guard !voiceActive, !sending, !holdingVoice else { return false }
            if g === swipeLeft, quickKeysExpanded { return false }
            if g === swipeRight, !quickKeysExpanded { return false }
            if dismissPan.state == .began || dismissPan.state == .changed { return false }
            if let stickDrag, stickDrag.state == .began || stickDrag.state == .changed { return false }
            let p = g.location(in: self)
            if !strip.isHidden, strip.frame.contains(convert(p, to: strip.superview)) {
                return false
            }
            if quickKeysExpanded, !quickPill.isHidden, stickBtn.frame.contains(convert(p, to: stickBtn.superview)) {
                return false
            }
            let inField = fieldClip.alpha > 0 && fieldClip.frame.contains(convert(p, to: rowBox))
            if inField, field.isFirstResponder, field.selectedRange.length > 0 {
                return false
            }
            return true
        }
        if g === dismissPan {
            guard !voiceActive, !sending, !holdingVoice else { return false }
            if let stickDrag, stickDrag.state == .began || stickDrag.state == .changed {
                return false
            }
            let v = dismissPan.velocity(in: self)
            guard v.y > 0, v.y > abs(v.x) else { return false }
            let p = g.location(in: self)
            if quickKeysExpanded, !quickPill.isHidden, stickBtn.frame.contains(convert(p, to: stickBtn.superview)) {
                return false
            }
            let inField = fieldClip.frame.contains(convert(p, to: rowBox))
            // Keep vertical drags inside multi-line or scrollable text dedicated
            // to UITextView cursor placement, selection handles, and scrolling.
            if inField, field.isScrollEnabled || (multiLine && field.isFirstResponder) {
                return false
            }
            return true
        }
        guard g === holdVoice else { return super.gestureRecognizerShouldBegin(g) }
        guard !voiceActive, !sending else { return false }
        if dismissPan.state == .began || dismissPan.state == .changed { return false }
        let p = g.location(in: self)
        if !attach.isHidden, attach.alpha > 0, attach.frame.contains(convert(p, to: rowBox)) { return false }
        if !quickPill.isHidden, quickPill.frame.contains(convert(p, to: rowBox)) { return false }
        if send.alpha > 0, send.frame.contains(convert(p, to: rowBox)) { return false }
        if !strip.isHidden, strip.frame.contains(convert(p, to: strip.superview)) { return false }
        // Keep UITextView's own long-press loupe/selection when text is in the
        // field, while still allowing press-and-hold on the mic or empty chrome.
        if !(field.text ?? "").isEmpty,
           fieldClip.frame.contains(convert(p, to: rowBox)),
           !micHolder.frame.contains(convert(p, to: rowBox)) {
            return false
        }
        return true
    }

    func gestureRecognizer(_ g: UIGestureRecognizer, shouldRecognizeSimultaneouslyWith other: UIGestureRecognizer) -> Bool {
        if g === dismissPan, !multiLine || !field.isFirstResponder, !field.isScrollEnabled, other.view?.isDescendant(of: field) == true {
            return true
        }
        if g === swipeLeft || g === swipeRight,
           !(field.isFirstResponder && field.selectedRange.length > 0),
           other.view?.isDescendant(of: field) == true {
            return true
        }
        return false
    }

    func gestureRecognizer(_ g: UIGestureRecognizer, shouldBeRequiredToFailBy other: UIGestureRecognizer) -> Bool {
        if g === swipeRight, quickKeysExpanded, other.view?.isDescendant(of: self) != true {
            return true
        }
        if g === swipeLeft, !quickKeysExpanded, other.view?.isDescendant(of: self) != true {
            return true
        }
        return false
    }

    @objc private func heldForVoice(_ g: UILongPressGestureRecognizer) {
        switch g.state {
        case .began:
            holdingVoice = true
            Haptics.refreshed()
            onHoldVoiceStart? { [weak self] in self?.holdingVoice == true }
        case .ended:
            let was = holdingVoice
            holdingVoice = false
            if was {
                Haptics.tapped()
                onHoldVoiceFinish?()
            }
        case .cancelled, .failed:
            let was = holdingVoice
            holdingVoice = false
            if was { onHoldVoiceCancel?() }
        default:
            break
        }
    }

    /// Pastes clipboard text into the compose field at the current selection
    /// (or at the end when unfocused).
    private func pasteClipboard() {
        guard let text = UIPasteboard.general.string, !text.isEmpty else { return }
        if let range = field.selectedTextRange {
            field.replace(range, withText: text)
        } else {
            field.text = (field.text ?? "") + text
        }
        textViewDidChange(field)
    }

    /// What the mic recognised, into the field where it can be read and
    /// edited before Send, spaced from what is already there.
    private func hear(_ text: String) {
        if !field.text.isEmpty, !field.text.hasSuffix(" "), !field.text.hasSuffix("\n"), !text.hasPrefix(" ") {
            field.text += " "
        }
        field.text += text
        textViewDidChange(field)
    }

    func textViewDidChange(_ t: UITextView) {
        hint.isHidden = !t.text.isEmpty || t.markedTextRange != nil
        updateFieldLayout(animated: true)
        update()
        if t.isScrollEnabled {
            scrollCaretToVisible(t)
        }
    }

    func textViewDidChangeSelection(_ t: UITextView) {
        guard t.isScrollEnabled, !t.isDragging, !t.isDecelerating else { return }
        scrollCaretToVisible(t)
    }

    func scrollViewDidScroll(_ scrollView: UIScrollView) {
        guard scrollView === field else { return }
        updateScrollFade()
    }

    /// The photo picker over the terminal; each pick is staged as its
    /// original file and joins the strip, as Add Photo's are.
    private func addPhotos() {
        field.resignFirstResponder()
        terminal?.present(PhotoPicker { [weak self] providers in
            guard let self, !providers.isEmpty else { return }
            Task {
                do {
                    for p in providers {
                        let url = try await TerminalView.photo(p, into: nextDir())
                        stage(Attachment(url: url, thumb: UIImage(contentsOfFile: url.path())))
                    }
                } catch {
                    failed(error)
                }
                focus()
            }
        })
    }

    /// The camera over the terminal, where the device has one; the shot
    /// is written as a JPEG and joins the strip.
    private func takePhoto() {
        field.resignFirstResponder()
        let p = UIImagePickerController()
        p.sourceType = .camera
        let shot = CameraShot { [weak self] image in
            guard let self else { return }
            defer { focus() }
            guard let image else { return }
            do {
                guard let data = image.jpegData(compressionQuality: 0.9) else { throw CocoaError(.fileWriteUnknown) }
                let into = nextDir()
                try FileManager.default.createDirectory(at: into, withIntermediateDirectories: true)
                let url = into.appending(path: Self.shotName())
                try data.write(to: url)
                stage(Attachment(url: url, thumb: image))
            } catch {
                failed(error)
            }
        }
        self.shot = shot
        p.delegate = shot
        terminal?.present(p)
    }

    /// A shot's file name, as the camera's are.
    private static func shotName() -> String {
        let f = DateFormatter()
        f.dateFormat = "yyyyMMdd-HHmmss"
        return "Photo-\(f.string(from: Date())).JPG"
    }

    /// The document picker over the terminal; its copies move into the
    /// folder and join the strip, as Add File's do.
    private func addFiles() {
        field.resignFirstResponder()
        let p = UIDocumentPickerViewController(forOpeningContentTypes: [.item], asCopy: true)
        p.allowsMultipleSelection = true
        let pick = FilePick { [weak self] urls in
            guard let self else { return }
            let into = nextDir()
            Task {
                do {
                    let moved = try await Task.detached { try TerminalView.stage(urls, into: into) }.value
                    moved.forEach { stage(Attachment(url: $0, thumb: nil)) }
                } catch {
                    failed(error)
                }
                focus()
            }
        }
        filePick = pick
        p.delegate = pick
        terminal?.present(p)
    }

    /// The staging folder of the next attachment.
    private func nextDir() -> URL {
        defer { staged += 1 }
        return dir.appending(path: String(staged))
    }

    /// An attachment joins the strip, which shows once it has one.
    private func stage(_ a: Attachment) {
        attachments.append(a)
        thumbs.addArrangedSubview(cell(a))
        strip.isHidden = false
        updateFieldLayout(animated: false)
        update()
    }

    /// Takes an attachment out of the strip, and its file with it.
    private func remove(_ a: Attachment, _ cell: UIView) {
        attachments.removeAll { $0.url == a.url }
        cell.removeFromSuperview()
        strip.isHidden = attachments.isEmpty
        let url = a.url.deletingLastPathComponent()
        Task.detached { try? FileManager.default.removeItem(at: url) }
        updateFieldLayout(animated: false)
        update()
    }

    /// Send lights up with the app's accent fill while a message or attachment
    /// is ready to send.
    private func update() {
        let on = !sending && (!(field.text ?? "").trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || !attachments.isEmpty)
        send.isEnabled = on
        send.setNeedsUpdateConfiguration()
    }

    /// Uploads the attachments as one batch — the server types their
    /// paths together once every file is up — then pastes the text and
    /// sends Return. A failure alerts and keeps the message; a send that
    /// went empties the bar for the next one.
    func sendNow() {
        guard let terminal, !sending else { return }
        let hasContent = !(field.text ?? "").trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || !attachments.isEmpty
        guard hasContent else { return }
        sending = true
        attach.isEnabled = false
        update()
        terminal.bar.uploading += 1
        Task { [weak self] in
            guard let self else { return }
            defer {
                sending = false
                attach.isEnabled = true
                terminal.bar.uploading -= 1
                update()
            }
            do {
                let files = attachments.map(\.url)
                let batchID = "composer-\(UUID().uuidString.lowercased())"
                for (i, f) in files.enumerated() {
                    try await terminal.session.host.upload(f, block: terminal.session.block, session: terminal.session.session,
                                                           batch: files.count > 1 ? (batchID, i, files.count) : nil)
                }
            } catch {
                failed(error)
                return
            }
            let text = field.text ?? ""
            let hasText = !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            if hasText {
                terminal.insert(text)
            }
            // Separate Enter from the preceding bracketed paste / attachment paths
            // so TUI agents (Devin CLI, Codex, Claude Code) don't swallow Return
            // inside their paste-burst window, and clear any held key-bar modifiers
            // so Enter is always an unmodified submit.
            if hasText || !attachments.isEmpty {
                try? await Task.sleep(for: .milliseconds(150))
            }
            terminal.release()
            terminal.send("enter")
            reset()
        }
    }

    /// A sent bar empties for the next message: its field, its strip and
    /// its staging folder, which is made again as it fills.
    private func reset() {
        field.text = ""
        for c in thumbs.arrangedSubviews { c.removeFromSuperview() }
        attachments = []
        strip.isHidden = true
        let dir = dir
        Task.detached { try? FileManager.default.removeItem(at: dir) }
        textViewDidChange(field)
    }

    /// An attachment's thumbnail, or a document mark, with a 44 × 44 pt remove
    /// button and inset circular × badge completely inside the thumbnail bounds.
    private func cell(_ a: Attachment) -> UIView {
        let c = UIView()
        c.translatesAutoresizingMaskIntoConstraints = false
        c.backgroundColor = .secondarySystemFill
        c.layer.cornerRadius = Self.thumbRadius
        c.layer.cornerCurve = .continuous
        c.clipsToBounds = true
        c.accessibilityIdentifier = "composer.attachment"
        c.accessibilityContainerType = .semanticGroup
        let body = a.thumb.map { UIImageView(image: $0) } ?? UIImageView(image: UIImage(systemName: "doc.fill"))
        body.contentMode = a.thumb == nil ? .center : .scaleAspectFill
        body.tintColor = .secondaryLabel
        body.isAccessibilityElement = false
        body.translatesAutoresizingMaskIntoConstraints = false
        c.addSubview(body)

        let badge = UIView()
        badge.isUserInteractionEnabled = false
        badge.isAccessibilityElement = false
        badge.backgroundColor = UIColor(white: 0.12, alpha: 0.82)
        badge.layer.cornerRadius = Self.removeBadgeSize / 2
        badge.layer.borderWidth = 0.75
        badge.layer.borderColor = UIColor.white.withAlphaComponent(0.32).cgColor
        badge.translatesAutoresizingMaskIntoConstraints = false

        let glyphCfg = UIImage.SymbolConfiguration(pointSize: 10, weight: .bold)
        let glyph = UIImageView(
            image: UIImage(systemName: "xmark", withConfiguration: glyphCfg)?
                .withTintColor(.white, renderingMode: .alwaysOriginal)
        )
        glyph.isUserInteractionEnabled = false
        glyph.isAccessibilityElement = false
        glyph.translatesAutoresizingMaskIntoConstraints = false
        badge.addSubview(glyph)

        var btnCfg = UIButton.Configuration.plain()
        btnCfg.background.backgroundColor = .clear
        btnCfg.contentInsets = .zero
        let x = UIButton(configuration: btnCfg, primaryAction: UIAction { [weak self, weak c] _ in
            Haptics.tapped()
            if let c { self?.remove(a, c) }
        })
        x.configurationUpdateHandler = { [weak badge] btn in
            UIView.performWithoutAnimation {
                badge?.transform = btn.isHighlighted ? CGAffineTransform(scaleX: 0.9, y: 0.9) : .identity
                badge?.alpha = btn.isHighlighted ? 0.85 : 1
            }
        }
        x.accessibilityLabel = String(localized: "Remove Attachment")
        x.accessibilityIdentifier = "composer.attachment.remove"
        x.translatesAutoresizingMaskIntoConstraints = false
        x.addSubview(badge)
        c.addSubview(x)

        NSLayoutConstraint.activate([
            c.widthAnchor.constraint(equalToConstant: Self.thumbSize),
            c.heightAnchor.constraint(equalToConstant: Self.thumbSize),
            body.leadingAnchor.constraint(equalTo: c.leadingAnchor),
            body.trailingAnchor.constraint(equalTo: c.trailingAnchor),
            body.topAnchor.constraint(equalTo: c.topAnchor),
            body.bottomAnchor.constraint(equalTo: c.bottomAnchor),
            x.topAnchor.constraint(equalTo: c.topAnchor),
            x.trailingAnchor.constraint(equalTo: c.trailingAnchor),
            x.widthAnchor.constraint(equalToConstant: Self.removeHitSize),
            x.heightAnchor.constraint(equalToConstant: Self.removeHitSize),
            badge.topAnchor.constraint(equalTo: x.topAnchor, constant: Self.removeBadgeInset),
            badge.trailingAnchor.constraint(equalTo: x.trailingAnchor, constant: -Self.removeBadgeInset),
            badge.widthAnchor.constraint(equalToConstant: Self.removeBadgeSize),
            badge.heightAnchor.constraint(equalToConstant: Self.removeBadgeSize),
            glyph.centerXAnchor.constraint(equalTo: badge.centerXAnchor),
            glyph.centerYAnchor.constraint(equalTo: badge.centerYAnchor),
        ])
        return c
    }

    private func failed(_ error: Error) {
        Toasts.shared.fail("Can't Upload", error)
    }
}

/// Text view that keeps `contentOffset` pinned to `.zero` while non-scrolling,
/// preventing UIKit from synchronously shifting Line 1 out of view when a
/// newline or wrap arrives before Auto Layout updates the view's height.
private final class ComposeTextView: UITextView {
    override var contentOffset: CGPoint {
        get { super.contentOffset }
        set { super.contentOffset = isScrollEnabled ? newValue : .zero }
    }
}

/// Inner container of `ComposerBar` that extends vertical hit-testing into the
/// outer 6 pt concentric padding in both single-row and multi-line layouts so
/// 36 pt controls at the bottom edge always receive their full 44 pt tap target.
private final class RowBoxView: UIView {
    override func point(inside point: CGPoint, with event: UIEvent?) -> Bool {
        bounds.insetBy(dx: -4, dy: -6).contains(point)
    }
}

/// Stack view that extends its vertical hit-test area to Apple HIG's 44 pt
/// minimum so touches near the pill's outer edges reach child controls instead
/// of clipping at the 32–36 pt stack bounds.
private final class QuickStackView: UIStackView {
    override func point(inside point: CGPoint, with event: UIEvent?) -> Bool {
        let dy = max(0, (44 - bounds.height) / 2)
        return bounds.insetBy(dx: 0, dy: -dy).contains(point)
    }
}

/// Container that extends its vertical hit-test area to Apple HIG's 44 pt
/// minimum so 36 pt controls inside the 48 pt pill stay visually compact
/// without sacrificing vertical tap comfort.
private final class QuickPillView: UIView {
    override func point(inside point: CGPoint, with event: UIEvent?) -> Bool {
        let dy = max(0, (44 - bounds.height) / 2)
        return bounds.insetBy(dx: 0, dy: -dy).contains(point)
    }
}

/// Button with a 44 pt minimum vertical hit target.
private final class QuickKeyButton: UIButton {
    override func point(inside point: CGPoint, with event: UIEvent?) -> Bool {
        let dy = max(0, (44 - bounds.height) / 2)
        return bounds.insetBy(dx: 0, dy: -dy).contains(point)
    }
}

/// The camera's delegate for the composer: its shot, or nil on Cancel.
private final class CameraShot: NSObject, UIImagePickerControllerDelegate, UINavigationControllerDelegate {
    private let picked: (UIImage?) -> Void

    init(picked: @escaping (UIImage?) -> Void) { self.picked = picked }

    func imagePickerController(_ picker: UIImagePickerController, didFinishPickingMediaWithInfo info: [UIImagePickerController.InfoKey: Any]) {
        picked(info[.originalImage] as? UIImage)
        picker.dismiss(animated: true)
    }

    func imagePickerControllerDidCancel(_ picker: UIImagePickerController) {
        picker.dismiss(animated: true)
    }
}

/// The document picker's delegate for the composer: the copies it made.
private final class FilePick: NSObject, UIDocumentPickerDelegate {
    private let picked: ([URL]) -> Void

    init(picked: @escaping ([URL]) -> Void) { self.picked = picked }

    func documentPicker(_ controller: UIDocumentPickerViewController, didPickDocumentsAt urls: [URL]) {
        picked(urls)
    }
}
