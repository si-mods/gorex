import Foundation

/// The last row with text in a range of rows, found with reads of runs of
/// rows. Ghostty joins soft-wrapped rows with no newline, so the text of a
/// run cannot tell which row a character is on; it tells only that the run
/// has text, which is so when one of its rows has. From the bottom up, the
/// runs are 1, 2, 4… rows long until one has text, then halves of it: a few
/// reads, not one for each row, and one read when the last row has text.
nonisolated enum LastDrawn {
    /// The last row in rows for which drawn is true of each run holding
    /// it, else nil. drawn of a run is true when one of its rows has text.
    static func row(in rows: ClosedRange<Int>, drawn: (ClosedRange<Int>) -> Bool) -> Int? {
        // Every row under the run read is blank.
        var hi = rows.upperBound, size = 1
        var run: ClosedRange<Int>
        while true {
            let lo = max(rows.lowerBound, hi - size + 1)
            if drawn(lo...hi) {
                run = lo...hi
                break
            }
            guard lo > rows.lowerBound else { return nil }
            hi = lo - 1
            size *= 2
        }
        // The run has text, and every row under it is blank.
        while run.count > 1 {
            let upper = (run.lowerBound + run.count / 2)...run.upperBound
            run = drawn(upper) ? upper : run.lowerBound...(upper.lowerBound - 1)
        }
        return run.lowerBound
    }

    /// Whether UTF-8 text has a character that is not whitespace, as
    /// Character.isWhitespace tells, with no String for ASCII text: an ASCII
    /// byte other than a space or a tab, line or page break is a character
    /// of its own or the first of one, and is not whitespace.
    static func hasText(_ bytes: UnsafeRawBufferPointer) -> Bool {
        var ascii = true
        for b in bytes {
            if b >= 0x80 {
                ascii = false
            } else if b != 0x20, !(0x09...0x0D).contains(b) {
                return true
            }
        }
        return !ascii && !String(decoding: bytes, as: UTF8.self).allSatisfy(\.isWhitespace)
    }
}
