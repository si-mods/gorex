import SwiftUI

/// The space picker atop the list (docs/projects.md, Rex Go): the space
/// shown, and a menu of the spaces, New Space… and Edit Spaces….
struct SpacePicker: View {
    let spaces: [String]
    let current: String
    let color: (String) -> Color?
    let choose: (String) -> Void
    let add: () -> Void
    let edit: () -> Void

    var body: some View {
        Menu {
            Picker("Space", selection: Binding { current } set: { choose($0) }) {
                ForEach(spaces, id: \.self) { s in
                    Text(verbatim: SpaceName.shown(s)).tag(s)
                }
            }
            Divider()
            Button("New Space…", systemImage: "plus") { add() }
            Button("Edit Spaces…", systemImage: "pencil") { edit() }
        } label: {
            HStack(spacing: 10) {
                RoundedRectangle(cornerRadius: 4, style: .continuous)
                    .fill(color(current) ?? Color.secondary.opacity(0.5))
                    .frame(width: 14, height: 14)
                Text(verbatim: SpaceName.shown(current))
                    .font(.title2.bold())
                    .lineLimit(1)
                Image(systemName: "chevron.down")
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(.secondary)
            }
            .foregroundStyle(.primary)
            .padding(.horizontal, 16)
            .frame(minHeight: 44)
            .glassEffect(.regular.interactive(), in: .capsule)
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Space")
        .accessibilityValue(SpaceName.shown(current))
    }
}

/// Edit Spaces…: each space's color and name, and their order. A rename
/// reaches every host with projects, now or as it connects; a space made
/// here that no project carries can be removed. Default keeps its name.
struct EditSpaces: View {
    let spaces: [String]
    let carried: Set<String>
    /// Renames a space, from one name to another.
    let rename: (String, String) -> Void
    @Environment(SpaceBook.self) private var book
    @Environment(\.dismiss) private var dismiss
    @State private var list: [String] = []
    @State private var renaming: String?
    @State private var name = ""

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
                            for i in at { book.remove(list[i]) }
                            list.remove(atOffsets: at)
                        }
                        .listRowBackground(Color.card)
                } footer: {
                    Text("A space is the name on its projects. Renaming one renames it on every host; a host that is offline takes the new name when it connects.")
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
        .deleteDisabled(!book.made.contains(s) || carried.contains(s))
    }
}
