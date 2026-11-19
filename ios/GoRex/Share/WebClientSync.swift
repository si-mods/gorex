import CryptoKit
import Foundation

/// The disk and network work of WebClientStore.update(), off the main actor
/// (#861): it stages the files next to WebClient/, copies the cached files
/// whose SHA-256 still matches, downloads the others at most
/// `maxDownloads` at a time, and swaps the staging folder in.
nonisolated enum WebClientSync {
    /// One file of files.json that the client needs.
    struct Entry: Sendable, Equatable {
        let path: String
        let sha256: String
    }

    /// Reads one file of the web client by its path in files.json.
    typealias Fetch = @Sendable (String) async throws -> Data

    /// Downloads at the same time, at most.
    static let maxDownloads = 4

    /// Stages `entries` and `indexData` (files.json), then puts them in
    /// place of `directory`. `progress` gets the count of files done, in
    /// increasing order. On an error, `directory` stays as it was.
    @concurrent
    static func sync(entries: [Entry], indexData: Data, into directory: URL,
                     fetch: @escaping Fetch,
                     progress: @escaping @Sendable (Int) async -> Void) async throws {
        let fm = FileManager.default
        let parent = directory.deletingLastPathComponent()
        try fm.createDirectory(at: parent, withIntermediateDirectories: true)
        let staging = parent.appendingPathComponent("WebClient.staging-\(UUID().uuidString)", isDirectory: true)
        try fm.createDirectory(at: staging, withIntermediateDirectories: true)
        defer { try? fm.removeItem(at: staging) }

        // Reuse an already-cached file if its SHA-256 matches: a copy (a
        // clone on APFS), not a read and a write.
        var done = 0
        var missing: [Entry] = []
        for entry in entries {
            let dst = staging.appendingPathComponent(entry.path)
            try fm.createDirectory(at: dst.deletingLastPathComponent(), withIntermediateDirectories: true)
            let cached = directory.appendingPathComponent(entry.path)
            if (try? sha256Hex(fileAt: cached)) == entry.sha256 {
                try? fm.removeItem(at: dst)
                try fm.copyItem(at: cached, to: dst)
                done += 1
            } else {
                missing.append(entry)
            }
        }
        await progress(done)

        try await withThrowingTaskGroup(of: Void.self) { group in
            var next = 0
            while next < missing.count && next < maxDownloads {
                let entry = missing[next]
                group.addTask { try await download(entry, into: staging, fetch: fetch) }
                next += 1
            }
            while try await group.next() != nil {
                done += 1
                await progress(done)
                if next < missing.count {
                    let entry = missing[next]
                    group.addTask { try await download(entry, into: staging, fetch: fetch) }
                    next += 1
                }
            }
        }

        try indexData.write(to: staging.appendingPathComponent("files.json"), options: .atomic)
        if fm.fileExists(atPath: directory.path) {
            _ = try fm.replaceItemAt(directory, withItemAt: staging)
        } else {
            try fm.moveItem(at: staging, to: directory)
        }
    }

    /// Fetches one file, checks its SHA-256 and writes it under `staging`.
    private static func download(_ entry: Entry, into staging: URL, fetch: Fetch) async throws {
        let data = try await fetch(entry.path)
        guard sha256Hex(data) == entry.sha256 else {
            throw NSError(domain: "WebClientStore", code: -1,
                          userInfo: [NSLocalizedDescriptionKey: "Checksum mismatch for \(entry.path)"])
        }
        try data.write(to: staging.appendingPathComponent(entry.path), options: .atomic)
    }

    /// Reads `path` under `base` over HTTP; a status other than 200 is an error.
    static func httpFetch(base: URL) -> Fetch {
        { path in
            let (data, resp) = try await URLSession.shared.data(from: base.appendingPathComponent(path))
            if let http = resp as? HTTPURLResponse, http.statusCode != 200 {
                throw NSError(domain: "WebClientStore", code: http.statusCode,
                              userInfo: [NSLocalizedDescriptionKey: "HTTP \(http.statusCode) fetching \(path)"])
            }
            return data
        }
    }

    static func sha256Hex(_ data: Data) -> String {
        hex(SHA256.hash(data: data))
    }

    /// The SHA-256 of a file, read 1 MiB at a time.
    static func sha256Hex(fileAt url: URL) throws -> String {
        let handle = try FileHandle(forReadingFrom: url)
        defer { try? handle.close() }
        var hasher = SHA256()
        while true {
            let chunk = try autoreleasepool { try handle.read(upToCount: 1 << 20) }
            guard let chunk, !chunk.isEmpty else { break }
            hasher.update(data: chunk)
        }
        return hex(hasher.finalize())
    }

    private static func hex(_ digest: SHA256.Digest) -> String {
        digest.map { String(format: "%02x", $0) }.joined()
    }
}
