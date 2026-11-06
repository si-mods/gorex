import GhosttyKit
import UIKit

/// The Ghostty app the terminals' surfaces belong to, after rootshell's
/// GhosttyApp. Ghostty calls back from its own threads: a wakeup ticks the
/// app on the main thread, and the actions a surface sends reach its view
/// there.
enum Ghostty {
    static let app: ghostty_app_t? = {
        guard ghostty_init(0, nil) == 0, let config = config("") else { return nil }
        var runtime = ghostty_runtime_config_s(
            userdata: nil,
            supports_selection_clipboard: false,
            wakeup_cb: { _ in Ghostty.wakeup() },
            action_cb: { _, target, action in Ghostty.action(target, action) },
            read_clipboard_cb: { _, _, _, _, _, _ in GHOSTTY_CLIPBOARD_READ_UNAVAILABLE },
            confirm_read_clipboard_cb: { _, _, _, _ in },
            write_clipboard_cb: { _, _, _, _, _ in },
            close_surface_cb: { _, _ in })
        // The app keeps the config it starts with.
        return ghostty_app_new(&runtime, config)
    }()

    /// The views by their surfaces, for the actions.
    private static var views: [Int: Weak] = [:]

    private struct Weak { weak var view: TerminalView? }

    static func register(_ view: TerminalView, for surface: ghostty_surface_t) {
        views[Int(bitPattern: surface)] = Weak(view: view)
    }

    static func unregister(_ surface: ghostty_surface_t) {
        views[Int(bitPattern: surface)] = nil
    }

    /// A config of Ghostty's lines over its defaults: a remote terminal's,
    /// whose keys all go to the program and whose pastes are not questioned.
    static func config(_ lines: String) -> ghostty_config_t? {
        let text = """
        keybind = clear
        clipboard-paste-protection = false
        clipboard-read = deny
        confirm-close-surface = false
        macos-option-as-alt = true
        \(lines)
        """
        guard let c = ghostty_config_new() else { return nil }
        let url = FileManager.default.temporaryDirectory.appending(path: "ghostty-\(UUID().uuidString).conf")
        if (try? text.write(to: url, atomically: false, encoding: .utf8)) != nil {
            url.path.withCString { ghostty_config_load_file(c, $0) }
            try? FileManager.default.removeItem(at: url)
        }
        ghostty_config_finalize(c)
        return c
    }

    nonisolated private static func wakeup() {
        DispatchQueue.main.async {
            MainActor.assumeIsolated { if let app { ghostty_app_tick(app) } }
        }
    }

    /// The actions a view takes: the grid size Ghostty settled on, the
    /// background a program set, a selection that changed, and the
    /// scrollback that moved or grew.
    nonisolated private static func action(_ target: ghostty_target_s, _ action: ghostty_action_s) -> Bool {
        guard target.tag == GHOSTTY_TARGET_SURFACE, let s = target.target.surface else { return false }
        let key = Int(bitPattern: s)
        switch action.tag {
        case GHOSTTY_ACTION_PTY_RESIZE:
            let r = action.action.pty_resize
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    views[key]?.view?.resized(cols: Int(r.cols), rows: Int(r.rows), width: Int(r.width_px), height: Int(r.height_px))
                }
            }
            return true
        case GHOSTTY_ACTION_COLOR_CHANGE where action.action.color_change.kind == GHOSTTY_ACTION_COLOR_KIND_BACKGROUND:
            let c = action.action.color_change
            DispatchQueue.main.async {
                MainActor.assumeIsolated { views[key]?.view?.session.drew(background: VTerminal.RGB(r: c.r, g: c.g, b: c.b)) }
            }
            return true
        case GHOSTTY_ACTION_SELECTION_CHANGED:
            DispatchQueue.main.async {
                MainActor.assumeIsolated { views[key]?.view?.selectionMoved() }
            }
            return true
        case GHOSTTY_ACTION_SCROLLBAR:
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    views[key]?.view?.selectionMoved()
                    views[key]?.view?.syncScroll()
                }
            }
            return true
        default:
            return false
        }
    }
}
