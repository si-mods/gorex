import SwiftUI
import UniformTypeIdentifiers

extension Color {
    static let page = Color(UIColor { $0.userInterfaceStyle == .dark
        ? UIColor(red: 0x22 / 255, green: 0x25 / 255, blue: 0x2F / 255, alpha: 1) : .systemGroupedBackground })
    static let card = Color(UIColor { $0.userInterfaceStyle == .dark
        ? UIColor(red: 0x36 / 255, green: 0x3A / 255, blue: 0x47 / 255, alpha: 1) : .secondarySystemGroupedBackground })
}

/// Every host, its sessions, and their windows, as Rex's iPhone app lists
/// them, or, as the desktop's sidebar, one space at a time, its windows by
/// host. The eye lists instead the windows by what their programs need.
/// A wide window shows the list beside the terminal open; a narrow one
/// pushes the terminal over the list.
struct HostListView: View {
    @Environment(HostStore.self) private var store
    @Environment(SpaceStore.self) private var spaceStore
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
    @State private var renamingSpace: CentralSpace?
    @State private var spaceName = ""
    /// The project sheet open: New Project, or a project's Change Folder.
    @State private var projectSheet: ProjectSheetMode?
    /// Why a project's new window did not open, and whether changing its
    /// folder is the way on.
    @State private var projectFailure: ProjectFailure?
    /// The project whose new window is on its way.
    @State private var openingProject: String?
    @AppStorage(Grouping.key) private var grouping = Grouping.standard
    /// The space chosen, by its centralized `CentralSpace.id` or `"__unassigned__"`.
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
        // And let go of on the bars.
        .onDrop(of: [Lifted.type], delegate: PageDrop())
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
        // A host gone, as Demo Mode's, closes the terminals open on it.
        .onChange(of: store.hosts.map(\.id)) { _, ids in
            path.removeAll { !ids.contains($0.host) }
        }
        .onChange(of: syncFingerprint, initial: true) { _, _ in
            spaceStore.sync(from: store.hosts.map(\.spaceSnapshot))
            pushSpacesToDesktop()
        }
        .sheet(item: $projectSheet) { mode in
            ProjectSheet(mode: mode) { host, dir, name in
                switch mode {
                case .new:
                    if let host, let dir {
                        createProject(on: host, dir: dir, name: name)
                    } else {
                        // A project that only groups windows, as a space did.
                        let created = spaceStore.createSpace(name: name)
                        chosen = created.id
                    }
                case .change(let id):
                    if let host, let dir { changeFolder(id, on: host, dir: dir) }
                }
            }
        }
        .alert(projectFailure?.title ?? "", isPresented: Binding { projectFailure != nil } set: { if !$0 { projectFailure = nil } }, presenting: projectFailure) { f in
            if let id = f.changeFolder {
                Button("Change Folder…") { projectSheet = .change(id) }
                Button("Cancel", role: .cancel) {}
            } else {
                Button("OK", role: .cancel) {}
            }
        } message: { f in
            Text(f.why)
        }
        .alert("Rename Project", isPresented: Binding { renamingSpace != nil } set: { if !$0 { renamingSpace = nil } }, presenting: renamingSpace) { s in
            TextField(s.name, text: $spaceName)
            Button("Cancel", role: .cancel) {}
            Button("Save") {
                spaceStore.renameSpace(s.id, to: spaceName)
                pushSpacesToDesktop()
            }
        }
    }

    private func pushSpacesToDesktop() {
        guard spaceStore.desktopSyncEnabled else { return }
        let all = spaceStore.orderedSpaces
        let primary = spaceStore.primaryHostID
        let targets = store.hosts.filter { h in
            h.supportsSpacesSync && (primary.isEmpty || h.id.uuidString == primary)
        }
        guard !targets.isEmpty else { return }
        Task {
            for h in targets { await h.applySpaces(all) }
            spaceStore.markDesktopSynced()
        }
    }

    private var syncFingerprint: [String] {
        store.hosts.map { h in
            let s = h.sessions.map { "\($0.id):\($0.label):\($0.view?.revision ?? -1):\((h.remote[$0.id] ?? []).count):\($0.project?.dir ?? "")" }.joined(separator: ",")
            return "\(h.id.uuidString):\(h.status == .online):\(h.spaceMachineKey):\(s)"
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
                        } else if grouping == .spaces {
                            let all = spaceStore.orderedSpaces
                            let hasUnassigned = hasUnassignedWindows
                            let selection = resolvedSelection(all: all, hasUnassigned: hasUnassigned)
                            strip(all, hasUnassigned: hasUnassigned, selection: selection)
                            if case .space(let id) = selection, let s = spaceStore.space(id: id) {
                                projectHeader(s)
                            }
                            ForEach(sections(selection)) { host in
                                HostSection(host: host, space: selection, collapsed: $collapsed, filter: filter, open: { go($0) })
                            }
                        } else {
                            ForEach(store.hosts) { host in
                                HostSection(host: host, space: nil, collapsed: $collapsed, filter: filter, open: { go($0) })
                            }
                        }
                    }
                    .padding(.horizontal, 20)
                    .padding(.top, 12)
                    .padding(.bottom, 20)
                }
                // Under the keyboard too: the list keeps clear of it as it
                // comes and goes, as a terminal's does going back, which
                // left the window's black there.
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

    private static let unassignedKey = "__unassigned__"

    private var hasUnassignedWindows: Bool {
        store.hosts.contains { h in
            h.status == .online && h.sessions.contains { s in
                (s.view?.windows ?? []).contains { spaceStore.isUnassigned(machine: h.spaceMachineKey, window: $0) }
            }
        }
    }

    private func resolvedSelection(all: [CentralSpace], hasUnassigned: Bool) -> SpaceSelection? {
        if chosen == Self.unassignedKey, hasUnassigned { return .unassigned }
        if let found = all.first(where: { $0.id == chosen }) { return .space(found.id) }
        if let first = all.first { return .space(first.id) }
        if hasUnassigned { return .unassigned }
        return nil
    }

    private func hostHasWindows(_ host: Host, in selection: SpaceSelection) -> Bool {
        let m = host.spaceMachineKey
        return host.sessions.contains { s in
            (s.view?.windows ?? []).contains { w in
                switch selection {
                case .space(let id): spaceStore.contains(spaceID: id, machine: m, window: w)
                case .unassigned: spaceStore.isUnassigned(machine: m, window: w)
                }
            }
        }
    }

    /// The hosts a space lists, in the hosts' order: offline/connecting hosts
    /// always appear so their status is visible; online hosts appear if they
    /// hold windows in the selected space, or if the space is currently empty
    /// across all online hosts (so the user can open a new terminal in it).
    private func sections(_ selection: SpaceSelection?) -> [Host] {
        guard let selection else { return store.hosts }
        let anyOnlineMatch = store.hosts.contains { $0.status == .online && hostHasWindows($0, in: selection) }
        return store.hosts.filter { host in
            if host.status != .online || !anyOnlineMatch { return true }
            return hostHasWindows(host, in: selection)
        }
    }

    private func strip(_ all: [CentralSpace], hasUnassigned: Bool, selection: SpaceSelection?) -> some View {
        ScrollViewReader { proxy in
            ScrollView(.horizontal) {
                GlassEffectContainer(spacing: 8) {
                    HStack(spacing: 8) {
                        ForEach(all) { s in
                            let sel = SpaceSelection.space(s.id)
                            let isChosen = sel == selection
                            Button { withAnimation(.snappy) { chosen = s.id } } label: {
                                // The folder under the name, as the
                                // desktop's sidebar header shows it.
                                VStack(spacing: 0) {
                                    Text(verbatim: s.name).lineLimit(1)
                                    if let f = s.folder {
                                        Text(verbatim: ProjectWindow.short(f))
                                            .font(.caption2)
                                            .lineLimit(1)
                                            .truncationMode(.head)
                                            .frame(maxWidth: 160)
                                            .opacity(0.7)
                                    }
                                }
                                    .foregroundStyle(isChosen ? Color(.systemBackground) : .primary)
                                    .padding(.horizontal, 16)
                                    .frame(minHeight: 42)
                                    .glassEffect(isChosen ? .regular.tint(.accentColor).interactive() : .regular.interactive(), in: .capsule)
                                    .glassEffectID(isChosen ? "chosen" : s.id, in: glass)
                            }
                            .buttonStyle(.plain)
                            .accessibilityAddTraits(isChosen ? .isSelected : [])
                            .contextMenu {
                                Button("Rename Project…", systemImage: "pencil") {
                                    spaceName = s.name
                                    renamingSpace = s
                                }
                                Button(s.folder == nil ? "Choose Folder…" : "Change Folder…", systemImage: "folder") {
                                    projectSheet = .change(s.id)
                                }
                                if s.folder != nil {
                                    Button("Remove Folder", systemImage: "folder.badge.minus") { removeFolder(s.id) }
                                }
                                Button("Delete Project", systemImage: "trash", role: .destructive) {
                                    spaceStore.deleteSpace(s.id)
                                    if chosen == s.id { chosen = "" }
                                }
                            }
                            .id(sel)
                        }
                        if hasUnassigned {
                            let sel = SpaceSelection.unassigned
                            let isChosen = sel == selection
                            Button { withAnimation(.snappy) { chosen = Self.unassignedKey } } label: {
                                Text("Unassigned").lineLimit(1)
                                    .foregroundStyle(isChosen ? Color(.systemBackground) : .secondary)
                                    .padding(.horizontal, 16)
                                    .frame(height: 42)
                                    .glassEffect(isChosen ? .regular.tint(.accentColor).interactive() : .regular.interactive(), in: .capsule)
                                    .glassEffectID(isChosen ? "chosen" : Self.unassignedKey, in: glass)
                            }
                            .buttonStyle(.plain)
                            .accessibilityAddTraits(isChosen ? .isSelected : [])
                            .id(sel)
                        }
                        Button { projectSheet = .new } label: {
                            Image(systemName: "plus")
                                .frame(width: 42, height: 42)
                                .glassEffect(.regular.interactive(), in: .circle)
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel("New Project")
                    }
                    .font(.subheadline.weight(.semibold))
                }
            }
            .scrollIndicators(.hidden)
            .scrollClipDisabled()
            .onChange(of: selection, initial: true) { old, p in
                guard let p else { return }
                if old == p { proxy.scrollTo(p) } else { withAnimation { proxy.scrollTo(p) } }
            }
        }
    }

    /// The chosen project's folder and home host, under the strip, with its
    /// New Window; a project with no folder offers to choose one.
    private func projectHeader(_ s: CentralSpace) -> some View {
        HStack(spacing: 12) {
            Image(systemName: s.folder == nil ? "folder.badge.questionmark" : "folder")
                .font(.title3)
                .foregroundStyle(.secondary)
                .frame(width: 28)
            VStack(alignment: .leading, spacing: 2) {
                if let f = s.folder {
                    Text(verbatim: ProjectWindow.short(f))
                        .font(.subheadline.monospaced())
                        .lineLimit(1)
                        .truncationMode(.head)
                    Text(verbatim: homeName(s) ?? String(localized: "Home host not added"))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                } else {
                    Text("No Folder").font(.subheadline)
                    Text("Choose one to open new windows in it.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }
            }
            Spacer(minLength: 8)
            if s.folder != nil {
                Button { newProjectWindow(s) } label: {
                    Group {
                        if openingProject == s.id {
                            ProgressView()
                        } else {
                            HStack(spacing: 6) {
                                Image(systemName: "plus")
                                Text("New Window")
                            }
                        }
                    }
                    .font(.subheadline.weight(.semibold))
                    .lineLimit(1)
                    .fixedSize()
                }
                .buttonStyle(.glass)
                .disabled(openingProject == s.id)
            } else {
                Button("Choose Folder…") { projectSheet = .change(s.id) }
                    .font(.subheadline.weight(.semibold))
                    .buttonStyle(.glass)
            }
        }
        .padding(.horizontal, 4)
        .padding(.top, -14)
    }

    /// The name of a project's home host, nil when no host of its machine
    /// is added here.
    private func homeName(_ s: CentralSpace) -> String? {
        store.hosts.first { $0.spaceMachineKey == s.home_machine }?.config.name
    }

    private var candidates: [ProjectWindow.Candidate] {
        store.hosts.map {
            .init(id: $0.id, name: $0.config.name, machine: $0.spaceMachineKey, online: $0.status == .online, sessions: $0.sessions)
        }
    }

    /// The machine a host's server takes as a project's home: its own, or
    /// Demo Mode's key, which has none.
    private func homeMachine(of host: Host) -> String? {
        host.machine ?? (host.isDemo ? host.spaceMachineKey : nil)
    }

    /// Opens a new window of a project with a folder on its home host, in
    /// the folder; nowhere else when the home host can't be reached.
    private func newProjectWindow(_ s: CentralSpace) {
        let cant = String(localized: "Can't Open a New Window")
        switch ProjectWindow.plan(s, hosts: candidates) {
        case .noFolder:
            projectSheet = .change(s.id)
        case .unknownHost:
            projectFailure = .init(title: cant, why: String(localized: "The home host of \(s.name) is not added on this device. Add it in Settings, or change the project's folder."), changeFolder: s.id)
        case .offline(_, let name):
            projectFailure = .init(title: cant, why: String(localized: "\(name), the home host of \(s.name), is offline. New windows of the project open only there."), changeFolder: nil)
        case .window(let id, let session, let cwd):
            guard let host = store.host(id) else { return }
            openProject(s, on: host) {
                // A cwd the server would leave for home when it is gone:
                // checked first, as the server checks a folder it keeps.
                if let cwd { try await ensureFolder(cwd, on: host) }
                return try await host.newWindow(in: session, cwd: cwd).map { (session, $0) }
            }
        case .session(let id, let label, let cwd):
            guard let host = store.host(id) else { return }
            let machine = homeMachine(of: host)
            openProject(s, on: host) {
                try await ensureFolder(cwd, on: host)
                guard let (session, block) = try await host.newSession(label: label, cwd: cwd) else { return nil }
                if host.supportsProjects, let machine {
                    try await host.setHome(session: session, machine: machine, dir: cwd)
                }
                return (session, block)
            }
        }
    }

    /// Fails as the server's folder_missing when the host lists folders and
    /// dir is not one it can read; a host of before gorex.dir_list is not
    /// asked.
    private func ensureFolder(_ dir: String, on host: Host) async throws {
        guard host.listsFolders else { return }
        try await ProjectWindow.ensureFolder(dir) { try await host.folders($0) }
    }

    /// Runs what opens a project's window, puts the window in the project
    /// and goes to it; tells why it failed, a folder gone with a way to
    /// change it.
    private func openProject(_ s: CentralSpace, on host: Host, _ body: @escaping () async throws -> (String, String)?) {
        openingProject = s.id
        Task {
            defer { openingProject = nil }
            do {
                guard let (session, block) = try await body() else { return }
                spaceStore.assign(machine: host.spaceMachineKey, block: block, window: nil, to: s.id)
                let moved = await host.applySpaces(spaceStore.orderedSpaces)
                let sid = moved ? await host.resolveSession(forBlock: block, fallback: session) : session
                go(TerminalTarget(host: host.id, session: sid, block: block, title: ""))
            } catch let error where ProjectWindow.folderMissing(error) {
                projectFailure = .init(title: String(localized: "Folder Not Found"),
                                       why: String(localized: "\(s.folder ?? "") is not on \(host.config.name) any more. Change the project's folder to open a new window."),
                                       changeFolder: s.id)
            } catch {
                projectFailure = .init(title: String(localized: "Can't Open a New Window"), why: error.localizedDescription, changeFolder: nil)
            }
        }
    }

    /// Makes a project of a folder on a host: the host keeps its home, and
    /// its first window opens there, in the folder.
    private func createProject(on host: Host, dir: String, name: String) {
        let s = spaceStore.createSpace(name: name, homeMachine: host.spaceMachineKey, dir: dir)
        withAnimation(.snappy) { chosen = s.id }
        let machine = homeMachine(of: host)
        openProject(s, on: host) {
            try await ensureFolder(dir, on: host)
            let existing = host.sessions.first {
                $0.id != SpaceStore.desktopTerminalsSessionID
                    && $0.label.trimmingCharacters(in: .whitespaces).caseInsensitiveCompare(s.name) == .orderedSame
            }
            let opened: (String, String)?
            if let existing {
                opened = try await host.newWindow(in: existing.id, cwd: dir).map { (existing.id, $0) }
            } else {
                opened = try await host.newSession(label: s.name, cwd: dir)
            }
            guard let opened else { return nil }
            if host.supportsProjects, let machine {
                try await host.setHome(session: opened.0, machine: machine, dir: dir)
            }
            return opened
        }
    }

    /// Gives a project another home: this device's manifest, the hosts that
    /// keep a session of it, and, by the desktop sync, the other desktops.
    private func changeFolder(_ id: String, on host: Host, dir: String) {
        spaceStore.setHome(id, machine: host.spaceMachineKey, dir: dir)
        setHomeOnHosts(id, machine: homeMachine(of: host) ?? host.spaceMachineKey, dir: dir)
    }

    private func removeFolder(_ id: String) {
        spaceStore.setHome(id, machine: "", dir: "")
        setHomeOnHosts(id, machine: "", dir: "")
    }

    /// Sets the home on each online host that keeps a session of the
    /// project, then sends the manifest to the desktops.
    private func setHomeOnHosts(_ id: String, machine: String, dir: String) {
        guard let s = spaceStore.space(id: id) else { return }
        let targets: [(Host, String)] = store.hosts.compactMap { h in
            guard h.status == .online, h.supportsProjects else { return nil }
            return h.sessions.first {
                $0.id != SpaceStore.desktopTerminalsSessionID
                    && $0.label.trimmingCharacters(in: .whitespaces).caseInsensitiveCompare(s.name) == .orderedSame
            }.map { (h, $0.id) }
        }
        Task {
            for (h, session) in targets {
                do { try await h.setHome(session: session, machine: machine, dir: dir) } catch {
                    projectFailure = .init(title: String(localized: "Can't Change the Folder"), why: error.localizedDescription, changeFolder: nil)
                }
            }
            pushSpacesToDesktop()
        }
    }

    /// Whether a window's row shows, by the search. The title is read only
    /// while searching: read, its change would redraw the host's rows.
    private func filter(_ host: Host, _ w: SessionView.Window) -> Bool {
        query.isEmpty || host.title(w).localizedCaseInsensitiveContains(query)
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

/// Why a project's window did not open; changeFolder is the project whose
/// folder to change, when that is the way on.
struct ProjectFailure {
    let title: String
    let why: String
    let changeFolder: String?
}

enum SpaceSelection: Hashable {
    case space(String)
    case unassigned
}

private struct HostSection: View {
    let host: Host
    /// The centralized space shown, if any: the host shows one card of the
    /// windows of it that belong to this space.
    let space: SpaceSelection?
    @Binding var collapsed: Set<String>
    let filter: (Host, SessionView.Window) -> Bool
    let open: (TerminalTarget) -> Void
    @Environment(HostStore.self) private var store
    @Environment(SpaceStore.self) private var spaceStore
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
        .onDrop(of: [.text], isTargeted: nil) { drop($0) }
        // The dragged session's server order: a dropped row waits for it to
        // change; a drag ends when its window goes.
        .onChange(of: drag.map { d in host.sessions.first { $0.id == d.session.id }?.view?.windows?.map(\.window_id) ?? [] }) { _, ids in
            guard let d = drag, let ids else { return }
            if d.dropped ? ids != d.from : !ids.contains(d.window) { drag = nil }
        }
        // A drag let go of with no drop, or off its card's rows.
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
                } else {
                    Text("No Window Open").foregroundStyle(.secondary)
                }
            } else {
                if host.sessions.isEmpty { Text("No sessions").foregroundStyle(.secondary) }
                ForEach(host.sessions) { s in session(s) }
            }
        }
    }

    /// Assigns a newly created block to the active centralized space, syncs
    /// it to the host, and returns the session ID that now holds the block.
    private func assignAndResolveSession(block: String, fallbackSession: String) async -> String {
        guard case .space(let spaceID) = space else { return fallbackSession }
        spaceStore.assign(machine: host.spaceMachineKey, block: block, window: nil, to: spaceID)
        let moved = await host.applySpaces(spaceStore.orderedSpaces)
        guard moved else { return fallbackSession }
        return await host.resolveSession(forBlock: block, fallback: fallbackSession)
    }

    /// Makes a session with a window, and goes to it.
    private func newSession() {
        creating = true
        Task {
            defer { creating = false }
            do {
                let spaceName: String? = if case .space(let spaceID) = space {
                    spaceStore.space(id: spaceID)?.name
                } else {
                    nil
                }
                if let spaceName,
                   let existing = host.sessions.first(where: {
                       $0.id != SpaceStore.desktopTerminalsSessionID &&
                       $0.label.trimmingCharacters(in: .whitespaces).caseInsensitiveCompare(spaceName) == .orderedSame
                   }),
                   let b = try await host.newWindow(in: existing.id) {
                    let sid = await assignAndResolveSession(block: b, fallbackSession: existing.id)
                    open(TerminalTarget(host: host.id, session: sid, block: b, title: existing.label))
                } else if let (s, b) = try await host.newSession(label: spaceName) {
                    let sid = await assignAndResolveSession(block: b, fallbackSession: s)
                    open(TerminalTarget(host: host.id, session: sid, block: b, title: ""))
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
                    let sid = await assignAndResolveSession(block: b, fallbackSession: s.id)
                    open(TerminalTarget(host: host.id, session: sid, block: b, title: s.label))
                }
            } catch {
                failure = ("Can't Open a New Window", error.localizedDescription)
            }
        }
    }

    /// A window's menu, as the desktop's tab menu. It moves a window past
    /// the rows shown, and closes only those: the server's order also has
    /// windows the list leaves out.
    @ViewBuilder private func menu(_ s: Host.Session, _ w: SessionView.Window, shown: [String]) -> some View {
        let id = w.window_id
        let i = shown.firstIndex(of: id) ?? 0
        // The title changes while the menu is open: it's read on the tap.
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
        Menu("Move to Project", systemImage: "rectangle.stack") {
            let m = host.spaceMachineKey
            let current = w.block.flatMap { spaceStore.space(for: m, block: $0) }
            ForEach(spaceStore.orderedSpaces) { target in
                Button {
                    spaceStore.assign(window: w, on: m, to: target.id)
                    if spaceStore.desktopSyncEnabled, spaceStore.primaryHostID.isEmpty || host.id.uuidString == spaceStore.primaryHostID {
                        let all = spaceStore.orderedSpaces
                        Task {
                            await host.applySpaces(all)
                            spaceStore.markDesktopSynced()
                        }
                    }
                } label: {
                    if current?.id == target.id {
                        Label(target.name, systemImage: "checkmark")
                    } else {
                        Text(target.name)
                    }
                }
                .disabled(current?.id == target.id)
            }
            if current != nil {
                Divider()
                Button("Unassign from Project", systemImage: "minus.circle") {
                    for b in w.blocks { spaceStore.unassign(machine: m, block: b) }
                }
            }
        }
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
        item.mark(d.token)
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
    private func drop(_ items: [NSItemProvider]) -> Bool {
        guard var d = drag, !d.dropped else {
            // Another card's row, which cannot move here.
            PageDrop.missed(items)
            return false
        }
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

    /// The windows of this host belonging to the selected centralized space.
    private func windows(in space: SpaceSelection) -> [(Host.Session, SessionView.Window)] {
        let m = host.spaceMachineKey
        return host.sessions.flatMap { s in
            (s.view?.windows ?? []).compactMap { w in
                let matches: Bool = switch space {
                case .space(let id): spaceStore.contains(spaceID: id, machine: m, window: w)
                case .unassigned: spaceStore.isUnassigned(machine: m, window: w)
                }
                return matches ? (s, w) : nil
            }
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
            w.block.flatMap { filter(host, w) ? Entry(session: s, block: $0, window: w) : nil }
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
            // Every host in a centralized space supports window menu and drag.
            let shown = windows.filter { $0.session.id == w.session.id }.map(\.window.window_id)
            let lifted = drag.map { !$0.dropped && $0.session.id == w.session.id && $0.window == w.window.window_id } ?? false
            row.onDrag { lift(w, shown: shown) }
                .contextMenu { menu(w.session, w.window, shown: shown) }
                .onDrop(of: [.text], delegate: RowDrop(pass: { pass(over: w) }, drop: { drop($0) }))
                .opacity(lifted ? 0.4 : 1)
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
    let drop: ([NSItemProvider]) -> Bool

    func dropEntered(info: DropInfo) { pass() }
    func dropUpdated(info: DropInfo) -> DropProposal? { DropProposal(operation: .move) }
    func performDrop(info: DropInfo) -> Bool { drop(info.itemProviders(for: [.text])) }
}

/// The page as a drop target, under the cards', for rows only: a row let
/// go of there, rather than cancelled, tells its card on iOS 26 too, where
/// no call says when a drag ends.
private struct PageDrop: DropDelegate {
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
