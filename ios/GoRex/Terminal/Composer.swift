import UIKit
import UniformTypeIdentifiers

/// The compose bar (docs/IOS-025-composer.md), up while Settings >
/// Terminal's Composer Mode is on: a glass capsule pinned over the key
/// bar — a message field that names its block and grows with its lines,
/// an attach menu, the screen's mic, and a send — over the terminal,
/// which stays in view with the keyboard. Typing and dictation land in
/// the field to read before Send. The attachments stage in a folder of
/// the temporary directory, emptied as a send goes. Send uploads them as
/// one batch, whose paths the server types together once every file is
/// up, then pastes the text and sends Return: the paths land first and
/// the lot is one submission. A send with no text submits the paths
/// alone, and empties the bar for the next message.
final class ComposerBar: UIView, UITextViewDelegate {
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
    private let attach = UIButton()
    private let send = UIButton()
    /// Where the screen's mic lands, next to Send.
    let micHolder = UIView()
    /// Where the screen's voice controls land while dictating, replacing the
    /// input row inline inside the compose capsule instead of the key bar.
    let voiceHolder = UIView()
    private let inputRow = UIStackView()
    private var voiceActive = false
    /// The attachments' thumbnails, over the input row while there are any.
    private let strip = UIScrollView()
    private let thumbs = UIStackView()
    /// Called when the bar's laid-out height changes (multi-line growth or
    /// attachments), so the host can slide the terminal and bottom edge.
    var onHeightChange: ((CGFloat) -> Void)?
    private var lastHeight: CGFloat = 0
    /// Minimum height of the single-line message field and vertical padding
    /// inside the capsule; together they fix the bar's resting height.
    private static let fieldMin: CGFloat = 46
    private static let vPad: CGFloat = 6
    /// Gap between the compose bar and the bar below it.
    static let gap: CGFloat = 6
    /// Resting height of the single-line bar with no attachments.
    static let baseHeight: CGFloat = fieldMin + vPad * 2
    /// Bottom inset reserved in the terminal grid so the resting bar never
    /// covers the program's own input row.
    static let baseInset: CGFloat = baseHeight + gap
    /// How tall the field grows before it scrolls instead.
    private static let fieldMax: CGFloat = 112

    init(terminal: TerminalView) {
        self.terminal = terminal
        dir = FileManager.default.temporaryDirectory.appending(path: "Composer").appending(path: UUID().uuidString)
        super.init(frame: .zero)
        accessibilityIdentifier = "composer"

        let glass = UIVisualEffectView(effect: UIGlassEffect(style: .regular))
        glass.cornerConfiguration = .uniformCorners(radius: 26)
        glass.clipsToBounds = true
        glass.translatesAutoresizingMaskIntoConstraints = false
        addSubview(glass)
        NSLayoutConstraint.activate([
            glass.leadingAnchor.constraint(equalTo: leadingAnchor),
            glass.trailingAnchor.constraint(equalTo: trailingAnchor),
            glass.topAnchor.constraint(equalTo: topAnchor),
            glass.bottomAnchor.constraint(equalTo: bottomAnchor),
        ])

        var cfg = UIButton.Configuration.plain()
        cfg.image = UIImage(systemName: "plus")
        cfg.preferredSymbolConfigurationForImage = UIImage.SymbolConfiguration(pointSize: 18, weight: .medium)
        cfg.baseForegroundColor = .label
        attach.configuration = cfg
        attach.accessibilityLabel = String(localized: "Add Attachment")
        var items = [
            UIAction(title: String(localized: "Photo"), image: UIImage(systemName: "photo")) { [weak self] _ in self?.addPhotos() },
        ]
        if UIImagePickerController.isSourceTypeAvailable(.camera) {
            items.append(UIAction(title: String(localized: "Camera"), image: UIImage(systemName: "camera")) { [weak self] _ in self?.takePhoto() })
        }
        items.append(UIAction(title: String(localized: "File"), image: UIImage(systemName: "doc")) { [weak self] _ in self?.addFiles() })
        attach.menu = UIMenu(children: items)
        attach.showsMenuAsPrimaryAction = true
        attach.isHidden = !terminal.session.host.canUpload

        field.font = .systemFont(ofSize: 17)
        field.backgroundColor = .clear
        field.textContainerInset = UIEdgeInsets(top: 11, left: 4, bottom: 11, right: 4)
        field.textContainer.lineFragmentPadding = 0
        field.isScrollEnabled = false
        field.delegate = self
        field.accessibilityLabel = String(localized: "Message")
        field.accessibilityIdentifier = "composer.message"

        let block = terminal.session.host.blocks[terminal.session.block]
        let name = block?.title.flatMap { $0.isEmpty ? nil : $0 } ?? block?.program.flatMap { $0.isEmpty ? nil : $0 }
        hint.text = name.map { String(format: String(localized: "Message to %@"), $0) } ?? String(localized: "Message")
        hint.font = field.font
        hint.textColor = .placeholderText
        hint.isUserInteractionEnabled = false
        hint.translatesAutoresizingMaskIntoConstraints = false
        field.addSubview(hint)

        var s = UIButton.Configuration.filled()
        s.image = UIImage(systemName: "arrow.up")
        s.preferredSymbolConfigurationForImage = UIImage.SymbolConfiguration(pointSize: 14, weight: .semibold)
        s.baseBackgroundColor = .systemGray5
        s.baseForegroundColor = .label
        s.background.cornerRadius = 15
        send.configuration = s
        send.accessibilityLabel = String(localized: "Send")
        send.isEnabled = false
        send.alpha = 0.4
        send.addAction(UIAction { _ in Haptics.tapped() }, for: .touchDown)
        send.addAction(UIAction { [weak self] _ in self?.sendNow() }, for: .touchUpInside)

        thumbs.axis = .horizontal
        thumbs.spacing = 10
        thumbs.alignment = .center
        thumbs.translatesAutoresizingMaskIntoConstraints = false
        strip.addSubview(thumbs)
        strip.showsHorizontalScrollIndicator = false
        strip.isHidden = true

        for v in [attach, field, micHolder, send] { inputRow.addArrangedSubview(v) }
        inputRow.alignment = .center
        inputRow.spacing = 6
        let body = UIStackView(arrangedSubviews: [strip, inputRow])
        body.axis = .vertical
        body.spacing = 8
        body.translatesAutoresizingMaskIntoConstraints = false
        glass.contentView.addSubview(body)

        voiceHolder.alpha = 0
        voiceHolder.isHidden = true
        voiceHolder.translatesAutoresizingMaskIntoConstraints = false
        glass.contentView.addSubview(voiceHolder)

        let micDown = PressWatcher(target: self, action: #selector(pressMic))
        micDown.minimumPressDuration = 0
        micDown.cancelsTouchesInView = false
        micHolder.addGestureRecognizer(micDown)

        attach.translatesAutoresizingMaskIntoConstraints = false
        field.translatesAutoresizingMaskIntoConstraints = false
        send.translatesAutoresizingMaskIntoConstraints = false
        strip.translatesAutoresizingMaskIntoConstraints = false
        micHolder.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            body.leadingAnchor.constraint(equalTo: glass.contentView.leadingAnchor, constant: 10),
            body.trailingAnchor.constraint(equalTo: glass.contentView.trailingAnchor, constant: -10),
            body.topAnchor.constraint(equalTo: glass.contentView.topAnchor, constant: Self.vPad),
            body.bottomAnchor.constraint(equalTo: glass.contentView.bottomAnchor, constant: -Self.vPad),
            voiceHolder.leadingAnchor.constraint(equalTo: glass.contentView.leadingAnchor),
            voiceHolder.trailingAnchor.constraint(equalTo: glass.contentView.trailingAnchor),
            voiceHolder.topAnchor.constraint(equalTo: inputRow.topAnchor),
            voiceHolder.bottomAnchor.constraint(equalTo: inputRow.bottomAnchor),
            thumbs.leadingAnchor.constraint(equalTo: strip.contentLayoutGuide.leadingAnchor),
            thumbs.trailingAnchor.constraint(equalTo: strip.contentLayoutGuide.trailingAnchor),
            thumbs.topAnchor.constraint(equalTo: strip.contentLayoutGuide.topAnchor),
            thumbs.bottomAnchor.constraint(equalTo: strip.contentLayoutGuide.bottomAnchor),
            thumbs.heightAnchor.constraint(equalTo: strip.frameLayoutGuide.heightAnchor),
            strip.heightAnchor.constraint(equalToConstant: 52),
            attach.widthAnchor.constraint(equalToConstant: 32),
            attach.heightAnchor.constraint(equalToConstant: 34),
            micHolder.widthAnchor.constraint(equalToConstant: 34),
            micHolder.heightAnchor.constraint(equalToConstant: 34),
            send.widthAnchor.constraint(equalToConstant: 30),
            send.heightAnchor.constraint(equalToConstant: 30),
            hint.leadingAnchor.constraint(equalTo: field.leadingAnchor, constant: 4),
            hint.topAnchor.constraint(equalTo: field.topAnchor, constant: 11),
            hint.trailingAnchor.constraint(lessThanOrEqualTo: field.trailingAnchor),
            field.heightAnchor.constraint(greaterThanOrEqualToConstant: Self.fieldMin),
            field.heightAnchor.constraint(lessThanOrEqualToConstant: Self.fieldMax),
        ])

        // The bar's mic dictates into the field, not the terminal.
        terminal.dictationInsert = { [weak self] text in self?.hear(text) }
    }

    required init?(coder: NSCoder) { fatalError() }

    override func layoutSubviews() {
        super.layoutSubviews()
        if bounds.height != lastHeight {
            lastHeight = bounds.height
            onHeightChange?(bounds.height)
        }
    }

    /// Where an open lands: the field's keyboard.
    func focus() { field.becomeFirstResponder() }

    /// Cross-fades the input row into the inline voice capsule while dictating,
    /// leaving the key bar below untouched.
    func setVoice(active: Bool) {
        guard active != voiceActive else { return }
        voiceActive = active
        if active {
            voiceHolder.isHidden = false
            voiceHolder.transform = CGAffineTransform(scaleX: 0.94, y: 0.94)
            inputRow.isUserInteractionEnabled = false
            UIView.animate(withDuration: 0.28, delay: 0, usingSpringWithDamping: 0.86, initialSpringVelocity: 0) {
                self.inputRow.alpha = 0
                self.inputRow.transform = CGAffineTransform(scaleX: 0.96, y: 0.96)
                self.voiceHolder.alpha = 1
                self.voiceHolder.transform = .identity
            }
        } else {
            inputRow.isUserInteractionEnabled = true
            UIView.animate(withDuration: 0.24, delay: 0, usingSpringWithDamping: 0.9, initialSpringVelocity: 0) {
                self.inputRow.alpha = 1
                self.inputRow.transform = .identity
                self.voiceHolder.alpha = 0
                self.voiceHolder.transform = CGAffineTransform(scaleX: 0.94, y: 0.94)
            } completion: { _ in
                if !self.voiceActive { self.voiceHolder.isHidden = true }
            }
        }
    }

    @objc private func pressMic(_ g: UILongPressGestureRecognizer) {
        guard g.state == .began else { return }
        Haptics.tapped()
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
        send.alpha = on ? 1 : 0.4
    }

    /// Uploads the attachments as one batch — the server types their
    /// paths together once every file is up — then pastes the text and
    /// sends Return. A failure alerts and keeps the message; a send that
    /// went empties the bar for the next one.
    private func sendNow() {
        guard let terminal, !sending else { return }
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
            if !field.text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                terminal.insert(field.text)
            }
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
        terminal?.alert(String(localized: "Can't Upload"), error.localizedDescription)
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
