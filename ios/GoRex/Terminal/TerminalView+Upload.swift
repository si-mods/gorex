import PhotosUI
import UIKit
import UniformTypeIdentifiers

/// Add Photo and Add File: photos from the library, as their original
/// files, or files picked, go into the temporary folder of the server's
/// machine (Upload), and their paths are pasted, as the app pastes those of
/// files dropped on a pane of a host. The keyboard comes back as the picker
/// closes; ⌘ spins while the files go up, and an alert tells of one that
/// failed.
extension TerminalView: PHPickerViewControllerDelegate, UIDocumentPickerDelegate {
    func addPhoto() {
        var c = PHPickerConfiguration()
        c.filter = .images
        // The file as the library keeps it: HEIC stays HEIC.
        c.preferredAssetRepresentationMode = .current
        c.selectionLimit = 0
        let p = PHPickerViewController(configuration: c)
        p.delegate = self
        present(p)
    }

    func addFile() {
        let p = UIDocumentPickerViewController(forOpeningContentTypes: [.item], asCopy: true)
        p.allowsMultipleSelection = true
        p.delegate = self
        present(p)
    }

    func picker(_ picker: PHPickerViewController, didFinishPicking results: [PHPickerResult]) {
        picker.dismiss(animated: true) { [weak self] in self?.becomeFirstResponder() }
        let providers = results.map(\.itemProvider)
        if providers.isEmpty { return }
        upload { dir in
            var files: [URL] = []
            for (i, p) in providers.enumerated() {
                files.append(try await Self.photo(p, into: dir.appending(path: String(i))))
            }
            return files
        }
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

    /// Presents a view controller over the one that shows the terminal.
    private func present(_ vc: UIViewController) {
        let shown = sequence(first: self as UIResponder, next: \.next).lazy.compactMap { $0 as? UIViewController }.first
        shown?.present(vc, animated: true)
    }
}
