import Foundation
import os

/// What the keyboard asks of the terminal, call by call, for a report of a
/// keyboard bug: off by default, set in Settings › Terminal; the last
/// thousand lines in memory only, each also in the system log, private, as
/// they hold what was typed.
enum InputTrace {
    static let key = "inputTrace"
    private static let log = Logger(subsystem: "dev.gorex.ios", category: "input")
    private static let limit = 1000
    private(set) static var lines: [String] = []
    /// The keyboard last noted, so a line names it only when it changes.
    private static var keyboard: String?
    private static let time = {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.dateFormat = "HH:mm:ss.SSS"
        return f
    }()

    static var isOn: Bool { UserDefaults.standard.bool(forKey: key) }

    static func note(_ line: @autoclosure () -> String) {
        guard isOn else { return }
        let line = line()
        log.notice("\(line, privacy: .private)")
        lines.append(time.string(from: .now) + " " + line)
        if lines.count > limit { lines.removeFirst(lines.count - limit) }
    }

    /// Notes the keyboard, as its language and its class, when it is not
    /// the one last noted: a third party's is an extension's.
    static func keyboard(_ name: @autoclosure () -> String) {
        guard isOn, case let k = name(), k != keyboard else { return }
        keyboard = k
        note("keyboard " + k)
    }

    static func clear() {
        lines = []
        keyboard = nil
    }

    /// Text in quotes, with control and invisible characters, the anchor
    /// among them, written out: "\u{200B}a\r".
    static func quote(_ s: String?) -> String {
        guard let s else { return "nil" }
        var q = "\""
        for u in s.unicodeScalars {
            switch u {
            case "\r": q += "\\r"
            case "\n": q += "\\n"
            case "\t": q += "\\t"
            case "\"": q += "\\\""
            case "\\": q += "\\\\"
            default:
                if CharacterSet.controlCharacters.contains(u) || u.properties.generalCategory == .format {
                    q += "\\u{" + String(u.value, radix: 16, uppercase: true) + "}"
                } else {
                    q.unicodeScalars.append(u)
                }
            }
        }
        return q + "\""
    }

    static func range(_ r: NSRange) -> String { "{\(r.location),\(r.length)}" }

    static func edits(_ e: [InputDocument.Edit]) -> String {
        "[" + e.map { e in
            switch e {
            case .type(let t): "type(\(quote(t)))"
            case .text(let t): "text(\(quote(t)))"
            case .backspace: "backspace"
            case .erase(let n): "erase(\(n))"
            case .preedit(let t): "preedit(\(quote(t)))"
            }
        }.joined(separator: ", ") + "]"
    }
}

extension InputDocument {
    /// The document as a trace line ends: typed="a" marked=nil sel={2,0}.
    var traced: String {
        "typed=\(InputTrace.quote(typed)) marked=\(InputTrace.quote(marked)) sel=\(InputTrace.range(selection))"
    }
}
