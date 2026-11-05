import SwiftUI

/// What a window's row opens: the window's block on a host.
struct TerminalTarget: Hashable {
    let host: UUID
    let session: String
    let block: String
    let title: String
}

/// A window's terminal, full screen, with a back button.
struct TerminalScreen: View {
    let target: TerminalTarget
    @State private var session: TerminalSession
    @Environment(\.dismiss) private var dismiss
    @Environment(\.scenePhase) private var phase

    init(target: TerminalTarget, host: Host) {
        self.target = target
        _session = State(initialValue: TerminalSession(host: host, session: target.session, block: target.block))
    }

    var body: some View {
        TerminalRepresentable(session: session)
            .background(Color.black)
            .overlay { status }
            .safeAreaInset(edge: .top, spacing: 0) {
                HStack(spacing: 12) {
                    Button { dismiss() } label: {
                        Image(systemName: "chevron.left").font(.body.weight(.semibold)).frame(width: 36, height: 36)
                    }
                    .accessibilityLabel("Back")
                    .glass(in: Circle())
                    Text(target.title).font(.subheadline.weight(.semibold)).lineLimit(1).foregroundStyle(.secondary)
                    Spacer()
                }
                .padding(.horizontal, 12)
                .padding(.bottom, 6)
            }
            .toolbar(.hidden, for: .navigationBar)
            .onAppear { session.start() }
            .onDisappear { session.close() }
            .onChange(of: phase) { _, p in
                if p == .background { session.suspend() }
                if p == .active { session.resume() }
            }
    }

    @ViewBuilder private var status: some View {
        switch session.state {
        case .connecting:
            ProgressView().tint(.white)
        case .live:
            EmptyView()
        case .exited(let code):
            label(String(localized: "The program exited with code \(code)."))
        case .failed(let why):
            VStack(spacing: 12) {
                label(why)
                Button("Retry") { session.start() }.buttonStyle(.borderedProminent)
            }
        }
    }

    private func label(_ s: String) -> some View {
        Text(s).font(.callout).padding(12).glass(in: RoundedRectangle(cornerRadius: 14))
    }
}

private struct TerminalRepresentable: UIViewRepresentable {
    let session: TerminalSession

    func makeUIView(context: Context) -> TerminalView {
        let v = TerminalView(session: session)
        DispatchQueue.main.async { v.becomeFirstResponder() }
        return v
    }

    func updateUIView(_ v: TerminalView, context: Context) {
        _ = session.frame
        v.setNeedsDisplay()
    }
}
