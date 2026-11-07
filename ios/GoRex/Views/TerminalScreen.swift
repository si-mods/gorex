import SwiftUI

/// What a window's row opens: the window's block on a host.
struct TerminalTarget: Hashable {
    let host: UUID
    let session: String
    let block: String
    let title: String
}

/// A window's terminal, full screen under a clear navigation bar.
struct TerminalScreen: View {
    let target: TerminalTarget
    let host: Host
    /// Made once, as the screen appears: SwiftUI makes the screen again
    /// on each update of the list, and keeps the first State.
    @State private var session: TerminalSession?
    @Environment(\.scenePhase) private var phase
    /// Whether the stream was left in the background, for a resume.
    @State private var suspended = false
    /// Where the terminal starts, under the bar, in the window.
    @State private var top: CGFloat = 0

    init(target: TerminalTarget, host: Host) {
        self.target = target
        self.host = host
    }

    var body: some View {
        // The terminal's background fills the page edge to edge, under the
        // bar and the key bar too; the bar and the text on it take its scheme.
        let bg = session?.background ?? .black
        let scheme: ColorScheme = 0.299 * Double(bg.r) + 0.587 * Double(bg.g) + 0.114 * Double(bg.b) < 128 ? .dark : .light
        // The terminal starts under the clear bar, which hides none of it;
        // the keyboard goes over it, which leaves room for it.
        Group { if let session { TerminalRepresentable(session: session) } else { Color.clear } }
            .ignoresSafeArea(.keyboard)
            .onGeometryChange(for: CGFloat.self) { $0.frame(in: .global).minY } action: { top = $0 }
            .overlay(alignment: .top) { if let p = session?.progress { ProgressLine(progress: p) } }
            .background(Color(cgColor: bg.cgColor).ignoresSafeArea())
            // As the list's edge under its bar: the bar and the status bar
            // blur, and what scrolls up fades into the background a little
            // below them.
            .overlay(alignment: .top) { TopEdge(color: Color(cgColor: bg.cgColor), height: top + 10) }
            .overlay { status }
            .navigationTitle(title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                // The title floats in a capsule of glass, with the tile of
                // the window's program, as its row in the list shows it.
                ToolbarItem(placement: .principal) {
                    // As tall as the back button beside it.
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
                .sharedBackgroundVisibility(.hidden)
            }
            .toolbarBackgroundVisibility(.hidden, for: .navigationBar)
            .toolbarColorScheme(scheme, for: .navigationBar)
            .environment(\.colorScheme, scheme)
            .onAppear {
                let s = session ?? TerminalSession(host: host, session: target.session, block: target.block)
                session = s
                s.start()
                Notifier.shared.onScreen = target
            }
            .onDisappear {
                session?.close()
                if Notifier.shared.onScreen == target { Notifier.shared.onScreen = nil }
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

    /// The window's title, else its session's name, as for a new session.
    private var title: String {
        target.title.isEmpty ? host.sessions.first { $0.id == target.session }?.label ?? "" : target.title
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

    func makeUIView(context: Context) -> TerminalView {
        let v = TerminalView(session: session)
        DispatchQueue.main.async { v.focus() }
        return v
    }

    func updateUIView(_ v: TerminalView, context: Context) {}

    static func dismantleUIView(_ v: TerminalView, coordinator: ()) { v.close() }
}
