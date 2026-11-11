import Foundation
import os

/// The app's recent connection events, as the diagnostics sheet shows
/// them: the last few hundred lines, in memory only, each also in the
/// system log. Lines name hosts, never a ticket, token or password.
enum ConnectionLog {
    private static let log = Logger(subsystem: "dev.gorex.ios", category: "connection")
    private static let limit = 300
    private(set) static var lines: [String] = []
    private static let time = {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.dateFormat = "HH:mm:ss.SSS"
        return f
    }()

    static func note(_ line: String) {
        log.notice("\(line, privacy: .public)")
        lines.append(time.string(from: .now) + " " + line)
        if lines.count > limit { lines.removeFirst(lines.count - limit) }
    }
}

extension Error {
    /// The error as a developer reads it: its domain, code and text, as
    /// "NSURLErrorDomain -1001: The request timed out."
    var diagnosis: String {
        let e = self as NSError
        return "\(e.domain) \(e.code): \(e.localizedDescription)"
    }
}

extension Duration {
    /// Milliseconds, as a log line shows them: "1234 ms".
    var ms: String { "\(Int(self / .milliseconds(1))) ms" }
}
