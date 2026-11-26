import UIKit

/// Finds the topmost view controller that can safely present a modal
/// `UIAlertController` right now, even when sheets (such as Settings or
/// Remote Hosts) are open over the root view.
///
/// Returns `nil` when a presentation or dismissal transition is in
/// progress, or another `UIAlertController` is already on top, so the
/// caller waits briefly and retries instead of having UIKit silently
/// drop the alert.
@MainActor
enum TopAlertPresenter {
    static func topViewController(from root: UIViewController?) -> UIViewController? {
        guard var vc = root else { return nil }
        while let next = vc.presentedViewController {
            if next.isBeingDismissed || next.isBeingPresented {
                return nil
            }
            vc = next
        }
        if vc.isBeingPresented || vc.isBeingDismissed || vc is UIAlertController {
            return nil
        }
        return vc
    }

    static func activeWindow() -> UIWindow? {
        let scenes = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }
        guard let scene = scenes.first(where: { $0.activationState == .foregroundActive }) else {
            return nil
        }
        return scene.keyWindow ?? scene.windows.first(where: { !$0.isHidden })
    }
}

/// A `UIAlertController` that reports when it disappears without one of
/// its action buttons having been tapped (for example if its presenting
/// sheet was dismissed programmatically).
@MainActor
final class ObservableAlertController: UIAlertController {
    var onUnexpectedDismiss: (() -> Void)?
    private var handled = false

    func markHandled() {
        handled = true
    }

    override func viewDidDisappear(_ animated: Bool) {
        super.viewDidDisappear(animated)
        if !handled && (isBeingDismissed || presentingViewController == nil) {
            let cb = onUnexpectedDismiss
            onUnexpectedDismiss = nil
            cb?()
        }
    }
}
