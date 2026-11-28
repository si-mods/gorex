import UIKit
import UniformTypeIdentifiers

/// The compose bar (docs/IOS-025-composer.md, docs/IOS-030-composer-centric-bar.md),
/// up while Settings > Terminal's Composer Mode is on: a single-row glass pill
/// pinned above the keyboard or home indicator — a circular attach/actions
/// menu on the left, a message field that names its block and grows upward with
/// its lines, and the screen's mic plus Send on the right — over the terminal,
/// which stays in view with the keyboard. Typing and dictation land in the
/// field to read before Send. On iPhone, `More Keys` in `+` unfurls the inline
/// 7-key strip across the pill on demand. The attachments stage in a folder of
/// the temporary directory, emptied as a send goes. Send uploads them as one
/// batch, whose paths the server types together once every file is up, then
/// pastes the text and sends Return: the paths land first and the lot is one
/// submission. A send with no text submits the paths alone, and empties the bar
/// for the next message.
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

    let field = UITextView()
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
    /// the single row from `+` › `More Keys` (temporarily fading `inputRow`) so
    /// all 7 keys meet Apple HIG's 44 × 44 pt minimum tap target.
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
    /// Where the screen's mic lands, between the message field and Send.
    let micHolder: UIView = QuickPillView()
    /// Where the screen's single-row voice controls land while dictating,
    /// cross-fading with `inputRow` inside the compose pill.
    let voiceHolder = UIView()
    private let inputRow = QuickStackView()
    private var voiceActive = false
    /// Press-and-hold anywhere on the compose bar to dictate (push-to-talk):
    /// holding starts voice recording, releasing converts speech to text.
    private lazy var holdVoice = UILongPressGestureRecognizer(target: self, action: #selector(heldForVoice))
    private var holdingVoice = false
    /// Downward drag or flick anywhere on the bar dismisses the keyboard first
    /// if up, and closes Composer Mode on the next swipe.
    private lazy var dismissPan = UIPanGestureRecognizer(target: self, action: #selector(draggedDown))
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
    /// Gap between the compose bar and the keyboard or safe area below it.
    static let gap: CGFloat = 6
    /// Resting height of the single-row pill with no attachments (48 pt).
    static let baseHeight: CGFloat = vPad + fieldMin + vPad
    /// Bottom inset reserved so the resting bar never covers the program's own
    /// input row (54 pt).
    static let baseInset: CGFloat = baseHeight + gap
    /// How tall the field grows before it scrolls instead.
    private static let fieldMax: CGFloat = 112

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
        field.backgroundColor = .clear
        field.textContainerInset = UIEdgeInsets(top: 7.5, left: 4, bottom: 7.5, right: 4)
        field.textContainer.lineFragmentPadding = 0
        field.isScrollEnabled = false
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

        var s = UIButton.Configuration.filled()
        s.image = UIImage(systemName: "arrow.up")
        s.preferredSymbolConfigurationForImage = UIImage.SymbolConfiguration(pointSize: 15, weight: .semibold)
        s.baseBackgroundColor = .tertiarySystemFill
        s.baseForegroundColor = .label
        s.background.cornerRadius = 18
        send.configuration = s
        send.accessibilityLabel = String(localized: "Send")
        send.isEnabled = false
        send.alpha = 0.45
        send.addAction(UIAction { _ in Haptics.tapped() }, for: .touchDown)
        send.addAction(UIAction { [weak self] _ in self?.sendNow() }, for: .touchUpInside)

        thumbs.axis = .horizontal
        thumbs.spacing = 10
        thumbs.alignment = .center
        thumbs.translatesAutoresizingMaskIntoConstraints = false
        strip.addSubview(thumbs)
        strip.contentInset = UIEdgeInsets(top: 4, left: 6, bottom: 0, right: 6)
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

        for v in [attach, field, micHolder, send] { inputRow.addArrangedSubview(v) }
        inputRow.axis = .horizontal
        inputRow.alignment = .bottom
        inputRow.spacing = 6
        inputRow.translatesAutoresizingMaskIntoConstraints = false

        voiceHolder.alpha = 0
        voiceHolder.isHidden = true
        voiceHolder.translatesAutoresizingMaskIntoConstraints = false

        let rowBox = QuickPillView()
        rowBox.translatesAutoresizingMaskIntoConstraints = false
        rowBox.addSubview(inputRow)
        rowBox.addSubview(quickPill)
        rowBox.addSubview(voiceHolder)

        let body = QuickStackView(arrangedSubviews: [strip, rowBox])
        body.axis = .vertical
        body.spacing = 8
        body.translatesAutoresizingMaskIntoConstraints = false
        glass.contentView.addSubview(body)

        let micDown = PressWatcher(target: self, action: #selector(pressMic))
        micDown.minimumPressDuration = 0
        micDown.cancelsTouchesInView = false
        micHolder.addGestureRecognizer(micDown)

        attach.translatesAutoresizingMaskIntoConstraints = false
        field.translatesAutoresizingMaskIntoConstraints = false
        send.translatesAutoresizingMaskIntoConstraints = false
        strip.translatesAutoresizingMaskIntoConstraints = false
        micHolder.translatesAutoresizingMaskIntoConstraints = false
        let inputBottomFit = inputRow.bottomAnchor.constraint(equalTo: rowBox.bottomAnchor)
        inputBottomFit.priority = .defaultHigh
        NSLayoutConstraint.activate([
            body.leadingAnchor.constraint(equalTo: glass.contentView.leadingAnchor, constant: Self.hPad),
            body.trailingAnchor.constraint(equalTo: glass.contentView.trailingAnchor, constant: -Self.hPad),
            body.topAnchor.constraint(equalTo: glass.contentView.topAnchor, constant: Self.vPad),
            body.bottomAnchor.constraint(equalTo: glass.contentView.bottomAnchor, constant: -Self.vPad),
            inputRow.leadingAnchor.constraint(equalTo: rowBox.leadingAnchor),
            inputRow.trailingAnchor.constraint(equalTo: rowBox.trailingAnchor),
            inputRow.topAnchor.constraint(equalTo: rowBox.topAnchor),
            inputRow.bottomAnchor.constraint(lessThanOrEqualTo: rowBox.bottomAnchor),
            inputBottomFit,
            quickPill.leadingAnchor.constraint(equalTo: rowBox.leadingAnchor),
            quickPill.trailingAnchor.constraint(equalTo: rowBox.trailingAnchor),
            quickPill.bottomAnchor.constraint(equalTo: rowBox.bottomAnchor),
            quickPill.heightAnchor.constraint(equalToConstant: Self.fieldMin),
            quickKeys.leadingAnchor.constraint(equalTo: quickPill.leadingAnchor, constant: 2),
            quickKeys.trailingAnchor.constraint(equalTo: quickPill.trailingAnchor, constant: -2),
            quickKeys.centerYAnchor.constraint(equalTo: quickPill.centerYAnchor),
            voiceHolder.leadingAnchor.constraint(equalTo: rowBox.leadingAnchor),
            voiceHolder.trailingAnchor.constraint(equalTo: rowBox.trailingAnchor),
            voiceHolder.centerYAnchor.constraint(equalTo: rowBox.centerYAnchor),
            voiceHolder.heightAnchor.constraint(equalToConstant: Self.fieldMin),
            thumbs.leadingAnchor.constraint(equalTo: strip.contentLayoutGuide.leadingAnchor),
            thumbs.trailingAnchor.constraint(equalTo: strip.contentLayoutGuide.trailingAnchor),
            thumbs.topAnchor.constraint(equalTo: strip.contentLayoutGuide.topAnchor),
            thumbs.bottomAnchor.constraint(equalTo: strip.contentLayoutGuide.bottomAnchor),
            thumbs.heightAnchor.constraint(equalTo: strip.frameLayoutGuide.heightAnchor, constant: -4),
            strip.heightAnchor.constraint(equalToConstant: 52),
            attach.widthAnchor.constraint(equalToConstant: 36),
            attach.heightAnchor.constraint(equalToConstant: 36),
            micHolder.widthAnchor.constraint(equalToConstant: 36),
            micHolder.heightAnchor.constraint(equalToConstant: 36),
            send.widthAnchor.constraint(equalToConstant: 36),
            send.heightAnchor.constraint(equalToConstant: 36),
            hint.leadingAnchor.constraint(equalTo: field.frameLayoutGuide.leadingAnchor, constant: 4),
            hint.centerYAnchor.constraint(equalTo: field.frameLayoutGuide.centerYAnchor),
            hint.trailingAnchor.constraint(equalTo: field.frameLayoutGuide.trailingAnchor, constant: -4),
            field.heightAnchor.constraint(greaterThanOrEqualToConstant: Self.fieldMin),
            field.heightAnchor.constraint(lessThanOrEqualToConstant: Self.fieldMax),
        ])

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
        addSubview(pad)
    }

    required init?(coder: NSCoder) { fatalError() }

    override func layoutSubviews() {
        super.layoutSubviews()
        field.layoutIfNeeded()
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

    /// Where an open lands: the field's keyboard.
    func focus() { field.becomeFirstResponder() }

    /// Cross-fades the input row into the inline single-row voice view while
    /// dictating, keeping the resting pill height steady without bouncing the
    /// terminal above it.
    func setVoice(active: Bool) {
        guard active != voiceActive else { return }
        voiceActive = active
        if active, quickKeysExpanded {
            setQuickKeysExpanded(false, animated: false)
        }
        let still = UIAccessibility.isReduceMotionEnabled
        if active {
            voiceHolder.isHidden = false
            voiceHolder.transform = still ? .identity : CGAffineTransform(scaleX: 0.96, y: 0.96)
            inputRow.isUserInteractionEnabled = false
            let apply = {
                self.inputRow.alpha = 0
                self.inputRow.transform = still ? .identity : CGAffineTransform(scaleX: 0.96, y: 0.96)
                self.voiceHolder.alpha = 1
                self.voiceHolder.transform = .identity
            }
            if still {
                UIView.animate(withDuration: 0.15, animations: apply)
            } else {
                UIView.animate(withDuration: 0.26, delay: 0, usingSpringWithDamping: 0.88, initialSpringVelocity: 0, animations: apply)
            }
        } else {
            inputRow.isUserInteractionEnabled = true
            let apply = {
                self.inputRow.alpha = 1
                self.inputRow.transform = .identity
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
            inputRow.isUserInteractionEnabled = false
        } else {
            quickPill.isUserInteractionEnabled = false
            inputRow.isUserInteractionEnabled = true
        }
        let still = UIAccessibility.isReduceMotionEnabled
        if expanded {
            quickKeys.transform = still ? .identity : CGAffineTransform(scaleX: 0.94, y: 0.94)
        }
        let apply = {
            self.inputRow.alpha = expanded ? 0 : 1
            self.inputRow.transform = (expanded && !still) ? CGAffineTransform(scaleX: 0.96, y: 0.96) : .identity
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

    override func gestureRecognizerShouldBegin(_ g: UIGestureRecognizer) -> Bool {
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
            if field.isScrollEnabled, field.frame.contains(convert(p, to: field.superview)) {
                return false
            }
            return true
        }
        guard g === holdVoice else { return super.gestureRecognizerShouldBegin(g) }
        guard !voiceActive, !sending else { return false }
        if dismissPan.state == .began || dismissPan.state == .changed { return false }
        let p = g.location(in: self)
        if !attach.isHidden, attach.frame.contains(convert(p, to: attach.superview)) { return false }
        if !quickPill.isHidden, quickPill.frame.contains(convert(p, to: quickPill.superview)) { return false }
        if send.frame.contains(convert(p, to: send.superview)) { return false }
        if !strip.isHidden, strip.frame.contains(convert(p, to: strip.superview)) { return false }
        // Keep UITextView's own long-press loupe/selection when editing
        // non-empty text, while still allowing press-and-hold on the mic.
        if field.isFirstResponder, !(field.text ?? "").isEmpty,
           !micHolder.frame.contains(convert(p, to: micHolder.superview)) {
            return false
        }
        return true
    }

    func gestureRecognizer(_ g: UIGestureRecognizer, shouldRecognizeSimultaneouslyWith other: UIGestureRecognizer) -> Bool {
        if g === dismissPan, !field.isScrollEnabled, other.view?.isDescendant(of: field) == true {
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
        hint.isHidden = !t.text.isEmpty
        let tall = t.contentSize.height > Self.fieldMax
        if t.isScrollEnabled != tall {
            t.isScrollEnabled = tall
            t.setNeedsLayout()
        }
        update()
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
        update()
    }

    /// Takes an attachment out of the strip, and its file with it.
    private func remove(_ a: Attachment, _ cell: UIView) {
        attachments.removeAll { $0.url == a.url }
        cell.removeFromSuperview()
        strip.isHidden = attachments.isEmpty
        let url = a.url.deletingLastPathComponent()
        Task.detached { try? FileManager.default.removeItem(at: url) }
        update()
    }

    /// Send is live while the message or an attachment is there to send.
    private func update() {
        let on = !sending && (!field.text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || !attachments.isEmpty)
        send.isEnabled = on
        send.alpha = on ? 1 : 0.45
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
        update()
        attach.isEnabled = false
        send.configuration?.showsActivityIndicator = true
        terminal.bar.uploading += 1
        Task { [weak self] in
            guard let self else { return }
            defer {
                sending = false
                attach.isEnabled = true
                send.configuration?.showsActivityIndicator = false
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

    /// An attachment's thumbnail, or a document mark, with the mark that
    /// takes it out of the strip.
    private func cell(_ a: Attachment) -> UIView {
        let c = UIView()
        c.translatesAutoresizingMaskIntoConstraints = false
        c.backgroundColor = .secondarySystemFill
        c.layer.cornerRadius = 10
        c.clipsToBounds = true
        let body = a.thumb.map { UIImageView(image: $0) } ?? UIImageView(image: UIImage(systemName: "doc.fill"))
        body.contentMode = a.thumb == nil ? .center : .scaleAspectFill
        body.tintColor = .secondaryLabel
        body.translatesAutoresizingMaskIntoConstraints = false
        c.addSubview(body)
        let x = UIButton(configuration: {
            var b = UIButton.Configuration.plain()
            b.image = UIImage(systemName: "xmark.circle.fill")
            b.preferredSymbolConfigurationForImage = UIImage.SymbolConfiguration(pointSize: 14)
            b.baseForegroundColor = .systemGray
            b.contentInsets = .zero
            return b
        }(), primaryAction: UIAction { [weak self, weak c] _ in if let c { self?.remove(a, c) } })
        x.accessibilityLabel = String(localized: "Remove Attachment")
        x.translatesAutoresizingMaskIntoConstraints = false
        c.addSubview(x)
        NSLayoutConstraint.activate([
            c.widthAnchor.constraint(equalToConstant: 44),
            c.heightAnchor.constraint(equalToConstant: 44),
            body.leadingAnchor.constraint(equalTo: c.leadingAnchor),
            body.trailingAnchor.constraint(equalTo: c.trailingAnchor),
            body.topAnchor.constraint(equalTo: c.topAnchor),
            body.bottomAnchor.constraint(equalTo: c.bottomAnchor),
            x.topAnchor.constraint(equalTo: c.topAnchor, constant: -4),
            x.trailingAnchor.constraint(equalTo: c.trailingAnchor, constant: 4),
            x.widthAnchor.constraint(equalToConstant: 20),
            x.heightAnchor.constraint(equalToConstant: 20),
        ])
        return c
    }

    private func failed(_ error: Error) {
        Toasts.shared.fail("Can't Upload", error)
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
