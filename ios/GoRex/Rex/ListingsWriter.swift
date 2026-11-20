import Foundation
import UIKit

/// Writes the hosts' last projects (projects-cache.json) off the main
/// actor. The encode and the file write run on one serial queue, so writes
/// keep their order; a state saved while a write runs waits, and the
/// states that come before the queue takes it are dropped for it, so a
/// burst of changes makes one write of the last state. A write pending as
/// the app goes to the background, or ends, is flushed.
nonisolated final class ListingsWriter: @unchecked Sendable {
    /// What a write does with the state: it encodes it and writes the file.
    typealias Perform = @Sendable (URL, [String: ProjectListing]) -> Void

    private let url: URL
    private let perform: Perform
    private let queue = DispatchQueue(label: "dev.gorex.listings-writer", qos: .utility)
    private let lock = NSLock()
    private var pending: [String: ProjectListing]?
    private var queued = false
    private var observers: [NSObjectProtocol] = []

    init(url: URL, center: NotificationCenter = .default, perform: @escaping Perform = ListingsWriter.write) {
        self.url = url
        self.perform = perform
        observers = [UIApplication.didEnterBackgroundNotification, UIApplication.willTerminateNotification].map { name in
            center.addObserver(forName: name, object: nil, queue: nil) { [weak self] _ in self?.flush() }
        }
    }

    deinit {
        for o in observers { NotificationCenter.default.removeObserver(o) }
    }

    /// Queues the state to be written; the last one queued wins.
    func save(_ listings: [String: ProjectListing]) {
        lock.lock()
        pending = listings
        let start = !queued
        queued = true
        lock.unlock()
        if start { queue.async { [self] in drain() } }
    }

    /// Returns once every state saved before it is written.
    func flush() {
        queue.sync {}
    }

    private func drain() {
        while true {
            lock.lock()
            guard let next = pending else {
                queued = false
                lock.unlock()
                return
            }
            pending = nil
            lock.unlock()
            perform(url, next)
        }
    }

    @Sendable static func write(_ url: URL, _ listings: [String: ProjectListing]) {
        guard let data = try? JSONEncoder().encode(listings) else { return }
        try? FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try? data.write(to: url, options: .atomic)
    }
}
