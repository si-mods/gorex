import Foundation
import UIKit
import UniformTypeIdentifiers
import WebKit

/// Resolves relative resource URLs for an HTML file opened from a remote host
/// (`docs/IOS-028-html-viewer.md`). All requests stay confined to the entry
/// file's directory and its subdirectories.
nonisolated struct HTMLResourcePath: Sendable, Equatable {
    static let scheme = "gorex-file"
    static let host = "local"

    /// The entry file's directory on the host (e.g. `~/.gemini/scratch`, `/tmp/demo`, `.`).
    let baseDir: String
    /// The entry file's name (e.g. `index.html`).
    let entryName: String

    /// Whether a filename or path ends in `.html` or `.htm` (case-insensitive).
    static func isHTML(_ path: String) -> Bool {
        guard !path.isEmpty, !path.hasSuffix("/"), !path.hasSuffix("\\") else { return false }
        let name = path.split(whereSeparator: { $0 == "/" || $0 == "\\" }).last.map(String.init) ?? ""
        guard let dot = name.lastIndex(of: "."), dot != name.startIndex else { return false }
        let ext = name[name.index(after: dot)...].lowercased()
        return ext == "html" || ext == "htm"
    }

    init(entryPath: String) {
        if let slash = entryPath.lastIndex(of: "/") {
            let dir = String(entryPath[..<slash])
            let name = String(entryPath[entryPath.index(after: slash)...])
            self.baseDir = dir.isEmpty ? "/" : dir
            self.entryName = name.isEmpty ? "index.html" : name
        } else {
            self.baseDir = "."
            self.entryName = entryPath.isEmpty ? "index.html" : entryPath
        }
    }

    /// The synthetic origin URL loaded into `WKWebView` for the entry document.
    var entryURL: URL {
        var c = URLComponents()
        c.scheme = Self.scheme
        c.host = Self.host
        c.path = "/" + entryName
        return c.url ?? URL(string: "\(Self.scheme)://\(Self.host)/index.html")!
    }

    /// Whether `url` points to the entry document itself (ignoring query/fragment).
    func isEntry(_ url: URL) -> Bool {
        guard url.scheme?.lowercased() == Self.scheme,
              url.host?.lowercased() == Self.host else { return false }
        return url.path(percentEncoded: false) == "/" + entryName
    }

    /// Maps a `gorex-file://local/...` URL back to a host path under `baseDir`.
    /// Returns `nil` for other schemes/hosts, empty or directory paths,
    /// backslashes, null bytes, or any `..` traversal segment.
    func hostPath(for url: URL) -> String? {
        guard url.scheme?.lowercased() == Self.scheme,
              url.host?.lowercased() == Self.host else { return nil }
        guard let encodedPath = URLComponents(url: url, resolvingAgainstBaseURL: false)?.percentEncodedPath else {
            return nil
        }
        let lowered = encodedPath.lowercased()
        if lowered.contains("%2e%2e") || lowered.contains("%5c") || lowered.contains("%00") || lowered.contains("..") {
            return nil
        }
        let decoded = url.path(percentEncoded: false)
        guard decoded.hasPrefix("/"), !decoded.hasSuffix("/"),
              !decoded.contains("\\"), !decoded.contains("\0") else { return nil }
        let body = decoded.dropFirst()
        guard !body.isEmpty else { return nil }
        var parts: [Substring] = []
        for seg in body.split(separator: "/", omittingEmptySubsequences: false) {
            if seg.isEmpty || seg == ".." { return nil }
            if seg == "." { continue }
            parts.append(seg)
        }
        guard !parts.isEmpty else { return nil }
        let rel = parts.joined(separator: "/")
        if baseDir == "/" { return "/" + rel }
        if baseDir == "." { return rel }
        return baseDir + "/" + rel
    }

    /// The `Content-Type` header for a resource path, with UTF-8 charset for text formats.
    static func mimeType(for path: String) -> String {
        let name = path.split(whereSeparator: { $0 == "/" || $0 == "\\" }).last.map(String.init) ?? ""
        guard let dot = name.lastIndex(of: "."), dot != name.startIndex else {
            return "application/octet-stream"
        }
        let ext = name[name.index(after: dot)...].lowercased()
        switch ext {
        case "html", "htm": return "text/html; charset=utf-8"
        case "css": return "text/css; charset=utf-8"
        case "js", "mjs", "cjs": return "text/javascript; charset=utf-8"
        case "json", "map": return "application/json; charset=utf-8"
        case "svg": return "image/svg+xml"
        case "png": return "image/png"
        case "jpg", "jpeg": return "image/jpeg"
        case "gif": return "image/gif"
        case "webp": return "image/webp"
        case "ico": return "image/x-icon"
        case "woff": return "font/woff"
        case "woff2": return "font/woff2"
        case "ttf": return "font/ttf"
        case "otf": return "font/otf"
        case "wasm": return "application/wasm"
        case "xml": return "application/xml; charset=utf-8"
        case "txt", "md", "csv": return "text/plain; charset=utf-8"
        case "pdf": return "application/pdf"
        default:
            return UTType(filenameExtension: ext)?.preferredMIMEType ?? "application/octet-stream"
        }
    }
}

/// Serves `gorex-file://local/...` requests for `HTMLViewerController`: the
/// already-downloaded entry file is served from disk on first load, and any
/// relative subresource (or a reloaded entry) is fetched from the host over
/// `download_begin` with concurrency capped below the server's 4-ticket limit.
final class HTMLSchemeHandler: NSObject, WKURLSchemeHandler {
    typealias Loader = @MainActor (_ path: String, _ maxBytes: Int) async throws -> DownloadResult

    /// Per-subresource download cap (32 MiB); the entry HTML uses `OpenedFiles.maxBytes`.
    static let maxAssetBytes = 32 << 20

    let resolver: HTMLResourcePath
    private let loader: Loader
    private let entryMaxBytes: Int
    private var cachedEntryURL: URL?
    private let onEntryUpdated: (@MainActor (Data) -> Void)?
    private let queue: BoundedTasks
    private var active: Set<ObjectIdentifier> = []
    private var inFlight: [ObjectIdentifier: Task<Void, Never>] = [:]

    init(resolver: HTMLResourcePath,
         cachedEntryURL: URL?,
         entryMaxBytes: Int = 256 << 20,
         maxConcurrent: Int = 3,
         loader: @escaping Loader,
         onEntryUpdated: (@MainActor (Data) -> Void)? = nil) {
        self.resolver = resolver
        self.cachedEntryURL = cachedEntryURL
        self.entryMaxBytes = entryMaxBytes
        self.queue = BoundedTasks(limit: maxConcurrent)
        self.loader = loader
        self.onEntryUpdated = onEntryUpdated
    }

    func invalidateEntryCache() {
        cachedEntryURL = nil
    }

    func setCachedEntryURL(_ url: URL) {
        cachedEntryURL = url
    }

    func cancelAll() {
        active.removeAll()
        let tasks = inFlight.values
        inFlight.removeAll()
        tasks.forEach { $0.cancel() }
        queue.cancelAll()
    }

    func webView(_ webView: WKWebView, start urlSchemeTask: any WKURLSchemeTask) {
        let id = ObjectIdentifier(urlSchemeTask)
        guard let url = urlSchemeTask.request.url, let hostPath = resolver.hostPath(for: url) else {
            let target = urlSchemeTask.request.url ?? resolver.entryURL
            respond(task: urlSchemeTask, url: target, status: 403, mime: "text/plain; charset=utf-8", data: Data())
            return
        }
        let mime = HTMLResourcePath.mimeType(for: hostPath)
        let isEntry = resolver.isEntry(url)

        if isEntry, let cached = cachedEntryURL {
            active.insert(id)
            let task = Task { [weak self] in
                guard let self else { return }
                defer { self.inFlight.removeValue(forKey: id) }
                do {
                    let data = try await Task.detached { try Data(contentsOf: cached) }.value
                    guard !Task.isCancelled, self.active.remove(id) != nil else { return }
                    self.respond(task: urlSchemeTask, url: url, status: 200, mime: mime, data: data)
                } catch {
                    guard !Task.isCancelled, self.active.remove(id) != nil else { return }
                    urlSchemeTask.didFailWithError(error)
                }
            }
            inFlight[id] = task
            return
        }

        active.insert(id)
        let maxBytes = isEntry ? entryMaxBytes : Self.maxAssetBytes
        queue.add("\(id)") { [weak self] in
            guard let self, self.active.contains(id) else { return }
            let task = Task { [weak self] in
                guard let self else { return }
                defer { self.inFlight.removeValue(forKey: id) }
                do {
                    let downloaded = try await self.loader(hostPath, maxBytes)
                    guard !Task.isCancelled, self.active.contains(id) else {
                        if let file = downloaded.file { try? FileManager.default.removeItem(at: file) }
                        return
                    }
                    guard let tempFile = downloaded.file else {
                        guard self.active.remove(id) != nil else { return }
                        self.respond(task: urlSchemeTask, url: url, status: 404, mime: "text/plain; charset=utf-8", data: Data())
                        return
                    }
                    let data: Data
                    do {
                        data = try await Task.detached {
                            defer { try? FileManager.default.removeItem(at: tempFile) }
                            return try Data(contentsOf: tempFile)
                        }.value
                    } catch {
                        guard !Task.isCancelled, self.active.remove(id) != nil else { return }
                        self.respond(task: urlSchemeTask, url: url, status: 500, mime: "text/plain; charset=utf-8", data: Data())
                        return
                    }
                    guard !Task.isCancelled, self.active.remove(id) != nil else { return }
                    if isEntry {
                        self.onEntryUpdated?(data)
                    }
                    self.respond(task: urlSchemeTask, url: url, status: 200, mime: mime, data: data)
                } catch let error as RexError {
                    guard !Task.isCancelled, self.active.remove(id) != nil else { return }
                    let status = switch error.code {
                    case "no_file": 404
                    case "denied": 403
                    case "is_folder", "not_regular": 409
                    case "too_large": 413
                    default: 502
                    }
                    self.respond(task: urlSchemeTask, url: url, status: status, mime: "text/plain; charset=utf-8", data: Data())
                } catch is CancellationError {
                    self.active.remove(id)
                } catch {
                    guard !Task.isCancelled, self.active.remove(id) != nil else { return }
                    urlSchemeTask.didFailWithError(error)
                }
            }
            self.inFlight[id] = task
            await task.value
        }
    }

    func webView(_ webView: WKWebView, stop urlSchemeTask: any WKURLSchemeTask) {
        let id = ObjectIdentifier(urlSchemeTask)
        active.remove(id)
        inFlight.removeValue(forKey: id)?.cancel()
    }

    private func respond(task: any WKURLSchemeTask, url: URL, status: Int, mime: String, data: Data) {
        let headers = [
            "Content-Type": mime,
            "Content-Length": "\(data.count)",
            "Cache-Control": "no-store",
        ]
        if let response = HTTPURLResponse(url: url, statusCode: status, httpVersion: "HTTP/1.1", headerFields: headers) {
            task.didReceive(response)
        } else {
            task.didReceive(URLResponse(url: url, mimeType: mime, expectedContentLength: data.count, textEncodingName: nil))
        }
        if !data.isEmpty {
            task.didReceive(data)
        }
        task.didFinish()
    }
}

/// In-app `WKWebView` viewer for tapped HTML files (`docs/IOS-028-html-viewer.md`).
final class HTMLViewerController: UIViewController, WKNavigationDelegate, WKUIDelegate {
    private let fileURL: URL
    private let resolver: HTMLResourcePath
    private let schemeHandler: HTMLSchemeHandler
    private let closed: () -> Void
    private var didClose = false

    private var webView: WKWebView!
    private let progressView = UIProgressView(progressViewStyle: .bar)
    private var observations: [NSKeyValueObservation] = []

    private lazy var closeButton = UIBarButtonItem(barButtonSystemItem: .close, target: self, action: #selector(closeTapped))
    private lazy var backButton = UIBarButtonItem(image: UIImage(systemName: "chevron.backward"), style: .plain, target: self, action: #selector(backTapped))
    private lazy var reloadButton = UIBarButtonItem(image: UIImage(systemName: "arrow.clockwise"), style: .plain, target: self, action: #selector(reloadTapped))
    private lazy var shareButton = UIBarButtonItem(image: UIImage(systemName: "square.and.arrow.up"), style: .plain, target: self, action: #selector(shareTapped))

    init(fileURL: URL, hostPath: String, loader: @escaping HTMLSchemeHandler.Loader, closed: @escaping () -> Void) {
        self.fileURL = fileURL
        let resolver = HTMLResourcePath(entryPath: hostPath)
        self.resolver = resolver
        self.closed = closed
        var updateHandler: (@MainActor (Data) -> Void)?
        self.schemeHandler = HTMLSchemeHandler(
            resolver: resolver,
            cachedEntryURL: fileURL,
            loader: loader,
            onEntryUpdated: { data in updateHandler?(data) }
        )
        super.init(nibName: nil, bundle: nil)
        updateHandler = { [weak self] data in
            guard let self else { return }
            let dst = self.fileURL
            Task { @MainActor [weak self] in
                let wrote = await Task.detached { (try? data.write(to: dst, options: .atomic)) != nil }.value
                if wrote { self?.schemeHandler.setCachedEntryURL(dst) }
            }
        }
    }

    required init?(coder: NSCoder) { fatalError() }

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .systemBackground
        navigationItem.title = resolver.entryName
        backButton.accessibilityLabel = String(localized: "Back")
        reloadButton.accessibilityLabel = String(localized: "Reload")
        shareButton.accessibilityLabel = String(localized: "Share")
        navigationItem.leftBarButtonItems = [closeButton]
        navigationItem.rightBarButtonItems = [shareButton, reloadButton]

        let config = WKWebViewConfiguration()
        config.websiteDataStore = .nonPersistent()
        config.allowsInlineMediaPlayback = true
        config.setURLSchemeHandler(schemeHandler, forURLScheme: HTMLResourcePath.scheme)

        let wv = WKWebView(frame: .zero, configuration: config)
        wv.navigationDelegate = self
        wv.uiDelegate = self
        wv.allowsBackForwardNavigationGestures = true
        wv.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(wv)
        webView = wv

        progressView.translatesAutoresizingMaskIntoConstraints = false
        progressView.isHidden = true
        view.addSubview(progressView)

        NSLayoutConstraint.activate([
            wv.topAnchor.constraint(equalTo: view.safeAreaLayoutGuide.topAnchor),
            wv.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            wv.trailingAnchor.constraint(equalTo: view.trailingAnchor),
            wv.bottomAnchor.constraint(equalTo: view.bottomAnchor),

            progressView.topAnchor.constraint(equalTo: view.safeAreaLayoutGuide.topAnchor),
            progressView.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            progressView.trailingAnchor.constraint(equalTo: view.trailingAnchor),
        ])

        observations = [
            wv.observe(\.title, options: .new) { [weak self] wv, _ in
                Task { @MainActor in
                    guard let self else { return }
                    let t = wv.title?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                    self.navigationItem.title = t.isEmpty ? self.resolver.entryName : t
                }
            },
            wv.observe(\.canGoBack, options: .new) { [weak self] wv, _ in
                Task { @MainActor in
                    guard let self else { return }
                    self.navigationItem.leftBarButtonItems = wv.canGoBack ? [self.closeButton, self.backButton] : [self.closeButton]
                }
            },
            wv.observe(\.estimatedProgress, options: .new) { [weak self] wv, _ in
                Task { @MainActor in
                    guard let self else { return }
                    self.progressView.setProgress(Float(wv.estimatedProgress), animated: true)
                    self.progressView.isHidden = !wv.isLoading && wv.estimatedProgress >= 1
                }
            },
            wv.observe(\.isLoading, options: .new) { [weak self] wv, _ in
                Task { @MainActor in
                    guard let self else { return }
                    self.progressView.isHidden = !wv.isLoading
                    if !wv.isLoading { self.progressView.progress = 0 }
                }
            },
        ]

        wv.load(URLRequest(url: resolver.entryURL))
    }

    override func viewDidDisappear(_ animated: Bool) {
        super.viewDidDisappear(animated)
        if isBeingDismissed || navigationController?.isBeingDismissed == true {
            finish()
        }
    }

    private func finish() {
        guard !didClose else { return }
        didClose = true
        observations.removeAll()
        schemeHandler.cancelAll()
        webView?.stopLoading()
        try? FileManager.default.removeItem(at: fileURL.deletingLastPathComponent())
        closed()
    }

    @objc private func closeTapped() {
        dismiss(animated: true)
    }

    @objc private func backTapped() {
        webView.goBack()
    }

    @objc private func reloadTapped() {
        schemeHandler.invalidateEntryCache()
        if webView.url != nil {
            webView.reload()
        } else {
            webView.load(URLRequest(url: resolver.entryURL))
        }
    }

    @objc private func shareTapped() {
        let activity = UIActivityViewController(activityItems: [fileURL], applicationActivities: nil)
        activity.popoverPresentationController?.barButtonItem = shareButton
        present(activity, animated: true)
    }

    // MARK: - WKNavigationDelegate

    func webView(_ webView: WKWebView,
                 decidePolicyFor navigationAction: WKNavigationAction,
                 decisionHandler: @escaping @MainActor (WKNavigationActionPolicy) -> Void) {
        guard let url = navigationAction.request.url else {
            decisionHandler(.cancel)
            return
        }
        let scheme = url.scheme?.lowercased() ?? ""
        switch scheme {
        case HTMLResourcePath.scheme, "about", "data", "blob":
            decisionHandler(.allow)
        case "http", "https":
            if navigationAction.navigationType == .linkActivated {
                UIApplication.shared.open(url)
                decisionHandler(.cancel)
            } else {
                decisionHandler(.allow)
            }
        case "mailto", "tel":
            if navigationAction.navigationType == .linkActivated {
                UIApplication.shared.open(url)
            }
            decisionHandler(.cancel)
        default:
            decisionHandler(.cancel)
        }
    }

    // MARK: - WKUIDelegate

    func webView(_ webView: WKWebView,
                 createWebViewWith configuration: WKWebViewConfiguration,
                 for navigationAction: WKNavigationAction,
                 windowFeatures: WKWindowFeatures) -> WKWebView? {
        guard let url = navigationAction.request.url else { return nil }
        let scheme = url.scheme?.lowercased() ?? ""
        if scheme == HTMLResourcePath.scheme {
            webView.load(navigationAction.request)
        } else if ["http", "https", "mailto", "tel"].contains(scheme) {
            UIApplication.shared.open(url)
        }
        return nil
    }

    func webView(_ webView: WKWebView,
                 runJavaScriptAlertPanelWithMessage message: String,
                 initiatedByFrame frame: WKFrameInfo,
                 completionHandler: @escaping @MainActor () -> Void) {
        let alert = UIAlertController(title: nil, message: message, preferredStyle: .alert)
        alert.addAction(UIAlertAction(title: String(localized: "OK"), style: .default) { _ in completionHandler() })
        present(alert, animated: true)
    }

    func webView(_ webView: WKWebView,
                 runJavaScriptConfirmPanelWithMessage message: String,
                 initiatedByFrame frame: WKFrameInfo,
                 completionHandler: @escaping @MainActor (Bool) -> Void) {
        let alert = UIAlertController(title: nil, message: message, preferredStyle: .alert)
        alert.addAction(UIAlertAction(title: String(localized: "Cancel"), style: .cancel) { _ in completionHandler(false) })
        alert.addAction(UIAlertAction(title: String(localized: "OK"), style: .default) { _ in completionHandler(true) })
        present(alert, animated: true)
    }
}
