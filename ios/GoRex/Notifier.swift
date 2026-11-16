import Observation
import UIKit
import UserNotifications

/// Local notifications of what a terminal tells: a program that waits for
/// the user, and the notifications programs send (OSC 9 and 777). They
/// come while the connection lives, in the foreground and for the short
/// while iOS keeps the app running after; a tap opens the terminal.
@Observable
final class Notifier: NSObject, UNUserNotificationCenterDelegate {
    static let shared = Notifier()

    /// The terminal on screen, whose news the user sees there.
    @ObservationIgnored var onScreen: TerminalTarget?
    /// The terminal a tapped notification opens, for the list to go to.
    var opening: TerminalTarget?

    /// Takes the taps and the hosts' blocked programs, and tells the hosts
    /// which terminal is on screen.
    func start() {
        UNUserNotificationCenter.current().delegate = self
        Host.shown = { host, block in Notifier.shared.onScreen?.host == host.id && Notifier.shared.onScreen?.block == block }
        Host.blocked = { host, session, block, r in
            let name = r.app ?? host.blocks[block]?.program ?? host.config.name
            Notifier.shared.post(String(localized: "\(name) needs input"), r.msg ?? r.title ?? "",
                                 host: host, session: session, block: block)
        }
    }

    /// Posts a notification of a block's terminal, unless the user looks
    /// at that terminal. Permission is asked the first time.
    func post(_ title: String, _ body: String, host: Host, session: String, block: String) {
        if UIApplication.shared.applicationState == .active, onScreen?.host == host.id, onScreen?.block == block { return }
        let window = host.blocks[block]?.title.flatMap { $0.isEmpty ? nil : $0 }
        let c = UNMutableNotificationContent()
        c.title = (title.isEmpty ? host.blocks[block]?.program ?? window ?? host.config.name : title).asText
        c.subtitle = [window, host.config.name].compactMap(\.self).joined(separator: " · ").asText
        c.body = body
        c.sound = .default
        // One window's notifications group together.
        c.threadIdentifier = "\(host.id.uuidString) \(block)"
        c.userInfo = ["host": host.id.uuidString, "session": session, "block": block, "title": window ?? ""]
        let request = UNNotificationRequest(identifier: UUID().uuidString, content: c, trigger: nil)
        Task {
            let center = UNUserNotificationCenter.current()
            if await center.notificationSettings().authorizationStatus == .notDetermined {
                _ = try? await center.requestAuthorization(options: [.alert, .sound])
            }
            try? await center.add(request)
        }
    }

    // The answers below use the completion handlers, not the async forms: for
    // those, the bridge calls the handler on whatever thread the task resumes
    // on, and UIKit asserts the main thread right after it, taking the app's
    // snapshot and state restoration. iOS 27 crashed every notification tap
    // that way (build 1.1 (86), "Call must be made on main thread").

    /// Shows them in the foreground too: post leaves out the terminal on screen.
    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter,
                                            willPresent notification: UNNotification,
                                            withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        nonisolated(unsafe) let finish = completionHandler
        Self.onMain { finish([.banner, .list, .sound]) }
    }

    /// Opens the terminal the tapped notification names, then answers.
    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter,
                                            didReceive response: UNNotificationResponse,
                                            withCompletionHandler completionHandler: @escaping () -> Void) {
        let info = response.notification.request.content.userInfo
        let host = (info["host"] as? String).flatMap(UUID.init(uuidString:))
        let session = info["session"] as? String
        let block = info["block"] as? String
        let title = info["title"] as? String ?? ""
        nonisolated(unsafe) let finish = completionHandler
        Self.onMain {
            if let host, let session, let block {
                self.opening = TerminalTarget(host: host, session: session, block: block, title: title, explicit: true)
            }
            finish()
        }
    }

    /// Runs an answer on the main thread: now when the caller is there, else
    /// on its next turn.
    nonisolated static func onMain(_ answer: @escaping @MainActor () -> Void) {
        if Thread.isMainThread {
            MainActor.assumeIsolated { answer() }
        } else {
            DispatchQueue.main.async { MainActor.assumeIsolated { answer() } }
        }
    }
}
