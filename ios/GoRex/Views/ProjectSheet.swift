import SwiftUI

/// What the project sheet does: make a project, or change one's folder.
enum ProjectSheetMode: Identifiable, Hashable {
    case new
    case change(String)

    var id: String {
        switch self {
        case .new: "new"
        case .change(let id): "change:" + id
        }
    }
}

/// New Project, and a project's Change Folder (docs/projects.md, Rex Go):
/// a host, a folder on it browsed with gorex.dir.list, and a name, the
/// folder's by default.
struct ProjectSheet: View {
    let mode: ProjectSheetMode
    /// The host, the folder and the name chosen. A new project with no
    /// folder chosen has neither host nor folder: it only groups windows.
    let done: (Host?, String?, String) -> Void
    @Environment(HostStore.self) private var store
    @Environment(SpaceStore.self) private var spaceStore
    @Environment(\.dismiss) private var dismiss
    @State private var hostID: UUID?
    @State private var dir: String?
    @State private var name = ""
    /// Whether the user typed the name: then a folder chosen keeps it.
    @State private var named = false
    @State private var started = false

    private var host: Host? { hostID.flatMap { store.host($0) } }
    private var isNew: Bool { mode == .new }

    var body: some View {
        NavigationStack {
            Form {
                Section("Host") {
                    ForEach(store.hosts) { h in hostRow(h) }
                }
                Section {
                    if let host, let dir {
                        NavigationLink {
                            FolderBrowser(host: host, start: dir, choose: choose)
                        } label: {
                            Label {
                                Text(verbatim: ProjectWindow.short(dir)).monospaced().lineLimit(1).truncationMode(.head)
                            } icon: {
                                Image(systemName: "folder")
                            }
                        }
                    } else if let host {
                        NavigationLink {
                            FolderBrowser(host: host, start: "", choose: choose)
                        } label: {
                            Label("Choose a Folder", systemImage: "folder.badge.plus")
                        }
                    } else {
                        Text("Choose a host first.").foregroundStyle(.secondary)
                    }
                } header: {
                    Text("Folder")
                } footer: {
                    if isNew {
                        Text("New windows of the project open on this host, in this folder. With no folder, the project only groups windows.")
                    } else {
                        Text("New windows of the project open on this host, in this folder.")
                    }
                }
                if isNew {
                    Section("Name") {
                        TextField("Name", text: Binding { name } set: { name = $0; named = true })
                    }
                }
            }
            .navigationTitle(isNew ? "New Project" : "Change Folder")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button(isNew ? "Create" : "Save") {
                        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
                        if let host, let dir {
                            done(host, dir, trimmed.isEmpty ? ProjectWindow.name(of: dir) : trimmed)
                        } else {
                            done(nil, nil, trimmed)
                        }
                        dismiss()
                    }
                    .disabled(dir == nil && !(isNew && !name.trimmingCharacters(in: .whitespaces).isEmpty))
                }
            }
        }
        .onAppear(perform: start)
    }

    /// A host to pick: one offline, or of a GoRex that lists no folders,
    /// shows why it cannot be picked.
    private func hostRow(_ h: Host) -> some View {
        Button {
            if hostID != h.id {
                hostID = h.id
                dir = nil
            }
        } label: {
            HStack(spacing: 12) {
                Image(systemName: h.config.symbol).foregroundStyle(.secondary).frame(width: 28)
                Text(h.config.name).foregroundStyle(.primary)
                Spacer()
                if h.status != .online {
                    Text("Offline").foregroundStyle(.secondary)
                } else if !h.listsFolders {
                    Text("Update GoRex").foregroundStyle(.secondary)
                } else if hostID == h.id {
                    Image(systemName: "checkmark").foregroundStyle(.tint).fontWeight(.semibold)
                }
            }
        }
        .disabled(!h.listsFolders)
    }

    private func choose(_ path: String) {
        dir = path
        if isNew, !named { name = ProjectWindow.name(of: path) }
    }

    private func start() {
        guard !started else { return }
        started = true
        if case .change(let id) = mode, let s = spaceStore.space(id: id) {
            let home = store.hosts.filter { $0.spaceMachineKey == s.home_machine }
            if let h = home.first(where: \.listsFolders) ?? home.first {
                hostID = h.id
                dir = s.folder
            }
        }
        if hostID == nil { hostID = store.hosts.first(where: \.listsFolders)?.id }
    }
}

/// A host's folders, one directory at a time: Home, Up, the folders below
/// to open, and a path to type. Folders only: it reads, it makes none.
struct FolderBrowser: View {
    let host: Host
    let start: String
    let choose: (String) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var listing: Host.Folders?
    @State private var failure: String?
    @State private var typed = ""
    @State private var loading = false
    /// The listing asked for last: an older answer is dropped.
    @State private var asked = 0

    var body: some View {
        List {
            Section {
                TextField("Path", text: $typed)
                    .font(.body.monospaced())
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .submitLabel(.go)
                    .onSubmit { go(typed) }
            } footer: {
                Text("Type a path and press Go, or open a folder below.")
            }
            if let failure {
                Section {
                    Label(failure, systemImage: "exclamationmark.triangle").foregroundStyle(.secondary)
                }
            }
            if let listing {
                if let code = listing.code {
                    Section {
                        Label(code == "denied" ? "You can't read this folder." : "This folder is not there.",
                              systemImage: "exclamationmark.triangle")
                            .foregroundStyle(.secondary)
                    }
                }
                Section {
                    ForEach(listing.dirs, id: \.self) { d in
                        Button { go(listing.child(d)) } label: {
                            HStack {
                                Label {
                                    Text(verbatim: d).foregroundStyle(.primary)
                                } icon: {
                                    Image(systemName: "folder.fill").foregroundStyle(.tint)
                                }
                                Spacer()
                                Image(systemName: "chevron.right").font(.footnote.weight(.semibold)).foregroundStyle(.gray)
                            }
                        }
                    }
                    if listing.dirs.isEmpty, listing.code == nil {
                        Text("No folders inside").foregroundStyle(.secondary)
                    }
                } header: {
                    Text(verbatim: ProjectWindow.short(listing.path))
                } footer: {
                    if listing.more { Text("Only the first 2000 folders are shown.") }
                }
            } else if loading {
                HStack { Spacer(); ProgressView(); Spacer() }
            }
        }
        .navigationTitle(listing.map { ProjectWindow.name(of: $0.path) } ?? String(localized: "Folder"))
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .confirmationAction) {
                Button("Choose") {
                    guard let listing else { return }
                    choose(listing.path)
                    dismiss()
                }
                .disabled(listing == nil || listing?.code != nil)
            }
            ToolbarItemGroup(placement: .bottomBar) {
                Button("Home", systemImage: "house") { go("") }
                Button("Up", systemImage: "arrow.up") { listing.map { go($0.parent) } }
                    .disabled((listing?.parent ?? "").isEmpty)
                Spacer()
            }
        }
        .task { go(start) }
    }

    private func go(_ path: String) {
        asked += 1
        let n = asked
        loading = true
        Task {
            defer { if n == asked { loading = false } }
            do {
                let l = try await host.folders(path)
                guard n == asked else { return }
                listing = l
                typed = l.path
                failure = nil
            } catch {
                guard n == asked else { return }
                failure = error.localizedDescription
            }
        }
    }
}
