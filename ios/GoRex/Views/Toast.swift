import SwiftUI

/// One toast (docs/IOS-034-toasts.md): a status line, a progress that turns
/// into its result, or a line with a button.
struct Toast: Identifiable {
    enum Kind {
        case status
        case error
        case progress
    }

    /// A button in the toast: Retry.
    struct Choice {
        let label: String
        let run: () -> Void
    }

    let id: UUID
    var kind: Kind
    var text: String
    /// The SF Symbol of a status.
    var symbol = "checkmark.circle.fill"
    var button: Choice?
    /// How long it stays, in seconds; nil stays until its button or its
    /// close, or until something replaces it.
    var seconds: Double?
    /// Runs once when the toast ends any way but its button: the time,
    /// its close, a swipe, or a new toast.
    var commit: (() -> Void)?

    init(id: UUID = UUID(), kind: Kind, text: String, symbol: String? = nil,
         button: Choice? = nil, seconds: Double?, commit: (() -> Void)? = nil) {
        self.id = id
        self.kind = kind
        self.text = text
        if let symbol { self.symbol = symbol } else if kind == .error { self.symbol = "exclamationmark.circle.fill" }
        self.button = button
        self.seconds = seconds
        self.commit = commit
    }

    /// How long a line stays: 4 s, and up to 4 s more for a long one, as
    /// 1 s for each 20 characters past 40.
    static func lifetime(_ text: String) -> Double {
        4 + min(4, Double(max(0, text.count - 40)) / 20)
    }

    /// The reason a failure shows. A server's not_found names the ids it
    /// looked for ("no block block:… in session:…"), which tell the user
    /// nothing: what it means is that the thing closed on the computer.
    static func reason(_ error: Error) -> String {
        if let e = error as? RexError, e.code == "not_found" || e.code == "session_not_found" {
            return String(localized: "It has closed on the computer.")
        }
        return error.localizedDescription
    }

    /// The first line of a reason, with a cut ending in an ellipsis past
    /// 120 characters: a toast wraps to three lines at most.
    static func oneLine(_ s: String) -> String {
        let first = s.split(whereSeparator: \.isNewline).first.map(String.init) ?? ""
        let line = first.trimmingCharacters(in: .whitespaces)
        return line.count > 120 ? String(line.prefix(119)) + "…" : line
    }
}

/// The toast on screen. One at a time, for the app's scene: a new one
/// replaces the old, which ends first. The terminal's UIKit views reach it here, as the SwiftUI
/// screens do.
@Observable final class Toasts {
    static let shared = Toasts()

    private(set) var current: Toast?
    /// The hosts that screens over the root mounted, latest last: a sheet,
    /// or a terminal, which shows the toast above its key bar. Only the
    /// latest shows it; with none, the root's does.
    private(set) var hosts: [UUID] = []
    func mount(_ host: UUID) { hosts.append(host) }
    func unmount(_ host: UUID) { hosts.removeAll { $0 == host } }
    /// Whether host shows the toast; nil is the root's.
    func shows(_ host: UUID?) -> Bool { hosts.last == host }
    /// Starts a countdown, and gives the way to stop it. Tests set their own.
    @ObservationIgnored private let schedule: @MainActor (Double, @escaping @MainActor () -> Void) -> () -> Void
    /// Tells VoiceOver. It never takes the focus.
    @ObservationIgnored private let announce: (String) -> Void
    @ObservationIgnored private var stop: (() -> Void)?

    init(schedule: @escaping @MainActor (Double, @escaping @MainActor () -> Void) -> () -> Void = Toasts.sleep,
         announce: @escaping (String) -> Void = { AccessibilityNotification.Announcement($0).post() }) {
        self.schedule = schedule
        self.announce = announce
    }

    static func sleep(_ seconds: Double, _ fire: @escaping @MainActor () -> Void) -> () -> Void {
        let task = Task {
            try? await Task.sleep(for: .seconds(seconds))
            if !Task.isCancelled { fire() }
        }
        return { task.cancel() }
    }

    // MARK: Showing

    /// Shows a toast. The one on screen ends first.
    func show(_ toast: Toast) {
        finish(.replaced)
        current = toast
        start(toast)
    }

    /// A line with a symbol.
    func status(_ text: String, symbol: String? = nil, seconds: Double? = nil) {
        show(Toast(kind: .status, text: text, symbol: symbol, seconds: seconds ?? Toast.lifetime(text)))
    }

    /// What failed and why. It replaces the alert that had
    /// only OK.
    func fail(_ title: LocalizedStringResource, _ error: Error) {
        fail(title, why: Toast.reason(error))
    }

    func fail(_ title: LocalizedStringResource, why: String) {
        let text = String(localized: "\(String(localized: title)): \(Toast.oneLine(why))")
        show(Toast(kind: .error, text: text, seconds: Toast.lifetime(text)))
    }

    /// A line that runs on: a spinner and its text. Give the id to
    /// settle(_:with:) when it ends.
    @discardableResult
    func progress(_ text: String) -> UUID {
        let toast = Toast(kind: .progress, text: text, seconds: nil)
        show(toast)
        return toast.id
    }

    /// Turns a progress into its result, in the same place. One that was
    /// closed or replaced meanwhile shows its result only when that is an
    /// error: the user must hear of a failure.
    func settle(_ id: UUID?, with result: Toast) {
        guard let id, current?.id == id else {
            if result.kind == .error { show(result) } else { announce(result.text) }
            return
        }
        let next = Toast(id: id, kind: result.kind, text: result.text, symbol: result.symbol, button: result.button,
                         seconds: result.seconds, commit: result.commit)
        current = next
        start(next)
    }

    // MARK: Ending

    /// The user's close or swipe.
    func dismiss() { finish(.closed) }

    /// The button of the toast shown.
    func press(_ id: UUID) {
        if current?.id == id { finish(.button) }
    }

    private enum Ending {
        case timeout, closed, replaced, button
    }

    private func start(_ toast: Toast) {
        stop?()
        stop = nil
        announce(toast.text)
        guard let seconds = toast.seconds else { return }
        let id = toast.id
        stop = schedule(seconds) { [weak self] in
            if self?.current?.id == id { self?.finish(.timeout) }
        }
    }

    private func finish(_ why: Ending) {
        guard let toast = current else { return }
        current = nil
        stop?()
        stop = nil
        if why == .button { toast.button?.run() } else { toast.commit?() }
    }
}

/// Shows the toast at the top of what it modifies, over every screen below.
/// A sheet is a view of its own, so it takes the modifier too.
private struct ToastHost: ViewModifier {
    /// Whether this host is a sheet's: it mounts while it shows, and the
    /// hosts under it hide then, so the toast shows once.
    let sheet: Bool
    @State private var id = UUID()

    func body(content: Content) -> some View {
        content
            .onAppear { if sheet { Toasts.shared.mount(id) } }
            .onDisappear { if sheet { Toasts.shared.unmount(id) } }
            .overlay(alignment: .top) { ToastSlot(host: sheet ? id : nil, edge: .top) }
    }
}

extension View {
    /// Shows the toast over the app's root.
    func toastHost() -> some View { modifier(ToastHost(sheet: false)) }

    /// Shows the toast over a sheet, and hides the root's host meanwhile.
    func sheetToastHost() -> some View { modifier(ToastHost(sheet: true)) }
}

/// Where host shows the toast, while it is the host that shows it: at the
/// top of the root and of a sheet, or at the bottom of a terminal, above
/// its key bar, where the header would else be covered. The screen mounts
/// host (Toasts.mount) while it shows.
struct ToastSlot: View {
    let host: UUID?
    let edge: VerticalEdge
    @Environment(\.accessibilityReduceMotion) private var still

    var body: some View {
        ZStack {
            if let toast = Toasts.shared.current, Toasts.shared.shows(host) {
                ToastView(toast: toast, edge: edge)
                    .id(toast.id)
                    .transition(still ? .opacity : .move(edge: edge == .top ? .top : .bottom).combined(with: .opacity))
            }
        }
        .animation(still ? .easeInOut(duration: 0.15) : .snappy, value: Toasts.shared.current?.id)
    }
}

/// The glass toast: a symbol or spinner, up to three lines, a button, and
/// for a line with a button a close. One line is a capsule; more lines
/// round its corners by the same radius.
private struct ToastView: View {
    let toast: Toast
    /// The edge it shows at, which a swipe toward dismisses it.
    let edge: VerticalEdge
    @Environment(\.accessibilityReduceMotion) private var still
    @State private var lift: CGFloat = 0

    var body: some View {
        HStack(spacing: 10) {
            switch toast.kind {
            case .progress:
                ProgressView().controlSize(.small)
            case .error:
                Image(systemName: toast.symbol).foregroundStyle(.red)
            default:
                Image(systemName: toast.symbol)
            }
            Text(verbatim: toast.text)
                .font(.subheadline)
                .lineLimit(3)
                .truncationMode(.tail)
                .multilineTextAlignment(.leading)
                .fixedSize(horizontal: false, vertical: true)
            if let button = toast.button {
                Button(button.label) { Toasts.shared.press(toast.id) }
                    .font(.subheadline.weight(.semibold))
                    .buttonStyle(.plain)
            }
            if toast.button != nil {
                Button { Toasts.shared.dismiss() } label: {
                    Image(systemName: "xmark")
                        .font(.caption2.weight(.bold))
                        .frame(width: 26, height: 26)
                        .contentShape(Rectangle().inset(by: -8))
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Close")
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
        .glassEffect(.regular, in: .rect(cornerRadius: 20, style: .continuous))
        .padding(.horizontal, 16)
        .padding(.top, edge == .top ? 8 : 0)
        .animation(.easeInOut(duration: 0.15), value: toast.text)
        .offset(y: lift)
        // A swipe toward its edge dismisses it, and it follows the finger.
        .gesture(DragGesture(minimumDistance: 8)
            .onChanged { lift = edge == .top ? min(0, $0.translation.height) : max(0, $0.translation.height) }
            .onEnded { v in
                if (edge == .top ? -v.translation.height : v.translation.height) > 24 {
                    Toasts.shared.dismiss()
                } else {
                    withAnimation(still ? .easeInOut(duration: 0.15) : .snappy) { lift = 0 }
                }
            })
        .accessibilityElement(children: .contain)
    }
}
