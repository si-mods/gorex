import XCTest

/// The toast on screen (docs/IOS-034-toasts.md): one at a time, a new one
/// ends the old, and each timer ends only its own toast. The clock is the test's own.
nonisolated final class ToastTests: XCTestCase {
    /// Timers that fire when the test says.
    @MainActor private final class Timers {
        private(set) var jobs: [Int: (seconds: Double, fire: @MainActor () -> Void)] = [:]
        private(set) var all: [@MainActor () -> Void] = []
        private var next = 0
        var announced: [String] = []

        func toasts() -> Toasts {
            Toasts(schedule: { seconds, fire in
                let id = self.next
                self.next += 1
                self.jobs[id] = (seconds, fire)
                self.all.append(fire)
                return { self.jobs[id] = nil }
            }, announce: { self.announced.append($0) })
        }

        /// The seconds of the one timer that runs.
        var running: Double? { jobs.values.first?.seconds }

        /// Fires the timers that run.
        func fire() {
            let now = Array(jobs.values)
            jobs.removeAll()
            for j in now { j.fire() }
        }
    }

    // MARK: Lifetimes

    /// A short line stays 4 s, and a long one longer, up to 8 s.
    @MainActor func testLifetime() {
        XCTAssertEqual(Toast.lifetime("Uploaded"), 4)
        XCTAssertEqual(Toast.lifetime(String(repeating: "a", count: 80)), 6)
        XCTAssertEqual(Toast.lifetime(String(repeating: "a", count: 500)), 8)
    }

    /// A reason is its first line, cut with an ellipsis past 120 characters.
    @MainActor func testOneLine() {
        XCTAssertEqual(Toast.oneLine("No route\nsecond line"), "No route")
        XCTAssertEqual(Toast.oneLine("  spaced  "), "spaced")
        let cut = Toast.oneLine(String(repeating: "x", count: 300))
        XCTAssertEqual(cut.count, 120)
        XCTAssertTrue(cut.hasSuffix("…"))
        XCTAssertEqual(Toast.oneLine(""), "")
    }

    // MARK: Showing

    /// A failure is an error toast with its title and its reason on one
    /// line, and it goes by itself.
    @MainActor func testFailure() {
        let c = Timers()
        let t = c.toasts()
        t.fail("Can't Open a New Tab", why: "The server is gone.\nTry later.")
        XCTAssertEqual(t.current?.kind, .error)
        XCTAssertEqual(t.current?.symbol, "exclamationmark.circle.fill")
        XCTAssertTrue(t.current?.text.contains("The server is gone.") == true)
        XCTAssertFalse(t.current?.text.contains("Try later.") == true)
        XCTAssertEqual(c.announced.count, 1)
        c.fire()
        XCTAssertNil(t.current)
    }

    /// A new toast replaces the old, and the old one's timer no longer
    /// ends anything.
    @MainActor func testReplaceAndStaleTimer() {
        let c = Timers()
        let t = c.toasts()
        t.status("One")
        let stale = c.all[0]
        t.status("Two")
        XCTAssertEqual(t.current?.text, "Two")
        stale()
        XCTAssertEqual(t.current?.text, "Two")
        c.fire()
        XCTAssertNil(t.current)
    }

    /// A result with nothing to show for it, as its progress was swiped
    /// away, is still announced.
    @MainActor func testSettleAnnouncesWhenNothingShows() {
        let c = Timers()
        let t = c.toasts()
        let id = t.progress("Uploading a.png")
        t.dismiss()
        t.settle(id, with: Toast(kind: .status, text: "Uploaded", seconds: 3))
        XCTAssertNil(t.current)
        XCTAssertEqual(c.announced, ["Uploading a.png", "Uploaded"])
    }

    /// Only the latest host shows the toast: a terminal's over the root,
    /// a sheet's over the terminal's, and each again as the one over it goes.
    @MainActor func testLatestHostShows() {
        let t = Timers().toasts()
        let terminal = UUID(), sheet = UUID()
        XCTAssertTrue(t.shows(nil))
        t.mount(terminal)
        XCTAssertFalse(t.shows(nil))
        XCTAssertTrue(t.shows(terminal))
        t.mount(sheet)
        XCTAssertFalse(t.shows(terminal))
        XCTAssertTrue(t.shows(sheet))
        t.unmount(sheet)
        XCTAssertTrue(t.shows(terminal))
        t.unmount(terminal)
        XCTAssertTrue(t.shows(nil))
    }

    // MARK: Progress

    /// A progress turns into its result in the same place, and the result
    /// has its own time, from then.
    @MainActor func testProgressSettles() {
        let c = Timers()
        let t = c.toasts()
        let id = t.progress("Uploading a.png")
        XCTAssertEqual(t.current?.kind, .progress)
        XCTAssertNil(c.running)
        t.settle(id, with: Toast(kind: .status, text: "Uploaded", seconds: 3))
        XCTAssertEqual(t.current?.id, id)
        XCTAssertEqual(t.current?.text, "Uploaded")
        XCTAssertEqual(c.running, 3)
        XCTAssertEqual(c.announced, ["Uploading a.png", "Uploaded"])
        c.fire()
        XCTAssertNil(t.current)
    }

    /// A progress that was replaced or closed shows a failure, and drops
    /// a success.
    @MainActor func testSettleAfterReplace() {
        let t = Timers().toasts()
        let id = t.progress("Uploading a.png")
        t.status("Other")
        t.settle(id, with: Toast(kind: .status, text: "Uploaded", seconds: 3))
        XCTAssertEqual(t.current?.text, "Other")
        t.settle(id, with: Toast(kind: .error, text: "Can't Upload", seconds: nil))
        XCTAssertEqual(t.current?.text, "Can't Upload")
        let closed = t.progress("Uploading b.png")
        t.dismiss()
        t.settle(closed, with: Toast(kind: .status, text: "Uploaded", seconds: 3))
        XCTAssertNil(t.current)
    }

    /// An error with Retry has no timer. Retry runs it, and the commit,
    /// which tidies up, does not run; closing it runs the commit instead.
    @MainActor func testRetryStays() throws {
        let c = Timers()
        let t = c.toasts()
        var retried = 0
        var tidied = 0
        let id = t.progress("Uploading a.png")
        t.settle(id, with: Toast(kind: .error, text: "Can't Upload: no", button: Toast.Choice(label: "Retry") { retried += 1 },
                                 seconds: nil, commit: { tidied += 1 }))
        XCTAssertNil(c.running)
        t.press(id)
        XCTAssertEqual(retried, 1)
        XCTAssertEqual(tidied, 0)
        XCTAssertNil(t.current)
        t.settle(nil, with: Toast(kind: .error, text: "Can't Upload: no", button: Toast.Choice(label: "Retry") { retried += 1 },
                                  seconds: nil, commit: { tidied += 1 }))
        t.dismiss()
        XCTAssertEqual(retried, 1)
        XCTAssertEqual(tidied, 1)
    }
}
