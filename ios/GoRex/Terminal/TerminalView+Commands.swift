import UIKit

/// A hardware keyboard's ⌘ keys, which the ⌘ held lists: the tab's, the
/// uploads', the font's, and the clear. Copy, Paste and Select All are the
/// system's Edit menu's, which call the view's own. None of them reaches the program:
/// pressesBegan leaves ⌘ with a letter, digit or sign to UIKit.
extension TerminalView {
    override var keyCommands: [UIKeyCommand]? {
        // ⌘+ is ⌘= on the key, as on the Mac; ⌘⇧= as well, unlisted.
        let plus = UIKeyCommand(action: #selector(bigger), input: "+", modifierFlags: .command)
        plus.attributes = .hidden
        return [
            UIKeyCommand(title: String(localized: "New Tab"), action: #selector(newTabKey), input: "t", modifierFlags: .command),
            UIKeyCommand(title: String(localized: "Close Tab"), action: #selector(closeTabKey), input: "w", modifierFlags: .command),
            // The key bar's uploads, which a hardware keyboard keeps away.
            UIKeyCommand(title: String(localized: "Add File"), action: #selector(addFileKey), input: "o", modifierFlags: .command),
            UIKeyCommand(title: String(localized: "Add Photo"), action: #selector(addPhotoKey), input: "o", modifierFlags: [.command, .shift]),
            UIKeyCommand(title: String(localized: "Clear Screen and Scrollback"), action: #selector(clear), input: "k", modifierFlags: .command),
            UIKeyCommand(title: String(localized: "Bigger Text"), action: #selector(bigger), input: "=", modifierFlags: .command),
            plus,
            UIKeyCommand(title: String(localized: "Smaller Text"), action: #selector(smaller), input: "-", modifierFlags: .command),
            UIKeyCommand(title: String(localized: "Actual Size"), action: #selector(actualSize), input: "0", modifierFlags: .command),
        ]
    }

    @objc private func newTabKey() { newTab?() }
    @objc private func closeTabKey() { closeTab?() }
    @objc private func addFileKey() { if session.host.canUpload { addFile() } }
    @objc private func addPhotoKey() { if session.host.canUpload { addPhoto() } }

    /// Clears the screen and the scrollback on the server, which sends the
    /// screen again, as the desktop's ⌘K.
    @objc private func clear() {
        // The demo's terminal runs here: Ctrl-L clears it, as the shell's does.
        if session.host.isDemo {
            session.type(Data([0x0C]))
            return
        }
        Task { [session] in
            _ = try? await session.host.live().callBlock("clear", session: session.session, block: session.block)
        }
    }

    @objc private func bigger() { flashSize { zoom(by: 1) } }
    @objc private func smaller() { flashSize { zoom(by: -1) } }
    /// Back to Settings' first size.
    @objc private func actualSize() { flashSize { zoom(to: 12) } }

    /// Zooms as a pinch does, the grid's size shown a moment.
    private func flashSize(_ zoom: () -> Void) {
        sizeLabel.layer.removeAllAnimations()
        zoom()
        UIView.animate(withDuration: 0.3, delay: 1) { self.sizeLabel.alpha = 0 }
    }
}
