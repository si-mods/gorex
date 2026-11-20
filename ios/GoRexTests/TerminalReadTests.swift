import Foundation
import XCTest
import os

/// The terminal's reads: the last row with text under the cursor, found
/// with reads of runs of rows, and the read of what Ghostty encodes.
nonisolated final class TerminalReadTests: XCTestCase {
    /// The row a read of each row from the bottom up finds, as
    /// drawnBottom read before.
    private func linear(_ drawn: [Bool], _ rows: ClosedRange<Int>) -> Int? {
        rows.reversed().first { drawn[$0] }
    }

    /// Every pattern of drawn rows up to 10 rows, in every range: the same
    /// row as a read of each row, and each run read in the range.
    func testLastDrawnFindsTheRowEachRowsReadFinds() {
        for n in 1...10 {
            for bits in 0..<(1 << n) {
                let drawn = (0..<n).map { bits & (1 << $0) != 0 }
                for lo in 0..<n {
                    let rows = lo...(n - 1)
                    let found = LastDrawn.row(in: rows) { run in
                        XCTAssertTrue(rows.contains(run.lowerBound) && rows.contains(run.upperBound))
                        return run.contains { drawn[$0] }
                    }
                    XCTAssertEqual(found, linear(drawn, rows), "rows \(rows) of \(drawn)")
                }
            }
        }
    }

    /// One read when the last row has text, as before; a few reads, not
    /// one for each row, when none has.
    func testLastDrawnReads() {
        var reads = 0
        XCTAssertEqual(LastDrawn.row(in: 0...59) { _ in reads += 1; return true }, 59)
        XCTAssertEqual(reads, 1)

        reads = 0
        XCTAssertNil(LastDrawn.row(in: 0...59) { _ in reads += 1; return false })
        XCTAssertLessThanOrEqual(reads, 7)

        // Text on row 3 alone.
        reads = 0
        XCTAssertEqual(LastDrawn.row(in: 0...59) { run in reads += 1; return run.contains(3) }, 3)
        XCTAssertLessThanOrEqual(reads, 13)
    }

    /// The same answer as Character.isWhitespace over the whole text.
    func testHasTextAsCharacterIsWhitespace() {
        let samples = ["", " ", "   \n\n  ", "\t\r\n\u{0B}\u{0C}", "a", "  x  ", "\u{0}", "\u{1B}", "\u{7F}",
                       "\u{A0}", "\u{3000}", " \u{3000} ", "\u{2028}", "\u{85}", "é", " 中 ", "\u{3000}a", " \u{301}",
                       "\u{1F600}", "\r\n", "\u{200B}"]
        for s in samples {
            let want = !s.allSatisfy(\.isWhitespace)
            let got = Array(s.utf8).withUnsafeBytes { LastDrawn.hasText($0) }
            XCTAssertEqual(got, want, "\(s.unicodeScalars.map { String($0.value, radix: 16) })")
        }
    }

    /// All that is written comes through, in order, over more than one
    /// buffer; the end of the file stops the reader and leaves the group.
    func testFDReaderDeliversAllAndStopsAtEnd() throws {
        var fds: [Int32] = [0, 0]
        XCTAssertEqual(pipe(&fds), 0)
        let sent = Data((0..<(FDReader.bufferSize * 3 + 123)).map { UInt8(truncatingIfNeeded: $0 &* 31) })
        let got = OSAllocatedUnfairLock(initialState: Data())
        let group = DispatchGroup()
        let source = FDReader.start(fds[0], group) { d in got.withLock { $0.append(d) } }
        let stopped = expectation(description: "the reader stopped")
        group.notify(queue: .global()) { stopped.fulfill() }
        sent.withUnsafeBytes { p in
            var off = 0
            while off < p.count {
                let n = write(fds[1], p.baseAddress! + off, p.count - off)
                if n > 0 { off += n } else if errno != EINTR { break }
            }
        }
        close(fds[1])
        wait(for: [stopped], timeout: 10)
        XCTAssertTrue(source.isCancelled)
        XCTAssertEqual(got.withLock { $0 }, sent)
        close(fds[0])
    }

    /// A cancel stops the reader, with no end of file, and leaves the group.
    func testFDReaderCancel() {
        var fds: [Int32] = [0, 0]
        XCTAssertEqual(pipe(&fds), 0)
        let group = DispatchGroup()
        let source = FDReader.start(fds[0], group) { _ in }
        let stopped = expectation(description: "the reader stopped")
        group.notify(queue: .global()) { stopped.fulfill() }
        source.cancel()
        wait(for: [stopped], timeout: 10)
        close(fds[0])
        close(fds[1])
    }
}
