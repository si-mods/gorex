import ActivityKit
import UIKit

extension TerminalLive {
    /// The app's: ActivityKit's activities, and the app's grace. The
    /// activity's push tokens bind at the hosts' relays (PushTokens,
    /// docs/NET-003-push-relay.md).
    static let shared: TerminalLive = {
        let driver = ActivityKitDriver()
        driver.onToken = { host, token in
            let hex = token.map { String(format: "%02x", $0) }.joined()
            PushTokens.shared.bind(tokenHex: hex, host: host)
        }
        driver.onStartToken = { token in
            let hex = token.map { String(format: "%02x", $0) }.joined()
            PushTokens.shared.bindStart(tokenHex: hex, attributesType: TerminalActivity.attributesType)
        }
        return TerminalLive(driver: driver, grace: .shared)
    }()
}

/// TerminalLive's driver on ActivityKit.
final class ActivityKitDriver: TerminalLive.Driver {
    private var activity: Activity<TerminalActivity>?
    /// Called with each push token an activity gets: its host
    /// (attributes.host) and the token's bytes. The app sets it.
    var onToken: @MainActor (UUID, Data) -> Void = { _, _ in }
    /// Called with each push-to-start token of TerminalActivity's
    /// attributes type: it starts an activity while the app is suspended
    /// (docs/NET-003-push-relay.md, Push to start). The app sets it.
    var onStartToken: @MainActor (Data) -> Void = { _ in }
    /// The consumption of each watched activity's token updates.
    private var tokenTasks: [Activity<TerminalActivity>.ID: Task<Void, Never>] = [:]

    /// Starts the listeners. The push-to-start token — of the attributes
    /// type, not of an activity — binds at the relays; and each
    /// activity's push token — among them the one a start push woke the
    /// app for — binds to the host its attributes name.
    init() {
        Task { @MainActor [weak self] in
            if let t = Activity<TerminalActivity>.pushToStartToken { self?.onStartToken(t) }
            for await t in Activity<TerminalActivity>.pushToStartTokenUpdates { self?.onStartToken(t) }
        }
        Task { @MainActor [weak self] in
            for a in Activity<TerminalActivity>.activities { self?.watch(a) }
            for await a in Activity<TerminalActivity>.activityUpdates { self?.watch(a) }
        }
    }

    /// Watches the activity's push token, unless watched already: the
    /// token it may have had at once — a remotely started activity's
    /// among them — and each next one bind to the host its attributes
    /// name.
    private func watch(_ a: Activity<TerminalActivity>) {
        guard tokenTasks[a.id] == nil else { return }
        nonisolated(unsafe) let b = a
        let host = a.attributes.host
        let onToken = self.onToken
        tokenTasks[a.id] = Task {
            if let t = b.pushToken { onToken(host, t) }
            for await t in b.pushTokenUpdates { onToken(host, t) }
        }
    }

    var enabled: Bool { ActivityAuthorizationInfo().areActivitiesEnabled }

    var running: Bool {
        guard let a = activity else { return false }
        return a.activityState == .active || a.activityState == .stale
    }

    var dismissed: Bool { activity?.activityState == .dismissed }

    var shown: (attributes: TerminalActivity, state: TerminalActivity.ContentState)? {
        // One of an earlier run, which the process no longer holds, is
        // among ActivityKit's own list.
        guard let a = activity ?? Activity<TerminalActivity>.activities.first else { return nil }
        return (a.attributes, a.content.state)
    }

    func start(_ attributes: TerminalActivity, _ state: TerminalActivity.ContentState) {
        // One at a time: those of an earlier run end. One of the same
        // attributes still running — a start push began it — is adopted,
        // not ended and started anew; a dismissed or ended one is not.
        var adopt: Activity<TerminalActivity>?
        for each in Activity<TerminalActivity>.activities {
            if adopt == nil, each.attributes == attributes,
               each.activityState == .active || each.activityState == .stale {
                adopt = each
                continue
            }
            tokenTasks[each.id]?.cancel()
            tokenTasks[each.id] = nil
            nonisolated(unsafe) let a = each
            finishing { await a.end(nil, dismissalPolicy: .immediate) }
        }
        if let adopt {
            activity = adopt
            watch(adopt)
            return
        }
        do {
            let a = try Activity.request(attributes: attributes, content: content(state, stale: nil), pushType: .token)
            activity = a
            watch(a)
        } catch {
            print("terminal activity: \(error)")
        }
    }

    func update(_ state: TerminalActivity.ContentState, stale: Date?, done: (() -> Void)?) {
        guard let activity else {
            done?()
            return
        }
        // Activity is not Sendable; ActivityKit takes its calls from any thread.
        nonisolated(unsafe) let a = activity
        let c = content(state, stale: stale)
        if let done {
            // Within the caller's background time.
            Task {
                await a.update(c)
                done()
            }
        } else {
            // Its own background time, so that an update made as the app
            // leaves gets to ActivityKit before the app stops.
            finishing { await a.update(c) }
        }
    }

    func end() {
        activity = nil
        tokenTasks.values.forEach { $0.cancel() }
        tokenTasks = [:]
        for each in Activity<TerminalActivity>.activities {
            nonisolated(unsafe) let a = each
            finishing { await a.end(nil, dismissalPolicy: .immediate) }
        }
    }

    /// While connected, it outranks the app's other activities in the
    /// Dynamic Island.
    private func content(_ s: TerminalActivity.ContentState, stale: Date?) -> ActivityContent<TerminalActivity.ContentState> {
        ActivityContent(state: s, staleDate: stale, relevanceScore: s.connected ? 100 : 50)
    }

    /// Runs work in background time of its own, which ends once, as the
    /// work finishes or as iOS takes the time back, whichever comes first.
    private func finishing(_ work: @escaping @Sendable () async -> Void) {
        let task = OneTask()
        task.id = UIApplication.shared.beginBackgroundTask(withName: "terminal activity") { MainActor.assumeIsolated { task.end() } }
        Task {
            await work()
            task.end()
        }
    }

    /// A background task that ends once.
    private final class OneTask {
        var id = UIBackgroundTaskIdentifier.invalid

        func end() {
            guard id != .invalid else { return }
            UIApplication.shared.endBackgroundTask(id)
            id = .invalid
        }
    }
}
