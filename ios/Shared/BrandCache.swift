import UIKit

/// On-demand loader and App Group disk cache for non-bundled brand glyphs.
/// Core brands ship inside Brands.xcassets; long-tail brands are fetched once
/// as 64x64 monochrome template PNGs from https://web.gorex.uk/brands-png/ and
/// cached in the App Group container so GoRex and GoRexWidgets share one copy.
@MainActor
final class BrandIconCache {
    static let shared = BrandIconCache()

    /// Base URL for on-demand template PNGs (overridable in tests).
    var baseURL = "https://web.gorex.uk/brands-png"

    private static let maxBytes = 64 * 1024
    private var memory: [String: UIImage] = [:]
    private var diskChecked: Set<String> = []
    private var failed: Set<String> = []
    private var inFlight: [String: Task<UIImage?, Never>] = [:]

    private init() {}

    static func validID(_ id: String) -> Bool {
        guard !id.isEmpty, id.count <= 64 else { return false }
        return id.utf8.allSatisfy { b in
            (b >= UInt8(ascii: "a") && b <= UInt8(ascii: "z"))
                || (b >= UInt8(ascii: "0") && b <= UInt8(ascii: "9"))
                || b == UInt8(ascii: "-") || b == UInt8(ascii: "_")
        }
    }

    private func cacheDirectory() -> URL? {
        let base = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: WidgetSnapshot.group)
            ?? FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first
        return base?.appending(path: "Library/Caches/brands", directoryHint: .isDirectory)
    }

    /// Synchronous lookup: memory first, then App Group disk cache.
    func cachedImage(for id: String) -> UIImage? {
        guard Self.validID(id) else { return nil }
        if let img = memory[id] { return img }
        if diskChecked.contains(id) { return nil }
        diskChecked.insert(id)
        guard let dir = cacheDirectory() else { return nil }
        let file = dir.appending(path: "\(id).png")
        guard let data = try? Data(contentsOf: file), data.count <= Self.maxBytes,
              let img = UIImage(data: data, scale: 2)?.withRenderingMode(.alwaysTemplate)
        else { return nil }
        memory[id] = img
        return img
    }

    /// Fetches and caches the template PNG if not already available locally.
    func fetchIfNeeded(_ id: String) async -> UIImage? {
        if let img = cachedImage(for: id) { return img }
        guard Self.validID(id), !failed.contains(id), !baseURL.isEmpty else { return nil }
        if let task = inFlight[id] { return await task.value }

        let urlString = "\(baseURL)/\(id).png"
        let dir = cacheDirectory()
        let task = Task<UIImage?, Never> {
            guard let url = URL(string: urlString) else { return nil }
            var req = URLRequest(url: url)
            req.timeoutInterval = 10
            guard let (data, resp) = try? await URLSession.shared.data(for: req),
                  let http = resp as? HTTPURLResponse, http.statusCode == 200,
                  !data.isEmpty, data.count <= Self.maxBytes,
                  let img = UIImage(data: data, scale: 2)?.withRenderingMode(.alwaysTemplate)
            else { return nil }
            if let dir {
                try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
                let file = dir.appending(path: "\(id).png")
                try? data.write(to: file, options: .atomic)
            }
            return img
        }
        inFlight[id] = task
        let img = await task.value
        inFlight[id] = nil
        if let img {
            memory[id] = img
        } else {
            failed.insert(id)
        }
        return img
    }
}
