import Foundation
import Observation
import os

/// The app's recent connection events, as the diagnostics sheet and Developer
/// Mode overlay show them: the last two thousand lines, each also in the
/// system log and in a file in Application Support, so a restart shows the
/// run before it. Lines name hosts, never a ticket, token or password.
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

        /// The file's lines the store starts with, in a run after one that
        /// wrote them.
        init(lines: [String] = []) {
            for line in lines { append(line: line, limit: limit) }
        }

        func append(timestamp: Date, shortTime: String, fullTime: String, message: String, limit: Int) {
            nextID += 1
            entries.append(Entry(id: nextID, timestamp: timestamp, shortTime: shortTime, fullTime: fullTime, message: message))
            if entries.count > limit {
                entries.removeFirst(entries.count - limit)
            }
        }

        /// An entry back from a file line, "HH:mm:ss.SSS message"; the file
        /// keeps no date, which nothing shows, so it stays distant past.
        func append(line: String, limit: Int) {
            guard line.count > 13 else { return }
            let time = line.prefix(12)
            append(
                timestamp: .distantPast,
                shortTime: String(time.prefix(10)),
                fullTime: String(time),
                message: String(line.dropFirst(13)),
                limit: limit
            )
        }

        func clear() {
            entries.removeAll()
        }
    }

    /// The file the same lines persist to, under Application Support and out
    /// of backups. Writes go on one serial queue: a caller of note() never
    /// waits on io, and an io error is reported once, then quiet — the log
    /// failing never breaks the app.
    final class File {
        private let url: URL
        private let queue = DispatchQueue(label: "dev.gorex.ios.connectionlog")
        // The rest runs on queue.
        private var handle: FileHandle?
        /// The lines the file holds, as read() counted and append() grew it.
        private var count = 0
        /// io failed once already: later errors stay quiet.
        private var reported = false

        init(url: URL) { self.url = url }

        /// The file's lines, as note() wrote them, for the store's first
        /// fill. A caller waits on the writes queued before it.
        func read() -> [String] {
            queue.sync {
                guard let data = try? Data(contentsOf: url),
                      let text = String(data: data, encoding: .utf8) else { return [] }
                let all = text.split(separator: "\n").map(String.init)
                count = all.count
                return all
            }
        }

        /// A line for the file's end, behind the ones queued before it.
        func append(_ line: String) {
            queue.async { [self] in
                guard let h = writer() else { return }
                do {
                    try h.write(contentsOf: Data((line + "\n").utf8))
                    count += 1
                    if count > limit + slack { trim() }
                } catch { report(error) }
            }
        }

        /// The file gone, once the writes queued before it have landed.
        func clear() {
            queue.sync {
                try? handle?.close()
                handle = nil
                try? FileManager.default.removeItem(at: url)
                count = 0
            }
        }

        /// The file's handle at its end, making the file and its directory
        /// as needed.
        private func writer() -> FileHandle? {
            if let handle { return handle }
            do {
                try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
                if !FileManager.default.fileExists(atPath: url.path) {
                    _ = FileManager.default.createFile(atPath: url.path, contents: nil)
                    // Remade every run: no place in a backup.
                    var u = url
                    var v = URLResourceValues()
                    v.isExcludedFromBackup = true
                    try? u.setResourceValues(v)
                }
                let h = try FileHandle(forWritingTo: url)
                _ = try h.seekToEnd()
                handle = h
                return h
            } catch {
                report(error)
                return nil
            }
        }

        /// The file cut back to its last limit lines.
        private func trim() {
            do {
                try handle?.close()
                handle = nil
                let all = String(decoding: try Data(contentsOf: url), as: UTF8.self).split(separator: "\n")
                let tail = all.suffix(limit)
                try tail.joined(separator: "\n").appending("\n").write(to: url, atomically: true, encoding: .utf8)
                count = tail.count
            } catch { report(error) }
        }

        /// An io error in the system log, once; the app never sees it.
        private func report(_ error: Error) {
            guard !reported else { return }
            reported = true
            log.error("connection log file: \(error.localizedDescription, privacy: .public)")
        }
    }

    private static let log = Logger(subsystem: "dev.gorex.ios", category: "connection")
    private static let limit = 2000
    /// The lines the file may hold past the limit before it is rewritten to
    /// its tail: a rewrite per that many new ones, not one per line.
    private static let slack = 500
    static let store = Store(lines: file.read())
    private static let file = File(url: URL.applicationSupportDirectory.appending(path: "connection.log"))

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
        let time = fullTime.string(from: now)
        store.append(
            timestamp: now,
            shortTime: shortTime.string(from: now),
            fullTime: time,
            message: line,
            limit: limit
        )
        file.append("\(time) \(line)")
    }

    static func clear() {
        store.clear()
        file.clear()
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
