import UIKit
import XCTest

/// The app's grace on leaving the foreground, apart from UIKit: the work
/// its owners register, its deadline, and its end on a memory warning
/// (docs/IOS-012-background-and-network-changes.md). The clock and the
/// waits are the test's: no time passes by itself.
nonisolated final class BackgroundGraceTests: XCTestCase {
    typealias Waits = TerminalLiveTests.Waits
    typealias Tasks = TerminalLiveTests.Tasks

    @MainActor private func grace(_ w: Waits, _ t: Tasks, remaining: @escaping () -> TimeInterval = { .infinity },
                                  center: NotificationCenter = NotificationCenter()) -> BackgroundGrace {
        BackgroundGrace(limit: 25, begin: t.begin, end: { t.ended.append($0) }, now: { Date(timeIntervalSince1970: 1_800_000_000) },
                        remaining: remaining, schedule: w.schedule, center: center)
    }

    /// Runs the second's wait, then the deadline it set.
    @MainActor private func runOut(_ w: Waits) {
        w.run()
        w.run()
    }

    /// Work registered before the start, during the grace and after its
    /// end runs once each; the task ends after the last work finished.
    /// Work removed does not run.
    @MainActor func testRegisterBeforeDuringAfter() {
        let w = Waits(), t = Tasks()
        let g = grace(w, t)
        var ran: [String] = []
        var finished: [String: () -> Void] = [:]
        func work(_ k: String) -> BackgroundGrace.Work {
            { done in
                ran.append(k)
                finished[k] = done
            }
        }
        g.register("before", work("before"))
        g.register("gone", work("gone"))
        g.unregister("gone")
        g.start()
        g.register("during", work("during"))
        XCTAssertEqual(ran, [])
        runOut(w)
        XCTAssertEqual(ran.sorted(), ["before", "during"])
        XCTAssertFalse(g.running)
        finished["before"]?()
        XCTAssertEqual(t.ended, [], "until all the work finished")
        finished["during"]?()
        XCTAssertEqual(t.ended, [1])
        finished["during"]?()
        XCTAssertEqual(t.ended, [1])

        g.register("after", work("after"))
        XCTAssertEqual(ran.sorted(), ["after", "before", "during"], "after the end, at once")
        finished["after"]?()
        runOut(w)
        XCTAssertEqual(ran.count, 3)
        XCTAssertEqual(t.ended, [1])

        // Back in front: work registered next waits for the next grace.
        g.cancel()
        g.register("next", work("next"))
        XCTAssertEqual(ran.count, 3)
    }

    /// The app comes back: no work runs, the task ends once, and the work
    /// registered is dropped.
    @MainActor func testCancelRunsNone() {
        let w = Waits(), t = Tasks()
        let g = grace(w, t)
        var ran = 0
        g.register("a") { _ in ran += 1 }
        g.start()
        g.register("b") { _ in ran += 1 }
        XCTAssertTrue(g.cancel())
        XCTAssertEqual(t.ended, [1])
        runOut(w)
        t.expirations[1]?()
        XCTAssertEqual(ran, 0)
        XCTAssertEqual(t.ended, [1])
        XCTAssertFalse(g.cancel(), "none running")
        // Dropped: the next grace runs only what is registered for it.
        g.start()
        runOut(w)
        XCTAssertEqual(ran, 0)
        XCTAssertEqual(t.ended, [1, 2])
    }

    /// A second after the start, the grace reads the time left and ends 5
    /// seconds before it runs out, after 25 seconds at most; with no limit,
    /// after 25 seconds.
    @MainActor func testDeadline() {
        for (left, wait) in [(12.0, 7.0), (100, 25), (30, 25), (3, 0), (.infinity, 25), (.nan, 25)] {
            let w = Waits(), t = Tasks()
            var reads = 0
            let g = grace(w, t, remaining: {
                reads += 1
                return left
            })
            g.start()
            XCTAssertEqual(w.all.map(\.wait), [1], "\(left)")
            XCTAssertEqual(reads, 0, "not read at the start")
            w.run()
            XCTAssertEqual(reads, 1)
            XCTAssertEqual(w.all.map(\.wait), [wait], "\(left)")
            XCTAssertTrue(g.running)
            w.run()
            XCTAssertFalse(g.running)
            XCTAssertEqual(t.ended, [1])
        }
    }

    /// A memory warning ends a running grace, with its work; with no grace
    /// running it does nothing.
    @MainActor func testMemoryWarning() {
        let w = Waits(), t = Tasks(), center = NotificationCenter()
        let g = grace(w, t, center: center)
        var ran = 0
        var finished: (() -> Void)?
        g.register("a") { done in
            ran += 1
            finished = done
        }
        center.post(name: UIApplication.didReceiveMemoryWarningNotification, object: nil)
        XCTAssertEqual(ran, 0)
        XCTAssertEqual(t.begun, 0)

        g.start()
        center.post(name: UIApplication.didReceiveMemoryWarningNotification, object: nil)
        XCTAssertFalse(g.running)
        XCTAssertEqual(ran, 1)
        XCTAssertEqual(t.ended, [])
        finished?()
        XCTAssertEqual(t.ended, [1])
        center.post(name: UIApplication.didReceiveMemoryWarningNotification, object: nil)
        runOut(w)
        XCTAssertEqual(ran, 1)
        XCTAssertEqual(t.ended, [1])
    }

    /// Leaving the app asks for a grace with a real host connected, from
    /// any screen, or with a terminal of a real host open; not with no
    /// host, a host not connected, or Demo Mode alone.
    @MainActor func testNeedsGrace() async throws {
        XCTAssertFalse(HostStore.needsGrace([], terminal: nil))
        let demo = Host(Demo.config())
        demo.connect()
        defer { demo.stop() }
        XCTAssertFalse(HostStore.needsGrace([demo], terminal: nil))
        XCTAssertFalse(HostStore.needsGrace([demo], terminal: demo.id), "a demo terminal keeps nothing")

        let rex = try FakeRex()
        defer { rex.stop() }
        let h = Host(HostConfig(name: "alice-laptop", url: try await rex.url()))
        XCTAssertFalse(HostStore.needsGrace([demo, h], terminal: nil), "not connected")
        XCTAssertTrue(HostStore.needsGrace([demo, h], terminal: h.id), "its terminal is open")
        h.connect()
        defer { h.stop() }
        await until("online") { h.status == .online }
        XCTAssertTrue(HostStore.needsGrace([demo, h], terminal: nil), "connected, no terminal")
    }
}
