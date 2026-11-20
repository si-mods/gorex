import ActivityKit
import Foundation

/// The terminal on screen as a Live Activity: its program, its state and
/// its last line on the Lock Screen and in the Dynamic Island
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
        /// The last line the screen shows that is not blank.
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

    /// The most characters of the line, and of a title or a name.
    static let maxLine = 80
    static let maxText = 200

    /// The text cut to max characters.
    static func cut(_ s: String, _ max: Int = maxText) -> String {
        s.count > max ? String(s.prefix(max)) : s
    }

    /// The last line of a screen's text that is not blank, without the
    /// spaces at its end, cut to maxLine characters.
    static func lastLine(_ text: String) -> String {
        let line = text.split(whereSeparator: \.isNewline).last { !$0.allSatisfy(\.isWhitespace) } ?? ""
        var s = Substring(line)
        while s.last?.isWhitespace == true { s.removeLast() }
        return cut(String(s), maxLine)
    }

    /// What a tap opens: this terminal, as a widget's row opens one.
    var url: URL { WidgetLink.open(host: host, session: session, block: block).url }
}
