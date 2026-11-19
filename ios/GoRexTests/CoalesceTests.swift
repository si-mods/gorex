import Foundation
import XCTest
import os

/// The terminal's work that comes faster than it is done is done once for
/// all of it: Ghostty's ticks, the actions' work for a view, and the writes
/// of a paste.
nonisolated final class CoalesceTests: XCTestCase {
    /// Wakeups while a tick waits on the main queue queue no other tick; one
    /// during the tick queues the next, so none is lost.
    @MainActor func testCoalescedQueuesOneTick() async {
        let c = Coalesced()
        let ran = Ran()
        XCTAssertTrue(c.run { MainActor.assumeIsolated { ran.count += 1 } })
        for _ in 0..<100 {
            XCTAssertFalse(c.run { MainActor.assumeIsolated { ran.count += 1 } })
        }
        await drainMain()
        XCTAssertEqual(ran.count, 1)

        let again = expectation(description: "queued from its own work")
        XCTAssertTrue(c.run {
            MainActor.assumeIsolated {
                ran.count += 1
                ran.queued = c.run { MainActor.assumeIsolated { ran.count += 1; again.fulfill() } }
            }
        })
        await fulfillment(of: [again], timeout: 5)
        XCTAssertTrue(ran.queued)
        XCTAssertEqual(ran.count, 3)
    }

    /// Wakeups from many threads at once: each work queued runs once, and
    /// the work that ran is the work queued.
    func testCoalescedFromThreads() {
        let queue = DispatchQueue(label: "coalesce-test")
        let c = Coalesced(on: queue)
        let queued = OSAllocatedUnfairLock(initialState: 0)
        let ran = OSAllocatedUnfairLock(initialState: 0)
        DispatchQueue.concurrentPerform(iterations: 1000) { _ in
            if c.run({ ran.withLock { $0 += 1 } }) { queued.withLock { $0 += 1 } }
        }
        // The queue is serial: what was queued before this ran before it.
        let flushed = expectation(description: "flushed")
        queue.async { flushed.fulfill() }
        wait(for: [flushed], timeout: 5)
        XCTAssertGreaterThanOrEqual(queued.withLock { $0 }, 1)
        XCTAssertEqual(ran.withLock { $0 }, queued.withLock { $0 })
    }

    /// The asks that come before the work is done schedule it once, and it
    /// does all they asked for.
    @MainActor func testFrameWorkSchedulesOnce() {
        var scheduled = 0
        let w = FrameWork { scheduled += 1 }
        w.owe(.selection)
        w.owe([.selection, .scroll])
        w.owe(.scroll)
        w.owe([])
        XCTAssertEqual(scheduled, 1)
        XCTAssertEqual(w.take(), [.selection, .scroll])
        XCTAssertEqual(w.take(), [])
        w.owe(.scroll)
        XCTAssertEqual(scheduled, 2)
        XCTAssertEqual(w.take(), .scroll)
    }

    /// A paste drains in writes of at most 64 KiB, whole, in order, and what
    /// is held never grows past twice what still waits: the written bytes
    /// are not copied again on each write.
    func testPendingBytesDrain() {
        let text = String(repeating: "中\u{1}", count: 1_000_000)
        var p = PendingBytes()
        p += Data(text.utf8)
        XCTAssertEqual(p.count, text.utf8.count)
        var parts: [String] = []
        while let (n, part) = p.nextWrite(max: 64 << 10) {
            XCTAssertLessThanOrEqual(n, 64 << 10)
            parts.append(part)
            p.removeFirst(n)
            XCTAssertLessThan(p.held, 2 * p.count + 1)
            // Typed meanwhile, it joins the end.
            if parts.count == 3 { p += Data("end".utf8) }
        }
        XCTAssertTrue(p.isEmpty)
        XCTAssertEqual(p.held, 0)
        XCTAssertEqual(parts.joined(), text + "end")
    }

    /// A character cut off at the end waits, unless flushed, as Data's.
    func testPendingBytesCutCharacter() {
        var p = PendingBytes()
        p += Data("ab中".utf8)
        p.removeFirst(1)
        p += Data([0xE4])
        XCTAssertNil(p.nextWrite(max: 64))
        XCTAssertEqual(p.nextWrite(max: 64, flush: true)?.count, 5)
        p += Data([0xB8, 0xAD])
        XCTAssertEqual(p.nextWrite(max: 64)?.text, "b中中")
    }

    @MainActor private func drainMain() async {
        let drained = expectation(description: "main queue drained")
        DispatchQueue.main.async { drained.fulfill() }
        await fulfillment(of: [drained], timeout: 5)
    }
}

@MainActor private final class Ran {
    var count = 0
    var queued = false
}
