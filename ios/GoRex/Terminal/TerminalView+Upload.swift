import PhotosUI
import UIKit
import UniformTypeIdentifiers

/// Add Photo and Add File: photos from the library, as their original
/// files, or files picked, go into the temporary folder of the server's
/// machine (Upload), and their paths are pasted, as the app pastes those of
/// files dropped on a pane of a host. The keyboard comes back as the picker
/// closes; ⌘ spins while the files go up, and an alert tells of one that
/// failed.
extension TerminalView: UIDocumentPickerDelegate {
    func addPhoto() {
        // Once: the picker may be up, or going.
        guard shown?.presentedViewController == nil else { return }
        // The keyboard goes, or it covers the sheet's lower half.
        resignFirstResponder()
        present(PhotoPicker { [weak self] providers in
            guard let self else { return }
            becomeFirstResponder()
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
        becomeFirstResponder()
        upload { dir in
            // The copies move in with the photos', to go when they are sent.
            try urls.enumerated().map { i, u in
                let to = dir.appending(path: String(i))
                try FileManager.default.createDirectory(at: to, withIntermediateDirectories: true)
                try FileManager.default.moveItem(at: u, to: to.appending(path: u.lastPathComponent))
                return to.appending(path: u.lastPathComponent)
            }
        }
    }

    func documentPickerWasCancelled(_ controller: UIDocumentPickerViewController) {
        becomeFirstResponder()
    }

    /// Sends the files that files puts into a folder of its own, one after
    /// another, then pastes the paths of those sent, and removes the folder.
    private func upload(_ files: @escaping (URL) async throws -> [URL]) {
        let dir = FileManager.default.temporaryDirectory.appending(path: "Uploads").appending(path: UUID().uuidString)
        bar.uploading += 1
        Task {
            var words: [String] = []
            do {
                for f in try await files(dir) {
                    words.append(try await session.host.upload(f, block: session.block))
                }
            } catch {
                failed(error)
            }
            try? FileManager.default.removeItem(at: dir)
            bar.uploading -= 1
            if !words.isEmpty { insert(words.joined(separator: " ")) }
        }
    }

    /// Copies a photo's original file into dir, named as the library
    /// names it.
    private static func photo(_ p: NSItemProvider, into dir: URL) async throws -> URL {
        let type = p.registeredTypeIdentifiers.first { UTType($0)?.conforms(to: .image) == true } ?? UTType.image.identifier
        let name = p.suggestedName
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
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

    private func failed(_ error: Error) {
        let a = UIAlertController(title: String(localized: "Can't Upload"), message: error.localizedDescription, preferredStyle: .alert)
        a.addAction(UIAlertAction(title: String(localized: "OK"), style: .default) { [weak self] _ in self?.becomeFirstResponder() })
        present(a)
    }

    /// The view controller that shows the terminal.
    private var shown: UIViewController? {
        sequence(first: self as UIResponder, next: \.next).lazy.compactMap { $0 as? UIViewController }.first
    }

    /// Presents a view controller over the topmost one, so an alert shows
    /// over a sheet that is up.
    private func present(_ vc: UIViewController) {
        var top = shown
        while let p = top?.presentedViewController { top = p }
        top?.present(vc, animated: true)
    }
}

/// The photo picker, half the screen with the terminal above undimmed, in a
/// navigation bar of its own: at half height the picker hides its bar, and
/// with it its Cancel and Add. Close, or a swipe down, picks nothing.
private final class PhotoPicker: UINavigationController, PHPickerViewControllerDelegate, UIAdaptivePresentationControllerDelegate {
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
