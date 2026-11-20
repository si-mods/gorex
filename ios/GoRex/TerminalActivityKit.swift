import ActivityKit
import UIKit

extension TerminalLive {
    /// The app's: ActivityKit's activities, and UIKit's background time.
    static let shared = TerminalLive(driver: ActivityKitDriver(), grace: BackgroundGrace(
        begin: { expired in
            let t = UIApplication.shared.beginBackgroundTask(withName: "terminal grace") { MainActor.assumeIsolated { expired() } }
            return t == .invalid ? nil : t.rawValue
        },
        end: { UIApplication.shared.endBackgroundTask(UIBackgroundTaskIdentifier(rawValue: $0)) }))
}

/// TerminalLive's driver on ActivityKit: local updates only, no push.
final class ActivityKitDriver: TerminalLive.Driver {
    private var activity: Activity<TerminalActivity>?

    var enabled: Bool { ActivityAuthorizationInfo().areActivitiesEnabled }

    var running: Bool {
        guard let a = activity else { return false }
        return a.activityState == .active || a.activityState == .stale
    }

    func start(_ attributes: TerminalActivity, _ state: TerminalActivity.ContentState) {
        // One at a time: those of an earlier run too.
        end()
        do {
            activity = try Activity.request(attributes: attributes, content: content(state, stale: nil), pushType: nil)
        } catch {
            print("terminal activity: \(error)")
        }
    }

    func update(_ state: TerminalActivity.ContentState, stale: Date?) {
        // Activity is not Sendable; ActivityKit takes its calls from any thread.
        guard let activity else { return }
        nonisolated(unsafe) let a = activity
        let c = content(state, stale: stale)
        // Its own background time, so that an update made as the app leaves
        // gets to ActivityKit before the app stops.
        finishing { await a.update(c) }
    }

    func end() {
        activity = nil
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

    private func finishing(_ work: @escaping @Sendable () async -> Void) {
        let app = UIApplication.shared
        let t = app.beginBackgroundTask(withName: "terminal activity", expirationHandler: nil)
        Task {
            await work()
            if t != .invalid { app.endBackgroundTask(t) }
        }
    }
}
