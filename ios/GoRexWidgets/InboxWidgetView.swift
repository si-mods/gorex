import SwiftUI
import WidgetKit

/// What a widget draws at a time: the snapshot, nil for one it can't read,
/// and whether it is old enough to show as stale.
nonisolated struct InboxEntry: TimelineEntry {
    let date: Date
    let snapshot: WidgetSnapshot?
    var stale = false

    /// How long a snapshot shows as current.
    static let fresh: TimeInterval = 10 * 60

    /// The entries of a snapshot: now, and from ten minutes after it was
    /// written, stale. Only the app asks for more.
    static func timeline(_ s: WidgetSnapshot?, now: Date) -> [InboxEntry] {
        guard let s else { return [InboxEntry(date: now, snapshot: nil)] }
        let stale = s.written.addingTimeInterval(fresh)
        if stale <= now { return [InboxEntry(date: now, snapshot: s, stale: true)] }
        return [InboxEntry(date: now, snapshot: s), InboxEntry(date: stale, snapshot: s, stale: true)]
    }
}

/// The Inbox widget in its family; the background opens the inbox.
struct InboxWidgetView: View {
    let entry: InboxEntry
    @Environment(\.widgetFamily) private var family

    var body: some View {
        InboxContent(entry: entry, family: family)
            .widgetURL(WidgetLink.inbox.url)
            .containerBackground(.background, for: .widget)
    }
}

/// The widget's face for a family, apart from WidgetKit, so that a test
/// can draw each one.
struct InboxContent: View {
    let entry: InboxEntry
    let family: WidgetFamily

    var body: some View {
        if let s = entry.snapshot {
            switch family {
            case .accessoryCircular: Circular(s: s, stale: entry.stale)
            case .accessoryRectangular: Rectangular(s: s, stale: entry.stale)
            case .accessoryInline: Text(entry.stale ? String(localized: "Open Rex Go to update") : s.counts(max: 2) ?? String(localized: "Nothing needs you"))
            case .systemSmall: Small(s: s, stale: entry.stale)
            case .systemLarge: Rows(s: s, stale: entry.stale, max: 7, grouped: true)
            default: Rows(s: s, stale: entry.stale, max: 3, grouped: false)
            }
        } else {
            OpenApp(family: family)
        }
    }
}

extension WidgetSnapshot {
    /// The counts of what needs the user and what works, the most urgent
    /// first, at most max of them; nil for none.
    func counts(max: Int = 4) -> String? {
        let parts = Self.states.compactMap { s -> String? in
            let n = count(s)
            guard n > 0 else { return nil }
            switch s {
            case "blocked": return String(localized: "\(n) waiting")
            case "error": return String(localized: "\(n) failed")
            case "done": return String(localized: "\(n) done")
            default: return String(localized: "\(n) working")
            }
        }
        return parts.isEmpty ? nil : parts.prefix(max).joined(separator: " · ")
    }

    /// The hosts that were not online.
    var away: [Host] { hosts.filter { !$0.online } }
}

/// The names the inbox gives its states, as StatusInbox's.
enum StateName {
    static func of(_ state: String) -> LocalizedStringKey {
        ["blocked": "Needs Input", "error": "Failed", "done": "Done"][state] ?? "Working"
    }
}

/// How long ago, moving as the minutes go without the widget drawn again.
private func ago(_ t: Date) -> Text {
    Text(.currentDate, format: .reference(to: t, allowedFields: [.minute, .hour, .day], maxFieldCount: 1))
}

/// When the snapshot was taken, or, once stale, what brings it up to date.
private struct Freshness: View {
    let s: WidgetSnapshot
    let stale: Bool

    var body: some View {
        Group {
            if stale {
                Text("Open Rex Go to update")
            } else {
                Text("Updated \(ago(s.written))")
            }
        }
        .font(.caption2)
        .foregroundStyle(.secondary)
        .lineLimit(1)
    }
}

/// The dot of a state, as the status inbox's row: the progress while
/// working with one, the dots of work without; still, as a widget does
/// not move.
private struct StateMark: View {
    let item: WidgetSnapshot.Item

    var body: some View {
        switch item.state {
        case "blocked", "done": Circle().fill(Color.attention).frame(width: 8, height: 8)
        case "error": Circle().fill(.red).frame(width: 8, height: 8)
        default:
            if let p = item.progress {
                ProgressRing(progress: p)
            } else {
                HStack(spacing: 3) {
                    ForEach(0..<3, id: \.self) { _ in Circle().fill(.secondary).frame(width: 3.5, height: 3.5) }
                }
            }
        }
    }
}

private struct Tile: View {
    let app: String?
    var scale: CGFloat = 1.5

    var body: some View {
        ProgramTile(program: .of(app), scale: scale)
            .frame(width: 21 * scale, height: 16 * scale)
    }
}

/// A row of the inbox: its program's tile, the heading and the detail, its
/// state and how long ago; a tap opens its terminal.
private struct Row: View {
    let host: WidgetSnapshot.Host
    let item: WidgetSnapshot.Item
    /// Whether the row names its host, as there are several.
    let named: Bool

    var body: some View {
        Link(destination: WidgetLink.open(host: host.id, session: item.session, block: item.block).url) {
            HStack(spacing: 10) {
                Tile(app: item.app)
                VStack(alignment: .leading, spacing: 1) {
                    HStack(spacing: 6) {
                        Text(item.heading.isEmpty ? item.window : item.heading)
                            .font(.subheadline.weight(.semibold))
                            .privacySensitive()
                        Spacer(minLength: 4)
                        StateMark(item: item)
                    }
                    HStack(spacing: 0) {
                        if named { Text(verbatim: host.name + " · ") }
                        Text(item.detail.isEmpty ? item.window : item.detail).privacySensitive()
                        Spacer(minLength: 6)
                        if let t = item.updated { ago(t).foregroundStyle(.tertiary) }
                    }
                    .font(.caption)
                    .foregroundStyle(.secondary)
                }
                .lineLimit(1)
            }
        }
    }
}

/// The hosts that were not online, a line each, as the status inbox lists
/// them under its rows.
private struct Away: View {
    let hosts: [WidgetSnapshot.Host]

    var body: some View {
        ForEach(hosts) { h in
            Label {
                Text(verbatim: h.name + " · " + String(localized: "Offline"))
            } icon: {
                Image(systemName: "wifi.slash")
            }
            .font(.caption2)
            .foregroundStyle(.secondary)
            .lineLimit(1)
        }
    }
}

/// Medium and large: the first rows, the large one under each state's
/// name, then how many more, the hosts away and the time.
private struct Rows: View {
    let s: WidgetSnapshot
    let stale: Bool
    let max: Int
    let grouped: Bool

    var body: some View {
        let all = s.items
        let shown = Array(all.prefix(max))
        let named = s.hosts.count > 1
        VStack(alignment: .leading, spacing: 8) {
            // Stale, the rows dim; what brings them up to date does not.
            Group {
                if shown.isEmpty {
                    Spacer(minLength: 0)
                    Label("Nothing needs you", systemImage: "checkmark.circle")
                        .font(.subheadline.weight(.semibold))
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity)
                } else if grouped {
                    ForEach(Self.groups(shown), id: \.state) { g in
                        Text(StateName.of(g.state)).font(.caption.weight(.semibold)).foregroundStyle(.gray)
                        ForEach(Array(g.rows.enumerated()), id: \.offset) { _, r in Row(host: r.host, item: r.item, named: named) }
                    }
                } else {
                    ForEach(Array(shown.enumerated()), id: \.offset) { _, r in Row(host: r.host, item: r.item, named: named) }
                }
            }
            .opacity(stale ? 0.5 : 1)
            Spacer(minLength: 0)
            if grouped { Away(hosts: s.away) }
            HStack(spacing: 6) {
                if all.count > shown.count { Text("+\(all.count - shown.count) more").font(.caption2.weight(.semibold)).foregroundStyle(.secondary) }
                if !grouped, let h = s.away.first {
                    Label {
                        Text(verbatim: h.name + " · " + String(localized: "Offline") + (s.away.count > 1 ? " +\(s.away.count - 1)" : ""))
                    } icon: {
                        Image(systemName: "wifi.slash")
                    }
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                }
                Spacer(minLength: 4)
                Freshness(s: s, stale: stale)
            }
        }
    }

    typealias Pair = (host: WidgetSnapshot.Host, item: WidgetSnapshot.Item)

    /// The rows by state, as Inbox.groups groups them.
    static func groups(_ rows: [Pair]) -> [(state: String, rows: [Pair])] {
        var out: [(state: String, rows: [Pair])] = []
        for st in WidgetSnapshot.states {
            let some = rows.filter { $0.item.state == st }
            if !some.isEmpty { out.append((st, some)) }
        }
        return out
    }
}

/// Small: the most urgent item's tile and heading, and the counts.
private struct Small: View {
    let s: WidgetSnapshot
    let stale: Bool

    var body: some View {
        let top = s.items.first
        VStack(alignment: .leading, spacing: 4) {
            Group {
                if let top {
                    HStack(alignment: .top) {
                        Tile(app: top.item.app, scale: 2)
                        Spacer()
                        StateMark(item: top.item)
                    }
                    Spacer(minLength: 4)
                    Text(StateName.of(top.item.state)).font(.caption.weight(.semibold)).foregroundStyle(tint(top.item.state))
                    Text(top.item.heading.isEmpty ? top.item.window : top.item.heading)
                        .font(.headline)
                        .lineLimit(2)
                        .privacySensitive()
                    if let c = s.counts(max: 2) { Text(c).font(.caption2).foregroundStyle(.secondary).lineLimit(1) }
                } else {
                    Image(systemName: "checkmark.circle").font(.title2).foregroundStyle(.secondary)
                    Spacer(minLength: 4)
                    Text("Nothing needs you").font(.headline).lineLimit(2)
                    if let h = s.away.first { Text(verbatim: h.name + " · " + String(localized: "Offline")).font(.caption2).foregroundStyle(.secondary).lineLimit(1) }
                }
            }
            .opacity(stale ? 0.5 : 1)
            Freshness(s: s, stale: stale)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    /// The color of a state's name, as its dot's.
    private func tint(_ state: String) -> Color {
        switch state {
        case "error": .red
        case "working": .secondary
        default: .attention
        }
    }
}

/// The Lock Screen's circle: how many wait for the user, else how many work.
private struct Circular: View {
    let s: WidgetSnapshot
    let stale: Bool

    var body: some View {
        let waiting = s.count("blocked")
        let working = s.count("working")
        ZStack {
            AccessoryWidgetBackground()
            VStack(spacing: 0) {
                Image(systemName: waiting > 0 ? "exclamationmark.bubble.fill" : working > 0 ? "ellipsis" : "checkmark")
                    .font(.system(size: 13, weight: .semibold))
                if waiting > 0 || working > 0 {
                    Text(verbatim: "\(waiting > 0 ? waiting : working)").font(.system(size: 20, weight: .semibold, design: .rounded))
                }
            }
        }
        .opacity(stale ? 0.5 : 1)
    }
}

/// The Lock Screen's rectangle: the most urgent item's state and heading.
private struct Rectangular: View {
    let s: WidgetSnapshot
    let stale: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            if let top = s.items.first {
                Text(StateName.of(top.item.state)).font(.headline)
                Text(top.item.heading.isEmpty ? top.item.window : top.item.heading).privacySensitive()
                if stale {
                    Text("Open Rex Go to update").foregroundStyle(.secondary)
                } else if let c = s.counts(max: 2) {
                    Text(c).foregroundStyle(.secondary)
                }
            } else {
                Text("Nothing needs you").font(.headline)
                if stale { Text("Open Rex Go to update").foregroundStyle(.secondary) }
            }
        }
        .lineLimit(1)
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// A snapshot the widget can't read: only the way to the app.
private struct OpenApp: View {
    let family: WidgetFamily

    var body: some View {
        switch family {
        case .accessoryInline:
            Text("Open Rex Go")
        case .accessoryCircular:
            ZStack {
                AccessoryWidgetBackground()
                Image(systemName: "terminal").font(.title3)
            }
        default:
            VStack(spacing: 6) {
                Image(systemName: "terminal").font(.title2)
                Text("Open Rex Go").font(.headline)
            }
            .foregroundStyle(.secondary)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }
}

extension WidgetSnapshot {
    /// Made-up hosts and agents, for the widget gallery and screenshots.
    nonisolated static func sample(now: Date) -> WidgetSnapshot {
        func item(_ block: String, _ window: String, _ app: String, _ state: String, _ heading: String, _ detail: String,
                  ago: TimeInterval, progress: Int? = nil) -> Item {
            Item(session: "s1", block: block, window: window, app: app, state: state, kind: nil, progress: progress,
                 heading: heading, detail: detail, updated: now.addingTimeInterval(-ago))
        }
        return WidgetSnapshot(written: now, hosts: [
            Host(id: UUID(uuidString: "00000000-0000-4000-8000-00000000a11c")!, name: "alice-laptop", icon: "laptopcomputer", online: true, items: [
                item("b1", "api", "claude", "blocked", "Edit main.go?", "Claude wants to edit 2 files", ago: 40),
                item("b2", "web", "codex", "error", "Tests failed", "3 of 120 tests failed", ago: 300),
                item("b3", "docs", "claude", "done", "Wrote the release notes", "CHANGELOG.md updated", ago: 900),
                item("b4", "build", "cargo", "working", "Building", "Compiling 48 crates", ago: 120, progress: 65),
                item("b5", "data", "python", "working", "Training", "Epoch 3 of 10", ago: 600),
            ], windows: []),
            Host(id: UUID(uuidString: "00000000-0000-4000-8000-00000000b0b0")!, name: "bob-desktop", icon: "desktopcomputer", online: false,
                 items: [], windows: []),
        ])
    }
}
