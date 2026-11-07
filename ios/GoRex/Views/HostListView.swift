import SwiftUI

extension Color {
    static let page = Color(UIColor { $0.userInterfaceStyle == .dark
        ? UIColor(red: 0x22 / 255, green: 0x25 / 255, blue: 0x2F / 255, alpha: 1) : .systemGroupedBackground })
    static let card = Color(UIColor { $0.userInterfaceStyle == .dark
        ? UIColor(red: 0x36 / 255, green: 0x3A / 255, blue: 0x47 / 255, alpha: 1) : .secondarySystemGroupedBackground })
    /// The desktop's dot of a program that waits for the user.
    static let attention = Color(UIColor { $0.userInterfaceStyle == .dark
        ? UIColor(red: 0xFB / 255, green: 0xBF / 255, blue: 0x24 / 255, alpha: 1)
        : UIColor(red: 0xF5 / 255, green: 0x9E / 255, blue: 0x0B / 255, alpha: 1) })
}

/// Every host, its sessions, and their windows, as Rex's iPhone app lists
/// them. The eye lists instead the windows by what their programs need.
/// A wide window shows the list beside the terminal open; a narrow one
/// pushes the terminal over the list.
struct HostListView: View {
    @Environment(HostStore.self) private var store
    @Environment(\.horizontalSizeClass) private var width
    /// The terminal open, pushed or beside the list: one at most.
    @State private var path: [TerminalTarget] = []
    @State private var collapsed: Set<String> = []
    @State private var inbox = false
    @State private var query = ""
    @State private var settings = false

    var body: some View {
        Group {
            if width == .regular {
                NavigationSplitView {
                    list
                } detail: {
                    // A terminal of its own for each target: the last one
                    // closes as the next opens.
                    if let t = path.last, let host = store.host(t.host) {
                        TerminalScreen(target: t, host: host, go: go).id(t)
                    } else {
                        ContentUnavailableView("No Window Open", systemImage: "terminal",
                                               description: Text("Choose a window in the list."))
                            .background(Color.page)
                    }
                }
            } else {
                NavigationStack(path: $path) {
                    // A terminal of its own here too: go replaces the path, which
                    // alone would keep the old one.
                    list.navigationDestination(for: TerminalTarget.self) { t in
                        if let host = store.host(t.host) { TerminalScreen(target: t, host: host, go: go).id(t) }
                    }
                }
            }
        }
        // A tapped notification opens its terminal.
        .onChange(of: Notifier.shared.opening, initial: true) { _, t in
            guard let t else { return }
            go(t)
            Notifier.shared.opening = nil
        }
    }

    /// Opens a terminal, in place of the one open; nil goes back to the list.
    private func go(_ t: TerminalTarget?) {
        path = t.map { [$0] } ?? []
    }

    private var list: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 30) {
                if store.hosts.isEmpty {
                    Text("No hosts yet. Add one in Settings.").foregroundStyle(.secondary).padding(.top, 40)
                }
                if inbox {
                    StatusInbox(query: query, collapsed: $collapsed, open: { go($0) })
                } else {
                    ForEach(store.hosts) { host in
                        HostSection(host: host, collapsed: $collapsed, filter: filter, open: { go($0) })
                    }
                }
            }
            .padding(.horizontal, 20)
            .padding(.top, 12)
            .padding(.bottom, 20)
        }
        .background(Color.page)
        .scrollEdgeEffectStyle(.soft, for: .all)
        // Beside the terminal the bar stays, for the sidebar's toggle.
        .toolbar(width == .regular ? .automatic : .hidden, for: .navigationBar)
        .toolbar {
            ToolbarItemGroup(placement: .bottomBar) {
                Button("Settings", systemImage: "gearshape") { settings = true }
                Button(inbox ? "Show All Windows" : "Show Activity", systemImage: inbox ? "eye.slash" : "eye") {
                    inbox.toggle()
                }
            }
            ToolbarSpacer(.flexible, placement: .bottomBar)
            DefaultToolbarItem(kind: .search, placement: .bottomBar)
        }
        .searchable(text: $query, prompt: "Search windows")
        .searchToolbarBehavior(.minimize)
        .textInputAutocapitalization(.never)
        .autocorrectionDisabled()
        .sheet(isPresented: $settings) { SettingsView() }
    }

    /// Whether a window's row shows, by the search.
    private func filter(_ title: String, _ program: String?) -> Bool {
        query.isEmpty || title.localizedCaseInsensitiveContains(query)
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
    /// Whether a new session is on its way.
    @State private var creating = false
    /// What failed last, and why, as the server says it.
    @State private var failure: (title: LocalizedStringKey, why: String)?
    /// The window being renamed, and the name being typed.
    @State private var renaming: (session: String, window: String, title: String)?
    @State private var name = ""
    /// The session being renamed, and the one whose close waits for a yes.
    @State private var renamingSession: Host.Session?
    @State private var closing: Host.Session?

    var body: some View {
        let key = host.id.uuidString
        let isCollapsed = collapsed.contains(key)
        VStack(alignment: .leading, spacing: 12) {
            // The whole row folds the host's sessions away, but its menu:
            // glass, as it floats over the page.
            HStack(spacing: 10) {
                Image(systemName: host.config.desktop ? "desktopcomputer" : "laptopcomputer")
                    .font(.title3)
                    .foregroundStyle(.secondary)
                Text(host.config.name).font(.title.bold()).lineLimit(1)
                Spacer()
                // One + per card: the host's new session is in its menu.
                Menu {
                    Button("New Session", systemImage: "plus.rectangle.on.rectangle") { newSession() }
                        .disabled(creating)
                    Button("Reconnect", systemImage: "arrow.clockwise") { host.reconnectIfNeeded() }
                    Button("Remove", systemImage: "trash", role: .destructive) { store.remove(host.id) }
                } label: {
                    Group {
                        if creating { ProgressView() } else { Image(systemName: "ellipsis") }
                    }
                    .frame(width: 30, height: 30)
                }
                .font(.body.weight(.semibold))
                .buttonStyle(.glass)
                .buttonBorderShape(.circle)
                .accessibilityLabel("Host Menu")
            }
            .foregroundStyle(.primary)
            .contentShape(.rect)
            .onTapGesture {
                withAnimation(.snappy) { if isCollapsed { collapsed.remove(key) } else { collapsed.insert(key) } }
            }
            .accessibilityAddTraits(.isButton)
            .accessibilityHint(isCollapsed ? "Expand" : "Collapse")
            if !isCollapsed { sessions }
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
        .alert("Rename Session", isPresented: Binding { renamingSession != nil } set: { if !$0 { renamingSession = nil } }, presenting: renamingSession) { s in
            TextField(s.label, text: $name)
            Button("Cancel", role: .cancel) {}
            Button("Save") {
                change("Can't Rename the Session") { try await host.renameSession(s.id, to: name) }
            }
        }
    }

    /// The host's sessions, or why there are none.
    @ViewBuilder private var sessions: some View {
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

    /// Makes a session with a window, and goes to it.
    private func newSession() {
        creating = true
        Task {
            defer { creating = false }
            do {
                if let (s, b) = try await host.newSession() {
                    open(TerminalTarget(host: host.id, session: s, block: b, title: ""))
                }
            } catch {
                failure = ("Can't Make a New Session", error.localizedDescription)
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
    private func menu(_ s: Host.Session, _ w: SessionView.Window, shown: [String]) -> some View {
        let all = (s.view?.windows ?? []).map(\.window_id)
        let id = w.window_id
        let i = shown.firstIndex(of: id) ?? 0
        // The title changes while the menu is open: it's read on the tap.
        return Unchanged([s.id, s.label, id, w.name, shown, all]) {
            Button("Rename Tab…", systemImage: "pencil") {
                name = w.name ?? ""
                renaming = (s.id, id, host.title(w))
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
        .equatable()
    }

    @ViewBuilder private func session(_ s: Host.Session) -> some View {
        let windows = (s.view?.windows ?? []).compactMap { w in
            w.block.flatMap { filter(host.title(w), host.programs[$0]) ? (block: $0, window: w) : nil }
        }
        // The room of the tiles of the window with the most, so that the titles line up.
        let tiles = ProgramTiles.width(behind: windows.map(\.window.back.count).max() ?? 0)
        let isCollapsed = collapsed.contains(s.id)
        // The session is one card: its header, then its windows.
        VStack(spacing: 0) {
            HStack(spacing: 4) {
                Text(s.label).font(.headline).foregroundStyle(.primary).lineLimit(1)
                Spacer(minLength: 8)
                // Plain ghost buttons: glass is for what floats over the page.
                Button {
                    newWindow(s)
                } label: {
                    Group {
                        if opening.contains(s.id) { ProgressView() } else { Image(systemName: "plus") }
                    }
                    .frame(width: 36, height: 36).contentShape(.circle)
                }
                .disabled(opening.contains(s.id))
                .accessibilityLabel("New Window")
                Button {
                    withAnimation(.snappy) { if isCollapsed { collapsed.remove(s.id) } else { collapsed.insert(s.id) } }
                } label: {
                    Image(systemName: "chevron.down").rotationEffect(.degrees(isCollapsed ? -90 : 0)).frame(width: 36, height: 36).contentShape(.circle)
                }
                .accessibilityLabel(isCollapsed ? "Expand" : "Collapse")
            }
            .buttonStyle(.plain)
            .font(.body.weight(.semibold))
            .foregroundStyle(.secondary)
            .padding(.leading, 16)
            .padding(.trailing, 6)
            .frame(minHeight: 50)
            .contentShape(.rect)
            .overlay(alignment: .bottom) {
                if !isCollapsed && !windows.isEmpty {
                    Rectangle().fill(Color.primary.opacity(0.12)).frame(height: 1 / 3).padding(.leading, 16)
                }
            }
            .contextMenu {
                Button("Rename Session…", systemImage: "pencil") {
                    name = s.label
                    renamingSession = s
                }
                Button("Close Session", systemImage: "xmark", role: .destructive) { closing = s }
            }
            // On the header, for the question to point at the session.
            .confirmationDialog("Close Session", isPresented: Binding { closing?.id == s.id } set: { if !$0 { closing = nil } }, presenting: closing) { s in
                Button("Close Session", role: .destructive) {
                    change("Can't Close the Session") { try await host.closeSession(s.id) }
                }
            } message: { s in
                Text("Close \(s.label) and every window in it?")
            }
            if !isCollapsed {
                ForEach(Array(windows.enumerated()), id: \.element.window.window_id) { i, w in
                    let title = host.title(w.window)
                    Button {
                        open(TerminalTarget(host: host.id, session: s.id, block: w.block, title: title))
                    } label: {
                        WindowRow(host: host, block: w.block, window: w.window, tiles: tiles, divider: i < windows.count - 1) {
                            Image(systemName: "chevron.right").font(.footnote.weight(.semibold)).foregroundStyle(.gray)
                        }
                    }
                    .buttonStyle(.plain)
                    .hoverEffect(.highlight)
                    .contextMenu { menu(s, w.window, shown: windows.map(\.window.window_id)) }
                }
            }
        }
        .background(.fill.quaternary)
        // The rows' highlights keep to the card's corners.
        .clipShape(.rect(cornerRadius: 22, style: .continuous))
    }
}

/// A menu's items, built again only when what they show changes. The
/// host changes many times a second, as titles spin, and an open menu
/// whose items are built again draws itself again.
private struct Unchanged<Content: View>: View, Equatable {
    let key: [AnyHashable]
    @ViewBuilder let content: () -> Content

    init(_ key: [AnyHashable], @ViewBuilder content: @escaping () -> Content) {
        self.key = key
        self.content = content
    }

    var body: some View { content() }

    static func == (a: Self, b: Self) -> Bool { a.key == b.key }
}

/// A window's row: the tiles of its programs, its title, and a mark at
/// the end.
struct WindowRow<Mark: View>: View {
    let host: Host
    let block: String
    let window: SessionView.Window
    let tiles: CGFloat
    let divider: Bool
    @ViewBuilder let mark: Mark

    var body: some View {
        let title = host.title(window)
        let status = host.status([block] + window.tiled)
        HStack(spacing: 14) {
            ProgramTiles(front: host.programs[block], back: window.back.map { host.programs[$0] }).frame(width: tiles, height: 16 * ProgramTiles.scale + 4, alignment: .leading)
            Text(title.isEmpty ? " " : title)
                .font(.body.weight(.medium))
                .foregroundStyle(.primary)
                .lineLimit(1)
                .truncationMode(.tail)
            Spacer(minLength: 8)
            // As the desktop's tab: a dot while the program waits for the
            // user, else its progress when it tells one.
            if status?.state == "blocked" {
                Circle().fill(Color.attention).frame(width: 7, height: 7).accessibilityLabel("Needs Input")
            } else if let p = status?.progress {
                Circle().trim(from: 0, to: CGFloat(p) / 100)
                    .stroke(.secondary, style: StrokeStyle(lineWidth: 2, lineCap: .round))
                    .rotationEffect(.degrees(-90))
                    .background(Circle().stroke(.quaternary, lineWidth: 2))
                    .frame(width: 12, height: 12)
                    .accessibilityLabel(Text(verbatim: "\(p)%"))
            }
            mark
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
