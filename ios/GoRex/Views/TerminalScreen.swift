import SwiftUI

/// What a window's row opens: the window's block on a host.
struct TerminalTarget: Hashable {
    let host: UUID
    let session: String
    let block: String
    let title: String
}

/// A window's terminal, full screen, under the navigation bar.
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
        // The keyboard goes over the terminal, which leaves room for it.
        Group { if let session { TerminalRepresentable(session: session) } else { Color.clear } }
            .ignoresSafeArea(.keyboard)
            .background(Color(cgColor: bg.cgColor).ignoresSafeArea())
            .overlay { status }
            .navigationTitle(target.title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .principal) {
                    Text(target.title).font(.subheadline.weight(.semibold)).foregroundStyle(.secondary).lineLimit(1)
                }
            }
            .toolbarBackgroundVisibility(.hidden, for: .navigationBar)
            .toolbarColorScheme(scheme, for: .navigationBar)
            .environment(\.colorScheme, scheme)
            .onAppear {
                let s = session ?? TerminalSession(host: host, session: target.session, block: target.block)
                session = s
                s.start()
            }
            .onDisappear { session?.close() }
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
