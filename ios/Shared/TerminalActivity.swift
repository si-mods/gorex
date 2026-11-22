import ActivityKit
import Foundation

/// The terminal on screen as a Live Activity: its program, its state and
/// its latest line on the Lock Screen and in the Dynamic Island
/// (docs/IOS-006-live-activities.md, Terminal activity). The app starts and
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
        /// The line that tells the latest state: the record's message
        /// (TerminalLive.state), else the line screenLine picks from the
        /// screen.
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
    /// spaces at its end or what iOS cannot show (shownText), cut to
    /// maxLine characters.
    static func lastLine(_ text: String) -> String {
        let line = text.split(whereSeparator: \.isNewline).last { !$0.allSatisfy(\.isWhitespace) } ?? ""
        var s = String(line).shownText[...]
        while s.last?.isWhitespace == true { s.removeLast() }
        return cut(String(s), maxLine, bytes: maxLineBytes)
    }

    /// The line of a screen's rows that tells the latest state, cursor the
    /// cursor's row among them, cut as lastLine cuts. A TUI agent draws its
    /// input near the bottom with its status lines under it, and the line
    /// is the first above its input that is not blank, a rule, a line in a
    /// box or the input's own; it is found by its input, wherever the
    /// cursor is (inputTop), or, failing that, by text under the cursor's
    /// row. Else, as for a shell, it is the last line at or above the
    /// cursor's that is not blank. With no such line, it is lastLine's.
    static func screenLine(_ rows: [String], cursor: Int?) -> String {
        let c = cursor.flatMap { rows.indices.contains($0) ? $0 : nil }
        var top = inputTop(rows, cursor: c)
        if top == nil, let c, rows[(c + 1)...].contains(where: { !blank($0) }) {
            top = promptTop(rows, from: c)
        }
        let pick: String?
        if let top {
            pick = rows[..<top].last { !blank($0) && !rule($0) && !boxed($0) }
                .map { String($0.drop(while: \.isWhitespace)) }
        } else {
            pick = c.flatMap { at in rows[...at].last { !blank($0) } }
        }
        return lastLine(pick ?? rows.joined(separator: "\n"))
    }

    /// The most status lines under a TUI's input, and the most rows of it.
    private static let maxFooter = 5
    private static let maxInput = 10

    /// The top row of a TUI's input drawn at the bottom of the screen, nil
    /// for none: rows between two rules, under which at most maxFooter
    /// lines follow, holding a prompt or the cursor (Claude Code, Devin,
    /// Pi); or, without rules, the lowest line that starts with a prompt
    /// only TUIs draw, with a blank line or a rule above its lines and at
    /// most maxFooter lines under it (Codex). A shell's prompt at the
    /// bottom has neither, nor does a table a command printed, without a
    /// prompt in it or the cursor.
    private static func inputTop(_ rows: [String], cursor: Int?) -> Int? {
        guard var i = rows.lastIndex(where: { !blank($0) }) else { return nil }
        let last = i
        var footer = 0
        while !rule(rows[i]) {
            if !blank(rows[i]) {
                if i < last, tuiPrompt(rows[i]) {
                    let t = promptTop(rows, from: i)
                    return t == 0 || blank(rows[t - 1]) || rule(rows[t - 1]) ? t : nil
                }
                footer += 1
                if footer > maxFooter { return nil }
            }
            guard i > 0 else { return nil }
            i -= 1
        }
        let bottom = i
        var j = bottom - 1
        while j >= 0, bottom - j <= maxInput + 1, !rule(rows[j]) { j -= 1 }
        guard j >= 0, rule(rows[j]) else { return nil }
        let inside = (j + 1)..<bottom
        guard inside.contains(where: { prompt(rows[$0]) }) || cursor.map { inside.contains($0) } == true else { return nil }
        return j
    }

    /// The top of an input whose last row is row: it and the lines right
    /// above it up to the topmost that starts with a prompt.
    private static func promptTop(_ rows: [String], from row: Int) -> Int {
        var top = row, k = row - 1
        while k >= 0, !blank(rows[k]), !rule(rows[k]), !boxed(rows[k]) {
            if prompt(rows[k]) { top = k }
            k -= 1
        }
        return top
    }

    private static func blank(_ s: String) -> Bool { s.allSatisfy(\.isWhitespace) }

    /// Box-drawing and block characters, which TUIs draw rules and boxes of.
    private static func boxChar(_ c: Character) -> Bool {
        c.unicodeScalars.allSatisfy { (0x2500...0x259F).contains($0.value) }
    }

    /// Characters a box's sides are drawn with.
    private static let verticals: Set<Character> = ["│", "┃", "║", "╎", "╏", "┆", "┇", "┊", "┋", "┤", "├", "╢", "╟"]

    /// A line of box-drawing characters and spaces only, such as a rule or
    /// a box's top or bottom; or the edge of a box that carries a title,
    /// drawn as a run of them on each side (──── title ────): it opens
    /// with two or more and ends with one. A side of a box alone opens
    /// none (│ text │), nor does a tree's ├─.
    static func rule(_ s: String) -> Bool {
        guard s.contains(where: { !$0.isWhitespace }) else { return false }
        if s.allSatisfy({ $0.isWhitespace || boxChar($0) }) { return true }
        let t = s.trimmingCharacters(in: .whitespaces)
        let lead = t.prefix(while: boxChar).count
        return lead >= 2 && t.last.map(boxChar) == true
    }

    /// A line inside a box: it starts and ends with one of its sides.
    private static func boxed(_ s: String) -> Bool {
        let t = s.trimmingCharacters(in: .whitespaces)
        guard t.count >= 2, let first = t.first, let last = t.last else { return false }
        return verticals.contains(first) && verticals.contains(last)
    }

    /// Characters an input's prompt starts with, and those of them shells
    /// do not use: Codex's, Devin's and others'.
    private static let prompts: Set<Character> = [">", "›", "❯", "❭", "▌", "»", "▶"]
    private static let tuiPrompts: Set<Character> = ["›", "❭", "▌"]

    /// A line that starts with an input's prompt, after spaces and a box's
    /// side.
    private static func prompt(_ s: String) -> Bool {
        let t = s.drop(while: { $0.isWhitespace || verticals.contains($0) })
        return t.first.map { prompts.contains($0) } ?? false
    }

    private static func tuiPrompt(_ s: String) -> Bool {
        let t = s.drop(while: { $0.isWhitespace || verticals.contains($0) })
        return t.first.map { tuiPrompts.contains($0) } ?? false
    }

    /// What a tap opens: this terminal, as a widget's row opens one.
    var url: URL { WidgetLink.open(host: host, session: session, block: block).url }
}
