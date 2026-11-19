import Foundation
import QuartzCore
import Synchronization

/// Work for a queue that waits there at most once: a request while it waits
/// is the same work, as a Ghostty tick does all the work that is ready. The
/// flag clears as the work starts, so a request during the work queues it
/// again and none is lost. Safe from any thread.
nonisolated final class Coalesced: Sendable {
    private let queued = Atomic<Bool>(false)
    private let queue: DispatchQueue

    init(on queue: DispatchQueue = .main) {
        self.queue = queue
    }

    /// Queues the work unless work waits already; true when it queued.
    @discardableResult
    func run(_ work: @escaping @Sendable () -> Void) -> Bool {
        guard queued.compareExchange(expected: false, desired: true, ordering: .acquiringAndReleasing).exchanged else { return false }
        queue.async { [self] in
            queued.store(false, ordering: .releasing)
            work()
        }
        return true
    }
}

/// What Ghostty's actions owe a view, done once for all the asks that came
/// before it is done: the first ask schedules it, the rest join it.
final class FrameWork {
    struct Kind: OptionSet {
        let rawValue: Int
        /// The selection's handles go where the selection is.
        static let selection = Kind(rawValue: 1 << 0)
        /// The scroll view's content follows Ghostty's scrollback.
        static let scroll = Kind(rawValue: 1 << 1)
    }

    private(set) var owed: Kind = []
    private let schedule: () -> Void

    init(schedule: @escaping () -> Void) {
        self.schedule = schedule
    }

    func owe(_ k: Kind) {
        guard !k.isEmpty else { return }
        let idle = owed.isEmpty
        owed.formUnion(k)
        if idle { schedule() }
    }

    /// What is owed, now to be done; nothing is owed after.
    func take() -> Kind {
        defer { owed = [] }
        return owed
    }
}

/// Calls its work once on the next frame each time it is armed, from a
/// display link that waits paused in between. The link holds it until stop.
final class NextFrame: NSObject {
    private var link: CADisplayLink?
    private let work: () -> Void

    init(_ work: @escaping () -> Void) {
        self.work = work
    }

    func arm() {
        if link == nil {
            let l = CADisplayLink(target: self, selector: #selector(tick))
            l.add(to: .main, forMode: .common)
            link = l
        }
        link?.isPaused = false
    }

    func stop() {
        link?.invalidate()
        link = nil
    }

    @objc private func tick(_ l: CADisplayLink) {
        l.isPaused = true
        work()
    }
}
