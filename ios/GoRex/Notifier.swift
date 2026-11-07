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

    /// Takes the taps and the hosts' blocked programs.
    func start() {
        UNUserNotificationCenter.current().delegate = self
        Host.blocked = { host, session, block, r in
            let name = r.app ?? host.programs[block] ?? host.config.name
            Notifier.shared.post(String(localized: "\(name) needs input"), r.msg ?? r.title ?? "",
                                 host: host, session: session, block: block)
        }
    }

    /// Posts a notification of a block's terminal, unless the user looks
    /// at that terminal. Permission is asked the first time.
    func post(_ title: String, _ body: String, host: Host, session: String, block: String) {
        if UIApplication.shared.applicationState == .active, onScreen?.host == host.id, onScreen?.block == block { return }
        let window = host.titles[block].flatMap { $0.isEmpty ? nil : $0 }
        let c = UNMutableNotificationContent()
        c.title = title.isEmpty ? host.programs[block] ?? window ?? host.config.name : title
        c.subtitle = [window, host.config.name].compactMap(\.self).joined(separator: " · ")
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

    /// Shows them in the foreground too: post leaves out the terminal on screen.
    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter,
                                            willPresent notification: UNNotification) async -> UNNotificationPresentationOptions {
        [.banner, .list, .sound]
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter,
                                            didReceive response: UNNotificationResponse) async {
        let info = response.notification.request.content.userInfo
        guard let host = (info["host"] as? String).flatMap(UUID.init(uuidString:)),
              let session = info["session"] as? String, let block = info["block"] as? String else { return }
        let title = info["title"] as? String ?? ""
        await MainActor.run { opening = TerminalTarget(host: host, session: session, block: block, title: title) }
    }
}
