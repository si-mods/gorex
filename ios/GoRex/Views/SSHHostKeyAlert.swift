import SwiftUI
import UIKit

/// The engine's questions of an ssh:// host: trust its key on first
/// sight or for the run, say a changed one is the host's own, give the
/// secret its login asks, and take the offer to install the phone's key
/// (docs/IOS-023-ssh-hosts.md, Trust). Presented via UIKit on the
/// topmost view controller so it still shows when a sheet (such as
/// Settings › Remote Hosts) is open over the root view.
private struct SSHHostKeyAlert: ViewModifier {
    @Environment(\.scenePhase) private var scenePhase
    private let engine = TailnetEngine.shared
    @State private var coordinator = SSHAlertCoordinator()

    func body(content: Content) -> some View {
        let item = SSHAlertItem.current(from: engine)
        content
            .onAppear { coordinator.sync(to: item, engine: engine) }
            .onChange(of: item) { _, next in coordinator.sync(to: next, engine: engine) }
            .onChange(of: scenePhase) { _, phase in
                if phase == .active { coordinator.sync(to: item, engine: engine) }
            }
    }
}

private enum SSHAlertItem: Equatable {
    case hostKey(TailnetEngine.SSHHostKeyQuestion)
    case auth(TailnetEngine.SSHAuthQuestion)
    case install(HostConfig, error: String?)

    @MainActor
    static func current(from engine: TailnetEngine) -> SSHAlertItem? {
        if let q = engine.sshHostKeyQuestion { return .hostKey(q) }
        if let q = engine.sshAuthQuestion { return .auth(q) }
        if let c = engine.sshInstallOffer, !engine.sshInstalling {
            return .install(c, error: engine.sshInstallError)
        }
        return nil
    }
}

@MainActor
private final class SSHAlertCoordinator {
    private var desired: SSHAlertItem?
    private var presentedItem: SSHAlertItem?
    private weak var presentedAlert: ObservableAlertController?
    private var retryTask: Task<Void, Never>?

    func sync(to item: SSHAlertItem?, engine: TailnetEngine) {
        desired = item
        retryTask?.cancel()
        retryTask = nil

        guard let item else {
            if let alert = presentedAlert, alert.presentingViewController != nil {
                alert.markHandled()
                alert.dismiss(animated: true)
            }
            presentedAlert = nil
            presentedItem = nil
            return
        }

        if item == presentedItem,
           let alert = presentedAlert,
           alert.presentingViewController != nil,
           !alert.isBeingDismissed {
            return
        }

        if let alert = presentedAlert, alert.presentingViewController != nil {
            if !alert.isBeingDismissed {
                alert.markHandled()
                presentedAlert = nil
                presentedItem = nil
                alert.dismiss(animated: true) { [weak self] in
                    guard let self else { return }
                    self.sync(to: self.desired, engine: engine)
                }
            } else {
                scheduleRetry(engine: engine)
            }
            return
        }

        presentedAlert = nil
        presentedItem = nil

        guard let top = TopAlertPresenter.topViewController(from: TopAlertPresenter.activeWindow()?.rootViewController) else {
            scheduleRetry(engine: engine)
            return
        }

        let alert = makeAlert(for: item, engine: engine)
        alert.onUnexpectedDismiss = { [weak self, weak alert] in
            guard let self, let alert, self.presentedAlert === alert else { return }
            self.presentedAlert = nil
            self.presentedItem = nil
            self.scheduleRetry(engine: engine)
        }
        presentedItem = item
        presentedAlert = alert
        top.present(alert, animated: true)
    }

    private func scheduleRetry(engine: TailnetEngine) {
        guard desired != nil, UIApplication.shared.applicationState != .background else { return }
        retryTask?.cancel()
        retryTask = Task { @MainActor [weak self] in
            try? await Task.sleep(for: .milliseconds(150))
            guard !Task.isCancelled, let self else { return }
            self.sync(to: self.desired, engine: engine)
        }
    }

    private func makeAlert(for item: SSHAlertItem, engine: TailnetEngine) -> ObservableAlertController {
        switch item {
        case .hostKey(let q):
            let title = q.changed
                ? String(localized: "SSH host key changed")
                : String(localized: "Trust this SSH host?")
            let message = q.changed
                ? String(localized: "\(q.user)@\(q.host):\(String(q.port)) answered with a different key than the one trusted before. This could be a man-in-the-middle attack, or the host was reinstalled.\n\nTrusted: \(q.previous)\nNew (\(q.keyType)): \(q.fingerprint)")
                : String(localized: "The \(q.keyType) key of \(q.user)@\(q.host):\(String(q.port)) is\n\n\(q.fingerprint)")
            let alert = ObservableAlertController(title: title, message: message, preferredStyle: .alert)
            let answer: (Bool?) -> Void = { [weak self, weak alert] save in
                alert?.markHandled()
                self?.presentedAlert = nil
                self?.presentedItem = nil
                engine.answerSSHHostKey(q, save: save)
            }
            if q.changed {
                alert.addAction(UIAlertAction(title: String(localized: "Replace & Connect"), style: .destructive) { _ in answer(true) })
                if !engine.savedSSHHostKey(q) {
                    alert.addAction(UIAlertAction(title: String(localized: "Connect Once"), style: .default) { _ in answer(false) })
                }
            } else {
                alert.addAction(UIAlertAction(title: String(localized: "Trust & Save"), style: .default) { _ in answer(true) })
                alert.addAction(UIAlertAction(title: String(localized: "Connect Once"), style: .default) { _ in answer(false) })
            }
            alert.addAction(UIAlertAction(title: String(localized: "Cancel"), style: .cancel) { _ in answer(nil) })
            return alert

        case .auth(let q):
            let title = "\(q.user)@\(q.host):\(String(q.port))"
            let message = q.retry ? String(localized: "Incorrect, try again.") : q.prompt
            let alert = ObservableAlertController(title: title, message: message, preferredStyle: .alert)
            alert.addTextField { tf in
                if q.otp {
                    tf.placeholder = String(localized: "Code")
                    tf.textContentType = .oneTimeCode
                    tf.keyboardType = .numberPad
                } else if q.secret {
                    tf.placeholder = String(localized: "Password")
                    tf.isSecureTextEntry = true
                    tf.textContentType = .password
                } else {
                    tf.placeholder = q.prompt.isEmpty ? String(localized: "Response") : q.prompt
                    tf.autocapitalizationType = .none
                    tf.autocorrectionType = .no
                }
            }
            let continueAction = UIAlertAction(title: String(localized: "Continue"), style: .default) { [weak self, weak alert] _ in
                alert?.markHandled()
                let value = alert?.textFields?.first?.text ?? ""
                self?.presentedAlert = nil
                self?.presentedItem = nil
                engine.answerSSHAuth(q, secret: value)
            }
            alert.addAction(continueAction)
            alert.preferredAction = continueAction
            alert.addAction(UIAlertAction(title: String(localized: "Cancel"), style: .cancel) { [weak self, weak alert] _ in
                alert?.markHandled()
                self?.presentedAlert = nil
                self?.presentedItem = nil
                engine.answerSSHAuth(q, secret: nil)
            })
            return alert

        case .install(let c, let error):
            let title = String(localized: "Install this phone's key?")
            let message = String(localized: "You logged in to \(c.name) with a password. Installing this phone's SSH key on it connects without one.\(error.map { "\n\n\($0)" } ?? "")")
            let alert = ObservableAlertController(title: title, message: message, preferredStyle: .alert)
            alert.addAction(UIAlertAction(title: String(localized: "Install Key"), style: .default) { [weak self, weak alert] _ in
                alert?.markHandled()
                self?.presentedAlert = nil
                self?.presentedItem = nil
                engine.installSSHKey(c)
            })
            alert.addAction(UIAlertAction(title: String(localized: "Not Now"), style: .cancel) { [weak self, weak alert] _ in
                alert?.markHandled()
                self?.presentedAlert = nil
                self?.presentedItem = nil
                engine.skipSSHInstallKey()
            })
            return alert
        }
    }
}

extension View {
    /// Shows the engine's SSH questions, on any screen of the app.
    func sshHostKeyAlert() -> some View {
        modifier(SSHHostKeyAlert())
    }
}
