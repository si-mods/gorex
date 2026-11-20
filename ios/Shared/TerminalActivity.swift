import ActivityKit
import Foundation

/// The terminal on screen as a Live Activity: its program, its state and
/// its latest line on the Lock Screen and in the Dynamic Island
/// (docs/ios-live-activities.md, Terminal activity). The app starts and
/// updates it; the widget extension draws it. It is encoded with every
/// update, attributes and state together under ActivityKit's 4 KB.
nonisolated struct TerminalActivity: ActivityAttributes, Hashable, Sendable {
    var host: UUID
    var hostName: String
    var session: String
    var block: String

    struct ContentState: Codable, Hashable, Sendable {
        /// The window's title.
        var title: String
        /// The program whose tile it shows, by its id or its process.
        var program: String?
        /// The block's most urgent record; nil for none, as a plain shell.
        var record: Record?
        /// The line that tells the latest state: a blocked record's
        /// message, else the line screenLine picks from the screen.
        var line: String
        /// Whether the stream is open; false once the app left it.
        var connected: Bool
    }

    /// An OSC 7501 record, as a status inbox's row shows it.
    struct Record: Codable, Hashable, Sendable {
        /// blocked, error, done or working.
        var state: String
        var kind: String?
        var progress: Int?
        var title: String?
        var updated: Date?
    }

    /// The most characters of the line, and of a title or a name, and the
    /// most bytes of each, for characters of many combining marks.
    static let maxLine = 80
    static let maxLineBytes = 240
    static let maxText = 200
    static let maxTextBytes = 400

    /// The text cut to at most max characters and at most bytes UTF-8
    /// bytes, whole characters only.
    static func cut(_ s: String, _ max: Int = maxText, bytes: Int = maxTextBytes) -> String {
        if s.utf8.count <= bytes, s.count <= max { return s }
        var n = 0, used = 0, end = s.startIndex
        for c in s {
            let b = c.utf8.count
            if n == max || used + b > bytes { break }
            n += 1
            used += b
            end = s.index(after: end)
        }
        return String(s[..<end])
    }

    /// The last line of a screen's text that is not blank, without the
    /// spaces at its end, cut to maxLine characters.
    static func lastLine(_ text: String) -> String {
        let line = text.split(whereSeparator: \.isNewline).last { !$0.allSatisfy(\.isWhitespace) } ?? ""
        var s = Substring(line)
        while s.last?.isWhitespace == true { s.removeLast() }
        return cut(String(s), maxLine, bytes: maxLineBytes)
    }

    /// The line of a screen's rows that tells the latest state, cursor the
    /// cursor's row among them, cut as lastLine cuts. A program that draws
    /// text under the cursor's row is a full-screen program with its cursor
    /// in its input, and a status bar at the bottom: the line is the first
    /// above its input that is not blank, a rule of box-drawing characters,
    /// a line in a box, or the input's own prompt lines. Else, as for a
    /// shell, it is the last line at or above the cursor's that is not
    /// blank. Without a cursor, or with no such line, it is lastLine's.
    static func screenLine(_ rows: [String], cursor: Int?) -> String {
        let fallback = lastLine(rows.joined(separator: "\n"))
        guard let c = cursor, rows.indices.contains(c) else { return fallback }
        func blank(_ s: String) -> Bool { s.allSatisfy(\.isWhitespace) }
        let pick: String?
        if rows[(c + 1)...].contains(where: { !blank($0) }) {
            // The input's lines: the cursor's row, and the lines right above
            // it up to the topmost that starts with a prompt.
            var top = c, k = c - 1
            while k >= 0, !blank(rows[k]), !rule(rows[k]), !boxed(rows[k]) {
                if prompt(rows[k]) { top = k }
                k -= 1
            }
            pick = rows[..<top].last { !blank($0) && !rule($0) && !boxed($0) }
        } else {
            pick = rows[...c].last { !blank($0) }
        }
        guard let pick else { return fallback }
        return lastLine(pick)
    }

    /// Box-drawing and block characters, which TUIs draw rules and boxes of.
    private static func boxChar(_ c: Character) -> Bool {
        c.unicodeScalars.allSatisfy { (0x2500...0x259F).contains($0.value) }
    }

    /// Characters a box's sides are drawn with.
    private static let verticals: Set<Character> = ["│", "┃", "║", "╎", "╏", "┆", "┇", "┊", "┋", "┤", "├", "╢", "╟"]

    /// A line of box-drawing characters and spaces only, such as a rule or
    /// a box's top or bottom.
    private static func rule(_ s: String) -> Bool {
        s.contains { !$0.isWhitespace } && s.allSatisfy { $0.isWhitespace || boxChar($0) }
    }

    /// A line inside a box: it starts and ends with one of its sides.
    private static func boxed(_ s: String) -> Bool {
        let t = s.trimmingCharacters(in: .whitespaces)
        guard t.count >= 2, let first = t.first, let last = t.last else { return false }
        return verticals.contains(first) && verticals.contains(last)
    }

    /// Characters an input's prompt starts with.
    private static let prompts: Set<Character> = [">", "›", "❯", "▌", "»", "▶"]

    /// A line that starts with an input's prompt, after spaces and a box's
    /// side.
    private static func prompt(_ s: String) -> Bool {
        let t = s.drop(while: { $0.isWhitespace || verticals.contains($0) })
        return t.first.map { prompts.contains($0) } ?? false
    }

    /// What a tap opens: this terminal, as a widget's row opens one.
    var url: URL { WidgetLink.open(host: host, session: session, block: block).url }
}
