import SwiftUI
import UniformTypeIdentifiers

extension Color {
    static let page = Color(UIColor { $0.userInterfaceStyle == .dark
        ? UIColor(red: 0x22 / 255, green: 0x25 / 255, blue: 0x2F / 255, alpha: 1) : .systemGroupedBackground })
    static let card = Color(UIColor { $0.userInterfaceStyle == .dark
        ? UIColor(red: 0x36 / 255, green: 0x3A / 255, blue: 0x47 / 255, alpha: 1) : .secondarySystemGroupedBackground })

    /// A project's or a space's color (ProjectColor); nil for none.
    init?(project color: String?) {
        guard let hex = ProjectColor.hex(color) else { return nil }
        self.init(red: Double(hex >> 16 & 0xFF) / 255, green: Double(hex >> 8 & 0xFF) / 255, blue: Double(hex & 0xFF) / 255)
    }
}

/// The space chosen atop the list, and under it each host with its
/// projects of the space and its tabs with no project, as the desktop's
/// sidebar (docs/MOD-002-projects.md, Rex Go); a host of before projects lists its
/// sessions. The eye lists instead the tabs by what their programs need.
/// A wide window shows the list beside the terminal open; a narrow one
/// pushes the terminal over the list.
struct HostListView: View {
    @Environment(HostStore.self) private var store
    @Environment(SpaceBook.self) private var book
    @Environment(\.horizontalSizeClass) private var width
    /// The terminal open, pushed or beside the list: one at most.
    @State private var path: [TerminalTarget] = []
    /// The hosts and the sessions folded away.
    @State private var collapsed: Set<String> = []
    /// The projects and Older open, by HostSection's keys, kept across
    /// launches in a default. The set lives in the view's state, like
    /// collapsed: a write to AppStorage reaches the views a beat later,
    /// with no animation.
    @State private var expanded: Set<String> = []
    @AppStorage("expanded") private var expandedText = ""
    @AppStorage("developerMode") private var developerMode = false
    @State private var inbox = false
    @State private var query = ""
    @State private var settings = false
    /// The button that opened Settings, for it to zoom out of
    /// (docs/IOS-011-continuous-transitions.md).
    @State private var settingsSource: ZoomSource = .settings
    @Namespace private var zoom
    /// The rows that move between All Tabs and Activity.
    @Namespace private var places
    @Environment(\.accessibilityReduceMotion) private var still
    @AppStorage("scheme") private var scheme = "dark"
    /// The window's scheme, a terminal's while one is open.
    @Environment(\.colorScheme) private var windowScheme
    /// The GoRex machine of a Rex Go code opened from outside, to pair with;
    /// waiting is one that waits for Settings to close first.
    @State private var pairing: HostConfig?
    @State private var waiting: HostConfig?
    /// The host whose New Project sheet is open.
    @State private var newProject: Host?
    @State private var addingSpace = false
    @State private var spaceName = ""
    @State private var editingSpaces = false
    /// Whether the panel of the list's space picker is open.
    @State private var spacePanel = false
    /// The project New Project found the folder to be already, in another
    /// space: to move it to the space the sheet chose, or to show it there.
    @State private var existing: Existing?
    /// The hosts taking their renames of spaces now.
    @State private var renaming: Set<UUID> = []
    /// The host whose section is being dragged, and the order the drag
    /// leaves the hosts in.
    @State private var hostDrag: HostDrag?
    /// Each host's lift, for its feel.
    @State private var lifts = 0

    /// A host's drag: the hosts' ids as they showed when its header
    /// lifted, moved as the drag passes over the other sections, and the
    /// item the system holds until the drag ends.
    private struct HostDrag {
        let host: UUID
        var order: [UUID]
        let token = UUID()
        weak var item: Lifted?
    }

    struct Existing {
        let host: Host
        let project: String
        let name: String
        /// The space the project is in.
        let space: String
        /// The space the sheet chose, to move it to.
        let into: String
    }

    init() {
        _expanded = State(initialValue: Set(UserDefaults.standard.string(forKey: "expanded")?
            .split(separator: "\n").map(String.init) ?? []))
    }

    var body: some View {
        Group {
            if width == .regular { split } else { stack }
        }
        // And let go of on the bars.
        .onDrop(of: [Lifted.type], delegate: PageDrop())
        .overlay(alignment: .top) {
            if developerMode {
                DeveloperModeOverlay()
                    .padding(.top, overlayTop)
            }
        }
        // A tapped notification opens its terminal.
        .onChange(of: Notifier.shared.opening, initial: true) { _, t in
            guard let t else { return }
            go(t)
            Notifier.shared.opening = nil
        }
        // A widget's tap opens a terminal, as a notification's does, or the
        // inbox; a host gone opens the list. A Rex Go code opened from the
        // camera pairs, once Settings, open over the list, has closed.
        .onOpenURL { url in
            if let link = WidgetLink(url: url) {
                switch link {
                case let .open(host, session, block) where store.host(host) != nil:
                    Notifier.shared.opening = TerminalTarget(host: host, session: session, block: block, title: "", explicit: true)
                case .open:
                    go(nil)
                case .inbox:
                    // Beside a terminal the list shows already.
                    if width != .regular { go(nil) }
                    inbox = true
                }
                return
            }
            guard let c = HostConfig(tailcat: url.absoluteString),
                  let t = c.tailcat, store.host(tailcat: t.address) == nil else { return }
            if settings {
                waiting = c
                settings = false
            } else {
                pairing = c
            }
        }
        // As in RemoteHostsView: a sheet on a Mac loses the injected
        // objects, so the store goes back in.
        .sheet(item: $pairing) { TailcatPairSheet(code: $0).environment(store) }
        // A host gone, as Demo Mode's, closes the terminals open on it, and
        // this device forgets its projects.
        .onChange(of: store.hosts.map(\.id)) { old, ids in
            path.removeAll { !ids.contains($0.host) }
            for id in old where !ids.contains(id) { book.forget(host: id.uuidString) }
        }
        // What each host with projects shows, kept for while it is away.
        .onChange(of: listingKey, initial: true) { _, _ in
            for h in store.hosts where h.status == .online && h.hasProjects && !h.isDemo {
                book.remember(ProjectListing(host: h), for: h.id.uuidString)
            }
        }
        .onChange(of: carried, initial: true) { _, c in book.settle(carried: c) }
        .onChange(of: renameKey, initial: true) { _, _ in sendRenames() }
        .sheet(item: $newProject) { host in
            ProjectSheet(host: host, spaces: spaces, space: current) { dir, name, space in createProject(on: host, dir: dir, name: name, space: space) }
                .navigationTransition(.zoom(sourceID: ZoomSource.newProject(host.id), in: zoom))
        }
        // A sheet on a Mac loses the injected objects; the space book
        // goes back in.
        .sheet(isPresented: $editingSpaces) {
            EditSpaces(spaces: spaces, carried: carried, projects: projects(in:)) { from, to in
                book.rename(from, to: to, on: spaceHosts)
            } delete: { name in
                book.delete(name, on: spaceHosts)
            }
            .environment(book)
        }
        .alert("New Space", isPresented: $addingSpace) {
            TextField("Name", text: $spaceName)
            Button("Cancel", role: .cancel) {}
            Button("Add") { book.add(spaceName, carried: carried) }
        } message: {
            Text("The space stays here, empty, until a project moves into it.")
        }
        .alert(existing.map { String(localized: "\($0.name) Is a Project Already") } ?? "", isPresented: Binding { existing != nil } set: { if !$0 { existing = nil } }, presenting: existing) { e in
            Button("Move to \(SpaceName.shown(e.into))") { moveHere(e) }
            Button("Show in \(SpaceName.shown(e.space))") {
                book.chosen = e.space
                expand(e.host, e.project)
            }
            Button("Cancel", role: .cancel) {}
        } message: { e in
            Text("Its folder is a project of \(e.host.config.name) in \(SpaceName.shown(e.space)).")
        }
    }

    /// The sidebar and the terminal beside it, on a wide window.
    private var split: some View {
        NavigationSplitView {
            list
        } detail: {
            // A terminal of its own for each target: the last one
            // closes as the next opens.
            if let t = path.last, let host = store.host(t.host) {
                // A row tapped in the sidebar fades to its terminal
                // (openRow); every other open cuts.
                TerminalScreen(target: t, host: host, go: go).id(t)
                    .transition(.opacity)
            } else {
                ContentUnavailableView("No Tab Open", systemImage: "terminal",
                                       description: Text("Choose a tab in the list."))
                    .background(Color.page)
            }
        }
    }

    /// The list, and the terminal pushed over it, on a narrow window.
    private var stack: some View {
        NavigationStack(path: $path) {
            // The list and its bars keep the app's scheme under a
            // terminal's, through the push and the pop.
            list
                .environment(\.colorScheme, own)
                .toolbarColorScheme(own, for: .navigationBar)
                .toolbarColorScheme(own, for: .bottomBar)
                // A terminal of its own here too: go replaces the path, which
                // alone would keep the old one.
                .navigationDestination(for: TerminalTarget.self) { t in
                    if let host = store.host(t.host) {
                        TerminalScreen(target: t, host: host, go: go).id(t)
                    }
                }
        }
        // The list back on screen gives the window its scheme back as the
        // path empties, at the start of a pop from a button; the terminal
        // clears it only at the pop's end. An iPad's sidebar keeps
        // following the terminal beside it, so this is the narrow window's.
        .onChange(of: path) { _, p in
            if p.isEmpty { ScreenStyle.shared.release() }
        }
    }

    /// The developer overlay's room at the top: under the bar beside a
    /// terminal, else under the demo's banner, else at the top.
    private var overlayTop: CGFloat {
        if !path.isEmpty || width == .regular { return 58 }
        return store.demo != nil ? 48 : 8
    }

    /// The room the developer overlay's collapsed pill takes atop the
    /// list, in overlayTop's cases: its offset and height plus a gap, so
    /// it covers no row. The list still scrolls under it once it is past.
    private var listTopPad: CGFloat {
        guard developerMode else { return 12 }
        if !path.isEmpty || width == .regular { return 52 }
        return store.demo != nil ? 30 : 52
    }

    /// Opens a terminal, in place of the one open; nil goes back to the list.
    private func go(_ t: TerminalTarget?) {
        path = t.map { [$0] } ?? []
    }

    /// A pull to refresh (docs/IOS-016-pull-refresh.md): the hand feels a
    /// tap, and each host not online dials at once. The refresh control
    /// stays while a host still dials, up to three seconds.
    private func refresh() async {
        Haptics.refreshed()
        store.reconnectDown()
        let end = ContinuousClock.now + .seconds(3)
        while ContinuousClock.now < end,
              store.hosts.contains(where: { !$0.isDemo && $0.status == .connecting }) {
            try? await Task.sleep(for: .milliseconds(80))
        }
    }

    /// Opens a row's tab: on a wide window the detail fades to it; a
    /// narrow one pushes it.
    private func openRow(_ t: TerminalTarget) {
        if width == .regular {
            withAnimation(.easeInOut(duration: still ? 0.15 : 0.2)) { go(t) }
        } else {
            go(t)
        }
    }

    /// The app's own scheme: Settings › Interface, else the system's, which
    /// ScreenStyle knows once a window shows.
    private var own: ColorScheme {
        GoRexApp.colorScheme(scheme) ?? ScreenStyle.shared.system ?? windowScheme
    }

    private var list: some View {
        Group {
            if store.hosts.isEmpty {
                // As tall as the page, so that the welcome sits in its
                // middle, above the keyboard or the bottom bar.
                NoHostsView(zoom: zoom, tryDemo: { store.enterDemo() }, addHost: {
                    settingsSource = .addHost
                    settings = true
                })
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background { Color.page.ignoresSafeArea() }
            } else {
                // Once for the list, not again for each host: each reads
                // every host's projects.
                let spaces = self.spaces
                let current = Self.current(in: spaces, chosen: book.chosen)
                let hasSpaces = self.hasSpaces
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 30) {
                        if inbox {
                            StatusInbox(query: query, collapsed: $collapsed, places: places, open: { openRow($0) })
                        } else {
                            if hasSpaces { ListPicker(wide: width == .regular) { picker(spaces: spaces, current: current) } }
                            if noMatch(current: current) {
                                // Every section the search leaves empty:
                                // name the state in place of the blank.
                                ContentUnavailableView.search(text: query)
                                    .frame(maxWidth: .infinity)
                                    .padding(.top, 40)
                            } else {
                                let hosts = store.hosts.filter { shows($0, hasSpaces: hasSpaces, current: current) }
                                ForEach(arranged(hosts)) { host in
                                    sectionDrop(host, current: current, spaces: spaces)
                                }
                                // A host let go of under the last section
                                // lands at the list's end.
                                Color.clear
                                    .frame(maxWidth: .infinity)
                                    .frame(height: 44)
                                    .contentShape(Rectangle())
                                    .onDrop(of: [.text], delegate: RowDrop(
                                        // Only a host's drag lands here: a
                                        // row's goes to the page's target.
                                        validate: { self.hostDrag != nil },
                                        pass: { passHostToEnd() },
                                        drop: { dropHost($0) }))
                            }
                        }
                    }
                    // The rows the search keeps move to their places, and the
                    // others fade; under Reduce Motion they change at once,
                    // as a fade alone would still move the rows kept.
                    .animation(still ? nil : .snappy, value: query)
                    .padding(.horizontal, 20)
                    .padding(.top, listTopPad)
                    .padding(.bottom, 20)
                }
                // Under the keyboard too: the list keeps clear of it as it
                // comes and goes, as a terminal's does going back, which
                // left the tab's black there.
                .background { Color.page.ignoresSafeArea() }
                .scrollEdgeEffectStyle(.soft, for: .all)
                .refreshable { await refresh() }
                .overlayPreferenceValue(SpacePickerBounds.self) { spacePanelLayer($0, spaces: spaces, current: current) }
            }
        }
        // Demo Mode's banner stays in place as the list scrolls, so that
        // leaving it is always a tap away.
        .safeAreaInset(edge: .top, spacing: 0) {
            if store.demo != nil {
                DemoBanner { go(nil); store.exitDemo() }
                    .background(Color.page)
            }
        }
        // The default keeps the open set across launches and windows:
        // one way for this view's writes, the other for another's.
        .onChange(of: expanded) { _, s in
            expandedText = s.sorted().joined(separator: "\n")
        }
        // The space panel goes with the picker, should its own close not
        // run as Activity takes the picker's place.
        .onChange(of: inbox) { _, _ in
            withAnimation(still ? .easeInOut(duration: 0.15) : .snappy) { spacePanel = false }
        }
        .onChange(of: expandedText) { _, t in
            expanded = Set(t.split(separator: "\n").map(String.init))
        }
        // A row let go of off the cards goes back to its place.
        .onDrop(of: [Lifted.type], delegate: PageDrop())
        // A host let go of off the sections keeps the hosts' old order.
        .onReceive(NotificationCenter.default.publisher(for: Lifted.ended)) { n in
            if let d = hostDrag, n.object as? UUID == d.token { hostDrag = nil }
        }
        .modifier(DragEnd { hostDrag = nil })
        .sensoryFeedback(.selection, trigger: lifts)
        // Beside the terminal the bar stays, for the sidebar's toggle.
        .toolbar(width == .regular ? .automatic : .hidden, for: .navigationBar)
        .toolbar {
            // First on a wide window's sidebar, in the bar's row, whose
            // search and toggle it shares; atop the list on a narrow one.
            ToolbarItem(placement: .topBarLeading) {
                if width == .regular, !inbox, hasSpaces { picker(spaces: spaces, current: current, place: .bar) }
            }
            // An item of its own, for Settings to zoom out of.
            ToolbarItem(placement: .bottomBar) {
                Button("Settings", systemImage: "gearshape") {
                    settingsSource = .settings
                    settings = true
                }
            }
            .matchedTransitionSource(id: ZoomSource.settings, in: zoom)
            ToolbarItem(placement: .bottomBar) {
                InboxToolbarButton(inbox: inbox) {
                    // A tab in both lists moves to its place in the other;
                    // the rest fades, and all of it under Reduce Motion.
                    withAnimation(still ? .easeInOut(duration: 0.15) : .snappy) { inbox.toggle() }
                }
            }
            ToolbarSpacer(.flexible, placement: .bottomBar)
            if width != .regular {
                DefaultToolbarItem(kind: .search, placement: .bottomBar)
            }
        }
        .searchable(text: $query, placement: width == .regular ? .sidebar : .automatic, prompt: "Search tabs")
        .searchToolbarBehavior(.minimize)
        .textInputAutocapitalization(.never)
        .autocorrectionDisabled()
        // A host added from Add a Host takes the button away: Settings
        // closes into its own button.
        .onChange(of: store.hosts.isEmpty) { _, empty in
            if !empty { settingsSource = .settings }
        }
        // A sheet on a Mac loses the injected objects; the store goes
        // back in.
        .sheet(isPresented: $settings, onDismiss: {
            if let c = waiting { pairing = c }
            waiting = nil
        }) { settingsSheet }
    }

    // MARK: Spaces

    /// The space shown, and the panel of the others: atop the list the
    /// list draws it (spacePanelLayer), in the bar a popover does.
    private func picker(spaces: [String], current: String, place: SpacePicker.Place = .list) -> some View {
        SpacePicker(spaces: spaces, current: current, color: { Color(project: book.colors[$0]) },
                    count: projectCount,
                    choose: { s in withAnimation(still ? .easeInOut(duration: 0.15) : .snappy) { book.chosen = s } },
                    add: { spaceName = ""; addingSpace = true },
                    edit: { editingSpaces = true },
                    place: place,
                    panel: place == .list ? $spacePanel : nil)
    }

    /// The panel of the list's picker, over the page, under the picker.
    private func spacePanelLayer(_ anchor: Anchor<CGRect>?, spaces: [String], current: String) -> some View {
        SpacePanelLayer(anchor: anchor, open: $spacePanel, spaces: spaces, current: current,
                        color: { Color(project: book.colors[$0]) },
                        count: projectCount,
                        choose: { s in withAnimation(still ? .easeInOut(duration: 0.15) : .snappy) { book.chosen = s } },
                        add: { spaceName = ""; addingSpace = true },
                        edit: { editingSpaces = true })
    }

    /// A space's projects across the hosts, an away host's as last seen.
    private func projectCount(_ s: String) -> Int {
        store.hosts.reduce(0) { $0 + (listing($1)?.projects.filter { $0.space == s }.count ?? 0) }
    }

    /// Whether a host keeps projects, connected or as last seen: only then
    /// does the list have spaces to pick.
    private var hasSpaces: Bool {
        store.hosts.contains { $0.hasProjects || book.listings[$0.id.uuidString] != nil }
    }

    /// A host's projects as the list shows them: its own while it is
    /// online, else the copy kept of them; nil for a host of before
    /// projects, and for one never seen with any.
    private func listing(_ h: Host) -> ProjectListing? {
        if h.status == .online {
            guard h.hasProjects else { return nil }
            var l = ProjectListing(host: h)
            for i in l.projects.indices { l.projects[i].space = book.space(l.projects[i].space, on: h.id.uuidString) }
            l.offered = l.offered.map { $0.map { book.space($0, on: h.id.uuidString) } }
            return l
        }
        return book.listing(for: h.id.uuidString)
    }

    /// The spaces the hosts' projects carry.
    private var carried: Set<String> {
        store.hosts.reduce(into: Set<String>()) { $0.formUnion(listing($1)?.pickerSpaces ?? []) }
    }

    private var spaces: [String] { book.spaces(carried: carried) }

    /// The space shown: the one chosen while the picker has it, else Default.
    private var current: String {
        Self.current(in: spaces, chosen: book.chosen)
    }

    private static func current(in spaces: [String], chosen: String) -> String {
        spaces.contains(chosen) ? chosen : ""
    }

    /// Whether a host shows in the space: one online or of before
    /// projects always; one away when it keeps a project of the space as
    /// last seen, or when nothing of it was seen, for its state to show.
    private func shows(_ h: Host, hasSpaces: Bool, current: String) -> Bool {
        if h.status == .online || !hasSpaces { return true }
        guard let l = book.listing(for: h.id.uuidString) else { return true }
        return l.spaces.contains(current)
    }

    /// Whether the search leaves nothing under the headers: no row of any
    /// section drawn matches it.
    private func noMatch(current: String) -> Bool {
        !query.isEmpty && !store.hosts.contains { draws($0, current: current) }
    }

    /// Whether a host's section draws a row of its own under the search:
    /// a project it keeps of the space, or a tab, whose name matches; the
    /// search does not filter a host away or one of before projects, so
    /// those always count.
    private func draws(_ h: Host, current: String) -> Bool {
        guard shows(h, hasSpaces: hasSpaces, current: current) else { return false }
        if h.status != .online || !h.hasProjects { return true }
        let key = h.id.uuidString
        return h.sessions.contains { s in
            if s.project == nil {
                return (s.view?.windows ?? []).contains {
                    $0.block != nil && h.title($0).localizedCaseInsensitiveContains(query)
                }
            }
            guard s.project.map({ book.space($0.space, on: key) == current }) ?? false else { return false }
            return s.label.localizedCaseInsensitiveContains(query)
                || (s.view?.windows ?? []).contains { h.title($0).localizedCaseInsensitiveContains(query) }
        }
    }

    /// A host's section of the list, as the body's ForEach builds it.
    /// Its header lifts a drag while the search is out and the host can
    /// move; the demo host's does not.
    private func section(_ host: Host, current: String, spaces: [String]) -> HostSection {
        HostSection(host: host, space: current, spaces: spaces, collapsed: $collapsed, expanded: $expanded,
                    query: query, zoom: zoom, places: places,
                    open: { go($0) }, openRow: { openRow($0) }, newProject: { newProject = host },
                    current: width == .regular ? path.last : nil,
                    liftHost: query.isEmpty && !host.isDemo ? { self.liftHost(host) } : nil,
                    hostDrop: HostDrop(active: { self.hostDrag != nil },
                                       pass: { self.passHost(over: host) },
                                       drop: { self.dropHost($0) }))
    }

    /// The settings sheet's content, of its own for the type-checker.
    private var settingsSheet: some View {
        SettingsView().environment(store).navigationTransition(.zoom(sourceID: settingsSource, in: zoom))
    }

    /// A host's section in the list: the dragged one dims, and each is
    /// its own drop target, through HostDrop (the deepest target at a
    /// point is the one the drop goes to, so a target wrapping the
    /// section would never see it).
    private func sectionDrop(_ host: Host, current: String, spaces: [String]) -> some View {
        section(host, current: current, spaces: spaces)
            .opacity(hostDrag?.host == host.id ? 0.4 : 1)
    }

    private func expand(_ host: Host, _ project: String) {
        expanded.insert(HostSection.key(host, project))
    }

    /// What, changed, makes the copy of a host's projects worth keeping
    /// again: its projects, and their tabs and names; not the terminals'
    /// titles, which change all the time.
    private var listingKey: [String] {
        store.hosts.filter { $0.status == .online && $0.hasProjects && !$0.isDemo }.map { h in
            h.id.uuidString + "|" + h.sessions.map { s in
                let p = s.project.map { "\($0.dir)|\($0.space)|\($0.used?.timeIntervalSince1970 ?? 0)|\($0.color ?? "")" } ?? "-"
                let tabs = (s.view?.windows ?? []).map { $0.window_id + "=" + ($0.name ?? "") }.joined(separator: ",")
                return "\(s.id):\(s.label):\(p):\(tabs)"
            }.joined(separator: ";") + "|" + h.offeredSpaces.joined(separator: ",")
        }
    }

    /// The hosts a rename or deletion of a space goes to: those with
    /// projects, connected or as last seen.
    private var spaceHosts: [String] {
        store.hosts.filter { $0.hasProjects || book.listings[$0.id.uuidString] != nil }.map(\.id.uuidString)
    }

    /// The projects of a space on the hosts connected now.
    private func projects(in space: String) -> Int {
        store.hosts.filter { $0.status == .online }.reduce(0) { n, h in
            n + (listing(h)?.projects.filter { $0.space == space }.count ?? 0)
        }
    }

    /// The renames each host is still to take, and whether it can now.
    private var renameKey: [String] {
        store.hosts.map { "\($0.id):\($0.status == .online && $0.hasProjects):\(book.renames[$0.id.uuidString]?.count ?? 0)" }
    }

    /// Sends each host online the renames and deletions of spaces it is
    /// still to take, in order; one that fails waits, with those after it,
    /// for the host to connect again.
    private func sendRenames() {
        for h in store.hosts where h.status == .online && h.hasProjects && !renaming.contains(h.id) {
            let key = h.id.uuidString
            guard let rs = book.renames[key], !rs.isEmpty else { continue }
            renaming.insert(h.id)
            Task {
                let all = await book.send(to: h)
                renaming.remove(h.id)
                // Renames made while these went wait for no other change.
                if all, book.renames[key]?.isEmpty == false { sendRenames() }
            }
        }
    }

    // MARK: Host Drag

    /// Lifts a host's section, from the order the hosts show: the order
    /// holds every host, the ones the space picked hides too.
    private func liftHost(_ host: Host) -> NSItemProvider {
        // The system asks again as the sections move: the same drag goes on.
        if let d = hostDrag, d.host == host.id, let item = d.item { return item }
        // The string makes the item a text drop for the system to
        // deliver: a mark alone (Lifted.type's way) is only seen by the
        // side that lifts, so a section's text targets take it.
        let item = Lifted(object: host.id.uuidString as NSString)
        var d = HostDrag(host: host.id, order: store.hosts.map(\.id))
        item.token = d.token
        item.mark(d.token, as: Lifted.hostType)
        d.item = item
        hostDrag = d
        lifts += 1
        return item
    }

    /// Makes way for the dragged host's section at another section.
    private func passHost(over host: Host) {
        guard let d = hostDrag,
              let from = d.order.firstIndex(of: d.host), let to = d.order.firstIndex(of: host.id), from != to
        else { return }
        withAnimation(.snappy) { hostDrag?.order.move(fromOffsets: [from], toOffset: to > from ? to + 1 : to) }
    }

    /// Makes way for the dragged host's section at the list's end.
    private func passHostToEnd() {
        guard let d = hostDrag, let from = d.order.firstIndex(of: d.host), from != d.order.count - 1 else { return }
        withAnimation(.snappy) { hostDrag?.order.move(fromOffsets: [from], toOffset: d.order.count) }
    }

    /// Moves the dropped host to where the drag left it.
    private func dropHost(_ items: [NSItemProvider]) -> Bool {
        guard let d = hostDrag else { return false }
        hostDrag = nil
        store.move(toOrder: d.order)
        return true
    }

    /// The sections in the order a drag of one of them leaves.
    private func arranged(_ hosts: [Host]) -> [Host] {
        guard let d = hostDrag else { return hosts }
        return hosts.sorted { (d.order.firstIndex(of: $0.id) ?? .max) < (d.order.firstIndex(of: $1.id) ?? .max) }
    }

    // MARK: New Project

    /// Makes a project of a folder on a host, in the space the sheet
    /// chose, which the list shows then, and goes to its first tab; a
    /// folder that is a project already shows that one, or offers to move
    /// it into the space chosen.
    private func createProject(on host: Host, dir: String, name: String, space: String) {
        Task {
            do {
                switch try await host.createProject(name: name, dir: dir, space: space) {
                case .made(let project, let tab):
                    book.chosen = space
                    expand(host, project)
                    if let block = try await host.block(ofTab: tab, in: project) {
                        go(TerminalTarget(host: host.id, session: project, block: block, title: ""))
                    }
                case .exists(let project):
                    let s = host.sessions.first { $0.id == project }
                    let there = book.space(s?.project?.space ?? space, on: host.id.uuidString)
                    if there == space {
                        book.chosen = space
                        expand(host, project)
                    } else {
                        existing = Existing(host: host, project: project, name: s?.label ?? name, space: there, into: space)
                    }
                }
            } catch {
                Toasts.shared.fail("Can't Make the Project", error)
            }
        }
    }

    /// Moves a project New Project found into the space the sheet chose.
    private func moveHere(_ e: Existing) {
        Task {
            do {
                try await e.host.updateProject(e.project, space: e.into)
                book.chosen = e.into
                expand(e.host, e.project)
            } catch {
                Toasts.shared.fail("Can't Move the Project", error)
            }
        }
    }
}

/// The picker atop the list: on a narrow window, and on a wide one while
/// the search is out, which takes the bar's whole row and filters the list
/// below.
private struct ListPicker<Content: View>: View {
    let wide: Bool
    @ViewBuilder let content: Content
    @Environment(\.isSearching) private var searching

    var body: some View {
        if !wide || searching { content }
    }
}

/// What the list shows before any host is added: how Rex Go reaches a
/// computer, and Demo Mode's way in.
private struct NoHostsView: View {
    /// Where Add a Host is the source Settings zooms out of.
    let zoom: Namespace.ID
    let tryDemo: () -> Void
    let addHost: () -> Void

    var body: some View {
        VStack(spacing: 16) {
            Image(systemName: "terminal")
                .font(.system(size: 46, weight: .light))
                .foregroundStyle(.secondary)
                .padding(.bottom, 2)
            Text("No Hosts Yet")
                .font(.title2.bold())
            Text("Rex Go connects to the computers where GoRex runs. Try the demo to explore the app before you add one.")
                .font(.callout)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 420)
            VStack(spacing: 12) {
                Button { tryDemo() } label: {
                    Label("Try Demo Mode", systemImage: "play.fill")
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.glassProminent)
                // The prominent glass is the accent, which is white in the
                // dark scheme: its label takes the page's color, as the
                // app's other prominent buttons do.
                .foregroundStyle(Color.page)
                .controlSize(.large)
                Button { addHost() } label: {
                    Text("Add a Host")
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.glass)
                .controlSize(.large)
                .matchedTransitionSource(id: ZoomSource.addHost, in: zoom)
            }
            .frame(maxWidth: 360)
            .padding(.top, 6)
        }
        .frame(maxWidth: .infinity)
        .padding(.horizontal, 20)
    }
}

/// The list's notice that the windows are Demo Mode's, and its way out.
private struct DemoBanner: View {
    let exit: () -> Void

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: "sparkles")
                .font(.body.weight(.semibold))
                .foregroundStyle(.tint)
            VStack(alignment: .leading, spacing: 2) {
                Text("Demo Mode")
                    .font(.subheadline.weight(.semibold))
                Text("You're exploring a demo Mac. Nothing is connected.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(3)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 8)
            Button("Exit Demo") { exit() }
                .font(.subheadline.weight(.semibold))
                .buttonStyle(.glass)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 10)
        .background(.fill.quaternary, in: .rect(cornerRadius: 18, style: .continuous))
        .padding(.horizontal, 20)
        .padding(.top, 8)
        .padding(.bottom, 4)
    }
}

/// What a sheet zooms out of (docs/IOS-011-continuous-transitions.md).
enum ZoomSource: Hashable {
    case settings
    case addHost
    /// A host's New Project button.
    case newProject(UUID)
}

/// A tab's place in All Tabs and in Activity, by its window: its row
/// moves from one list to the other.
struct RowPlace: Hashable {
    let host: UUID
    let session: String
    let window: String
}

/// A row's id in the lists' namespace: its place, which the other list
/// shares, or one of its own in its list, which nothing matches.
enum RowMatch: Hashable {
    case place(RowPlace)
    case tab(RowPlace)
    case inbox(String)
}

/// Moves a row to its place in the other list; a row with no place, or
/// any row under Reduce Motion, fades. One effect for both, so a change of
/// Reduce Motion keeps the rows.
struct MatchedPlace: ViewModifier {
    let place: RowPlace?
    /// The row's id when it fades.
    let own: RowMatch
    let namespace: Namespace.ID
    @Environment(\.accessibilityReduceMotion) private var still

    func body(content: Content) -> some View {
        content.matchedGeometryEffect(id: still ? own : place.map(RowMatch.place) ?? own, in: namespace)
    }
}

/// A row as a drop target: the dragged row makes way at it, and lands on
/// the drop. A target that takes a drop only in a state, like a host's
/// while its drag is on, says so in validate.
struct RowDrop: DropDelegate {
    /// The type the target takes its items as: the tab rows' text.
    var type: UTType = .text
    /// Whether the target takes a drop now; yes by default.
    var validate: () -> Bool = { true }
    let pass: () -> Void
    let drop: ([NSItemProvider]) -> Bool

    func validateDrop(info: DropInfo) -> Bool { validate() }
    func dropEntered(info: DropInfo) { pass() }
    func dropUpdated(info: DropInfo) -> DropProposal? { DropProposal(operation: .move) }
    func performDrop(info: DropInfo) -> Bool { drop(info.itemProviders(for: [type])) }
}

/// The page as a drop target, under the cards', for rows only: a row let
/// go of there, rather than cancelled, tells its card on iOS 26 too, where
/// no call says when a drag ends.
struct PageDrop: DropDelegate {
    /// Text from elsewhere is not the page's, whatever types it offers.
    func validateDrop(info: DropInfo) -> Bool {
        info.itemProviders(for: [Lifted.type]).contains { $0.registeredTypeIdentifiers.contains(Lifted.type.identifier) }
    }

    func dropUpdated(info: DropInfo) -> DropProposal? { DropProposal(operation: .move) }
    func performDrop(info: DropInfo) -> Bool {
        Self.missed(info.itemProviders(for: [Lifted.type]))
        return true
    }

    /// Tells a dragged row's card, by the drag's token its item carries,
    /// that the row was let go of off its rows.
    static func missed(_ items: [NSItemProvider]) {
        for item in items where item.hasItemConformingToTypeIdentifier(Lifted.type.identifier) {
            _ = item.loadDataRepresentation(forTypeIdentifier: Lifted.type.identifier) { data, _ in
                let token = data.flatMap { UUID(uuidString: String(decoding: $0, as: UTF8.self)) }
                guard let token else { return }
                DispatchQueue.main.async { NotificationCenter.default.post(name: Lifted.ended, object: token) }
            }
        }
    }
}

/// Tells when a drag from the view ends, where the system says so (iOS
/// 27). Only the iOS 27 SDK (Swift 6.4, Xcode 27) has the call; built
/// with an older one, the item's release alone tells.
struct DragEnd: ViewModifier {
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
nonisolated final class Lifted: NSItemProvider, @unchecked Sendable {
    static let ended = Notification.Name("GoRex.Lifted.ended")
    /// A row's drag, which only the app's own targets take.
    static let type = UTType(exportedAs: "dev.gorex.window-row")
    /// A host's drag, under which its item's token is marked: the drop
    /// side sees only the string's text, and the sections' targets know
    /// the drag is on from HostDrop.
    static let hostType = UTType(exportedAs: "dev.gorex.host")
    var token: UUID?

    /// Carries the drag's token under the type given, for this app only.
    func mark(_ token: UUID, as type: UTType = Lifted.type) {
        registerDataRepresentation(forTypeIdentifier: type.identifier, visibility: .ownProcess) { done in
            done(Data(token.uuidString.utf8), nil)
            return nil
        }
    }

    deinit {
        let token = token
        DispatchQueue.main.async { NotificationCenter.default.post(name: Lifted.ended, object: token) }
    }
}

/// A window's row: the tiles of its programs, its title, and a mark at
/// the end.
struct WindowRow<Mark: View>: View {
    let host: Host
    let block: String
    let window: SessionView.Window
    let tiles: CGFloat
    let divider: Bool
    /// Whether the tiles keep the white ring of a light page.
    var plate = true
    @ViewBuilder let mark: Mark

    var body: some View {
        let title = host.title(window)
        let status = host.status([block] + window.panes)
        let agentActive = host.windowAgentState(window) != nil
        HStack(spacing: 14) {
            ProgramTiles(front: host.blocks[block]?.program, back: window.back.map { host.blocks[$0]?.program }, state: status?.state, plate: plate).frame(width: tiles, height: 16 * ProgramTiles.scale + 4, alignment: .leading)
            Text(title.isEmpty ? " " : title.asText)
                // Regular and a little dimmed, as Rex sets a window's title.
                .font(.body)
                .foregroundStyle(.primary.opacity(0.8))
                .lineLimit(1)
                .truncationMode(.tail)
                .accessibilityLabel(Text(AgentHighlight.accessibilityLabel(for: title.asText, active: agentActive)))
            Spacer(minLength: 8)
            AgentWindowBadge(host: host, window: window)
            // As the desktop's tab: a dot while the program waits for the
            // user or failed, else its progress or animated dots while working.
            ProgramStatusMark(state: status?.state, progress: status?.progress)
            mark
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 11)
        .frame(minHeight: 50)
        .contentShape(Rectangle())
        .modifier(AgentVoiceOverModifier(host: host, window: window, title: title.asText))
        .overlay(alignment: .bottom) {
            if divider {
                Rectangle().fill(Color.primary.opacity(0.12)).frame(height: 1 / 3).padding(.leading, 16 + tiles + 14)
            }
        }
    }
}
