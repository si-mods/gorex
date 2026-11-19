import CryptoKit
import Foundation
import XCTest

/// Hotspot Share's web client download (WebClientSync) and its ordered Go
/// calls (ShareCalls), off the main actor (#861, #860).
nonisolated final class WebClientSyncTests: XCTestCase {
    private var root: URL!

    override func setUpWithError() throws {
        root = FileManager.default.temporaryDirectory
            .appendingPathComponent("WebClientSyncTests-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: root)
    }

    private func sha(_ s: String) -> String {
        SHA256.hash(data: Data(s.utf8)).map { String(format: "%02x", $0) }.joined()
    }

    private func stagingLeft() throws -> [String] {
        try FileManager.default.contentsOfDirectory(atPath: root.path).filter { $0.hasPrefix("WebClient.staging") }
    }

    private func read(_ url: URL) throws -> String {
        String(decoding: try Data(contentsOf: url), as: UTF8.self)
    }

    /// A cached file whose SHA-256 matches is copied; a stale one and a
    /// missing one are downloaded; files.json goes in with them.
    func testCopiesCachedAndDownloadsTheRest() async throws {
        let dir = root.appendingPathComponent("WebClient", isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        try Data("a".utf8).write(to: dir.appendingPathComponent("a.js"))
        try Data("old".utf8).write(to: dir.appendingPathComponent("b.js"))
        let body = ["a.js": "a", "b.js": "new", "sub/c.css": "c"]
        let entries = ["a.js", "b.js", "sub/c.css"].map { WebClientSync.Entry(path: $0, sha256: sha(body[$0]!)) }
        let fetched = Recorder<String>()
        let progress = Recorder<Int>()

        try await WebClientSync.sync(
            entries: entries, indexData: Data("{}".utf8), into: dir,
            fetch: { path in
                await fetched.add(path)
                return Data(body[path]!.utf8)
            },
            progress: { await progress.add($0) })

        let fetchedPaths = await fetched.items
        let counts = await progress.items
        XCTAssertEqual(Set(fetchedPaths), ["b.js", "sub/c.css"])
        XCTAssertEqual(counts, [1, 2, 3])
        for (path, text) in body {
            XCTAssertEqual(try read(dir.appendingPathComponent(path)), text, path)
        }
        XCTAssertEqual(try read(dir.appendingPathComponent("files.json")), "{}")
        XCTAssertEqual(try stagingLeft(), [])
    }

    /// A download whose SHA-256 is wrong fails the update, and the cached
    /// copy stays as it was, with no staging folder left.
    func testChecksumMismatchKeepsTheCache() async throws {
        let dir = root.appendingPathComponent("WebClient", isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        try Data("old".utf8).write(to: dir.appendingPathComponent("a.js"))
        do {
            try await WebClientSync.sync(
                entries: [WebClientSync.Entry(path: "a.js", sha256: sha("new"))],
                indexData: Data("{}".utf8), into: dir,
                fetch: { _ in Data("bad".utf8) },
                progress: { _ in })
            XCTFail("synced a file with a wrong checksum")
        } catch {}
        XCTAssertEqual(try read(dir.appendingPathComponent("a.js")), "old")
        XCTAssertEqual(try stagingLeft(), [])
    }

    /// Downloads run at the same time, at most four. Each download waits
    /// until as many as can be in flight are, so a serial download would
    /// never finish.
    func testAtMostFourDownloadsAtOnce() async throws {
        let dir = root.appendingPathComponent("WebClient", isDirectory: true)
        let paths = (0..<10).map { "f\($0).js" }
        let gate = Gate(total: paths.count, limit: WebClientSync.maxDownloads)
        let entries = paths.map { WebClientSync.Entry(path: $0, sha256: sha($0)) }
        let finished = expectation(description: "synced")
        let sync = Task {
            defer { finished.fulfill() }
            try await WebClientSync.sync(
                entries: entries,
                indexData: Data("{}".utf8), into: dir,
                fetch: { path in
                    await gate.enter()
                    await gate.leave()
                    return Data(path.utf8)
                },
                progress: { _ in })
        }
        await fulfillment(of: [finished], timeout: 30)
        try await sync.value
        let peak = await gate.peak
        XCTAssertEqual(peak, 4)
        for p in paths {
            XCTAssertEqual(try read(dir.appendingPathComponent(p)), p)
        }
    }
}

nonisolated final class ShareCallsTests: XCTestCase {
    /// An operation starts only when the one added before it has ended,
    /// even while that one waits: a Stop never runs before an earlier Start.
    @MainActor
    func testRunsInOrder() async {
        let calls = ShareCalls()
        let log = Recorder<String>()
        let (gate, open) = AsyncStream<Void>.makeStream()
        let (started, didStart) = AsyncStream<Void>.makeStream()
        calls.add {
            await log.add("start")
            didStart.yield()
            for await _ in gate { break }
            await log.add("start done")
        }
        let stop = calls.add { await log.add("stop") }
        for await _ in started { break }
        let early = await log.items
        XCTAssertEqual(early, ["start"])
        open.yield()
        await stop.value
        let all = await log.items
        XCTAssertEqual(all, ["start", "start done", "stop"])
    }

    @MainActor
    func testOffMain() async {
        let onMain = await ShareCalls.offMain { Thread.isMainThread }
        XCTAssertFalse(onMain)
    }
}

/// Values in the order they came.
private actor Recorder<T: Sendable> {
    private(set) var items: [T] = []
    func add(_ item: T) { items.append(item) }
}

/// Holds each caller of enter() until as many as can be in flight are in.
private actor Gate {
    let total: Int
    let limit: Int
    private(set) var peak = 0
    private var inFlight = 0
    private var finished = 0
    private var waiters: [CheckedContinuation<Void, Never>] = []

    init(total: Int, limit: Int) {
        self.total = total
        self.limit = limit
    }

    func enter() async {
        inFlight += 1
        peak = max(peak, inFlight)
        if full() {
            release()
        } else {
            await withCheckedContinuation { waiters.append($0) }
        }
    }

    func leave() {
        inFlight -= 1
        finished += 1
        if inFlight > 0 && full() { release() }
    }

    private func full() -> Bool {
        inFlight >= min(limit, total - finished)
    }

    private func release() {
        for w in waiters { w.resume() }
        waiters = []
    }
}
