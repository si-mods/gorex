import Foundation
import WebKit
import XCTest

/// Unit tests for `HTMLResourcePath` (`docs/IOS-028-html-viewer.md`).
nonisolated final class HTMLResourcePathTests: XCTestCase {
    func testIsHTML() {
        for path in [
            "index.html",
            "page.htm",
            "REPORT.HTML",
            "Sub/Page.Htm",
            "~/.gemini/scratch/compare.html",
            "/tmp/demo/index.html",
        ] {
            XCTAssertTrue(HTMLResourcePath.isHTML(path), path)
        }
        for path in [
            "",
            ".html",
            "dir.html/",
            "dir.html\\",
            "index.xhtml",
            "index.html.bak",
            "shot.png",
            "notes.md",
            "/tmp/html",
        ] {
            XCTAssertFalse(HTMLResourcePath.isHTML(path), path)
        }
    }

    func testSplitAndEntryURL() {
        let tilde = HTMLResourcePath(entryPath: "~/.gemini/brain/demo-id/scratch/compare.html")
        XCTAssertEqual(tilde.baseDir, "~/.gemini/brain/demo-id/scratch")
        XCTAssertEqual(tilde.entryName, "compare.html")
        XCTAssertEqual(tilde.entryURL.absoluteString, "gorex-file://local/compare.html")
        XCTAssertTrue(tilde.isEntry(tilde.entryURL))
        XCTAssertTrue(tilde.isEntry(URL(string: "gorex-file://local/compare.html?v=1#top")!))
        XCTAssertFalse(tilde.isEntry(URL(string: "gorex-file://local/other.html")!))

        let root = HTMLResourcePath(entryPath: "/index.html")
        XCTAssertEqual(root.baseDir, "/")
        XCTAssertEqual(root.entryName, "index.html")

        let bare = HTMLResourcePath(entryPath: "report.html")
        XCTAssertEqual(bare.baseDir, ".")
        XCTAssertEqual(bare.entryName, "report.html")

        let spaced = HTMLResourcePath(entryPath: "/tmp/demo/对比 报告.html")
        XCTAssertTrue(spaced.isEntry(spaced.entryURL))
        XCTAssertEqual(spaced.hostPath(for: spaced.entryURL), "/tmp/demo/对比 报告.html")
    }

    func testHostPathResolvesRelativeAssets() {
        let r = HTMLResourcePath(entryPath: "~/.gemini/scratch/compare.html")
        XCTAssertEqual(r.hostPath(for: URL(string: "gorex-file://local/compare.html")!), "~/.gemini/scratch/compare.html")
        XCTAssertEqual(r.hostPath(for: URL(string: "gorex-file://local/style.css")!), "~/.gemini/scratch/style.css")
        XCTAssertEqual(r.hostPath(for: URL(string: "gorex-file://local/assets/app.js?v=2#init")!), "~/.gemini/scratch/assets/app.js")
        XCTAssertEqual(r.hostPath(for: URL(string: "gorex-file://local/assets/my%20chart.png")!), "~/.gemini/scratch/assets/my chart.png")

        let root = HTMLResourcePath(entryPath: "/index.html")
        XCTAssertEqual(root.hostPath(for: URL(string: "gorex-file://local/app.js")!), "/app.js")

        let dot = HTMLResourcePath(entryPath: "./index.html")
        XCTAssertEqual(dot.hostPath(for: URL(string: "gorex-file://local/sub/a.css")!), "sub/a.css")
    }

    func testHostPathRejectsTraversalAndForeignURLs() {
        let r = HTMLResourcePath(entryPath: "/tmp/untrusted/index.html")
        for raw in [
            "gorex-file://local/../secret.txt",
            "gorex-file://local/sub/../../secret.txt",
            "gorex-file://local/%2e%2e/secret.txt",
            "gorex-file://local/%2E%2E/secret.txt",
            "gorex-file://local/a%5cb.js",
            "gorex-file://local/a%00.js",
            "gorex-file://local/",
            "gorex-file://local/sub/",
            "gorex-file://local//a.js",
            "gorex-file://other/index.html",
            "https://local/index.html",
            "file:///etc/passwd",
        ] {
            let url = URL(string: raw)!
            XCTAssertNil(r.hostPath(for: url), raw)
        }
    }

    func testMimeTypes() {
        XCTAssertEqual(HTMLResourcePath.mimeType(for: "index.html"), "text/html; charset=utf-8")
        XCTAssertEqual(HTMLResourcePath.mimeType(for: "PAGE.HTM"), "text/html; charset=utf-8")
        XCTAssertEqual(HTMLResourcePath.mimeType(for: "style.css"), "text/css; charset=utf-8")
        XCTAssertEqual(HTMLResourcePath.mimeType(for: "app.mjs"), "text/javascript; charset=utf-8")
        XCTAssertEqual(HTMLResourcePath.mimeType(for: "data.json"), "application/json; charset=utf-8")
        XCTAssertEqual(HTMLResourcePath.mimeType(for: "icon.svg"), "image/svg+xml")
        XCTAssertEqual(HTMLResourcePath.mimeType(for: "shot.png"), "image/png")
        XCTAssertEqual(HTMLResourcePath.mimeType(for: "font.woff2"), "font/woff2")
        XCTAssertEqual(HTMLResourcePath.mimeType(for: "noext"), "application/octet-stream")
    }
}

/// Unit tests for `HTMLSchemeHandler` (`docs/IOS-028-html-viewer.md`).
nonisolated final class HTMLSchemeHandlerTests: XCTestCase {
    @MainActor
    private final class FakeSchemeTask: NSObject, WKURLSchemeTask {
        let request: URLRequest
        var response: URLResponse?
        var data = Data()
        var finished = false
        var error: Error?
        var callbackAfterStop = false
        private var stopped = false
        private var done: CheckedContinuation<Void, Never>?

        init(_ url: URL) {
            self.request = URLRequest(url: url)
        }

        func markStopped() {
            stopped = true
        }

        func wait() async {
            if finished || error != nil { return }
            await withCheckedContinuation { done = $0 }
        }

        func didReceive(_ response: URLResponse) {
            if stopped { callbackAfterStop = true; return }
            self.response = response
        }

        func didReceive(_ data: Data) {
            if stopped { callbackAfterStop = true; return }
            self.data.append(data)
        }

        func didFinish() {
            if stopped { callbackAfterStop = true; return }
            finished = true
            done?.resume()
            done = nil
        }

        func didFailWithError(_ error: any Error) {
            if stopped { callbackAfterStop = true; return }
            self.error = error
            done?.resume()
            done = nil
        }
    }

    private func makeTempFile(_ contents: String, name: String = "index.html") throws -> URL {
        let dir = FileManager.default.temporaryDirectory.appending(path: "HTMLViewerTests-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let file = dir.appending(path: name)
        try Data(contents.utf8).write(to: file)
        addTeardownBlock { try? FileManager.default.removeItem(at: dir) }
        return file
    }

    @MainActor
    func testServesCachedEntryWithoutCallingLoader() async throws {
        let html = "<!doctype html><h1>Hello</h1>"
        let cached = try makeTempFile(html)
        var loaderCalls = 0
        let resolver = HTMLResourcePath(entryPath: "/tmp/demo/index.html")
        let handler = HTMLSchemeHandler(resolver: resolver, cachedEntryURL: cached, loader: { _, _ in
            loaderCalls += 1
            throw RexError(code: "no_file", message: "unexpected")
        })
        let wv = WKWebView()
        let task = FakeSchemeTask(resolver.entryURL)
        handler.webView(wv, start: task)
        await task.wait()

        XCTAssertEqual(loaderCalls, 0)
        XCTAssertTrue(task.finished)
        XCTAssertEqual((task.response as? HTTPURLResponse)?.statusCode, 200)
        XCTAssertEqual((task.response as? HTTPURLResponse)?.value(forHTTPHeaderField: "Content-Type"), "text/html; charset=utf-8")
        XCTAssertEqual(String(data: task.data, encoding: .utf8), html)
    }

    @MainActor
    func testFetchesRelativeAssetAndCleansTempFile() async throws {
        let cached = try makeTempFile("<h1>Hello</h1>")
        let cssFile = try makeTempFile("body { color: red; }", name: "style.css")
        var requestedPath: String?
        let resolver = HTMLResourcePath(entryPath: "~/.gemini/scratch/compare.html")
        let handler = HTMLSchemeHandler(resolver: resolver, cachedEntryURL: cached, loader: { path, _ in
            requestedPath = path
            return DownloadResult(name: "style.css", version: "1", file: cssFile, notModified: false)
        })
        let wv = WKWebView()
        let task = FakeSchemeTask(URL(string: "gorex-file://local/assets/style.css")!)
        handler.webView(wv, start: task)
        await task.wait()

        XCTAssertEqual(requestedPath, "~/.gemini/scratch/assets/style.css")
        XCTAssertEqual((task.response as? HTTPURLResponse)?.statusCode, 200)
        XCTAssertEqual((task.response as? HTTPURLResponse)?.value(forHTTPHeaderField: "Content-Type"), "text/css; charset=utf-8")
        XCTAssertEqual(String(data: task.data, encoding: .utf8), "body { color: red; }")
        XCTAssertFalse(FileManager.default.fileExists(atPath: cssFile.path), "downloaded temp file should be removed after serving")
    }

    @MainActor
    func testReloadInvalidatesCacheAndNotifiesUpdate() async throws {
        let cached = try makeTempFile("<h1>v1</h1>")
        let updated = try makeTempFile("<h1>v2</h1>", name: "v2.html")
        var receivedUpdate: String?
        let resolver = HTMLResourcePath(entryPath: "/tmp/demo/index.html")
        let handler = HTMLSchemeHandler(
            resolver: resolver,
            cachedEntryURL: cached,
            loader: { _, _ in DownloadResult(name: "index.html", version: "2", file: updated, notModified: false) },
            onEntryUpdated: { data in receivedUpdate = String(data: data, encoding: .utf8) }
        )
        handler.invalidateEntryCache()
        let wv = WKWebView()
        let task = FakeSchemeTask(resolver.entryURL)
        handler.webView(wv, start: task)
        await task.wait()

        XCTAssertEqual(String(data: task.data, encoding: .utf8), "<h1>v2</h1>")
        XCTAssertEqual(receivedUpdate, "<h1>v2</h1>")
    }

    @MainActor
    func testMissingAssetReturns404AndTraversalReturns403() async throws {
        let cached = try makeTempFile("<h1>Hello</h1>")
        let resolver = HTMLResourcePath(entryPath: "/tmp/demo/index.html")
        var loaderCalls = 0
        let handler = HTMLSchemeHandler(resolver: resolver, cachedEntryURL: cached, loader: { _, _ in
            loaderCalls += 1
            throw RexError(code: "no_file", message: "missing")
        })
        let wv = WKWebView()

        let missing = FakeSchemeTask(URL(string: "gorex-file://local/missing.js")!)
        handler.webView(wv, start: missing)
        await missing.wait()
        XCTAssertEqual((missing.response as? HTTPURLResponse)?.statusCode, 404)
        XCTAssertEqual(loaderCalls, 1)

        let traversal = FakeSchemeTask(URL(string: "gorex-file://local/%2e%2e/secret.txt")!)
        handler.webView(wv, start: traversal)
        await traversal.wait()
        XCTAssertEqual((traversal.response as? HTTPURLResponse)?.statusCode, 403)
        XCTAssertEqual(loaderCalls, 1, "rejected URL must not invoke loader")
    }

    @MainActor
    private final class StopState {
        var entered: CheckedContinuation<Void, Never>?
        var cancelled: CheckedContinuation<Void, Never>?
        var pending: CheckedContinuation<DownloadResult, Error>?

        func cancel() {
            pending?.resume(throwing: CancellationError())
            pending = nil
            cancelled?.resume()
            cancelled = nil
        }
    }

    @MainActor
    func testStopCancelsWithoutLateCallbacks() async throws {
        let cached = try makeTempFile("<h1>Hello</h1>")
        let resolver = HTMLResourcePath(entryPath: "/tmp/demo/index.html")
        let state = StopState()
        let handler = HTMLSchemeHandler(resolver: resolver, cachedEntryURL: cached, loader: { _, _ in
            state.entered?.resume()
            state.entered = nil
            return try await withTaskCancellationHandler {
                try await withCheckedThrowingContinuation { state.pending = $0 }
            } onCancel: {
                Task { @MainActor in state.cancel() }
            }
        })
        let wv = WKWebView()
        let task = FakeSchemeTask(URL(string: "gorex-file://local/slow.js")!)
        let waitEnter = Task { @MainActor in
            await withCheckedContinuation { state.entered = $0 }
        }
        let waitCancel = Task { @MainActor in
            await withCheckedContinuation { state.cancelled = $0 }
        }
        handler.webView(wv, start: task)
        await waitEnter.value
        task.markStopped()
        handler.webView(wv, stop: task)
        await waitCancel.value
        XCTAssertFalse(task.callbackAfterStop)
    }

    @MainActor
    func testConcurrencyLimitCapsSimultaneousDownloads() async throws {
        let cached = try makeTempFile("<h1>Hello</h1>")
        let resolver = HTMLResourcePath(entryPath: "/tmp/demo/index.html")
        var current = 0
        var peak = 0
        var gates: [CheckedContinuation<Void, Never>] = []
        var reachedLimit: CheckedContinuation<Void, Never>?
        let handler = HTMLSchemeHandler(resolver: resolver, cachedEntryURL: cached, maxConcurrent: 2, loader: { path, _ in
            current += 1
            peak = max(peak, current)
            if current == 2, let r = reachedLimit {
                reachedLimit = nil
                r.resume()
            }
            await withCheckedContinuation { gates.append($0) }
            current -= 1
            let dir = FileManager.default.temporaryDirectory.appending(path: "HTMLConc-\(UUID().uuidString)")
            try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
            let file = dir.appending(path: "asset.js")
            try Data("console.log('\(path)');".utf8).write(to: file)
            return DownloadResult(name: "asset.js", version: "1", file: file, notModified: false)
        })
        let wv = WKWebView()
        let tasks = (0..<5).map { FakeSchemeTask(URL(string: "gorex-file://local/a\($0).js")!) }
        let waitLimit = Task { @MainActor in
            await withCheckedContinuation { reachedLimit = $0 }
        }
        for t in tasks { handler.webView(wv, start: t) }
        await waitLimit.value
        XCTAssertEqual(peak, 2)
        // Drain each wave deterministically until all 5 complete.
        for t in tasks {
            while !t.finished && t.error == nil {
                if !gates.isEmpty {
                    let g = gates.removeFirst()
                    g.resume()
                }
                await Task.yield()
            }
        }
        XCTAssertLessThanOrEqual(peak, 2)
        for t in tasks {
            XCTAssertEqual((t.response as? HTTPURLResponse)?.statusCode, 200)
        }
    }
}
