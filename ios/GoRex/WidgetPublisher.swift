import Foundation
import Observation
import UIKit
import WidgetKit

/// Writes what the widgets show (WidgetSnapshot) as the hosts' inboxes and
/// windows change, at most once a second, and only what changed; then asks
/// WidgetKit to draw the widgets again, which is free while the app is in
/// front. Going to the background, it writes once more, so the snapshot's
/// time is when the user left (docs/ios-widgets.md, Writer).
final class WidgetPublisher {
    static let shared = WidgetPublisher()

    /// Where the file goes; nil writes nothing, as without the App Group.
    private let url: URL?
    /// The least time between two writes.
    private let interval: Duration
    private let now: () -> Date
    private let reload: () -> Void
    /// The hosts written last, to skip a write of the same.
    private(set) var written: [WidgetSnapshot.Host]?
    private var lastWrite: ContinuousClock.Instant?
    private var scheduled: Task<Void, Never>?
    private var hosts: () -> [Host] = { [] }

    init(url: URL? = WidgetSnapshot.url, interval: Duration = .seconds(1), now: @escaping () -> Date = Date.init,
         reload: @escaping () -> Void = { WidgetCenter.shared.reloadAllTimelines() }) {
        self.url = url
        self.interval = interval
        self.now = now
        self.reload = reload
    }

    /// Follows the store's hosts from now on, and writes what they show.
    func start(_ store: HostStore) {
        start { store.hosts }
    }

    func start(hosts: @escaping () -> [Host]) {
        self.hosts = hosts
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
        guard scheduled == nil else { return }
        let wait = lastWrite.map { interval - (ContinuousClock.now - $0) } ?? .zero
        scheduled = Task { [weak self] in
            if wait > .zero { try? await Task.sleep(for: wait) }
            guard !Task.isCancelled, let self else { return }
            self.scheduled = nil
            self.publish()
        }
    }

    /// Builds the hosts' entries, watching what they read, and writes
    /// them unless they are those written last; forced, it writes them
    /// anyway, with a new time.
    func publish(force: Bool = false) {
        scheduled?.cancel()
        scheduled = nil
        let entries = withObservationTracking {
            hosts().map(Self.entry)
        } onChange: { [weak self] in
            Task { @MainActor in self?.changed() }
        }
        lastWrite = .now
        guard force || entries != written, let url else { return }
        let snapshot = WidgetSnapshot(written: now(), hosts: entries)
        do {
            try snapshot.write(to: url)
            written = entries
            reload()
        } catch {
            print("widgets: \(error)")
        }
    }

    /// A host as the widgets show it: its inbox while online, none
    /// otherwise, and its windows either way.
    static func entry(_ h: Host) -> WidgetSnapshot.Host {
        let items = Inbox.groups(h.inbox).flatMap(\.items).prefix(WidgetSnapshot.maxItems).map { i in
            let title = h.title(i.window)
            return WidgetSnapshot.Item(session: i.session, block: i.block, window: title, app: program(i.status.app, block: h.blocks[i.block]?.program),
                                       state: i.status.state, kind: i.status.kind, progress: i.status.progress,
                                       heading: WidgetSnapshot.cut(i.heading(title: title)), detail: WidgetSnapshot.cut(i.detail(title: title)),
                                       updated: i.status.updated)
        }
        let windows = h.sessions.flatMap { s in
            (s.view?.windows ?? []).compactMap { w in
                w.block.map { WidgetSnapshot.Window(session: s.id, block: $0, title: h.title(w), app: h.blocks[$0]?.program) }
            }
        }
        return WidgetSnapshot.Host(id: h.id, name: h.config.name, icon: h.config.icon, online: h.status == .online,
                                   items: Array(items), windows: Array(windows.prefix(WidgetSnapshot.maxWindows)))
    }

    /// The program a row's tile shows: the one the record names when it is
    /// one known, else the block's, as the status inbox picks it.
    static func program(_ app: String?, block: String?) -> String? {
        if let app, Program.programs[app.lowercased()] != nil { return app }
        return block
    }
}
