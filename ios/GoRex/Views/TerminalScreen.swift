import SwiftUI

/// What a window's row opens: the window's block on a host.
struct TerminalTarget: Hashable {
    let host: UUID
    let session: String
    let block: String
    let title: String
}

/// A window's terminal, full screen, with the navigation bar clear over it.
struct TerminalScreen: View {
    let target: TerminalTarget
    let host: Host
    /// Made once, as the screen appears: SwiftUI makes the screen again
    /// on each update of the list, and keeps the first State.
    @State private var session: TerminalSession?
    @Environment(\.scenePhase) private var phase
    /// Whether the stream was left in the background, for a resume.
    @State private var suspended = false

    init(target: TerminalTarget, host: Host) {
        self.target = target
        self.host = host
    }

    var body: some View {
        // The terminal's background fills the page edge to edge, under the
        // bar and the key bar too; the bar and the text on it take its scheme.
        let bg = session?.background ?? .black
        let scheme: ColorScheme = 0.299 * Double(bg.r) + 0.587 * Double(bg.g) + 0.114 * Double(bg.b) < 128 ? .dark : .light
        // The terminal goes up under the clear bar to the status bar; the
        // keyboard goes over it, which leaves room for it.
        let top = Self.statusBar
        Group { if let session { TerminalRepresentable(session: session) } else { Color.clear } }
            .padding(.top, top)
            .ignoresSafeArea(.container, edges: .top)
            .ignoresSafeArea(.keyboard)
            .background(Color(cgColor: bg.cgColor).ignoresSafeArea())
            .overlay { status }
            .overlay(alignment: .top) { if let p = session?.progress { ProgressLine(progress: p).padding(.top, top).ignoresSafeArea(.container, edges: .top) } }
            .navigationTitle(title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                // The title floats in a capsule of glass, with the tile of
                // the window's program, as its row in the list shows it.
                ToolbarItem(placement: .principal) {
                    HStack(spacing: 8) {
                        let k = ProgramTiles.scale, small = 0.75
                        ProgramTile(program: .of(host.programs[target.block]))
                            .frame(width: 21 * k, height: 16 * k)
                            .scaleEffect(small)
                            .frame(width: 21 * k * small, height: 16 * k * small)
                        Text(title).font(.subheadline.weight(.semibold)).lineLimit(1)
                    }
                    .padding(.leading, 8)
                    .padding(.trailing, 14)
                    .padding(.vertical, 7)
                    .glassEffect(.regular, in: .capsule)
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

    /// The height of the status bar, which the terminal stays under.
    private static var statusBar: CGFloat {
        UIApplication.shared.connectedScenes.lazy.compactMap { ($0 as? UIWindowScene)?.keyWindow }.first?.safeAreaInsets.top ?? 0
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
