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
/// sidebar (docs/projects.md, Rex Go); a host of before projects lists its
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
    /// launches.
    @AppStorage("expanded") private var expandedText = ""
    @AppStorage("developerMode") private var developerMode = false
    @State private var inbox = false
    @State private var query = ""
    @State private var settings = false
    /// The GoRex machine of a Rex Go code opened from outside, to pair with;
    /// waiting is one that waits for Settings to close first.
    @State private var pairing: HostConfig?
    @State private var waiting: HostConfig?
    /// The host whose New Project sheet is open.
    @State private var newProject: Host?
    @State private var addingSpace = false
    @State private var spaceName = ""
    @State private var editingSpaces = false
    /// The project New Project found the folder to be already, in another
    /// space: to move it here, or to show it there.
    @State private var existing: Existing?
    /// The hosts taking their renames of spaces now.
    @State private var renaming: Set<UUID> = []
    /// What failed last, and why, as the server says it.
    @State private var failure: (title: String, why: String)?

    struct Existing {
        let host: Host
        let project: String
        let name: String
        let space: String
    }

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
                        ContentUnavailableView("No Tab Open", systemImage: "terminal",
                                               description: Text("Choose a tab in the list."))
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
        // And let go of on the bars.
        .onDrop(of: [Lifted.type], delegate: PageDrop())
        .overlay(alignment: .top) {
            if developerMode {
                DeveloperModeOverlay()
                    .padding(.top, (!path.isEmpty || width == .regular) ? 58 : (store.demo != nil ? 48 : 8))
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
            ProjectSheet(host: host, space: SpaceName.shown(current)) { dir, name in createProject(on: host, dir: dir, name: name) }
        }
        .sheet(isPresented: $editingSpaces) {
            EditSpaces(spaces: spaces, carried: carried, projects: projects(in:)) { from, to in
                book.rename(from, to: to, on: spaceHosts)
            } delete: { name in
                book.delete(name, on: spaceHosts)
            }
        }
        .alert("New Space", isPresented: $addingSpace) {
            TextField("Name", text: $spaceName)
            Button("Cancel", role: .cancel) {}
            Button("Add") { book.add(spaceName, carried: carried) }
        } message: {
            Text("The space stays here, empty, until a project moves into it.")
        }
        .alert(existing.map { String(localized: "\($0.name) Is a Project Already") } ?? "", isPresented: Binding { existing != nil } set: { if !$0 { existing = nil } }, presenting: existing) { e in
            Button("Move to \(SpaceName.shown(current))") { moveHere(e) }
            Button("Show in \(SpaceName.shown(e.space))") {
                book.chosen = e.space
                expand(e.host, e.project)
            }
            Button("Cancel", role: .cancel) {}
        } message: { e in
            Text("Its folder is a project of \(e.host.config.name) in \(SpaceName.shown(e.space)).")
        }
        .alert(failure?.title ?? "", isPresented: Binding { failure != nil } set: { if !$0 { failure = nil } }) {
        } message: {
            Text(failure?.why ?? "")
        }
    }

    /// Opens a terminal, in place of the one open; nil goes back to the list.
    private func go(_ t: TerminalTarget?) {
        path = t.map { [$0] } ?? []
    }

    private var list: some View {
        Group {
            if store.hosts.isEmpty {
                // As tall as the page, so that the welcome sits in its
                // middle, above the keyboard or the bottom bar.
                NoHostsView(tryDemo: { store.enterDemo() }, addHost: { settings = true })
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background { Color.page.ignoresSafeArea() }
            } else {
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 30) {
                        if inbox {
                            StatusInbox(query: query, collapsed: $collapsed, open: { go($0) })
                        } else {
                            if hasSpaces { ListPicker(wide: width == .regular) { picker() } }
                            ForEach(store.hosts.filter(shows)) { host in
                                HostSection(host: host, space: current, spaces: spaces, collapsed: $collapsed, expanded: expanded,
                                            query: query, open: { go($0) }, newProject: { newProject = host })
                            }
                        }
                    }
                    .padding(.horizontal, 20)
                    .padding(.top, 12)
                    .padding(.bottom, 20)
                }
                // Under the keyboard too: the list keeps clear of it as it
                // comes and goes, as a terminal's does going back, which
                // left the tab's black there.
                .background { Color.page.ignoresSafeArea() }
                .scrollEdgeEffectStyle(.soft, for: .all)
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
        // A row let go of off the cards goes back to its place.
        .onDrop(of: [Lifted.type], delegate: PageDrop())
        // Beside the terminal the bar stays, for the sidebar's toggle.
        .toolbar(width == .regular ? .automatic : .hidden, for: .navigationBar)
        .toolbar {
            // First on a wide window's sidebar, in the bar's row, whose
            // search and toggle it shares; atop the list on a narrow one.
            ToolbarItem(placement: .topBarLeading) {
                if width == .regular, !inbox, hasSpaces { picker(place: .bar) }
            }
            ToolbarItemGroup(placement: .bottomBar) {
                Button("Settings", systemImage: "gearshape") { settings = true }
                Button(inbox ? "Show All Tabs" : "Show Activity", systemImage: inbox ? "eye.slash" : "eye") {
                    inbox.toggle()
                }
            }
            ToolbarSpacer(.flexible, placement: .bottomBar)
            DefaultToolbarItem(kind: .search, placement: .bottomBar)
        }
        .searchable(text: $query, prompt: "Search tabs")
        .searchToolbarBehavior(.minimize)
        .textInputAutocapitalization(.never)
        .autocorrectionDisabled()
        .sheet(isPresented: $settings, onDismiss: {
            if let c = waiting { pairing = c }
            waiting = nil
        }) { SettingsView() }
    }

    // MARK: Spaces

    /// The space shown, and the menu of the others.
    private func picker(place: SpacePicker.Place = .list) -> some View {
        SpacePicker(spaces: spaces, current: current, color: { Color(project: book.colors[$0]) },
                    choose: { s in withAnimation(.snappy) { book.chosen = s } },
                    add: { spaceName = ""; addingSpace = true },
                    edit: { editingSpaces = true },
                    place: place)
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
        spaces.contains(book.chosen) ? book.chosen : ""
    }

    /// Whether a host shows in the space: one online or of before
    /// projects always; one away when it keeps a project of the space as
    /// last seen, or when nothing of it was seen, for its state to show.
    private func shows(_ h: Host) -> Bool {
        if h.status == .online || !hasSpaces { return true }
        guard let l = book.listing(for: h.id.uuidString) else { return true }
        return l.spaces.contains(current)
    }

    /// The projects open in the list, and the hosts' Older, by
    /// HostSection's keys.
    private var expanded: Binding<Set<String>> {
        Binding {
            Set(expandedText.split(separator: "\n").map(String.init))
        } set: {
            expandedText = $0.sorted().joined(separator: "\n")
        }
    }

    private func expand(_ host: Host, _ project: String) {
        expanded.wrappedValue.insert(HostSection.key(host, project))
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

    // MARK: New Project

    /// Makes a project of a folder on a host, in the space shown, and goes
    /// to its first tab; a folder that is a project already shows that
    /// one, or offers to move it here from another space.
    private func createProject(on host: Host, dir: String, name: String) {
        let space = current
        Task {
            do {
                switch try await host.createProject(name: name, dir: dir, space: space) {
                case .made(let project, let tab):
                    expand(host, project)
                    if let block = try await host.block(ofTab: tab, in: project) {
                        go(TerminalTarget(host: host.id, session: project, block: block, title: ""))
                    }
                case .exists(let project):
                    let s = host.sessions.first { $0.id == project }
                    let there = book.space(s?.project?.space ?? space, on: host.id.uuidString)
                    if there == space {
                        expand(host, project)
                    } else {
                        existing = Existing(host: host, project: project, name: s?.label ?? name, space: there)
                    }
                }
            } catch {
                failure = (String(localized: "Can't Make the Project"), error.localizedDescription)
            }
        }
    }

    /// Moves a project New Project found into the space shown.
    private func moveHere(_ e: Existing) {
        let space = current
        Task {
            do {
                try await e.host.updateProject(e.project, space: space)
                expand(e.host, e.project)
            } catch {
                failure = (String(localized: "Can't Move the Project"), error.localizedDescription)
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

/// A row as a drop target: the dragged row makes way at it, and lands on
/// the drop.
struct RowDrop: DropDelegate {
    let pass: () -> Void
    let drop: ([NSItemProvider]) -> Bool

    func dropEntered(info: DropInfo) { pass() }
    func dropUpdated(info: DropInfo) -> DropProposal? { DropProposal(operation: .move) }
    func performDrop(info: DropInfo) -> Bool { drop(info.itemProviders(for: [.text])) }
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
    var token: UUID?

    /// Carries the drag's token, for this app only.
    func mark(_ token: UUID) {
        registerDataRepresentation(forTypeIdentifier: Self.type.identifier, visibility: .ownProcess) { done in
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
    @ViewBuilder let mark: Mark

    var body: some View {
        let title = host.title(window)
        let status = host.status([block] + window.panes)
        HStack(spacing: 14) {
            ProgramTiles(front: host.blocks[block]?.program, back: window.back.map { host.blocks[$0]?.program }, state: status?.state).frame(width: tiles, height: 16 * ProgramTiles.scale + 4, alignment: .leading)
            Text(title.isEmpty ? " " : title.asText)
                // Regular and a little dimmed, as Rex sets a window's title.
                .font(.body)
                .foregroundStyle(.primary.opacity(0.8))
                .lineLimit(1)
                .truncationMode(.tail)
            Spacer(minLength: 8)
            // As the desktop's tab: a dot while the program waits for the
            // user or failed, else its progress or animated dots while working.
            ProgramStatusMark(state: status?.state, progress: status?.progress)
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
