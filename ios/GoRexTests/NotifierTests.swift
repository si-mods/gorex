import Foundation
import XCTest
import os

/// A notification delegate must answer UIKit on the main thread: right after
/// the answer, UIKit takes the app's snapshot and state restoration there, and
/// asserts it. The async delegate forms answered from the cooperative pool and
/// crashed every tap (build 1.1 (86)); these pin the completion handlers' hop.
nonisolated final class NotifierTests: XCTestCase {
    /// Asked from the pool, the answer comes on the main thread, not there.
    func testAnswerFromThePoolComesOnMain() {
        let answered = expectation(description: "answered")
        let onMain = OSAllocatedUnfairLock(initialState: false)
        DispatchQueue.global().async {
            Notifier.onMain {
                onMain.withLock { $0 = Thread.isMainThread }
                answered.fulfill()
            }
        }
        wait(for: [answered], timeout: 5)
        XCTAssertTrue(onMain.withLock { $0 })
    }

    /// Asked on the main thread, the answer is done before it returns, so the
    /// tap's terminal is open when UIKit looks.
    func testAnswerOnMainIsImmediate() async {
        let answered = OSAllocatedUnfairLock(initialState: false)
        await MainActor.run {
            Notifier.onMain { answered.withLock { $0 = true } }
        }
        XCTAssertTrue(answered.withLock { $0 })
    }
}
