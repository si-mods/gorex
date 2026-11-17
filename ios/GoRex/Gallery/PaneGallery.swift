import Foundation
import Observation

/// A pane's gallery as the screen shows it: its images and whether some
/// are new. The pane's output is scanned and kept off the main thread, on
/// one queue for all panes, so that the terminal draws as fast as before.
@Observable
final class PaneGallery {
    private(set) var entries: [GalleryEntry] = []
    private(set) var unseen = false
    @ObservationIgnored nonisolated let dir: URL
    @ObservationIgnored private let worker: Worker

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
        // before this one notes that it opened.
        let prune = !Self.pruned
        Self.pruned = true
        worker.run { store in
            if prune { GalleryStore.prune() }
            if store.exists {
                store.use()
                try? store.save()
            }
            return store.index
        } done: { [weak self] in self?.show($0) }
    }

    /// Scans what the pane printed; a new stream's snapshot starts over.
    func feed(_ bytes: Data, fresh: Bool = false) {
        worker.scan(bytes, fresh: fresh) { [weak self] in self?.show($0) }
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

        func scan(_ bytes: Data, fresh: Bool, done: @escaping @MainActor @Sendable (GalleryIndex) -> Void) {
            Self.queue.async { [self] in
                if fresh { scanner.reset() }
                let found = scanner.feed(bytes)
                guard !found.isEmpty else { return }
                var added = false
                for f in found where store.add(f) { added = true }
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
