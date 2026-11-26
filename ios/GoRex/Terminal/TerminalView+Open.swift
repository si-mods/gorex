import GhosttyKit
import QuickLook
import UIKit

/// A file a tap opens (docs/WEB-001-open-files.md, The iOS client): a path the
/// terminal printed, or a file:// link, downloaded from the host for this
/// pane's block, so that a relative path resolves against the directory of
/// its program, and shown by Quick Look, whose share button saves it or
/// sends it to another app. After a moment without the file a spinner
/// shows, and a tap beside it cancels; a failure tells why in an alert.
extension TerminalView {
    func openFile(_ path: String, hidden: Bool = false) {
        let host = session.host, block = session.block, sid = session.session
        // On an older host a path is plain text, as before.
        guard host.canDownload else { return }
        if hidden {
            ask(path) { [weak self] in self?.openFile(path) }
            return
        }
        // A second tap on the same path while it downloads does nothing.
        let tapRow = hidden ? nil : lastTapRow
        let key = [sid, block, path].joined(separator: "\n")
        guard OpenedFiles.opening.insert(key).inserted else { return }
        let download = Task { () -> (URL, String) in
            let resolved = await self.resolveWrappedPath(fragment: path, tapRow: tapRow)
            let chosen = resolved ?? path
            do {
                let r = try await host.download(chosen, block: block, session: sid, maxBytes: OpenedFiles.maxBytes)
                let saved = try await OpenedFiles.save(r, path: chosen)
                return (saved, chosen)
            } catch let error as RexError where resolved != nil && error.code == "no_file" {
                let r = try await host.download(path, block: block, session: sid, maxBytes: OpenedFiles.maxBytes)
                let saved = try await OpenedFiles.save(r, path: path)
                return (saved, path)
            }
        }
        let veil = OpenedFiles.Veil {
            download.cancel()
            OpenedFiles.opening.remove(key)
        }
        let waits = Task {
            try await Task.sleep(for: .milliseconds(300))
            // The result may have come in the same moment: a veil added
            // after it was taken off would stay.
            guard !Task.isCancelled, let v = shown?.view else { return }
            veil.frame = v.bounds
            v.addSubview(veil)
        }
        Task {
            let result = await download.result
            waits.cancel()
            veil.removeFromSuperview()
            if !download.isCancelled { OpenedFiles.opening.remove(key) }
            switch result {
            case .success(let (url, _)) where download.isCancelled:
                try? FileManager.default.removeItem(at: url.deletingLastPathComponent())
            case .success(let (url, openedPath)):
                preview(url, path: openedPath)
            case .failure(let error):
                guard !download.isCancelled, !(error is CancellationError) else { return }
                let reason = (error as? RexError).map { FileLink.reason(code: $0.code, message: $0.message) } ?? error.localizedDescription
                alert(String(localized: "Can't Open File"), reason + "\n" + path)
            }
        }
    }

    /// Reads the visible rows to check whether `fragment` is one piece of a
    /// long file path hard-wrapped across consecutive rows by a TUI.
    private func resolveWrappedPath(fragment: String, tapRow: Int?) async -> String? {
        guard let tapRow, let s = surface else { return nil }
        let columns = Int(ghostty_surface_size(s).columns)
        guard columns > 0 else { return nil }
        let rows: [String] = await withCheckedContinuation { cont in
            readRows { lines, _ in cont.resume(returning: lines) }
        }
        return FileLink.wrappedPath(rows: rows, row: tapRow, fragment: fragment, columns: columns)
    }

    /// Asks before a program's link (OSC 8) downloads a file: the text
    /// tapped need not show the path, so the alert names the file and its
    /// host in full. Cancel gives the keyboard back, if it was up.
    private func ask(_ path: String, open: @escaping () -> Void) {
        let typing = keyboardUp
        let message = String(localized: "The link you tapped names a file on \(session.host.config.name) that its text may not show:")
        let a = UIAlertController(title: String(localized: "Open This File?"), message: message + "\n\n" + path, preferredStyle: .alert)
        a.addAction(UIAlertAction(title: String(localized: "Cancel"), style: .cancel) { [weak self] _ in if typing { self?.becomeFirstResponder() } })
        a.addAction(UIAlertAction(title: String(localized: "Open"), style: .default) { _ in open() })
        present(a)
    }

    /// Shows a downloaded file over the terminal; HTML files open in the
    /// in-app web view (docs/IOS-028-html-viewer.md), and other files in Quick
    /// Look. The keyboard goes while it is up and comes back as it closes, if
    /// it was up.
    private func preview(_ url: URL, path: String) {
        // With no view controller to show it, the file would stay.
        guard shown != nil else {
            try? FileManager.default.removeItem(at: url.deletingLastPathComponent())
            return
        }
        let typing = typingBeforeLink
        resignFirstResponder()
        let restore: () -> Void = { [weak self] in if typing { self?.becomeFirstResponder() } }
        if HTMLResourcePath.isHTML(url.lastPathComponent) {
            let host = session.host, block = session.block, sid = session.session
            let viewer = HTMLViewerController(fileURL: url, hostPath: path, loader: { resourcePath, maxBytes in
                try await host.download(resourcePath, block: block, session: sid, maxBytes: maxBytes)
            }, closed: restore)
            let nav = UINavigationController(rootViewController: viewer)
            nav.modalPresentationStyle = .fullScreen
            present(nav)
        } else {
            present(OpenedFiles.Preview(url, closed: restore))
        }
    }
}

/// The files taps open: those downloading, and those shown, each in a
/// folder of its own under the app's temporary folder, which goes as its
/// preview closes. The app empties the folder as it starts, of the files
/// a run left.
enum OpenedFiles {
    static var opening: Set<String> = []

    /// The largest file a tap downloads, as the web client holds one in
    /// memory (web/src/files.ts maxBlobBytes): the download is read whole.
    static let maxBytes = 256 << 20

    nonisolated static let folder = FileManager.default.temporaryDirectory.appending(path: "Opened")

    static func clean() {
        try? FileManager.default.removeItem(at: folder)
    }

    /// Moves the file in off the main thread.
    static func save(_ r: DownloadResult, path: String) async throws -> URL {
        // A cancelled download leaves no file.
        if Task.isCancelled, let file = r.file {
            try? FileManager.default.removeItem(at: file)
            throw CancellationError()
        }
        let folder = folder
        return try await Task.detached { try write(r, path: path, in: folder) }.value
    }

    nonisolated private static func write(_ r: DownloadResult, path: String, in folder: URL) throws -> URL {
        guard let file = r.file else { throw RexError(code: "protocol", message: "the download sent no file") }
        let dir = folder.appending(path: UUID().uuidString)
        let url = dir.appending(path: FileLink.fileName(r.name, path: path))
        do {
            try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
            try FileManager.default.moveItem(at: file, to: url)
        } catch {
            try? FileManager.default.removeItem(at: dir)
            try? FileManager.default.removeItem(at: file)
            throw error
        }
        return url
    }

    /// Quick Look over one downloaded file.
    final class Preview: QLPreviewController, QLPreviewControllerDataSource, QLPreviewControllerDelegate {
        private let url: URL
        private let closed: () -> Void

        init(_ url: URL, closed: @escaping () -> Void) {
            self.url = url
            self.closed = closed
            super.init(nibName: nil, bundle: nil)
            dataSource = self
            delegate = self
        }

        required init?(coder: NSCoder) { fatalError() }

        func numberOfPreviewItems(in controller: QLPreviewController) -> Int { 1 }

        func previewController(_ controller: QLPreviewController, previewItemAt index: Int) -> any QLPreviewItem {
            url as NSURL
        }

        func previewControllerDidDismiss(_ controller: QLPreviewController) {
            try? FileManager.default.removeItem(at: url.deletingLastPathComponent())
            closed()
        }
    }

    /// What shows while a file downloads: a spinner on glass in the
    /// middle, over a clear veil that takes the touches, and whose tap
    /// beside the spinner cancels.
    final class Veil: UIView {
        private let cancel: () -> Void
        private let pill = UIVisualEffectView(effect: UIGlassEffect(style: .regular))

        init(cancel: @escaping () -> Void) {
            self.cancel = cancel
            super.init(frame: .zero)
            autoresizingMask = [.flexibleWidth, .flexibleHeight]
            let spinner = UIActivityIndicatorView(style: .medium)
            spinner.startAnimating()
            pill.cornerConfiguration = .capsule()
            pill.clipsToBounds = true
            pill.isAccessibilityElement = true
            pill.accessibilityLabel = String(localized: "Opening File")
            pill.contentView.addSubview(spinner)
            addSubview(pill)
            for v in [pill, spinner] as [UIView] { v.translatesAutoresizingMaskIntoConstraints = false }
            NSLayoutConstraint.activate([
                pill.centerXAnchor.constraint(equalTo: centerXAnchor),
                pill.centerYAnchor.constraint(equalTo: centerYAnchor),
                pill.widthAnchor.constraint(equalToConstant: 56),
                pill.heightAnchor.constraint(equalToConstant: 56),
                spinner.centerXAnchor.constraint(equalTo: pill.contentView.centerXAnchor),
                spinner.centerYAnchor.constraint(equalTo: pill.contentView.centerYAnchor),
            ])
            addGestureRecognizer(UITapGestureRecognizer(target: self, action: #selector(tapped)))
        }

        required init?(coder: NSCoder) { fatalError() }

        @objc private func tapped(_ g: UITapGestureRecognizer) {
            guard !pill.frame.contains(g.location(in: self)) else { return }
            removeFromSuperview()
            cancel()
        }
    }
}
