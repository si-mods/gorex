import Foundation
import Observation

/// A pane's gallery as the screen shows it: its images and whether some
/// are new. The pane's output is scanned and kept off the main thread, on
/// one queue for all panes, so that the terminal draws as fast as before.
@Observable
final class PaneGallery {
    private(set) var entries: [GalleryEntry] = []
    private(set) var unseen = false
    /// Whether the screen shows the gallery.
    var presenting = false
    @ObservationIgnored nonisolated let dir: URL
    @ObservationIgnored private let worker: Worker
    /// The downloads of thumbnails, three at once (docs/ios-pane-gallery.md).
    @ObservationIgnored let thumbnails = Limiter(3)

    /// One per pane, shared by its terminal and the screen.
    private static var all: [URL: PaneGallery] = [:]
    private static var pruned = false

    static func of(host: String, block: String) -> PaneGallery {
        let dir = GalleryStore.dir(host: host, block: block)
        if let g = all[dir] { return g }
        let g = PaneGallery(dir: dir)
        all[dir] = g
        return g
    }

    private init(dir: URL) {
        self.dir = dir
        worker = Worker(dir: dir)
        // Galleries not opened for 30 days go as the first pane opens,
        // before this one is read from disk and notes that it opened.
        if !Self.pruned { Worker.prune() }
        Self.pruned = true
        worker.run { store in
            if store.exists {
                store.use()
                try? store.save()
            }
            return store.index
        } done: { [weak self] in self?.show($0) }
    }

    /// The terminal whose screen is read (§3.4), the read owed once the
    /// output pauses, and when output began to come since the last read.
    @ObservationIgnored private weak var screen: TerminalOutput?
    @ObservationIgnored private var pause: DispatchWorkItem?
    @ObservationIgnored private var lastRead = Date.distantPast
    @ObservationIgnored private var busySince: Date?
    /// The pause in the output after which the screen is read, and the
    /// least time between reads while it does not pause.
    static let readPause: TimeInterval = 0.3
    static let readEvery: TimeInterval = 2

    /// Scans what the pane printed; a new stream's snapshot starts over.
    /// The screen it drew on is read once the output pauses, and every
    /// two seconds while it does not.
    func feed(_ bytes: Data, fresh: Bool = false, screen: TerminalOutput? = nil) {
        worker.scan(bytes, fresh: fresh) { [weak self] in self?.show($0) }
        if let screen { self.screen = screen }
        guard self.screen != nil else { return }
        pause?.cancel()
        let read = DispatchWorkItem { [weak self] in self?.readScreen() }
        pause = read
        DispatchQueue.main.asyncAfter(deadline: .now() + Self.readPause, execute: read)
        let now = Date.now, since = busySince ?? now
        busySince = since
        if now.timeIntervalSince(max(since, lastRead)) >= Self.readEvery { readScreen() }
    }

    /// Reads the screen's text and scans it on the queue: the read is the
    /// only work the main thread has.
    private func readScreen() {
        lastRead = .now
        busySince = nil
        let worker = worker
        screen?.readScreen { [weak self] text, columns in
            worker.scan(screen: text, columns: columns) { self?.show($0) }
        }
    }

    /// Notes that the gallery was seen, as it opens and closes.
    func opened() {
        unseen = false
        change { $0.seen(); $0.use() }
    }

    func remove(_ ids: Set<String>) {
        entries.removeAll { ids.contains($0.id) }
        change { $0.remove(ids) }
    }

    /// Drops an image whose file the host answered with no_file, and does
    /// not note it as removed.
    func drop(missing id: String) {
        entries.removeAll { $0.id == id }
        change { $0.drop(missing: [id]) }
    }

    func clear() {
        entries = []
        change { $0.clear() }
    }

    nonisolated func file(_ e: GalleryEntry) -> URL { GalleryStore.file(e, in: dir) }
    nonisolated func thumb(_ e: GalleryEntry) -> URL { GalleryStore.thumb(e, in: dir) }

    private func change(_ f: @escaping @Sendable (GalleryStore) -> Void) {
        worker.run { store in
            f(store)
            try? store.save()
            return store.index
        } done: { [weak self] in self?.show($0) }
    }

    private func show(_ index: GalleryIndex) {
        if entries != index.entries { entries = index.entries }
        if unseen != index.unseen { unseen = index.unseen }
    }

    /// The scanner and the store of a pane, used on the queue only.
    nonisolated final class Worker: @unchecked Sendable {
        private static let queue = DispatchQueue(label: "dev.gorex.gallery", qos: .utility)
        private var scanner = GalleryScanner()
        private let dir: URL
        /// Read from disk at first use, on the queue.
        private var loaded: GalleryStore?
        private var store: GalleryStore {
            if let loaded { return loaded }
            let s = GalleryStore(dir: dir)
            loaded = s
            return s
        }

        init(dir: URL) {
            self.dir = dir
        }

        /// Deletes the old galleries, before any store on the queue reads its own.
        static func prune() {
            queue.async { GalleryStore.prune() }
        }

        func scan(_ bytes: Data, fresh: Bool, done: @escaping @MainActor @Sendable (GalleryIndex) -> Void) {
            Self.queue.async { [self] in
                if fresh { scanner.reset() }
                // A fragment of a path the screen joined is not collected again.
                let found = scanner.feed(bytes).filter { f in
                    if case .path(let p) = f { return !fragments.contains(p) }
                    return true
                }
                guard !found.isEmpty else { return }
                var added = false
                // A new stream's first bytes are a snapshot, not live output.
                for f in found where store.add(f, live: !fresh) { added = true }
                guard added else { return }
                try? store.save()
                let index = store.index
                Task { @MainActor in done(index) }
            }
        }

        /// The pieces of wrapped paths the screen joined, oldest first: the
        /// bytes of a snapshot or a redraw show them again.
        private var fragments: [String] = []
        private static let fragmentLimit = 500

        /// The text the screen showed at the last read: the same again is
        /// not scanned.
        private var lastScreen = ""

        /// Adds the image paths the screen's text shows. They are added as
        /// a snapshot's are: a path removed or missing does not come back.
        func scan(screen text: String, columns: Int, done: @escaping @MainActor @Sendable (GalleryIndex) -> Void) {
            Self.queue.async { [self] in
                guard text != lastScreen else { return }
                lastScreen = text
                let rows = text.split(separator: "\n", omittingEmptySubsequences: false).suffix(GalleryScanner.maxScreenRows).map(String.init)
                let screen = GalleryScanner.screen(rows: rows, columns: columns)
                // The pieces of a wrapped path the bytes found go.
                var added = store.drop(pieces: Set(screen.pieces))
                for p in screen.pieces where !fragments.contains(p) { fragments.append(p) }
                if fragments.count > Self.fragmentLimit { fragments.removeFirst(fragments.count - Self.fragmentLimit) }
                for p in screen.paths where !scanner.concealedPaths.contains(p) && store.add(.path(p), live: false) {
                    added = true
                }
                guard added else { return }
                try? store.save()
                let index = store.index
                Task { @MainActor in done(index) }
            }
        }

        func run(_ f: @escaping @Sendable (GalleryStore) -> GalleryIndex, done: @escaping @MainActor @Sendable (GalleryIndex) -> Void) {
            Self.queue.async { [self] in
                let index = f(store)
                Task { @MainActor in done(index) }
            }
        }
    }
}

/// At most a number of tasks at once; the others wait their turn, in order.
final class Limiter {
    private var free: Int
    private var waiting: [CheckedContinuation<Void, Never>] = []

    init(_ n: Int) {
        free = n
    }

    func run<T>(_ f: () async throws -> T) async throws -> T {
        if free > 0 {
            free -= 1
        } else {
            await withCheckedContinuation { waiting.append($0) }
        }
        defer {
            if waiting.isEmpty { free += 1 } else { waiting.removeFirst().resume() }
        }
        // A cell that went while it waited downloads nothing.
        try Task.checkCancellation()
        return try await f()
    }
}
