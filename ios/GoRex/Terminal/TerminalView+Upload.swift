import PhotosUI
import UIKit
import UniformTypeIdentifiers

/// Add Photo and Add File: photos from the library, as their original
/// files, or files picked, go into the temporary folder of the server's
/// machine (Upload), and their paths are pasted, as the app pastes those of
/// files dropped on a pane of a host. The keyboard comes back as the picker
/// closes, if it was up; ⌘ spins while the files go up, and a toast tells
/// how it went: Uploading, then Uploaded, or the error with Retry
/// (docs/IOS-034-toasts.md).
extension TerminalView: UIDocumentPickerDelegate {
    func addPhoto() {
        // Once: the picker may be up, or going.
        guard shown?.presentedViewController == nil else { return }
        // The keyboard goes, or it covers the sheet's lower half.
        let typing = keyboardUp
        resignFirstResponder()
        present(PhotoPicker { [weak self] providers in
            guard let self else { return }
            if typing { becomeFirstResponder() }
            if providers.isEmpty { return }
            upload { dir in
                var files: [URL] = []
                for (i, p) in providers.enumerated() {
                    files.append(try await Self.photo(p, into: dir.appending(path: String(i))))
                }
                return files
            }
        })
    }

    func addFile() {
        let p = UIDocumentPickerViewController(forOpeningContentTypes: [.item], asCopy: true)
        p.allowsMultipleSelection = true
        p.delegate = self
        present(p)
    }

    func documentPicker(_ controller: UIDocumentPickerViewController, didPickDocumentsAt urls: [URL]) {
        upload { dir in
            try await Task.detached { try Self.stage(urls, into: dir) }.value
        }
    }

    /// The copies move in with the photos', to go when they are sent. The
    /// file work is off the main actor: the caller runs this detached. The
    /// composer stages its attachments the same way (Composer.swift).
    nonisolated static func stage(_ urls: [URL], into dir: URL) throws -> [URL] {
        try urls.enumerated().map { i, u in
            let to = dir.appending(path: String(i))
            try FileManager.default.createDirectory(at: to, withIntermediateDirectories: true)
            try FileManager.default.moveItem(at: u, to: to.appending(path: u.lastPathComponent))
            return to.appending(path: u.lastPathComponent)
        }
    }

    /// Sends the files that files puts into a folder of its own, one after
    /// another. The folder goes when the files are sent, or when the
    /// failure's toast ends with no Retry.
    private func upload(_ files: @escaping (URL) async throws -> [URL]) {
        let dir = FileManager.default.temporaryDirectory.appending(path: "Uploads").appending(path: UUID().uuidString)
        send(files, into: dir, staged: nil)
    }

    /// One try. A toast shows it: Uploading, then Uploaded. A failure
    /// turns it into the error with Retry, which stays until used or
    /// closed. Retry sends the files staged and not yet sent, so it needs
    /// no picker again, and a file that went up is not typed twice. The
    /// toast does not name the folder: the server's answer to a stream
    /// upload has no path (docs/IOS-034-toasts.md).
    private func send(_ files: @escaping (URL) async throws -> [URL], into dir: URL, staged: [URL]?, sent: Set<URL> = []) {
        bar.uploading += 1
        Task {
            var list = staged
            var done = sent
            var toast: UUID?
            do {
                if list == nil { list = try await files(dir) }
                let names = (list ?? []).filter { !done.contains($0) }
                toast = Toasts.shared.progress(names.count == 1
                    ? String(localized: "Uploading \(names[0].lastPathComponent)")
                    : String(localized: "Uploading \(names.count) files"))
                try await Upload.sendEach(names, sent: &done) { f in
                    try await session.host.upload(f, block: session.block, session: session.session)
                }
                Toasts.shared.settle(toast, with: Toast(kind: .status, text: String(localized: "Uploaded"), seconds: 3))
                await Self.remove(dir)
            } catch {
                let text = String(localized: "Can't Upload: \(Toast.oneLine(error.localizedDescription))")
                // A staging that failed part-way left sources moved and
                // copies half made: a retry of it would fail again. The
                // folder goes, and the error has no Retry; the user picks
                // again.
                guard let kept = list else {
                    await Self.remove(dir)
                    Toasts.shared.settle(toast, with: Toast(kind: .error, text: text, seconds: Toast.lifetime(text)))
                    bar.uploading -= 1
                    return
                }
                let keptDone = done
                let retry = Toast.Choice(label: String(localized: "Retry")) { [weak self] in
                    if let self {
                        self.send(files, into: dir, staged: kept, sent: keptDone)
                    } else {
                        Task { await Self.remove(dir) }
                    }
                }
                Toasts.shared.settle(toast, with: Toast(kind: .error, text: text, button: retry, seconds: nil,
                                                        commit: { Task { await Self.remove(dir) } }))
            }
            bar.uploading -= 1
        }
    }

    nonisolated private static func remove(_ dir: URL) async {
        await Task.detached { try? FileManager.default.removeItem(at: dir) }.value
    }

    /// Copies a photo's original file into dir, named as the library
    /// names it. The composer stages its photo attachments the same way.
    static func photo(_ p: NSItemProvider, into dir: URL) async throws -> URL {
        let type = p.registeredTypeIdentifiers.first { UTType($0)?.conforms(to: .image) == true } ?? UTType.image.identifier
        let name = p.suggestedName
        try await Task.detached { try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true) }.value
        return try await withCheckedThrowingContinuation { c in
            // The file the provider gives, on a queue of its own, is gone
            // once this returns.
            _ = p.loadFileRepresentation(forTypeIdentifier: type) { @Sendable url, error in
                c.resume(with: Result {
                    guard let url else { throw error ?? CocoaError(.fileReadUnknown) }
                    let to = dir.appending(path: Upload.photoName(suggested: name, file: url))
                    try FileManager.default.copyItem(at: url, to: to)
                    return to
                })
            }
        }
    }

    /// Tells of a problem; the keyboard comes back once it is read, if it
    /// was up.
    func alert(_ title: String, _ message: String) {
        let typing = keyboardUp
        let a = UIAlertController(title: title, message: message, preferredStyle: .alert)
        a.addAction(UIAlertAction(title: String(localized: "OK"), style: .default) { [weak self] _ in if typing { self?.becomeFirstResponder() } })
        present(a)
    }

    /// The view controller that shows the terminal.
    var shown: UIViewController? {
        sequence(first: self as UIResponder, next: \.next).lazy.compactMap { $0 as? UIViewController }.first
    }

    /// Presents a view controller over the topmost one, so an alert shows
    /// over a sheet that is up.
    func present(_ vc: UIViewController) {
        var top = shown
        while let p = top?.presentedViewController { top = p }
        top?.present(vc, animated: true)
    }
}

/// The photo picker, half the screen with the terminal above undimmed, in a
/// navigation bar of its own: at half height the picker hides its bar, and
/// with it its Cancel and Add. Close, or a swipe down, picks nothing. The
/// composer shows it over its own sheet for photo attachments.
final class PhotoPicker: UINavigationController, PHPickerViewControllerDelegate, UIAdaptivePresentationControllerDelegate {
    private let closed: ([NSItemProvider]) -> Void
    private var picked: [NSItemProvider] = []
    /// Whether Close or Add went: the sheet takes a moment to go.
    private var done = false
    private let add = UIBarButtonItem()

    init(closed: @escaping ([NSItemProvider]) -> Void) {
        self.closed = closed
        var c = PHPickerConfiguration()
        c.filter = .images
        // The file as the library keeps it: HEIC stays HEIC.
        c.preferredAssetRepresentationMode = .current
        c.selectionLimit = 0
        // Each change of the selection comes to the delegate, and the
        // picker's own buttons go for ours.
        c.selection = .continuous
        c.disabledCapabilities = [.selectionActions]
        let p = PHPickerViewController(configuration: c)
        super.init(rootViewController: p)
        p.delegate = self
        p.navigationItem.title = String(localized: "Add Photo")
        p.navigationItem.leftBarButtonItem = UIBarButtonItem(systemItem: .close, primaryAction: UIAction { [unowned self] _ in finish([]) })
        add.primaryAction = UIAction(title: String(localized: "Add")) { [unowned self] _ in finish(picked) }
        add.style = .prominent
        add.isEnabled = false
        p.navigationItem.rightBarButtonItem = add
        presentationController?.delegate = self
        // The grabber pulls it up.
        if let s = sheetPresentationController {
            s.detents = [.medium(), .large()]
            s.prefersGrabberVisible = true
            s.prefersScrollingExpandsWhenScrolledToEdge = false
            s.largestUndimmedDetentIdentifier = .medium
        }
    }

    required init?(coder: NSCoder) { fatalError() }

    func picker(_ picker: PHPickerViewController, didFinishPicking results: [PHPickerResult]) {
        picked = results.map(\.itemProvider)
        add.isEnabled = !results.isEmpty
    }

    func presentationControllerDidDismiss(_ presentationController: UIPresentationController) {
        closed([])
    }

    private func finish(_ providers: [NSItemProvider]) {
        guard !done else { return }
        done = true
        dismiss(animated: true) { [closed] in closed(providers) }
    }
}
