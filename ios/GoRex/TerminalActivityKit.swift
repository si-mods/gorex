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
        return TerminalLive(driver: driver, grace: .shared)
    }()
}

/// TerminalLive's driver on ActivityKit.
final class ActivityKitDriver: TerminalLive.Driver {
    private var activity: Activity<TerminalActivity>?
    /// Called with each push token an activity gets: its host
    /// (attributes.host) and the token's bytes. The app sets it.
    var onToken: @MainActor (UUID, Data) -> Void = { _, _ in }
    /// The consumption of the activity's token updates.
    private var tokenTask: Task<Void, Never>?

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
        // One at a time: those of an earlier run too.
        end()
        do {
            let a = try Activity.request(attributes: attributes, content: content(state, stale: nil), pushType: .token)
            activity = a
            // The token it may have had at once, and each next one.
            nonisolated(unsafe) let b = a
            let host = attributes.host
            let onToken = self.onToken
            tokenTask = Task {
                if let t = b.pushToken { onToken(host, t) }
                for await t in b.pushTokenUpdates { onToken(host, t) }
            }
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
        tokenTask?.cancel()
        tokenTask = nil
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
