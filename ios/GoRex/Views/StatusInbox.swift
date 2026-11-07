import SwiftUI

/// The windows of every host by what their programs need, as Rex's iPhone
/// app lists them with the eye on: those waiting for the user first, then
/// failed, done and working ones.
struct StatusInbox: View {
    let query: String
    @Binding var collapsed: Set<String>
    let open: (TerminalTarget) -> Void
    @Environment(HostStore.self) private var store

    var body: some View {
        let groups = Inbox.groups(store.hosts.flatMap(\.inbox).filter { $0.matches(query) })
        // Hosts not online may need the user too: the inbox can't tell.
        let away = store.hosts.filter { $0.status != .online }
        if groups.isEmpty && away.isEmpty && !store.hosts.isEmpty {
            Text("Nothing needs you").foregroundStyle(.secondary).frame(maxWidth: .infinity).padding(.top, 40)
        }
        // The times go on as the minutes do.
        TimelineView(.everyMinute) { tl in
            VStack(alignment: .leading, spacing: 30) {
                ForEach(groups, id: \.state) { g in
                    section(g.state, g.items, now: tl.date)
                }
                if !away.isEmpty {
                    VStack(alignment: .leading, spacing: 8) {
                        ForEach(away) { h in
                            Label {
                                Text(verbatim: h.config.name + " · " + (h.status == .connecting ? String(localized: "Connecting…") : String(localized: "Offline")))
                            } icon: {
                                Image(systemName: "wifi.slash")
                            }
                        }
                    }
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                }
            }
        }
    }

    @ViewBuilder private func section(_ state: String, _ items: [InboxItem], now: Date) -> some View {
        let key = "inbox." + state
        let isCollapsed = collapsed.contains(key)
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text(Self.names[state] ?? "").font(.title3.weight(.semibold)).foregroundStyle(.gray)
                Spacer()
                Button {
                    withAnimation(.snappy) { if isCollapsed { collapsed.remove(key) } else { collapsed.insert(key) } }
                } label: {
                    Image(systemName: "chevron.down").rotationEffect(.degrees(isCollapsed ? -90 : 0)).frame(width: 36, height: 36).contentShape(.circle)
                }
                .accessibilityLabel(isCollapsed ? "Expand" : "Collapse")
                .buttonStyle(.plain)
                .foregroundStyle(.secondary)
                .font(.body.weight(.semibold))
            }
            if !isCollapsed {
                VStack(spacing: 12) {
                    ForEach(items) { item in
                        Button {
                            open(TerminalTarget(host: item.host, session: item.session, block: item.block, title: item.title))
                        } label: {
                            StatusRow(item: item, now: now)
                        }
                        .buttonStyle(.plain)
                        .hoverEffect(.highlight)
                    }
                }
            }
        }
    }

    static let names: [String: LocalizedStringKey] = [
        "blocked": "Needs Input", "error": "Failed", "done": "Done", "working": "Working",
    ]
}

/// A window in the inbox: its program's tile, what its program tells, and
/// how long ago.
private struct StatusRow: View {
    let item: InboxItem
    let now: Date

    var body: some View {
        HStack(spacing: 14) {
            ProgramTile(program: tile)
                .frame(width: 21 * ProgramTiles.scale, height: 16 * ProgramTiles.scale)
                .scaleEffect(1.4)
                .frame(width: 44, height: 44)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 8) {
                    Text(item.heading.isEmpty ? " " : item.heading).font(.body.weight(.medium)).lineLimit(1)
                    Spacer(minLength: 4)
                    mark
                }
                HStack(spacing: 8) {
                    Text(item.detail.isEmpty ? " " : item.detail).foregroundStyle(.secondary).lineLimit(1)
                    Spacer(minLength: 4)
                    if let t = item.status.updated { Text(Inbox.ago(t, now: now)).foregroundStyle(.tertiary) }
                }
                .font(.subheadline)
            }
            Image(systemName: "chevron.right").font(.footnote.weight(.semibold)).foregroundStyle(.gray)
        }
        .truncationMode(.tail)
        .padding(.leading, 14)
        .padding(.trailing, 18)
        .frame(minHeight: 70)
        .background(.fill.quaternary, in: RoundedRectangle(cornerRadius: 28, style: .continuous))
        .contentShape(RoundedRectangle(cornerRadius: 28, style: .continuous))
    }

    /// The program the record names when it is one known, else the block's.
    private var tile: Program {
        if let app = item.status.app, let p = Program.programs[app.lowercased()] { return p }
        return .of(item.program)
    }

    /// As the desktop's dot: the progress or the dots of work while
    /// working, else the dot of attention, red for an error.
    @ViewBuilder private var mark: some View {
        switch item.status.state {
        case "working":
            if let p = item.status.progress {
                Circle().trim(from: 0, to: CGFloat(p) / 100)
                    .stroke(.secondary, style: StrokeStyle(lineWidth: 2, lineCap: .round))
                    .rotationEffect(.degrees(-90))
                    .background(Circle().stroke(.quaternary, lineWidth: 2))
                    .frame(width: 12, height: 12)
                    .accessibilityLabel(Text(verbatim: "\(p)%"))
            } else {
                Image(systemName: "ellipsis").font(.footnote.weight(.bold)).foregroundStyle(.secondary).accessibilityLabel("Working")
            }
        case "error":
            Circle().fill(.red).frame(width: 8, height: 8).accessibilityLabel("Failed")
        default:
            Circle().fill(Color.attention).frame(width: 8, height: 8)
                .accessibilityLabel(item.status.state == "blocked" ? "Needs Input" : "Done")
        }
    }
}
