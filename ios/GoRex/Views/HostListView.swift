import SwiftUI

extension Color {
    static let page = Color(UIColor { $0.userInterfaceStyle == .dark
        ? UIColor(red: 0x22 / 255, green: 0x25 / 255, blue: 0x2F / 255, alpha: 1) : .systemGroupedBackground })
    static let card = Color(UIColor { $0.userInterfaceStyle == .dark
        ? UIColor(red: 0x36 / 255, green: 0x3A / 255, blue: 0x47 / 255, alpha: 1) : .secondarySystemGroupedBackground })
}

/// Every host, its sessions, and their windows, as Rex's iPhone app lists
/// them. The eye hides idle windows: those whose program is only the shell.
struct HostListView: View {
    @Environment(HostStore.self) private var store
    @State private var path: [TerminalTarget] = []
    @State private var collapsed: Set<String> = []
    @State private var hideIdle = false
    @State private var query = ""
    @State private var settings = false

    var body: some View {
        NavigationStack(path: $path) {
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 30) {
                    if store.hosts.isEmpty {
                        Text("No hosts yet. Add one in Settings.").foregroundStyle(.secondary).padding(.top, 40)
                    }
                    ForEach(store.hosts) { host in
                        HostSection(host: host, collapsed: $collapsed, filter: filter, open: { path.append($0) })
                    }
                }
                .padding(.horizontal, 20)
                .padding(.top, 12)
                .padding(.bottom, 20)
            }
            .background(Color.page)
            .scrollEdgeEffectStyle(.soft, for: .all)
            .toolbar(.hidden, for: .navigationBar)
            .toolbar {
                ToolbarItemGroup(placement: .bottomBar) {
                    Button("Settings", systemImage: "gearshape") { settings = true }
                    Button(hideIdle ? "Show Idle Windows" : "Hide Idle Windows", systemImage: hideIdle ? "eye.slash" : "eye") {
                        hideIdle.toggle()
                    }
                }
                ToolbarSpacer(.flexible, placement: .bottomBar)
                DefaultToolbarItem(kind: .search, placement: .bottomBar)
            }
            .searchable(text: $query, prompt: "Search windows")
            .searchToolbarBehavior(.minimize)
            .textInputAutocapitalization(.never)
            .autocorrectionDisabled()
            .navigationDestination(for: TerminalTarget.self) { t in
                if let host = store.host(t.host) { TerminalScreen(target: t, host: host) }
            }
            .sheet(isPresented: $settings) { SettingsView() }
        }
    }

    /// Whether a window's row shows, by the search and the eye.
    private func filter(_ title: String, _ program: String?) -> Bool {
        if hideIdle, Program.of(program).shell { return false }
        return query.isEmpty || title.localizedCaseInsensitiveContains(query)
    }
}

private struct HostSection: View {
    let host: Host
    @Binding var collapsed: Set<String>
    let filter: (String, String?) -> Bool
    let open: (TerminalTarget) -> Void
    @Environment(HostStore.self) private var store
    /// The sessions whose new window is on its way.
    @State private var opening: Set<String> = []
    /// What failed last, and why, as the server says it.
    @State private var failure: (title: LocalizedStringKey, why: String)?
    /// The window being renamed, and the name being typed.
    @State private var renaming: (session: String, window: String, title: String)?
    @State private var name = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            HStack(spacing: 10) {
                Image(systemName: host.config.desktop ? "desktopcomputer" : "laptopcomputer")
                    .font(.title2)
                Text(host.config.name).font(.system(size: 30, weight: .bold)).lineLimit(1)
                Spacer()
                Menu {
                    Button("Reconnect", systemImage: "arrow.clockwise") { host.reconnectIfNeeded() }
                    Button("Remove", systemImage: "trash", role: .destructive) { store.remove(host.id) }
                } label: {
                    Image(systemName: "ellipsis").font(.title3).frame(width: 36, height: 36)
                }
                .glassEffect(.regular.interactive(), in: .circle)
                .accessibilityLabel("Host Menu")
            }
            .foregroundStyle(.primary)
            switch host.status {
            case .connecting where host.sessions.isEmpty:
                Text("Connecting…").foregroundStyle(.secondary)
            case .offline(let why):
                Label(why.isEmpty ? String(localized: "Offline") : String(localized: "Offline: \(why)"), systemImage: "wifi.slash")
                    .foregroundStyle(.secondary)
            default:
                if host.sessions.isEmpty { Text("No sessions").foregroundStyle(.secondary) }
                ForEach(host.sessions) { s in session(s) }
            }
        }
        .alert(failure?.title ?? "", isPresented: Binding { failure != nil } set: { if !$0 { failure = nil } }) {
        } message: {
            Text(failure?.why ?? "")
        }
        .alert("Rename Tab", isPresented: Binding { renaming != nil } set: { if !$0 { renaming = nil } }, presenting: renaming) { r in
            TextField(r.title, text: $name)
            Button("Cancel", role: .cancel) {}
            Button("Save") {
                change("Can't Rename the Tab") { try await host.renameWindow(r.window, in: r.session, to: name) }
            }
        }
    }

    /// Runs a change to a session's windows, and tells why when it fails.
    private func change(_ title: LocalizedStringKey, _ body: @escaping () async throws -> Void) {
        Task {
            do { try await body() } catch { failure = (title, error.localizedDescription) }
        }
    }

    /// Opens a new window in a session, and goes to it.
    private func newWindow(_ s: Host.Session) {
        opening.insert(s.id)
        Task {
            defer { opening.remove(s.id) }
            do {
                if let b = try await host.newWindow(in: s.id) {
                    open(TerminalTarget(host: host.id, session: s.id, block: b, title: s.label))
                }
            } catch {
                failure = ("Can't Open a New Window", error.localizedDescription)
            }
        }
    }

    /// A window's menu, as the desktop's tab menu. It moves a window past
    /// the rows shown, and closes only those: the server's order also has
    /// windows the list leaves out.
    @ViewBuilder private func menu(_ s: Host.Session, _ w: SessionView.Window, title: String, shown: [String]) -> some View {
        let all = (s.view?.windows ?? []).map(\.window_id)
        let id = w.window_id
        let i = shown.firstIndex(of: id) ?? 0
        Button("Rename Tab…", systemImage: "pencil") {
            name = w.name ?? ""
            renaming = (s.id, id, title)
        }
        if w.name != nil {
            Button("Reset Name", systemImage: "arrow.uturn.backward") {
                change("Can't Rename the Tab") { try await host.renameWindow(id, in: s.id, to: "") }
            }
        }
        Divider()
        Button("Move Up", systemImage: "arrow.up") {
            change("Can't Move the Tab") { try await host.moveWindow(id, in: s.id, before: shown[i - 1]) }
        }
        .disabled(i <= 0)
        Button("Move Down", systemImage: "arrow.down") {
            // Just after the next row: before the window after it, if any.
            let after = all.firstIndex(of: shown[i + 1]).map { $0 + 1 } ?? all.count
            change("Can't Move the Tab") { try await host.moveWindow(id, in: s.id, before: after < all.count ? all[after] : nil) }
        }
        .disabled(i >= shown.count - 1)
        Divider()
        Button("New Tab", systemImage: "plus") { newWindow(s) }
        Button("Close Tab", systemImage: "xmark", role: .destructive) {
            change("Can't Close the Tab") { try await host.closeWindow(id, in: s.id) }
        }
        Button("Close Other Tabs", systemImage: "xmark.square", role: .destructive) {
            change("Can't Close the Tab") {
                for o in shown where o != id { try await host.closeWindow(o, in: s.id) }
            }
        }
        .disabled(shown.count < 2)
    }

    @ViewBuilder private func session(_ s: Host.Session) -> some View {
        let windows = (s.view?.windows ?? []).compactMap { w -> (String, String, String?, [String?], SessionView.Window)? in
            guard let b = w.block else { return nil }
            // As the desktop names a tab: its name when it has one, else its terminal's title.
            let title = w.name ?? host.titles[b].flatMap { $0.isEmpty ? nil : $0 } ?? w.label ?? ""
            let back = w.tiled.filter { $0 != b }.map { host.programs[$0] }
            return filter(title, host.programs[b]) ? (b, title, host.programs[b], back, w) : nil
        }
        // The room of the tiles of the window with the most, so that the titles line up.
        let tiles = ProgramTiles.width(behind: windows.map(\.3.count).max() ?? 0)
        let isCollapsed = collapsed.contains(s.id)
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text(s.label).font(.title3.weight(.semibold)).foregroundStyle(.gray)
                Spacer()
                GlassEffectContainer(spacing: 8) {
                    HStack(spacing: 8) {
                        Button {
                            newWindow(s)
                        } label: {
                            Group {
                                if opening.contains(s.id) { ProgressView() } else { Image(systemName: "plus") }
                            }
                            .frame(width: 32, height: 32)
                        }
                        .disabled(opening.contains(s.id))
                        .glassEffect(.regular.interactive(), in: .circle)
                        .accessibilityLabel("New Window")
                        Button {
                            withAnimation(.snappy) { if isCollapsed { collapsed.remove(s.id) } else { collapsed.insert(s.id) } }
                        } label: {
                            Image(systemName: "chevron.down").rotationEffect(.degrees(isCollapsed ? -90 : 0)).frame(width: 32, height: 32)
                        }
                        .glassEffect(.regular.interactive(), in: .circle)
                        .accessibilityLabel(isCollapsed ? "Expand" : "Collapse")
                    }
                }
            }
            .font(.body.weight(.semibold))
            .foregroundStyle(.primary)
            if !isCollapsed && !windows.isEmpty {
                VStack(spacing: 0) {
                    ForEach(Array(windows.enumerated()), id: \.element.0) { i, w in
                        Button {
                            open(TerminalTarget(host: host.id, session: s.id, block: w.0, title: w.1))
                        } label: {
                            WindowRow(title: w.1, program: w.2, back: w.3, tiles: tiles, divider: i < windows.count - 1)
                        }
                        .buttonStyle(.plain)
                        .contextMenu { menu(s, w.4, title: w.1, shown: windows.map(\.4.window_id)) }
                    }
                }
                .background(Color.card, in: RoundedRectangle(cornerRadius: 22, style: .continuous))
            }
        }
    }
}

private struct WindowRow: View {
    let title: String
    let program: String?
    let back: [String?]
    let tiles: CGFloat
    let divider: Bool

    var body: some View {
        HStack(spacing: 14) {
            ProgramTiles(front: program, back: back).frame(width: tiles, height: 16 * ProgramTiles.scale + 4, alignment: .leading)
            Text(title.isEmpty ? " " : title)
                .font(.body.weight(.medium))
                .foregroundStyle(.primary)
                .lineLimit(1)
                .truncationMode(.tail)
            Spacer(minLength: 8)
            Image(systemName: "chevron.right").font(.footnote.weight(.semibold)).foregroundStyle(.gray)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
        .frame(minHeight: 44)
        .contentShape(Rectangle())
        .overlay(alignment: .bottom) {
            if divider {
                Rectangle().fill(Color.primary.opacity(0.12)).frame(height: 1 / 3).padding(.leading, 16 + tiles + 14)
            }
        }
    }
}
