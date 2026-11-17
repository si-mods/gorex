import CryptoKit
import Foundation

/// An image of a pane's gallery.
nonisolated struct GalleryEntry: Codable, Equatable, Identifiable, Sendable {
    enum Source: String, Codable, Sendable {
        case path
        case kitty
    }

    var id: String
    var source: Source
    /// The path as printed, for an image file of the host.
    var path: String?
    /// The SHA-256 of a Kitty image's PNG, which a replay sends again.
    var hash: String?
    /// When the image first showed.
    var first: Date
    var width: Int?
    var height: Int?

    /// The name the viewer shows and a share gives the file.
    var name: String {
        if let path { return FileLink.fileName("", path: path) }
        return "image-\(id.prefix(8).lowercased()).png"
    }
}

/// What index.json holds.
nonisolated struct GalleryIndex: Codable, Equatable, Sendable {
    var entries: [GalleryEntry] = []
    /// When the pane's terminal or its gallery last opened: a gallery not
    /// opened for 30 days goes.
    var used: Date
    /// Whether an image came since the gallery was last open.
    var unseen = false
}

/// A pane's gallery on disk (docs/ios-pane-gallery.md, Storage): index.json,
/// the PNG of each Kitty image under files/, and a thumbnail of each image
/// file of the host under thumbs/, whose original is downloaded to view.
/// It is not thread-safe: one queue uses it.
nonisolated final class GalleryStore {
    static let limit = 500
    static let maxAge: TimeInterval = 30 * 24 * 3600

    let dir: URL
    private(set) var index: GalleryIndex

    /// Where the galleries live: Application Support/Gallery/<host>/<block>/.
    static let root = URL.applicationSupportDirectory.appending(path: "Gallery")

    static func dir(root: URL = GalleryStore.root, host: String, block: String) -> URL {
        root.appending(path: safe(host)).appending(path: safe(block))
    }

    /// A name that stays one folder.
    private static func safe(_ s: String) -> String {
        let t = s.map { "/\\:".contains($0) ? "_" : $0 }
        let name = String(t)
        return name.isEmpty || name == "." || name == ".." ? "_" : name
    }

    /// The gallery in dir, as index.json last kept it, else an empty one.
    init(dir: URL, now: Date = .now) {
        self.dir = dir
        if let d = try? Data(contentsOf: dir.appending(path: "index.json")),
           let i = try? Self.decoder.decode(GalleryIndex.self, from: d) {
            index = i
        } else {
            index = GalleryIndex(used: now)
        }
    }

    var exists: Bool { FileManager.default.fileExists(atPath: dir.appending(path: "index.json").path) }

    func file(_ e: GalleryEntry) -> URL { Self.file(e, in: dir) }
    func thumb(_ e: GalleryEntry) -> URL { Self.thumb(e, in: dir) }

    /// A Kitty image's PNG, and the thumbnail of an image file of the host.
    static func file(_ e: GalleryEntry, in dir: URL) -> URL { dir.appending(path: "files").appending(path: "\(e.id).png") }
    static func thumb(_ e: GalleryEntry, in dir: URL) -> URL { dir.appending(path: "thumbs").appending(path: "\(e.id).jpg") }

    /// Adds what the scanner found, unless the gallery has it: the same
    /// path, or a Kitty image of the same bytes. The oldest go past the
    /// limit. Whether it was new.
    @discardableResult
    func add(_ found: GalleryScanner.Found, now: Date = .now) -> Bool {
        let id = UUID().uuidString
        switch found {
        case .path(let p):
            guard !index.entries.contains(where: { $0.path == p }) else { return false }
            index.entries.append(GalleryEntry(id: id, source: .path, path: p, first: now))
        case .kitty(let png, let w, let h):
            let hash = SHA256.hash(data: png).map { String(format: "%02x", $0) }.joined()
            guard !index.entries.contains(where: { $0.hash == hash }) else { return false }
            let e = GalleryEntry(id: id, source: .kitty, hash: hash, first: now, width: w, height: h)
            do {
                try FileManager.default.createDirectory(at: file(e).deletingLastPathComponent(), withIntermediateDirectories: true)
                try png.write(to: file(e))
            } catch {
                return false
            }
            index.entries.append(e)
        }
        index.unseen = true
        if index.entries.count > Self.limit {
            let gone = index.entries.prefix(index.entries.count - Self.limit)
            for e in gone { deleteFiles(e) }
            index.entries.removeFirst(gone.count)
        }
        return true
    }

    func remove(_ ids: Set<String>) {
        for e in index.entries where ids.contains(e.id) { deleteFiles(e) }
        index.entries.removeAll { ids.contains($0.id) }
    }

    func clear() {
        remove(Set(index.entries.map(\.id)))
    }

    /// Notes that the pane's terminal or gallery opened.
    func use(now: Date = .now) {
        index.used = now
    }

    func seen() {
        index.unseen = false
    }

    func save() throws {
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        try Self.encoder.encode(index).write(to: dir.appending(path: "index.json"), options: .atomic)
    }

    private func deleteFiles(_ e: GalleryEntry) {
        try? FileManager.default.removeItem(at: file(e))
        try? FileManager.default.removeItem(at: thumb(e))
    }

    /// Deletes the galleries under root not opened for maxAge, and the
    /// host folders they leave empty.
    static func prune(root: URL = GalleryStore.root, now: Date = .now) {
        let fm = FileManager.default
        for host in (try? fm.contentsOfDirectory(at: root, includingPropertiesForKeys: nil)) ?? [] {
            for block in (try? fm.contentsOfDirectory(at: host, includingPropertiesForKeys: nil)) ?? [] {
                let s = GalleryStore(dir: block, now: .distantPast)
                if now.timeIntervalSince(s.index.used) > maxAge { try? fm.removeItem(at: block) }
            }
            if (try? fm.contentsOfDirectory(atPath: host.path))?.isEmpty == true { try? fm.removeItem(at: host) }
        }
    }

    private static let encoder: JSONEncoder = {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        e.outputFormatting = [.sortedKeys]
        return e
    }()

    private static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return d
    }()
}
