import SwiftUI

/// The space picker atop the list (docs/MOD-002-projects.md, Rex Go), or in a wide
/// window's bar beside the search: the space shown, and a panel of the
/// spaces, with Edit and New Space.
struct SpacePicker: View {
    /// Where the picker shows: a wide window's bar packs its items at the
    /// start, so one wider than there is room for goes into the menu beside
    /// the search.
    enum Place {
        case list
        case bar
    }

    let spaces: [String]
    let current: String
    let color: (String) -> Color?
    /// The projects of a space.
    let count: (String) -> Int
    let choose: (String) -> Void
    let add: () -> Void
    let edit: () -> Void
    var place: Place = .list
    @State private var open = false

    var body: some View {
        Button { open = true } label: {
            if place == .list {
                // Glass of its own over the page; a bar's own is behind it,
                // and two glass effects nest.
                label.glassEffect(.regular.interactive(), in: .capsule)
            } else {
                // A name longer than the room ends in an ellipsis, leaving
                // the search and the toggle their places.
                label.frame(maxWidth: 160)
            }
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Space")
        .accessibilityValue(SpaceName.shown(current))
        .popover(isPresented: $open, attachmentAnchor: .rect(.bounds), arrowEdge: .top) {
            SpacePanel(spaces: spaces, current: current, color: color, count: count,
                       choose: { s in open = false; choose(s) },
                       add: { open = false; add() },
                       edit: { open = false; edit() })
                .presentationCompactAdaptation(.popover)
        }
    }

    private var label: some View {
        let bar = place == .bar
        return HStack(spacing: 10) {
            RoundedRectangle(cornerRadius: 4, style: .continuous)
                .fill(color(current) ?? Color.secondary.opacity(0.5))
                .frame(width: 14, height: 14)
            Text(verbatim: SpaceName.shown(current))
                .font(bar ? .title3.bold() : .title2.bold())
                .lineLimit(1)
            Image(systemName: "chevron.down")
                .font(.subheadline.weight(.semibold))
                .foregroundStyle(.secondary)
        }
        .foregroundStyle(.primary)
        .padding(.horizontal, bar ? 12 : 16)
        .frame(minHeight: 44)
    }
}

/// The panel the picker opens: a header with Edit, each space with its
/// color and its projects, the one shown marked, and New Space last.
struct SpacePanel: View {
    let spaces: [String]
    let current: String
    let color: (String) -> Color?
    let count: (String) -> Int
    let choose: (String) -> Void
    let add: () -> Void
    let edit: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text("Spaces")
                    .font(.headline)
                    .accessibilityAddTraits(.isHeader)
                Spacer()
                Button(action: edit) {
                    Text("Edit")
                        .font(.subheadline.weight(.semibold))
                }
                .buttonStyle(.borderless)
            }
            .padding(.horizontal, 18)
            .padding(.top, 16)
            .padding(.bottom, 10)
            VStack(spacing: 2) {
                ForEach(spaces, id: \.self) { s in row(s) }
                Divider().padding(.vertical, 6).padding(.horizontal, 12)
                Button(action: add) {
                    HStack(spacing: 12) {
                        Image(systemName: "plus")
                            .font(.subheadline.weight(.semibold))
                            .frame(width: 16)
                        Text("New Space")
                            .lineLimit(1)
                        Spacer()
                    }
                    .padding(.horizontal, 12)
                    .frame(minHeight: 44)
                    .contentShape(.rect)
                }
                .buttonStyle(.plain)
                .foregroundStyle(.tint)
            }
            .padding(.horizontal, 6)
            .padding(.bottom, 10)
        }
        .frame(minWidth: 290, idealWidth: 290)
    }

    /// A space's row: its color, its name and its projects; the one shown
    /// is bold, checked and tinted.
    private func row(_ s: String) -> some View {
        let on = s == current
        return Button { choose(s) } label: {
            HStack(spacing: 12) {
                RoundedRectangle(cornerRadius: 5, style: .continuous)
                    .fill(color(s) ?? Color.secondary.opacity(0.45))
                    .frame(width: 16, height: 16)
                VStack(alignment: .leading, spacing: 1) {
                    Text(verbatim: SpaceName.shown(s))
                        .font(.body.weight(on ? .semibold : .regular))
                        .lineLimit(1)
                    Text(projects(count(s)))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
                Spacer(minLength: 8)
                if on {
                    Image(systemName: "checkmark")
                        .font(.subheadline.weight(.bold))
                        .foregroundStyle(color(s) ?? .primary)
                }
            }
            .padding(.horizontal, 12)
            .frame(minHeight: 52)
            .background {
                if on {
                    RoundedRectangle(cornerRadius: 14, style: .continuous)
                        .fill((color(s) ?? .primary).opacity(0.12))
                }
            }
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(on ? .isSelected : [])
    }

    private func projects(_ n: Int) -> String {
        n == 0 ? String(localized: "No projects") : String(localized: "\(n) projects")
    }
}

/// Edit Spaces…: each space's color and name, and their order. A rename
/// or a deletion reaches every host with projects, now or as it connects;
/// a deletion moves the space's projects to Default and closes no tab.
/// Default keeps its name and stays.
struct EditSpaces: View {
    let spaces: [String]
    let carried: Set<String>
    /// The projects of a space on the hosts connected now.
    let projects: (String) -> Int
    /// Renames a space, from one name to another.
    let rename: (String, String) -> Void
    /// Deletes a space, after the user confirms.
    let delete: (String) -> Void
    @Environment(SpaceBook.self) private var book
    @Environment(\.dismiss) private var dismiss
    @State private var list: [String] = []
    @State private var renaming: String?
    @State private var name = ""
    /// The space to delete, once the user confirms.
    @State private var deleting: String?

    var body: some View {
        NavigationStack {
            List {
                Section {
                    ForEach(list, id: \.self) { s in row(s) }
                        .onMove { from, to in
                            list.move(fromOffsets: from, toOffset: to)
                            book.reorder(list)
                        }
                        .onDelete { at in
                            deleting = at.first.map { list[$0] }
                        }
                        .listRowBackground(Color.card)
                } footer: {
                    Text("A space is the name on its projects. Renaming one renames it on every host; deleting one moves its projects to Default. A host that is offline takes the change when it connects.")
                }
            }
            .environment(\.editMode, .constant(.active))
            .scrollContentBackground(.hidden)
            .background(Color.page.ignoresSafeArea())
            .navigationTitle("Edit Spaces")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }
                }
            }
            .alert("Rename Space", isPresented: Binding { renaming != nil } set: { if !$0 { renaming = nil } }, presenting: renaming) { s in
                TextField(s, text: $name)
                Button("Cancel", role: .cancel) {}
                Button("Save") {
                    let to = name.trimmingCharacters(in: .whitespacesAndNewlines)
                    guard !to.isEmpty, to != s else { return }
                    rename(s, to)
                    list = list.contains(to) ? list.filter { $0 != s } : list.map { $0 == s ? to : $0 }
                }
            }
            .alert(deleting.map { String(localized: "Delete space \(SpaceName.shown($0))?") } ?? "",
                   isPresented: Binding { deleting != nil } set: { if !$0 { deleting = nil } }, presenting: deleting) { s in
                Button("Cancel", role: .cancel) {}
                Button("Delete", role: .destructive) {
                    delete(s)
                    list.removeAll { $0 == s }
                }
            } message: { s in
                Text("Its \(projects(s)) projects move to Default. No tab closes.")
            }
        }
        .onAppear { list = spaces }
    }

    /// A space's row: its color to pick, its name, and Rename.
    private func row(_ s: String) -> some View {
        HStack(spacing: 12) {
            Menu {
                ForEach(ProjectColor.all, id: \.id) { c in
                    Button(HostSection.colorName(c.id)) { book.setColor(c.id, for: s) }
                }
                Divider()
                Button("No Color") { book.setColor(nil, for: s) }
            } label: {
                Circle()
                    .fill(Color(project: book.colors[s]) ?? Color.secondary.opacity(0.4))
                    .frame(width: 22, height: 22)
            }
            .accessibilityLabel("Color")
            Text(verbatim: SpaceName.shown(s))
            Spacer()
            if !s.isEmpty {
                Button("Rename") {
                    name = s
                    renaming = s
                }
                .buttonStyle(.borderless)
            }
        }
        .deleteDisabled(s.isEmpty)
    }
}
