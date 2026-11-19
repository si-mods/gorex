import Foundation
import Observation
import os

/// The app's recent connection events, as the diagnostics sheet and Developer
/// Mode overlay show them: the last few hundred lines, in memory only, each
/// also in the system log. Lines name hosts, never a ticket, token or password.
enum ConnectionLog {
    struct Entry: Identifiable, Equatable {
        let id: Int
        let timestamp: Date
        /// Compact timestamp for the Developer Mode log ("HH:mm:ss.S").
        let shortTime: String
        /// Full timestamp for exported diagnostics ("HH:mm:ss.SSS").
        let fullTime: String
        let message: String

        var line: String { "\(fullTime) \(message)" }
    }

    @Observable
    final class Store {
        private(set) var entries: [Entry] = []
        @ObservationIgnored private var nextID = 0

        func append(timestamp: Date, shortTime: String, fullTime: String, message: String, limit: Int) {
            nextID += 1
            entries.append(Entry(id: nextID, timestamp: timestamp, shortTime: shortTime, fullTime: fullTime, message: message))
            if entries.count > limit {
                entries.removeFirst(entries.count - limit)
            }
        }

        func clear() {
            entries.removeAll()
        }
    }

    private static let log = Logger(subsystem: "dev.gorex.ios", category: "connection")
    private static let limit = 300
    static let store = Store()

    static var entries: [Entry] { store.entries }
    static var lines: [String] { store.entries.map(\.line) }

    private static let fullTime = {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.dateFormat = "HH:mm:ss.SSS"
        return f
    }()

    private static let shortTime = {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.dateFormat = "HH:mm:ss.S"
        return f
    }()

    static func note(_ line: String) {
        log.notice("\(line, privacy: .public)")
        let now = Date.now
        store.append(
            timestamp: now,
            shortTime: shortTime.string(from: now),
            fullTime: fullTime.string(from: now),
            message: line,
            limit: limit
        )
    }

    static func clear() {
        store.clear()
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
