import SwiftUI
import UniformTypeIdentifiers

extension Color {
    static let page = Color(UIColor { $0.userInterfaceStyle == .dark
        ? UIColor(red: 0x22 / 255, green: 0x25 / 255, blue: 0x2F / 255, alpha: 1) : .systemGroupedBackground })
    static let card = Color(UIColor { $0.userInterfaceStyle == .dark
        ? UIColor(red: 0x36 / 255, green: 0x3A / 255, blue: 0x47 / 255, alpha: 1) : .secondarySystemGroupedBackground })
    /// The desktop's dot of a program that waits for the user.
    static let attention = Color(UIColor { $0.userInterfaceStyle == .dark
        ? UIColor(red: 0xFB / 255, green: 0xBF / 255, blue: 0x24 / 255, alpha: 1)
        : UIColor(red: 0xF5 / 255, green: 0x9E / 255, blue: 0x0B / 255, alpha: 1) })
    /// Rex's dot of a program done that the user has not seen.
    static let done = Color(red: 0x7A / 255, green: 0xA0 / 255, blue: 0xC2 / 255)
}

/// Every host, its sessions, and their windows, as Rex's iPhone app lists
/// them, or, as the desktop's sidebar, one space at a time, its windows by
/// host. The eye lists instead the windows by what their programs need.
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
    /// The GoRex machine of a Rex Go code opened from outside, to pair with;
    /// waiting is one that waits for Settings to close first.
    @State private var pairing: HostConfig?
    @State private var waiting: HostConfig?
    @AppStorage(Grouping.key) private var grouping = Grouping.standard
    /// The space chosen, by its host and session.
    @AppStorage("space") private var chosen = ""
    /// The chosen space's glass, which slides to the next one chosen.
    @Namespace private var glass

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
        // A Rex Go code opened from the camera pairs, once Settings, open
        // over the list, has closed.
        .onOpenURL { url in
            guard let c = HostConfig(tailcat: url.absoluteString),
                  let t = c.tailcat, store.host(tailcat: t.address) == nil else { return }
            if settings {
                waiting = c
                settings = false
            } else {
                pairing = c
            }
        }
        .sheet(item: $pairing) { TailcatPairSheet(code: $0) }
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
                } else if grouping == .spaces {
                    let cross = CrossHost(store.hosts.compactMap(\.crossHost))
                    let all = spaces(cross)
                    let space = (all.first { key($0.place) == chosen } ?? all.first)?.place
                    if !all.isEmpty { strip(all, space) }
                    ForEach(sections(cross, all, space)) { host in
                        HostSection(host: host, cross: cross, space: space, collapsed: $collapsed, filter: filter, open: { go($0) })
                    }
                } else {
                    ForEach(store.hosts) { host in
                        HostSection(host: host, cross: CrossHost(), space: nil, collapsed: $collapsed, filter: filter, open: { go($0) })
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
        .sheet(isPresented: $settings, onDismiss: {
            if let c = waiting { pairing = c }
            waiting = nil
        }) { SettingsView() }
    }

    /// A space to switch to: a session of a host online that is a space
    /// of its own.
    private struct Space {
        let host: Host
        let session: Host.Session
        var place: CrossHost.Place { .init(host: host.id, session: session.id) }
    }

    /// Every space, in the hosts' order, then their sessions'.
    private func spaces(_ cross: CrossHost) -> [Space] {
        store.hosts.filter { if case .offline = $0.status { false } else { true } }.flatMap { h in
            h.sessions.filter { !cross.isGone(h.id, $0.id, windows: ($0.view?.windows ?? []).map(\.window_id)) }
                .map { Space(host: h, session: $0) }
        }
    }

    /// The hosts a space lists, in the hosts' order, so that they stay in
    /// place as the space changes: its host, the hosts it holds windows
    /// of, and those that tell why they show none or have no space, for
    /// their menu. A function of its own, as Xcode 26 cannot type-check it
    /// inside the view in time.
    private func sections(_ cross: CrossHost, _ all: [Space], _ space: CrossHost.Place?) -> [Host] {
        store.hosts.filter { host in
            if host.status != .online || !all.contains(where: { $0.host === host }) { return true }
            guard let s = space else { return false }
            return s.host == host.id || cross.rows(s.host, s.session).contains { $0.host == host.id }
        }
    }

    private func key(_ p: CrossHost.Place) -> String { p.host.uuidString + "/" + p.session }

    /// The strip of spaces, as the desktop's: the chosen one's glass has
    /// the accent's tint, and slides to the next one chosen. Each has its
    /// session's menu. A label two spaces share also names the host.
    private func strip(_ all: [Space], _ space: CrossHost.Place?) -> some View {
        ScrollViewReader { proxy in
            ScrollView(.horizontal) {
                GlassEffectContainer(spacing: 8) {
                    HStack(spacing: 8) {
                        ForEach(all, id: \.place) { s in
                            let label = s.session.label
                            let title = all.count { $0.session.label == label } > 1 ? "\(label) · \(s.host.config.name)" : label
                            let isChosen = s.place == space
                            Button { withAnimation(.snappy) { chosen = key(s.place) } } label: {
                                Text(verbatim: title).lineLimit(1)
                                    .foregroundStyle(isChosen ? Color(.systemBackground) : .primary)
                                    .padding(.horizontal, 16)
                                    .frame(height: 42)
                                    .glassEffect(isChosen ? .regular.tint(.accentColor).interactive() : .regular.interactive(), in: .capsule)
                                    .glassEffectID(isChosen ? "chosen" : key(s.place), in: glass)
                            }
                            .buttonStyle(.plain)
                            .accessibilityAddTraits(isChosen ? .isSelected : [])
                            .modifier(SessionMenu(host: s.host, session: s.session, open: { go($0) }))
                        }
                    }
                    .font(.subheadline.weight(.semibold))
                }
            }
            .scrollIndicators(.hidden)
            // The glass's edges show past the page's margins.
            .scrollClipDisabled()
            // At once when the list shows, animated as the space changes.
            .onChange(of: space, initial: true) { old, p in
                if old == p { proxy.scrollTo(p) } else { withAnimation { proxy.scrollTo(p) } }
            }
        }
    }

    /// Whether a window's row shows, by the search.
    private func filter(_ title: String, _ program: String?) -> Bool {
        query.isEmpty || title.localizedCaseInsensitiveContains(query)
    }
}

private struct HostSection: View {
    let host: Host
    /// Where each window shows, grouped by the desktop's spaces.
    let cross: CrossHost
    /// The space shown, if any: the host shows one card of the windows of
    /// it the space holds.
    let space: CrossHost.Place?
    @Binding var collapsed: Set<String>
    let filter: (String, String?) -> Bool
    let open: (TerminalTarget) -> Void
    @Environment(HostStore.self) private var store
    /// The sessions whose new window is on its way.
    @State private var opening: Set<String> = []
    /// The row being dragged, and its session's rows in the order it moves
    /// them to: they make way as it passes over them.
    @State private var drag: Drag?
    @State private var order: [String] = []
    /// Each lift, for its feel.
    @State private var lifts = 0
    /// Whether a new session is on its way.
    @State private var creating = false
    /// What failed last, and why, as the server says it.
    @State private var failure: (title: LocalizedStringKey, why: String)?
    /// The window being renamed, and the name being typed.
    @State private var renaming: (session: String, window: String, title: String)?
    @State private var name = ""

    var body: some View {
        let key = host.id.uuidString
        let isCollapsed = collapsed.contains(key)
        VStack(alignment: .leading, spacing: 12) {
            // The whole row folds the host's sessions away, but its menu:
            // glass, as it floats over the page.
            HStack(spacing: 10) {
                Image(systemName: host.config.symbol)
                    .font(.title3)
                    .foregroundStyle(.secondary)
                Text(host.config.name).font(.title3.bold()).lineLimit(1)
                Spacer()
                // One + per card: the host's new session is in its menu.
                Menu {
                    Button("New Session", systemImage: "plus.rectangle.on.rectangle") { newSession() }
                        .disabled(creating)
                    Button("Reconnect", systemImage: "arrow.clockwise") { host.resume() }
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
        // A row dropped in the section, off the rows, stays where it is.
        .onDrop(of: [.text], isTargeted: nil) { _ in drop() }
        // The dragged session's server order: a dropped row waits for it to
        // change; a drag ends when its window goes.
        .onChange(of: drag.map { d in host.sessions.first { $0.id == d.session.id }?.view?.windows?.map(\.window_id) ?? [] }) { _, ids in
            guard let d = drag, let ids else { return }
            if d.dropped ? ids != d.from : !ids.contains(d.window) { drag = nil }
        }
        // A drag let go of with no drop.
        .onReceive(NotificationCenter.default.publisher(for: Lifted.ended)) { n in
            if let d = drag, !d.dropped, n.object as? UUID == d.token { drag = nil }
        }
        .modifier(DragEnd { if drag?.dropped == false { drag = nil } })
        .sensoryFeedback(.selection, trigger: lifts)
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

    /// The host's sessions, or why there are none.
    @ViewBuilder private var sessions: some View {
        switch host.status {
        case .connecting where host.sessions.isEmpty:
            Text("Connecting…").foregroundStyle(.secondary)
        case .offline(let why):
            Label(why.isEmpty ? String(localized: "Offline") : String(localized: "Offline: \(why)"), systemImage: "wifi.slash")
                .foregroundStyle(.secondary)
        default:
            if let space {
                // No session headers: the space's windows on this host.
                let rows = entries(windows(in: space))
                if !rows.isEmpty {
                    card { self.rows(rows) }
                } else if host.status != .online {
                    Text("Connecting…").foregroundStyle(.secondary)
                } else if space.host == host.id {
                    Text("No Window Open").foregroundStyle(.secondary)
                } else {
                    // A host with no space of its own.
                    Text("No sessions").foregroundStyle(.secondary)
                }
            } else {
                if host.sessions.isEmpty { Text("No sessions").foregroundStyle(.secondary) }
                ForEach(host.sessions) { s in session(s) }
            }
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
            Button("Move Up", systemImage: "arrow.up") { move(id, in: s, onto: shown[i - 1], shown: shown) }
                .disabled(i <= 0)
            Button("Move Down", systemImage: "arrow.down") { move(id, in: s, onto: shown[i + 1], shown: shown) }
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

    /// Moves a window of a session to the place of another row shown.
    private func move(_ id: String, in s: Host.Session, onto target: String, shown: [String]) {
        let before = Host.before(id, onto: target, shown: shown, all: (s.view?.windows ?? []).map(\.window_id))
        change("Can't Move the Tab") { [host] in
            do { try await host.moveWindow(id, in: s.id, before: before) } catch {
                // The dropped row goes back to the server's order.
                if drag?.dropped == true { drag = nil }
                throw error
            }
        }
    }

    /// A row's drag: its session's rows as they showed when it lifted, and
    /// the item the system holds until the drag ends. Dropped, it waits for
    /// the server's order.
    private struct Drag {
        let session: Host.Session
        let window: String
        let from: [String]
        let token = UUID()
        weak var item: Lifted?
        var dropped = false
    }

    /// Lifts a row, from the order the rows show.
    private func lift(_ w: Entry, shown: [String]) -> NSItemProvider {
        // The system asks again as the rows move: the same drag goes on.
        if let d = drag, !d.dropped, d.window == w.window.window_id, let item = d.item { return item }
        let item = Lifted(object: w.id as NSString)
        var d = Drag(session: w.session, window: w.window.window_id, from: shown)
        item.token = d.token
        d.item = item
        drag = d
        order = shown
        lifts += 1
        return item
    }

    /// Makes way for the dragged row at another row of its session.
    private func pass(over w: Entry) {
        guard let d = drag, !d.dropped, d.session.id == w.session.id,
              let from = order.firstIndex(of: d.window), let to = order.firstIndex(of: w.window.window_id), from != to
        else { return }
        withAnimation(.snappy) { order.move(fromOffsets: [from], toOffset: to > from ? to + 1 : to) }
    }

    /// Moves the dropped row on the server to where the drag left it.
    private func drop() -> Bool {
        guard var d = drag, !d.dropped else { return false }
        guard let i = order.firstIndex(of: d.window), let j = d.from.firstIndex(of: d.window), i != j else {
            drag = nil
            return true
        }
        d.dropped = true
        drag = d
        move(d.window, in: d.session, onto: d.from[i], shown: d.from)
        // Should the server's order not change, the rows go back to it.
        Task {
            try? await Task.sleep(for: .seconds(2))
            if drag?.token == d.token { drag = nil }
        }
        return true
    }

    /// A card's rows in the order a drag of one of them leaves.
    private func arranged(_ windows: [Entry]) -> [Entry] {
        guard let d = drag, windows.allSatisfy({ $0.session.id == d.session.id }) else { return windows }
        return windows.sorted { (order.firstIndex(of: $0.window.window_id) ?? .max) < (order.firstIndex(of: $1.window.window_id) ?? .max) }
    }

    /// The windows of this host a space holds, in its order, with their
    /// sessions: of its own host, those in no other host's space.
    private func windows(in space: CrossHost.Place) -> [(Host.Session, SessionView.Window)] {
        if space.host == host.id {
            guard let s = host.sessions.first(where: { $0.id == space.session }) else { return [] }
            return (s.view?.windows ?? []).filter { !cross.isMoved(host.id, s.id, $0.window_id) }.map { (s, $0) }
        }
        return cross.rows(space.host, space.session).filter { $0.host == host.id }.compactMap { r in
            host.sessions.first { $0.id == r.session }.flatMap { s in s.view?.windows?.first { $0.window_id == r.window }.map { (s, $0) } }
        }
    }

    /// A window's row in a card.
    private struct Entry {
        let session: Host.Session
        let block: String
        let window: SessionView.Window
        var id: String { session.id + "/" + window.window_id }
    }

    /// The rows the search keeps.
    private func entries(_ windows: [(Host.Session, SessionView.Window)]) -> [Entry] {
        windows.compactMap { s, w in
            w.block.flatMap { filter(host.title(w), host.programs[$0]) ? Entry(session: s, block: $0, window: w) : nil }
        }
    }

    private func card(@ViewBuilder _ content: () -> some View) -> some View {
        VStack(spacing: 0, content: content)
            .background(.fill.quaternary)
            // The rows' highlights keep to the card's corners.
            .clipShape(.rect(cornerRadius: 22, style: .continuous))
    }

    /// The rows of a card, each with its window's menu, and dragged onto
    /// another of its session to move there.
    private func rows(_ entries: [Entry]) -> some View {
        let windows = arranged(entries)
        // The room of the tiles of the window with the most, so that the titles line up.
        let tiles = ProgramTiles.width(behind: windows.map(\.window.back.count).max() ?? 0)
        return ForEach(Array(windows.enumerated()), id: \.element.id) { i, w in
            let row = Button {
                open(TerminalTarget(host: host.id, session: w.session.id, block: w.block, title: host.title(w.window)))
            } label: {
                WindowRow(host: host, block: w.block, window: w.window, tiles: tiles, divider: i < windows.count - 1) {
                    Image(systemName: "chevron.right").font(.footnote.weight(.semibold)).foregroundStyle(.gray)
                }
            }
            .buttonStyle(.plain)
            .hoverEffect(.highlight)
            // The menu and the drag change the windows of the space's own
            // host only.
            if space.map({ $0.host == host.id }) ?? true {
                // The order the rows show, a drag's while it lasts.
                let shown = windows.filter { $0.session.id == w.session.id }.map(\.window.window_id)
                let lifted = drag.map { !$0.dropped && $0.session.id == w.session.id && $0.window == w.window.window_id } ?? false
                row.onDrag { lift(w, shown: shown) }
                    .contextMenu { menu(w.session, w.window, shown: shown) }
                    .onDrop(of: [.text], delegate: RowDrop(pass: { pass(over: w) }, drop: drop))
                    .opacity(lifted ? 0.4 : 1)
            } else {
                row
            }
        }
    }

    @ViewBuilder private func session(_ s: Host.Session) -> some View {
        let windows = entries((s.view?.windows ?? []).map { (s, $0) })
        let isCollapsed = collapsed.contains(s.id)
        // The session's header on the page, then its windows in a card.
        VStack(spacing: 4) {
            HStack(spacing: 4) {
                Text(s.label).font(.headline).lineLimit(1)
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
            .modifier(SessionMenu(host: host, session: s))
            if !isCollapsed && !windows.isEmpty { card { rows(windows) } }
        }
    }
}

/// A session's menu: rename it, or close it after a yes, and, with open,
/// a new window in it. On the header, for the question to point at the
/// session.
private struct SessionMenu: ViewModifier {
    let host: Host
    let session: Host.Session
    var open: ((TerminalTarget) -> Void)?
    @State private var renaming = false
    @State private var closing = false
    @State private var name = ""
    /// What failed, and why, as the server says it.
    @State private var failure: (title: LocalizedStringKey, why: String)?

    func body(content: Content) -> some View {
        content
            .contextMenu {
                if let open {
                    Button("New Tab", systemImage: "plus") {
                        change("Can't Open a New Window") { [host, session] in
                            if let b = try await host.newWindow(in: session.id) {
                                open(TerminalTarget(host: host.id, session: session.id, block: b, title: session.label))
                            }
                        }
                    }
                }
                Button("Rename Session…", systemImage: "pencil") {
                    name = session.label
                    renaming = true
                }
                Button("Close Session", systemImage: "xmark", role: .destructive) { closing = true }
            }
            .confirmationDialog("Close Session", isPresented: $closing) {
                Button("Close Session", role: .destructive) {
                    change("Can't Close the Session") { [host, session] in try await host.closeSession(session.id) }
                }
            } message: {
                Text("Close \(session.label) and every window in it?")
            }
            .alert("Rename Session", isPresented: $renaming) {
                TextField(session.label, text: $name)
                Button("Cancel", role: .cancel) {}
                Button("Save") {
                    change("Can't Rename the Session") { [host, session, name] in try await host.renameSession(session.id, to: name) }
                }
            }
            .alert(failure?.title ?? "", isPresented: Binding { failure != nil } set: { if !$0 { failure = nil } }) {
            } message: {
                Text(failure?.why ?? "")
            }
    }

    private func change(_ title: LocalizedStringKey, _ body: @escaping () async throws -> Void) {
        Task {
            do { try await body() } catch { failure = (title, error.localizedDescription) }
        }
    }
}

/// A row as a drop target: the dragged row makes way at it, and lands on
/// the drop.
private struct RowDrop: DropDelegate {
    let pass: () -> Void
    let drop: () -> Bool

    func dropEntered(info: DropInfo) { pass() }
    func dropUpdated(info: DropInfo) -> DropProposal? { DropProposal(operation: .move) }
    func performDrop(info: DropInfo) -> Bool { drop() }
}

/// Tells when a drag from the view ends, where the system says so (iOS
/// 27). Only the iOS 27 SDK (Swift 6.4, Xcode 27) has the call; built
/// with an older one, the item's release alone tells.
private struct DragEnd: ViewModifier {
    let ended: () -> Void

    func body(content: Content) -> some View {
        #if compiler(>=6.4)
        if #available(iOS 27, *) {
            content.onDragSessionUpdated { if case .ended = $0.phase { ended() } }
        } else {
            content
        }
        #else
        content
        #endif
    }
}

/// A dragged row's item. The system lets go of it once it is done with
/// the drag, which, after a cancelled one, can be late: it tells so then,
/// by its drag's token.
nonisolated private final class Lifted: NSItemProvider, @unchecked Sendable {
    static let ended = Notification.Name("GoRex.Lifted.ended")
    var token: UUID?

    deinit {
        let token = token
        DispatchQueue.main.async { NotificationCenter.default.post(name: Lifted.ended, object: token) }
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
            ProgramTiles(front: host.programs[block], back: window.back.map { host.programs[$0] }, done: status?.state == "done").frame(width: tiles, height: 16 * ProgramTiles.scale + 4, alignment: .leading)
            Text(title.isEmpty ? " " : title.asText)
                // Regular and a little dimmed, as Rex sets a window's title.
                .font(.body)
                .foregroundStyle(.primary.opacity(0.8))
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
        .padding(.vertical, 11)
        .frame(minHeight: 50)
        .contentShape(Rectangle())
        .overlay(alignment: .bottom) {
            if divider {
                Rectangle().fill(Color.primary.opacity(0.12)).frame(height: 1 / 3).padding(.leading, 16 + tiles + 14)
            }
        }
    }
}
