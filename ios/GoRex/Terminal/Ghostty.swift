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
    /// background a program set, a selection that changed, the scrollback
    /// that moved or grew, and what a program tells the user: a bell, a
    /// notification, its progress and a long command's end.
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
        case GHOSTTY_ACTION_RING_BELL:
            DispatchQueue.main.async { MainActor.assumeIsolated { bell() } }
            return true
        case GHOSTTY_ACTION_DESKTOP_NOTIFICATION:
            let n = action.action.desktop_notification
            let title = n.title.map { String(cString: $0) } ?? "", body = n.body.map { String(cString: $0) } ?? ""
            DispatchQueue.main.async {
                MainActor.assumeIsolated { views[key]?.view.map { notify(title, body, $0.session) } }
            }
            return true
        case GHOSTTY_ACTION_PROGRESS_REPORT:
            let r = action.action.progress_report
            let fraction = r.progress < 0 ? nil : Double(r.progress) / 100
            let p: TerminalSession.Progress? = switch r.state {
            case GHOSTTY_PROGRESS_STATE_REMOVE: nil
            case GHOSTTY_PROGRESS_STATE_INDETERMINATE: .init()
            case GHOSTTY_PROGRESS_STATE_ERROR: .init(fraction: fraction, failed: true)
            default: .init(fraction: fraction)
            }
            DispatchQueue.main.async {
                MainActor.assumeIsolated { views[key]?.view?.session.progress = p }
            }
            return true
        case GHOSTTY_ACTION_COMMAND_FINISHED:
            // Only a long one, told while the user is away.
            let f = action.action.command_finished
            guard f.duration > 10_000_000_000 else { return true }
            let code = Int(f.exit_code)
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    guard UIApplication.shared.applicationState != .active, let v = views[key]?.view else { return }
                    notify(String(localized: "Command finished"), code > 0 ? String(localized: "Exit code \(code)") : "", v.session)
                }
            }
            return true
        default:
            return false
        }
    }

    private static func notify(_ title: String, _ body: String, _ s: TerminalSession) {
        Notifier.shared.post(title, body, host: s.host, session: s.session, block: s.block)
    }

    /// A light tap for a bell, at most one each half second.
    private static let haptic = UIImpactFeedbackGenerator(style: .light)
    private static var rang = ContinuousClock.now - .seconds(1)

    private static func bell() {
        guard rang.duration(to: .now) >= .milliseconds(500) else { return }
        rang = .now
        haptic.impactOccurred()
    }
}
