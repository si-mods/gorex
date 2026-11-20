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
    /// The size of a Kitty image's PNG, which counts to the gallery's bound.
    var bytes: Int?

    /// The name the viewer shows and a share gives the file.
    var name: String {
        if let path { return FileLink.fileName("", path: path) }
        return "image-\(id.prefix(8).lowercased()).png"
    }

    /// Whether the path names one file wherever the program is: from the
    /// root, the home folder or a drive. A relative one resolves against the
    /// program's directory now, which may differ from when it printed.
    var absolute: Bool {
        guard let p = path else { return false }
        let c = Array(p.prefix(3))
        return p.hasPrefix("/") || p.hasPrefix("~/")
            || (c.count == 3 && c[0].isLetter && c[1] == ":" && (c[2] == "\\" || c[2] == "/"))
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
    /// The paths and Kitty hashes of the images the user removed, oldest
    /// first: a snapshot or the server's replay does not bring them back.
    var removed: [String] = []
    /// The paths whose file the host did not have, oldest first: the
    /// snapshot of a reconnect, which still holds their lines, does not
    /// bring them back.
    var missing: [String] = []
}

/// An index.json of an older build lacks some of the lists.
extension GalleryIndex {
    nonisolated init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        entries = try c.decodeIfPresent([GalleryEntry].self, forKey: .entries) ?? []
        used = try c.decode(Date.self, forKey: .used)
        unseen = try c.decodeIfPresent(Bool.self, forKey: .unseen) ?? false
        removed = try c.decodeIfPresent([String].self, forKey: .removed) ?? []
        missing = try c.decodeIfPresent([String].self, forKey: .missing) ?? []
    }
}

/// A pane's gallery on disk (docs/IOS-007-pane-gallery.md, Storage): index.json,
/// the PNG of each Kitty image under files/, and a thumbnail of each image
/// file of the host under thumbs/, whose original is downloaded to view.
/// It is not thread-safe: one queue uses it.
nonisolated final class GalleryStore {
    static let limit = 500
    /// The most removed images remembered.
    static let removedLimit = 2000
    /// A Kitty image of the same number within this time of the last one
    /// replaces it: a program draws an animation or a chart in frames.
    static let frameTime: TimeInterval = 2
    static let maxAge: TimeInterval = 30 * 24 * 3600

    let dir: URL
    private(set) var index: GalleryIndex
    /// The most bytes of Kitty images the gallery keeps; a test lowers it.
    var maxBytes = 256 << 20
    /// The entry and the time of the last Kitty image of each number.
    private var frames: [Int: (id: String, at: Date)] = [:]

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

    /// Adds what the scanner found, unless the gallery has it or the user
    /// removed it: the same path, or a Kitty image of the same bytes. A
    /// Kitty image replaces the last of its number shown within frameTime.
    /// The oldest go past the limits. A path whose file was missing is added
    /// again only by live output (not a snapshot). Whether it was new.
    @discardableResult
    func add(_ found: GalleryScanner.Found, live: Bool = true, now: Date = .now) -> Bool {
        let id = UUID().uuidString
        switch found {
        case .path(let p):
            if index.missing.contains(p) {
                guard live else { return false }
                index.missing.removeAll { $0 == p }
            }
            guard !index.entries.contains(where: { $0.path == p }), !index.removed.contains(p) else { return false }
            index.entries.append(GalleryEntry(id: id, source: .path, path: p, first: now))
        case .kitty(let png, let w, let h, let number):
            let hash = SHA256.hash(data: png).map { String(format: "%02x", $0) }.joined()
            let last = number.flatMap { frames[$0] }.flatMap { now.timeIntervalSince($0.at) < Self.frameTime ? $0 : nil }
            if let number { frames[number]?.at = now }
            guard !index.entries.contains(where: { $0.hash == hash }), !index.removed.contains(hash) else { return false }
            let e = GalleryEntry(id: id, source: .kitty, hash: hash, first: now, width: w, height: h, bytes: png.count)
            do {
                try FileManager.default.createDirectory(at: file(e).deletingLastPathComponent(), withIntermediateDirectories: true)
                try png.write(to: file(e))
            } catch {
                return false
            }
            if let last, let old = index.entries.firstIndex(where: { $0.id == last.id }) {
                deleteFiles(index.entries[old])
                index.entries.remove(at: old)
            }
            if let number { frames[number] = (id, now) }
            index.entries.append(e)
        }
        index.unseen = true
        var bytes = index.entries.reduce(0) { $0 + ($1.bytes ?? 0) }
        var gone = 0
        while index.entries.count - gone > Self.limit || bytes > maxBytes && gone < index.entries.count - 1 {
            deleteFiles(index.entries[gone])
            bytes -= index.entries[gone].bytes ?? 0
            gone += 1
        }
        index.entries.removeFirst(gone)
        return true
    }

    /// Removes the images, and remembers them so that they do not return.
    func remove(_ ids: Set<String>) {
        for e in index.entries where ids.contains(e.id) {
            deleteFiles(e)
            if let key = e.path ?? e.hash {
                index.removed.removeAll { $0 == key }
                index.removed.append(key)
            }
        }
        index.entries.removeAll { ids.contains($0.id) }
        if index.removed.count > Self.removedLimit { index.removed.removeFirst(index.removed.count - Self.removedLimit) }
    }

    /// Drops the images whose file the host no longer has. Unlike remove, it
    /// notes them in missing: live output that prints the path again adds it.
    func drop(missing ids: Set<String>) {
        for e in index.entries where ids.contains(e.id) {
            deleteFiles(e)
            if let p = e.path {
                index.missing.removeAll { $0 == p }
                index.missing.append(p)
            }
        }
        index.entries.removeAll { ids.contains($0.id) }
        if index.missing.count > Self.removedLimit { index.missing.removeFirst(index.missing.count - Self.removedLimit) }
    }

    /// Drops the images of these paths, pieces of a longer path the screen
    /// shows wrapped, as neither removed nor missing. Whether any went.
    func drop(pieces: Set<String>) -> Bool {
        guard !pieces.isEmpty else { return false }
        let gone = index.entries.filter { $0.path.map(pieces.contains) ?? false }
        gone.forEach(deleteFiles)
        index.entries.removeAll { $0.path.map(pieces.contains) ?? false }
        return !gone.isEmpty
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
