import Foundation
import Observation

/// Downloads and caches the GoRex web client from a Cloudflare R2 bucket
/// (or any static host serving web/dist + files.json) into
/// Application Support/WebClient/ so the iOS app does not embed it.
@MainActor
@Observable
final class WebClientStore {
    static let shared = WebClientStore()
    static let baseURLKey = "webClientBaseURL"
    /// Default R2 public bucket URL (overridable via Info.plist `GoRexWebClientURL` or Settings).
    static let defaultBaseURL = ((Bundle.main.object(forInfoDictionaryKey: "GoRexWebClientURL") as? String)
        ?? "https://pub-6fdb3aa21b0246a2bfa088f5b9d9f83d.r2.dev")
        .trimmingCharacters(in: .whitespacesAndNewlines)

    var baseURLString: String {
        didSet {
            UserDefaults.standard.set(baseURLString, forKey: Self.baseURLKey)
        }
    }

    var baseURL: URL? {
        let s = baseURLString.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !s.isEmpty, let u = URL(string: s),
              let scheme = u.scheme?.lowercased(), scheme == "http" || scheme == "https" else {
            return nil
        }
        return u
    }

    enum State: Equatable {
        case empty
        case downloading(done: Int, total: Int)
        case ready
        case failed(String)
    }

    struct FileEntry: Codable {
        let path: String
        let bytes: Int
        let sha256: String
        let optional: Bool?
    }

    struct FileIndex: Codable {
        let version: Int
        let files: [FileEntry]
    }

    private(set) var state: State = .empty

    var directory: URL {
        let appSupport = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
        return appSupport.appendingPathComponent("WebClient", isDirectory: true)
    }

    var isCached: Bool {
        FileManager.default.fileExists(atPath: directory.appendingPathComponent("index.html").path)
    }

    private init() {
        baseURLString = UserDefaults.standard.string(forKey: Self.baseURLKey) ?? Self.defaultBaseURL
        if isCached { state = .ready }
    }

    /// Ensures a cached copy exists, downloading it if missing.
    func ensureReady() async -> Bool {
        if isCached {
            state = .ready
            return true
        }
        return await update()
    }

    /// Downloads files.json from the configured R2 URL, fetches any missing or changed
    /// non-optional files, verifies SHA-256, and swaps them into WebClient/.
    /// The disk work and the downloads run off the main actor (WebClientSync).
    @discardableResult
    func update() async -> Bool {
        guard let baseURL else {
            state = .failed(String(localized: "Set a Cloudflare R2 URL first."))
            return false
        }
        state = .downloading(done: 0, total: 1)
        do {
            let indexURL = baseURL.appendingPathComponent("files.json")
            var req = URLRequest(url: indexURL)
            req.cachePolicy = .reloadIgnoringLocalCacheData
            let (indexData, resp) = try await URLSession.shared.data(for: req)
            if let http = resp as? HTTPURLResponse, http.statusCode != 200 {
                throw NSError(domain: "WebClientStore", code: http.statusCode,
                              userInfo: [NSLocalizedDescriptionKey: "HTTP \(http.statusCode) fetching files.json"])
            }
            let index = try JSONDecoder().decode(FileIndex.self, from: indexData)
            let wanted = index.files.filter { $0.optional != true }

            let total = wanted.count
            state = .downloading(done: 0, total: max(total, 1))
            try await WebClientSync.sync(
                entries: wanted.map { WebClientSync.Entry(path: $0.path, sha256: $0.sha256) },
                indexData: indexData,
                into: directory,
                fetch: WebClientSync.httpFetch(base: baseURL),
                progress: { done in
                    await self.setProgress(done: done, total: total)
                }
            )
            state = .ready
            return true
        } catch {
            state = isCached ? .ready : .failed(error.localizedDescription)
            return false
        }
    }

    private func setProgress(done: Int, total: Int) {
        guard case .downloading = state else { return }
        state = .downloading(done: done, total: max(total, 1))
    }
}
