import UIKit
import XCTest

/// The hosts' last projects are written off the main actor: in order, the
/// last state last, a burst as one write, and a pending one on going to the
/// background.
nonisolated final class ListingsWriterTests: XCTestCase {
    private final class Log: @unchecked Sendable {
        let lock = NSLock()
        var seen: [[String]] = []
        let started = DispatchSemaphore(value: 0)
        let release = DispatchSemaphore(value: 0)
        var block = false
        func add(_ l: [String: ProjectListing]) {
            lock.lock()
            seen.append(l.keys.sorted())
            let wait = block
            block = false
            lock.unlock()
            if wait {
                started.signal()
                release.wait()
            }
        }
        var all: [[String]] { lock.lock(); defer { lock.unlock() }; return seen }
    }

    private func state(_ key: String) -> [String: ProjectListing] {
        [key: ProjectListing(projects: [], loose: [])]
    }

    private func writer(_ log: Log, center: NotificationCenter = NotificationCenter()) -> ListingsWriter {
        ListingsWriter(url: URL(fileURLWithPath: "/unused"), center: center) { _, l in log.add(l) }
    }

    /// Saves one after another are written in that order.
    func testWritesKeepOrder() {
        let log = Log()
        let w = writer(log)
        for k in ["a", "b", "c"] { w.save(state(k)); w.flush() }
        XCTAssertEqual(log.all, [["a"], ["b"], ["c"]])
    }

    /// While a write runs, a burst of saves makes one more write, of the last.
    func testBurstCoalescesAndLastWins() {
        let log = Log()
        log.block = true
        let w = writer(log)
        w.save(state("a"))
        XCTAssertEqual(log.started.wait(timeout: .now() + 5), .success)
        for k in ["b", "c", "d"] { w.save(state(k)) }
        log.release.signal()
        w.flush()
        XCTAssertEqual(log.all, [["a"], ["d"]])
    }

    /// Going to the background writes what is pending before it returns.
    func testBackgroundFlushes() {
        let log = Log()
        let center = NotificationCenter()
        let w = writer(log, center: center)
        w.save(state("a"))
        center.post(name: UIApplication.didEnterBackgroundNotification, object: nil)
        XCTAssertEqual(log.all.last, ["a"])
        withExtendedLifetime(w) {}
    }

    /// The real write puts the listings in the file, and a new folder is made for it.
    func testWritesFile() throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("lw-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: dir) }
        let url = dir.appendingPathComponent("projects-cache.json")
        let w = ListingsWriter(url: url, center: NotificationCenter())
        w.save(state("h"))
        w.flush()
        let back = try JSONDecoder().decode([String: ProjectListing].self, from: Data(contentsOf: url))
        XCTAssertEqual(Array(back.keys), ["h"])
    }
}
