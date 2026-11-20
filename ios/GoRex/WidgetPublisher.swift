import Foundation
import Observation
import UIKit
import WidgetKit

/// Writes what the widgets show (WidgetSnapshot) as the hosts' inboxes
/// change, at most once an interval, and only what changed; then asks
/// WidgetKit to draw the widgets again, which is free while the app is in
/// front. Going to the background, it writes once more, so the snapshot's
/// time is when the user left (docs/IOS-010-widgets.md, Writer).
final class WidgetPublisher {
    static let shared = WidgetPublisher()

    /// Runs work after a wait, in seconds; a test runs it when it chooses.
    typealias Schedule = (TimeInterval, @escaping @MainActor () -> Void) -> Void

    /// Where the file goes; nil writes nothing, as without the App Group.
    private let url: URL?
    /// The least time between two writes, in seconds.
    private let interval: TimeInterval
    private let now: () -> Date
    private let schedule: Schedule
    private let reload: () -> Void
    /// The hosts written last: to skip a write of the same, and to keep a
    /// host's entry while it connects. At the start, those of the file.
    private(set) var written: [WidgetSnapshot.Host]?
    /// When it wrote last.
    private var lastWrite: Date?
    /// The write waiting, by its number; nil for none.
    private var due: Int?
    private var count = 0
    private var hosts: () -> [Host] = { [] }

    init(url: URL? = WidgetSnapshot.url, interval: TimeInterval = 1, now: @escaping () -> Date = Date.init,
         schedule: @escaping Schedule = WidgetPublisher.after,
         reload: @escaping () -> Void = { WidgetCenter.shared.reloadAllTimelines() }) {
        self.url = url
        self.interval = interval
        self.now = now
        self.schedule = schedule
        self.reload = reload
    }

    nonisolated static func after(_ wait: TimeInterval, _ work: @escaping @MainActor () -> Void) {
        Task { @MainActor in
            if wait > 0 { try? await Task.sleep(for: .seconds(wait)) }
            work()
        }
    }

    /// Follows the store's hosts from now on, and writes what they show.
    func start(_ store: HostStore) {
        start { store.hosts }
    }

    func start(hosts: @escaping () -> [Host]) {
        self.hosts = hosts
        written = url.flatMap(WidgetSnapshot.read)?.hosts
        publish()
    }

    /// Writes once more, now, as the app goes to the background, so that
    /// the snapshot's time is when the user left; the write is synchronous,
    /// inside a background task that lets it finish.
    func flush() {
        let app = UIApplication.shared
        let task = app.beginBackgroundTask(withName: "widgets", expirationHandler: nil)
        publish(force: true)
        if task != .invalid { app.endBackgroundTask(task) }
    }

    /// Something a widget shows changed: a write within the interval of
    /// the last one waits for its end, and those that come meanwhile join it.
    func changed() {
        guard due == nil else { return }
        count += 1
        let n = count
        due = n
        let wait = lastWrite.map { interval - now().timeIntervalSince($0) } ?? 0
        schedule(max(0, wait)) { [weak self] in
            guard let self, self.due == n else { return }
            self.publish()
        }
    }

    /// Builds the hosts' entries, watching what they read, and writes
    /// them unless they are those written last; forced, it writes them
    /// anyway, with a new time.
    func publish(force: Bool = false) {
        due = nil
        let previous = written ?? []
        let entries = withObservationTracking {
            hosts().map { Self.entry($0, previous: previous) }
        } onChange: { [weak self] in
            Task { @MainActor in self?.changed() }
        }
        guard force || entries != written, let url else { return }
        let snapshot = WidgetSnapshot(written: now(), hosts: entries)
        do {
            try snapshot.write(to: url)
            lastWrite = snapshot.written
            written = entries
            reload()
        } catch {
            print("widgets: \(error)")
        }
    }

    /// A host as the widgets show it: its inbox while online. While it
    /// connects, as at launch and back in the foreground, what was written
    /// of it last, which it likely still shows; a host that failed, or
    /// connects with nothing written of it, has no items.
    static func entry(_ h: Host, previous: [WidgetSnapshot.Host] = []) -> WidgetSnapshot.Host {
        if h.status == .connecting, let p = previous.first(where: { $0.id == h.id }) { return p }
        let items = Inbox.groups(h.inbox).flatMap(\.items).prefix(WidgetSnapshot.maxItems).map { i in
            let title = h.title(i.window)
            return WidgetSnapshot.Item(session: i.session, block: i.block, window: title, app: program(i.status.app, block: h.blocks[i.block]?.program),
                                       state: i.status.state, kind: i.status.kind, progress: i.status.progress,
                                       heading: WidgetSnapshot.cut(i.heading(title: title)), detail: WidgetSnapshot.cut(i.detail(title: title)),
                                       updated: i.status.updated)
        }
        // No windows until the Terminal widget reads them: their titles
        // change often, and each change would be a write.
        return WidgetSnapshot.Host(id: h.id, name: h.config.name, icon: h.config.icon, online: h.status == .online, items: Array(items))
    }

    /// The program a row's tile shows: the one the record names when it is
    /// one known, else the block's, as the status inbox picks it.
    static func program(_ app: String?, block: String?) -> String? {
        if let app, Program.programs[app.lowercased()] != nil { return app }
        return block
    }
}
