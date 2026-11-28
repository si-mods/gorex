import SwiftUI

/// Bottom-bar toggle for the Needs You inbox. Kept in its own view so
/// status updates redraw only the badge, without reading block titles.
struct InboxToolbarButton: View {
    let inbox: Bool
    let action: () -> Void
    @Environment(HostStore.self) private var store

    var body: some View {
        let summary = Inbox.summary(store.hosts.flatMap(\.inbox))
        Button(action: action) {
            Image(systemName: inbox ? "tray.fill" : "tray")
                .overlay(alignment: .topTrailing) {
                    if let text = Inbox.badgeText(summary.attentionCount) {
                        Text(text)
                            .font(.system(size: 10, weight: .bold, design: .rounded))
                            .monospacedDigit()
                            .foregroundStyle(summary.hasError ? Color.white : Color.black)
                            .padding(.horizontal, 4)
                            .frame(minWidth: 16, minHeight: 16)
                            .background(summary.hasError ? Color.red : Color.attention, in: Capsule())
                            .offset(x: 7, y: -5)
                            .accessibilityHidden(true)
                    }
                }
        }
        .accessibilityLabel(inbox ? Text("Show All Tabs") : Text("Needs You"))
        .accessibilityValue(summary.attentionCount > 0 ? Text("\(summary.attentionCount) needs you") : Text(""))
    }
}

/// The windows of every host by what their programs need: actionable items
/// (blocked, failed, unseen done) first, then background working tasks.
struct StatusInbox: View {
    let query: String
    @Binding var collapsed: Set<String>
    /// What the rows move between the lists in.
    let places: Namespace.ID
    let open: (TerminalTarget) -> Void
    @Environment(HostStore.self) private var store

    var body: some View {
        let items = store.hosts.flatMap { $0.inbox(matching: query) }
        let sections = Inbox.sections(items)
        let summary = Inbox.summary(items)
        let multiHost = store.hosts.count > 1
        // Hosts not online may need the user too: the inbox can't tell.
        let away = store.hosts.filter { $0.status != .online }

        if !store.hosts.isEmpty {
            header(summary: summary)
        }

        if sections.attention.isEmpty && sections.working.isEmpty && away.isEmpty && !store.hosts.isEmpty {
            if query.isEmpty {
                // All done: nothing the search took away.
                Label("Nothing needs you", systemImage: "checkmark.circle")
                    .symbolRenderingMode(.hierarchical)
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity)
                    .padding(.top, 32)
            } else {
                ContentUnavailableView.search(text: query)
                    .frame(maxWidth: .infinity)
                    .padding(.top, 32)
            }
        }
        // The times go on as the minutes do.
        TimelineView(.everyMinute) { tl in
            VStack(alignment: .leading, spacing: 24) {
                if sections.attention.isEmpty && !sections.working.isEmpty && query.isEmpty {
                    caughtUpBanner
                }
                ForEach(sections.attention, id: \.state) { g in
                    section(g.state, g.items, now: tl.date, secondary: false, multiHost: multiHost)
                }
                if !sections.working.isEmpty {
                    if !sections.attention.isEmpty {
                        Divider().opacity(0.5)
                    }
                    section("working", sections.working, now: tl.date, secondary: true, multiHost: multiHost)
                }
                if !away.isEmpty {
                    VStack(alignment: .leading, spacing: 8) {
                        ForEach(away) { h in
                            Label {
                                Text(verbatim: h.config.name + " · " + (h.status == .connecting ? String(localized: "Connecting…") : String(localized: "Offline")))
                            } icon: {
                                Image(systemName: "wifi.slash").symbolRenderingMode(.hierarchical)
                            }
                        }
                    }
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                }
            }
        }
    }

    private func header(summary: Inbox.Summary) -> some View {
        HStack(spacing: 8) {
            Text("Needs You")
                .font(.title2.weight(.bold))
            if let text = Inbox.badgeText(summary.attentionCount) {
                Text(text)
                    .font(.caption.weight(.bold))
                    .monospacedDigit()
                    .foregroundStyle(summary.hasError ? Color.white : Color.black)
                    .padding(.horizontal, 7)
                    .padding(.vertical, 2)
                    .background(summary.hasError ? Color.red : Color.attention, in: Capsule())
            }
            Spacer()
        }
    }

    private var caughtUpBanner: some View {
        HStack(spacing: 10) {
            Image(systemName: "checkmark.circle.fill")
                .symbolRenderingMode(.hierarchical)
                .foregroundStyle(.secondary)
            Text("Nothing needs you")
                .font(.subheadline.weight(.medium))
                .foregroundStyle(.secondary)
            Spacer()
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
        .background(.fill.quinary, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
    }

    @ViewBuilder private func section(_ state: String, _ items: [InboxItem], now: Date, secondary: Bool, multiHost: Bool) -> some View {
        let key = "inbox." + state
        let isCollapsed = collapsed.contains(key)
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 8) {
                if let tint = Self.sectionTint(state) {
                    Circle()
                        .fill(tint)
                        .frame(width: 8, height: 8)
                }
                Text(Self.names[state] ?? "")
                    .font(secondary ? .headline.weight(.semibold) : .title3.weight(.semibold))
                    .foregroundStyle(secondary ? .secondary : .primary)
                Text(verbatim: "\(items.count)")
                    .font(.subheadline.weight(.medium))
                    .monospacedDigit()
                    .foregroundStyle(.tertiary)
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
                        if let host = store.hosts.first(where: { $0.id == item.host }) {
                            Button {
                                open(TerminalTarget(host: item.host, session: item.session, block: item.block, title: host.title(item.window), explicit: true))
                            } label: {
                                StatusRow(host: host, item: item, now: now, secondary: secondary, showHost: multiHost)
                            }
                            .buttonStyle(.plain)
                            .hoverEffect(.highlight)
                            .modifier(MatchedPlace(place: Self.place(item), own: .inbox(item.id), namespace: places))
                        }
                    }
                }
            }
        }
    }

    private static func sectionTint(_ state: String) -> Color? {
        switch state {
        case "blocked": Color.attention
        case "error": .red
        case "done": Color.done
        default: nil
        }
    }

    private static func place(_ item: InboxItem) -> RowPlace {
        RowPlace(host: item.host, session: item.session, window: item.window.window_id)
    }

    static let names: [String: LocalizedStringKey] = [
        "blocked": "Needs Input", "error": "Failed", "done": "Done", "working": "Working",
    ]
}

/// A window in the inbox: its program's tile, what its program tells, and
/// how long ago. It reads its own block's title and program, so a title
/// that changes redraws this row only.
private struct StatusRow: View {
    let host: Host
    let item: InboxItem
    let now: Date
    let secondary: Bool
    let showHost: Bool

    private var title: String { host.title(item.window) }
    private var detail: String { item.detail(title: title) }

    var body: some View {
        let shape = RoundedRectangle(cornerRadius: 28, style: .continuous)
        HStack(spacing: 14) {
            ProgramTile(program: tile)
                .frame(width: 21 * ProgramTiles.scale, height: 16 * ProgramTiles.scale)
                .scaleEffect(1.4)
                .tileBounce(state: item.status.state)
                .frame(width: 44, height: 44)
            if detail.isEmpty {
                HStack(spacing: 8) {
                    headingText
                    Spacer(minLength: 4)
                    AgentWindowBadge(host: host, block: item.block)
                    VStack(alignment: .trailing, spacing: 4) {
                        mark
                        metaText
                    }
                }
            } else {
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 8) {
                        headingText
                        Spacer(minLength: 4)
                        AgentWindowBadge(host: host, block: item.block)
                        mark
                    }
                    HStack(spacing: 8) {
                        Text(detail.asText).foregroundStyle(.secondary).lineLimit(1)
                        Spacer(minLength: 4)
                        metaText
                    }
                    .font(.subheadline)
                }
            }
            Image(systemName: "chevron.right").font(.footnote.weight(.semibold)).foregroundStyle(.gray)
        }
        .truncationMode(.tail)
        .padding(.leading, 14)
        .padding(.trailing, 18)
        .frame(minHeight: secondary ? 64 : 70)
        .background(secondary ? AnyShapeStyle(.fill.quinary) : AnyShapeStyle(.fill.quaternary), in: shape)
        .overlay {
            if let stroke = borderColor {
                shape.strokeBorder(stroke, lineWidth: 1)
            }
        }
        .contentShape(shape)
    }

    private var borderColor: Color? {
        switch item.status.state {
        case "blocked": Color.attention.opacity(0.35)
        case "error": Color.red.opacity(0.35)
        default: nil
        }
    }

    private var headingText: some View {
        let heading = item.heading(title: title)
        let agentActive = host.agentState(for: item.block, at: now) != nil
        return Text(heading.isEmpty ? " " : heading.asText)
            .font(.body.weight(.medium))
            .foregroundStyle(secondary ? .secondary : .primary)
            .lineLimit(1)
            .accessibilityLabel(Text(AgentHighlight.accessibilityLabel(for: heading.asText, active: agentActive)))
    }

    @ViewBuilder private var metaText: some View {
        let ago = item.status.updated.map { Inbox.ago($0, now: now) }
        let parts = [showHost ? host.config.name : nil, ago].compactMap { $0 }.filter { !$0.isEmpty }
        if !parts.isEmpty {
            Text(verbatim: parts.joined(separator: " · "))
                .font(.subheadline)
                .foregroundStyle(.tertiary)
                .lineLimit(1)
        }
    }

    /// The program the record names when it is one known, else the block's.
    private var tile: Program {
        if let app = item.status.app, let p = Program.programs[app.lowercased()] { return p }
        return .of(host.blocks[item.block]?.program)
    }

    /// As the desktop's dot: the progress or the dots of work while
    /// working, else the dot of attention, red for an error.
    @ViewBuilder private var mark: some View {
        ProgramStatusMark(state: item.status.state, progress: item.status.progress, dotSize: 8, showDone: true)
    }
}
