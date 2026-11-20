import Foundation
import UIKit

/// The time iOS gives a backgrounded app to finish (a background task),
/// used to keep the hosts' connections and the terminal's streams open for
/// a while (docs/IOS-012-background-and-network-changes.md): it ends 5
/// seconds before iOS would take the time back, after limit seconds, on a
/// memory warning, or as iOS takes the time back, whichever comes first.
/// Its end runs the work its owners registered, once each; work registered
/// after the end, in the same trip to the background, runs at once. The
/// task ends exactly once: as all the work says it finished, at once as
/// iOS takes the time back, or as the app comes back.
final class BackgroundGrace {
    /// Asks for background time, calling expired as iOS takes it back; nil
    /// when iOS gives none.
    typealias Begin = (_ expired: @escaping @MainActor () -> Void) -> Int?
    /// The work at the grace's end, which calls finished when done.
    typealias Work = (_ finished: @escaping () -> Void) -> Void

    /// The app's: UIKit's background time.
    static let shared = BackgroundGrace(
        begin: { expired in
            let t = UIApplication.shared.beginBackgroundTask(withName: "background grace") { MainActor.assumeIsolated { expired() } }
            return t == .invalid ? nil : t.rawValue
        },
        end: { UIApplication.shared.endBackgroundTask(UIBackgroundTaskIdentifier(rawValue: $0)) },
        remaining: { UIApplication.shared.backgroundTimeRemaining })

    let limit: TimeInterval
    private let begin: Begin
    private let endTask: (Int) -> Void
    private let now: () -> Date
    private let remaining: () -> TimeInterval
    private let schedule: TerminalLive.Schedule
    private var memoryWarnings: NSObjectProtocol?

    private var task: Int?
    /// The work registered, by its owner's key.
    private var works: [AnyHashable: Work] = [:]
    /// The work that ran and has not said it finished.
    private var unfinished = 0
    private(set) var running = false
    /// Whether the grace ended in this trip to the background.
    private var over = false
    private var generation = 0
    /// When the app was last kept running: the grace's end.
    private(set) var keptUntil: Date?

    init(limit: TimeInterval = 25, begin: @escaping Begin, end: @escaping (Int) -> Void,
         now: @escaping () -> Date = Date.init, remaining: @escaping () -> TimeInterval = { .infinity },
         schedule: @escaping TerminalLive.Schedule = WidgetPublisher.after, center: NotificationCenter = .default) {
        self.limit = limit
        self.begin = begin
        self.endTask = end
        self.now = now
        self.remaining = remaining
        self.schedule = schedule
        memoryWarnings = center.addObserver(forName: UIApplication.didReceiveMemoryWarningNotification, object: nil, queue: nil) { [weak self] _ in
            MainActor.assumeIsolated { self?.end(reason: "memory warning") }
        }
    }

    /// Adds the work of an owner, in place of its last: it waits for the
    /// grace's end, or runs at once when the grace ended in this trip to
    /// the background.
    func register(_ key: AnyHashable, _ work: @escaping Work) {
        guard over else {
            works[key] = work
            return
        }
        run([work], byIOS: false)
    }

    /// Removes the work of an owner, which is not to run.
    func unregister(_ key: AnyHashable) {
        works[key] = nil
    }

    /// Starts the grace. Without background time, it ends at once, as the
    /// app may stop any moment. A second after the start, when iOS tells
    /// it, it reads the time left and ends 5 seconds before that runs out.
    func start() {
        guard !running else { return }
        // The last grace's task, should its work still be finishing.
        finish()
        generation += 1
        let g = generation
        running = true
        over = false
        unfinished = 0
        task = begin { [weak self] in self?.expire(g, byIOS: true) }
        guard task != nil else { return expire(g, byIOS: false) }
        schedule(1) { [weak self] in
            guard let self, self.generation == g, self.running else { return }
            let r = self.remaining()
            let wait = r.isFinite ? min(self.limit, r - 5) : self.limit
            self.schedule(max(0, wait)) { [weak self] in self?.expire(g, byIOS: false) }
        }
    }

    /// Ends a running grace now, as its deadline does.
    func end(reason: String) {
        guard running else { return }
        ConnectionLog.note("background grace ended: \(reason)")
        expire(generation, byIOS: false)
    }

    /// The grace ends with its work, then its task: once all the work
    /// finished, or at once when iOS takes the time back, which it may do
    /// while the work finishes.
    private func expire(_ g: Int, byIOS: Bool) {
        guard g == generation else { return }
        guard running else {
            if byIOS { finish() }
            return
        }
        running = false
        over = true
        keptUntil = now()
        let all = Array(works.values)
        works = [:]
        run(all, byIOS: byIOS)
    }

    private func run(_ all: [Work], byIOS: Bool) {
        if byIOS {
            for w in all { w {} }
            return finish()
        }
        guard !all.isEmpty else { return finish() }
        let g = generation
        unfinished += all.count
        for w in all {
            var done = false
            w { [weak self] in
                guard !done, let self, self.generation == g else { return }
                done = true
                self.unfinished -= 1
                if self.unfinished == 0 { self.finish() }
            }
        }
    }

    /// The app came back: a grace still running ends without its work,
    /// and the work registered is dropped. True when one was running.
    @discardableResult
    func cancel() -> Bool {
        works = [:]
        over = false
        guard running else { return false }
        generation += 1
        running = false
        unfinished = 0
        keptUntil = now()
        finish()
        return true
    }

    private func finish() {
        guard let t = task else { return }
        task = nil
        endTask(t)
    }

    /// Since when the app, in the background since away, was likely not
    /// running: nil while a grace runs, else the later of away and the
    /// grace's end (HostStore.suspended).
    func awake(since away: Date?) -> Date? {
        guard !running else { return nil }
        return [away, keptUntil].compactMap(\.self).max()
    }
}
