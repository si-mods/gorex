import SwiftUI

/// New Project on a host (docs/projects.md, New Project): a folder on it,
/// browsed with gorex.dir.list, and a name, the folder's by default. The
/// project is made in the space shown, and its first tab opens in the
/// folder.
struct ProjectSheet: View {
    let host: Host
    /// The space shown, which the project is made in.
    let space: String
    /// The folder and the name chosen.
    let done: (String, String) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var dir: String?
    @State private var name = ""
    /// Whether the user typed the name: then a folder chosen keeps it.
    @State private var named = false

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    NavigationLink {
                        FolderBrowser(host: host, start: dir ?? "", choose: choose)
                    } label: {
                        if let dir {
                            Label {
                                Text(verbatim: ProjectFolder.short(dir)).monospaced().lineLimit(1).truncationMode(.head)
                            } icon: {
                                Image(systemName: "folder")
                            }
                        } else {
                            Label("Choose a Folder", systemImage: "folder.badge.plus")
                        }
                    }
                } header: {
                    Text("Folder")
                } footer: {
                    Text("The project's tabs open on \(host.config.name), in this folder. A folder is one project: if it is one already, that project shows.")
                }
                Section("Name") {
                    TextField("Name", text: Binding { name } set: { name = $0; named = true })
                }
                Section {
                    LabeledContent("Space", value: space)
                }
            }
            .navigationTitle("New Project on \(host.config.name)")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Create") {
                        guard let dir else { return }
                        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
                        done(dir, trimmed.isEmpty ? ProjectFolder.name(of: dir) : trimmed)
                        dismiss()
                    }
                    .disabled(dir == nil)
                }
            }
        }
    }

    private func choose(_ path: String) {
        dir = path
        if !named { name = ProjectFolder.name(of: path) }
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
                Text("Type a path and press Go, or tap a folder below to open it.")
            }
            // What Choose takes: the folder open, not a row below it.
            if let listing, listing.code == nil {
                Section {
                    Button { use(listing.path) } label: {
                        Label {
                            Text("Use This Folder: \(ProjectFolder.name(of: listing.path))")
                        } icon: {
                            Image(systemName: "checkmark.circle.fill")
                        }
                    }
                }
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
                        // A tap opens the folder; a swipe or the menu uses it.
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
                        .swipeActions(edge: .trailing, allowsFullSwipe: true) {
                            Button("Use", systemImage: "checkmark") { use(listing.child(d)) }
                                // Blue: the accent is white in the dark scheme, as its icon is.
                                .tint(.blue)
                        }
                        .contextMenu {
                            Button("Use This Folder", systemImage: "checkmark.circle") { use(listing.child(d)) }
                        }
                    }
                    if listing.dirs.isEmpty, listing.code == nil {
                        Text("No folders inside").foregroundStyle(.secondary)
                    }
                } header: {
                    Text(verbatim: ProjectFolder.short(listing.path))
                } footer: {
                    if listing.more { Text("Only the first 2000 folders are shown.") }
                }
            } else if loading {
                HStack { Spacer(); ProgressView(); Spacer() }
            }
        }
        .navigationTitle(listing.map { ProjectFolder.name(of: $0.path) } ?? String(localized: "Folder"))
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .confirmationAction) {
                Button("Choose") {
                    guard let listing else { return }
                    use(listing.path)
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

    /// Takes a folder for the project, and goes back to the sheet.
    private func use(_ path: String) {
        choose(path)
        dismiss()
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
