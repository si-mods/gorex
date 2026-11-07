import SwiftUI

/// The session of the terminal open, as its title opens it: the session's
/// windows to go to, a new one, and the host's other sessions.
struct SessionSheet: View {
    let target: TerminalTarget
    let host: Host
    let go: (TerminalTarget?) -> Void
    let newTab: () -> Void
    @Environment(\.dismiss) private var dismiss
    /// How tall the sheet's content is, for a sheet as tall as it.
    @State private var height: CGFloat = 0

    var body: some View {
        let session = host.sessions.first { $0.id == target.session }
        NavigationStack {
            ScrollView {
                VStack(spacing: 16) {
                    HStack(alignment: .top) {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(host.config.name).font(.subheadline).foregroundStyle(.secondary)
                            Text(session?.label ?? "").font(.title2.bold())
                        }
                        .lineLimit(1)
                        Spacer()
                        Button { dismiss() } label: {
                            Image(systemName: "xmark").font(.title3.weight(.medium)).frame(width: 48, height: 48)
                        }
                        .glassEffect(.regular.interactive(), in: .circle)
                        .accessibilityLabel("Close")
                    }
                    .padding(.bottom, 8)
                    if let session { windows(session) }
                    Button {
                        dismiss()
                        newTab()
                    } label: {
                        Row(icon: "plus", title: Text("New Tab"), divider: false) {}
                    }
                    .background(Color.card, in: RoundedRectangle(cornerRadius: 22, style: .continuous))
                    NavigationLink {
                        sessions
                    } label: {
                        Row(icon: "rectangle.stack", title: Text("Switch Session"), divider: false) {
                            Image(systemName: "chevron.right").font(.footnote.weight(.semibold)).foregroundStyle(.gray)
                        }
                    }
                    .background(Color.card, in: RoundedRectangle(cornerRadius: 22, style: .continuous))
                }
                .padding(20)
                .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { height = $0 }
            }
            .toolbar(.hidden, for: .navigationBar)
        }
        .buttonStyle(.plain)
        .presentationDetents([height > 0 ? .height(height) : .medium, .large])
        .presentationDragIndicator(.hidden)
    }

    /// The session's windows, as its card in the list, with a check on the
    /// one open.
    private func windows(_ s: Host.Session) -> some View {
        let windows = (s.view?.windows ?? []).compactMap { w in w.block.map { (block: $0, window: w) } }
        let tiles = ProgramTiles.width(behind: windows.map(\.window.back.count).max() ?? 0)
        return VStack(spacing: 0) {
            ForEach(Array(windows.enumerated()), id: \.element.window.window_id) { i, w in
                let open = w.window.holds(target.block)
                Button {
                    dismiss()
                    // The one open stays, with its scrollback.
                    if !open { go(TerminalTarget(host: host.id, session: s.id, block: w.block, title: host.title(w.window))) }
                } label: {
                    WindowRow(host: host, block: w.block, window: w.window, tiles: tiles, divider: i < windows.count - 1) {
                        if open { Image(systemName: "checkmark").font(.body.weight(.semibold)) }
                    }
                }
            }
        }
        .background(Color.card, in: RoundedRectangle(cornerRadius: 22, style: .continuous))
    }

    /// The host's sessions; another one goes to its first window.
    private var sessions: some View {
        ScrollView {
            VStack(spacing: 0) {
                ForEach(Array(host.sessions.enumerated()), id: \.element.id) { i, s in
                    let first = s.view?.windows?.first { $0.block != nil }
                    Button {
                        dismiss()
                        guard s.id != target.session, let first, let b = first.block else { return }
                        go(TerminalTarget(host: host.id, session: s.id, block: b, title: host.title(first)))
                    } label: {
                        Row(title: Text(s.label), divider: i < host.sessions.count - 1) {
                            if s.id == target.session { Image(systemName: "checkmark").font(.body.weight(.semibold)) }
                        }
                    }
                    .disabled(first == nil)
                }
            }
            .background(Color.card, in: RoundedRectangle(cornerRadius: 22, style: .continuous))
            .padding(20)
        }
        .navigationTitle("Switch Session")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar(.visible, for: .navigationBar)
    }
}

/// A row of the sheet's cards: an icon, a title, and a mark at the end.
private struct Row<Mark: View>: View {
    var icon: String?
    let title: Text
    let divider: Bool
    @ViewBuilder let mark: Mark

    var body: some View {
        HStack(spacing: 14) {
            if let icon { Image(systemName: icon).font(.title3).frame(width: 28) }
            title.font(.body.weight(.medium)).lineLimit(1)
            Spacer(minLength: 8)
            mark
        }
        .padding(.horizontal, 16)
        .frame(minHeight: 50)
        .contentShape(Rectangle())
        .overlay(alignment: .bottom) {
            if divider { Rectangle().fill(Color.primary.opacity(0.12)).frame(height: 1 / 3).padding(.leading, 16) }
        }
    }
}
