import UIKit

/// How each window's terminal looked when it was left, for the cards of
/// the list's grid: by host and block, in memory and as small JPEGs in
/// Caches, which the next launch reads back. One left alone for a week
/// goes, as its window most likely did.
@Observable
final class Thumbnails {
    static let shared = Thumbnails()

    /// The width they are kept at, in pixels: twice a phone's card.
    static let width: CGFloat = 400

    private var images: [String: UIImage] = [:]
    @ObservationIgnored private let dir = URL.cachesDirectory.appending(path: "thumbnails")

    private init() {
        let dir = dir
        Task {
            let read = await Task.detached { Self.read(dir) }.value
            // What was left since the launch is newer.
            images.merge(read) { now, _ in now }
        }
    }

    func image(host: UUID, block: String) -> UIImage? { images[Self.key(host, block)] }

    /// Keeps how a terminal looks; the file is written off the main thread.
    func keep(_ image: UIImage, host: UUID, block: String) {
        let key = Self.key(host, block)
        images[key] = image
        let (dir, file) = (dir, file(key))
        Task.detached(priority: .utility) {
            try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
            try? image.jpegData(compressionQuality: 0.7)?.write(to: file, options: .atomic)
        }
    }

    /// Forgets a window's, as when it closes.
    func drop(host: UUID, block: String) {
        let key = Self.key(host, block)
        images[key] = nil
        try? FileManager.default.removeItem(at: file(key))
    }

    private static func key(_ host: UUID, _ block: String) -> String {
        host.uuidString + " " + (block.addingPercentEncoding(withAllowedCharacters: .alphanumerics) ?? block)
    }

    private func file(_ key: String) -> URL { dir.appending(path: key + ".jpg") }

    /// The ones in Caches, by key; those a week old are removed.
    nonisolated private static func read(_ dir: URL) -> [String: UIImage] {
        let fm = FileManager.default
        let files = (try? fm.contentsOfDirectory(at: dir, includingPropertiesForKeys: [.contentModificationDateKey])) ?? []
        var all: [String: UIImage] = [:]
        for f in files where f.pathExtension == "jpg" {
            let changed = (try? f.resourceValues(forKeys: [.contentModificationDateKey]))?.contentModificationDate ?? .distantPast
            if changed < .now.addingTimeInterval(-7 * 86400) {
                try? fm.removeItem(at: f)
            } else if let i = UIImage(contentsOfFile: f.path) {
                all[f.deletingPathExtension().lastPathComponent] = i
            }
        }
        return all
    }
}
