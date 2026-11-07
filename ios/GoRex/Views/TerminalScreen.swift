import SwiftUI

/// What a window's row opens: the window's block on a host.
struct TerminalTarget: Hashable {
    let host: UUID
    let session: String
    let block: String
    let title: String
}

/// A window's terminal, full screen, under a clear navigation bar that
/// steps aside from the cursor.
struct TerminalScreen: View {
    let target: TerminalTarget
    let host: Host
    /// Opens another terminal in place of this one, or goes back to the
    /// list, for ⌘T and ⌘W.
    let go: (TerminalTarget?) -> Void
    /// Made once, as the screen appears: SwiftUI makes the screen again
    /// on each update of the list, and keeps the first State.
    @State private var session: TerminalSession?
    @Environment(\.scenePhase) private var phase
    /// Whether the stream was left in the background, for a resume.
    @State private var suspended = false
    /// Where the bar ends and the status bar, down from the top of the
    /// screen; whether a tap brought the bar back over the cursor.
    @State private var top: CGFloat = 0
    @State private var statusBar: CGFloat = 0
    @State private var revealed = false
    /// What failed last, and why, as the server says it.
    @State private var failure: (title: LocalizedStringKey, why: String)?
    /// Whether the session's sheet is open, as the title opens it.
    @State private var sheet = false

    init(target: TerminalTarget, host: Host, go: @escaping (TerminalTarget?) -> Void) {
        self.target = target
        self.host = host
        self.go = go
    }

    var body: some View {
        // The terminal's background fills the page edge to edge, under the
        // bar and the key bar too; the bar and the text on it take its scheme.
        let bg = session?.background ?? .black
        let scheme: ColorScheme = 0.299 * Double(bg.r) + 0.587 * Double(bg.g) + 0.114 * Double(bg.b) < 128 ? .dark : .light
        // The terminal goes up under the clear bar to the status bar, and the
        // bar steps aside while the cursor is under it, as in a new
        // terminal: a scroll back or a tap at the top brings it back. The
        // keyboard goes over the terminal, which leaves room for it.
        let away = session?.covered == true && !revealed
        let bar = top - statusBar
        Group { if let session { TerminalRepresentable(session: session, cover: bar, newTab: newTab, closeTab: closeTab) } else { Color.clear } }
            .padding(.top, statusBar)
            .ignoresSafeArea(.container, edges: .top)
            .ignoresSafeArea(.keyboard)
            .overlay(alignment: .top) { if let p = session?.progress { ProgressLine(progress: p).padding(.top, statusBar).ignoresSafeArea(.container, edges: .top) } }
            .background {
                // Where the bar ends; the status bar is the window's inset.
                Color(cgColor: bg.cgColor)
                    .ignoresSafeArea()
                    .onGeometryChange(for: CGFloat.self) { $0.safeAreaInsets.top } action: {
                        // Measured with the bar there: away, it leaves the
                        // status bar's alone, and must not step back for that.
                        statusBar = Self.statusBar
                        if $0 > statusBar { top = $0 }
                    }
            }
            // As the list's edge under its bar: what scrolls up under the
            // bar blurs and fades into the background; with the bar away,
            // only under the status bar.
            .overlay(alignment: .top) { TopEdge(color: Color(cgColor: bg.cgColor), height: (away ? statusBar : top) + 10) }
            .overlay(alignment: .top) {
                if away {
                    Color.clear.frame(height: bar).contentShape(.rect)
                        .onTapGesture { withAnimation(.snappy) { revealed = true } }
                }
            }
            .overlay { status }
            .toolbar(away ? .hidden : .visible, for: .navigationBar)
            .animation(.snappy, value: away)
            .onChange(of: session?.covered) { _, c in if c != true { revealed = false } }
            .navigationTitle(title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                // The title floats in a capsule of glass, with the tile of
                // the window's program, as its row in the list shows it; a
                // tap opens the session's sheet.
                ToolbarItem(placement: .principal) {
                    // As tall as the back button beside it.
                    Button { sheet = true } label: {
                        HStack(spacing: 10) {
                            let k = ProgramTiles.scale, small = 0.85
                            ProgramTile(program: .of(host.programs[target.block]))
                                .frame(width: 21 * k, height: 16 * k)
                                .scaleEffect(small)
                                .frame(width: 21 * k * small, height: 16 * k * small)
                            Text(title).font(.body.weight(.semibold)).lineLimit(1)
                        }
                        .padding(.leading, 12)
                        .padding(.trailing, 18)
                        .frame(height: 44)
                        // Glass behind, not over: on the content it would fade the tile.
                        .background { Capsule().fill(.clear).glassEffect(.regular, in: .capsule) }
                    }
                    .buttonStyle(.plain)
                }
                .sharedBackgroundVisibility(.hidden)
                // The window's menu; the bar's glass draws it.
                ToolbarItem(placement: .topBarTrailing) {
                    Menu {
                        Button("New Tab", systemImage: "plus") { newTab() }
                    } label: {
                        Image(systemName: "ellipsis")
                    }
                    .accessibilityLabel("More")
                }
            }
            // The sheet takes the terminal's scheme as the bar does; the
            // one below does not reach it under a dark system.
            .sheet(isPresented: $sheet) { SessionSheet(target: target, host: host, go: go, newTab: newTab).environment(\.colorScheme, scheme) }
            .toolbarBackgroundVisibility(.hidden, for: .navigationBar)
            .toolbarColorScheme(scheme, for: .navigationBar)
            .environment(\.colorScheme, scheme)
            .onAppear {
                let s = session ?? TerminalSession(host: host, session: target.session, block: target.block)
                session = s
                s.start()
                Notifier.shared.onScreen = target
                host.see(target.block)
            }
            .onDisappear {
                host.see(target.block)
                session?.close()
                if Notifier.shared.onScreen == target { Notifier.shared.onScreen = nil }
            }
            .alert(failure?.title ?? "", isPresented: Binding { failure != nil } set: { if !$0 { failure = nil } }) {
            } message: {
                Text(failure?.why ?? "")
            }
            .onChange(of: phase) { _, p in
                // Inactive, as under Control Center or an alert, keeps it.
                if p == .background {
                    session?.suspend()
                    suspended = true
                }
                if p == .active, suspended {
                    session?.resume()
                    suspended = false
                }
            }
    }

    /// The height of the status bar, the window's top inset.
    private static var statusBar: CGFloat {
        UIApplication.shared.connectedScenes.lazy.compactMap { ($0 as? UIWindowScene)?.keyWindow }.first?.safeAreaInsets.top ?? 0
    }

    /// The session of the window open, as the host has it.
    private var hostSession: Host.Session? {
        host.sessions.first { $0.id == target.session }
    }

    /// The window open, as its session's view has it.
    private var window: SessionView.Window? {
        hostSession?.view?.windows?.first { $0.holds(target.block) }
    }

    /// The window's title as its row shows it, when the block open is the
    /// one in front, else as the row gave it, else its session's name, as
    /// for a new session.
    private var title: String {
        let live = window.flatMap { $0.block == target.block ? host.title($0) : nil } ?? ""
        return [live, target.title].first { !$0.isEmpty } ?? sessionLabel
    }

    /// The name of the window's session.
    private var sessionLabel: String {
        hostSession?.label ?? ""
    }

    /// Opens a new window in the session, and goes to it.
    private func newTab() {
        Task {
            do {
                if let b = try await host.newWindow(in: target.session) {
                    go(TerminalTarget(host: host.id, session: target.session, block: b, title: ""))
                }
            } catch {
                failure = ("Can't Open a New Window", error.localizedDescription)
            }
        }
    }

    /// Closes the window, without asking, as its row's Close Tab does, and
    /// goes back to the list.
    private func closeTab() {
        guard let w = window else { return }
        Task {
            do {
                try await host.closeWindow(w.window_id, in: target.session)
                go(nil)
            } catch {
                failure = ("Can't Close the Tab", error.localizedDescription)
            }
        }
    }

    @ViewBuilder private var status: some View {
        switch session?.state ?? .connecting {
        case .connecting:
            ProgressView()
        case .live:
            EmptyView()
        case .exited(let code):
            label(String(localized: "The program exited with code \(code)."))
        case .failed(let why):
            VStack(spacing: 12) {
                label(why)
                Button("Retry") { session?.start() }.buttonStyle(.glassProminent).foregroundStyle(Color.page)
            }
        }
    }

    private func label(_ s: String) -> some View {
        Text(s).font(.callout).padding(12).glassEffect(.regular, in: .rect(cornerRadius: 14))
    }
}

/// A soft edge at the top of the page: blurred and in the background's
/// color at the top, clear below.
private struct TopEdge: View {
    let color: Color
    /// How far down from the top of the screen it reaches.
    let height: CGFloat

    var body: some View {
        let fade = LinearGradient(stops: [.init(color: .black, location: 0), .init(color: .black, location: 0.35), .init(color: .clear, location: 1)],
                                  startPoint: .top, endPoint: .bottom)
        // From the top of the screen, height down.
        VStack(spacing: 0) {
            ZStack {
                Rectangle().fill(.ultraThinMaterial)
                color.opacity(0.75)
            }
            .mask(fade)
            .frame(height: height)
            Spacer(minLength: 0)
        }
        .ignoresSafeArea(.container, edges: .top)
        .allowsHitTesting(false)
    }
}

/// The progress a program reports, as a thin line atop the terminal: its
/// share, or all of it faint while it can't tell, red on an error.
private struct ProgressLine: View {
    let progress: TerminalSession.Progress

    var body: some View {
        GeometryReader { g in
            Rectangle()
                .fill(progress.failed ? Color.red : Color.accentColor)
                .opacity(progress.fraction == nil ? 0.4 : 1)
                .frame(width: g.size.width * (progress.fraction ?? 1))
                .animation(.default, value: progress)
        }
        .frame(height: 2)
        .allowsHitTesting(false)
    }
}

private struct TerminalRepresentable: UIViewRepresentable {
    let session: TerminalSession
    /// How far down the bar reaches over the terminal.
    let cover: CGFloat
    let newTab: () -> Void
    let closeTab: () -> Void

    func makeUIView(context: Context) -> TerminalHost {
        let v = TerminalView(session: session)
        let host = TerminalHost(v)
        updateUIView(host, context: context)
        DispatchQueue.main.async { v.focus() }
        return host
    }

    func updateUIView(_ v: TerminalHost, context: Context) {
        v.terminal.cover = cover
        v.terminal.newTab = newTab
        v.terminal.closeTab = closeTab
    }

    static func dismantleUIView(_ v: TerminalHost, coordinator: ()) { v.terminal.close() }
}
