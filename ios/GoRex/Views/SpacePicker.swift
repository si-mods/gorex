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
    /// Atop the list: whether the panel is open. The list draws the panel
    /// under the capsule (SpacePanelLayer), with no arrow; the bar, which
    /// has no room below it of its own, opens a popover instead.
    var panel: Binding<Bool>?
    @State private var popover = false

    var body: some View {
        if let panel {
            capsule { withAnimation(.snappy) { panel.wrappedValue = true } }
                .anchorPreference(key: SpacePickerBounds.self, value: .bounds) { $0 }
                // The panel goes with the picker, as Activity takes its place.
                .onDisappear { panel.wrappedValue = false }
        } else {
            capsule { popover = true }
                .popover(isPresented: $popover, attachmentAnchor: .rect(.bounds), arrowEdge: .top) {
                    SpacePanel(spaces: spaces, current: current, color: color, count: count,
                               choose: { s in popover = false; choose(s) },
                               add: { popover = false; add() },
                               edit: { popover = false; edit() })
                        .presentationCompactAdaptation(.popover)
                }
        }
    }

    private func capsule(_ open: @escaping () -> Void) -> some View {
        Button(action: open) {
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

/// Where the list's picker is, for the panel to open under it.
struct SpacePickerBounds: PreferenceKey {
    static var defaultValue: Anchor<CGRect>? { nil }

    static func reduce(value: inout Anchor<CGRect>?, nextValue: () -> Anchor<CGRect>?) {
        value = value ?? nextValue()
    }
}

/// The panel atop the list, over the page: it grows from the capsule's
/// corner, 8 pt under it, and a tap beside it closes it. It keeps 16 pt
/// from the foot of the page and scrolls its spaces past that.
struct SpacePanelLayer: View {
    let anchor: Anchor<CGRect>?
    @Binding var open: Bool
    let spaces: [String]
    let current: String
    let color: (String) -> Color?
    let count: (String) -> Int
    let choose: (String) -> Void
    let add: () -> Void
    let edit: () -> Void

    var body: some View {
        GeometryReader { g in
            if open, let anchor {
                let r = g[anchor]
                ZStack(alignment: .topLeading) {
                    Color.clear
                        .contentShape(.rect)
                        .onTapGesture { close() }
                        .accessibilityHidden(true)
                    SpacePanel(spaces: spaces, current: current, color: color, count: count,
                               choose: { s in close(); choose(s) },
                               add: { close(); add() },
                               edit: { close(); edit() })
                        .frame(maxHeight: max(160, g.size.height - r.maxY - 8 - 16), alignment: .top)
                        .fixedSize(horizontal: true, vertical: false)
                        .glassEffect(.regular, in: .rect(cornerRadius: SpacePanel.radius, style: .continuous))
                        .shadow(color: .black.opacity(0.16), radius: 24, y: 8)
                        .offset(x: r.minX, y: r.maxY + 8)
                        .transition(.scale(0.85, anchor: .topLeading).combined(with: .opacity))
                        .accessibilityAddTraits(.isModal)
                        .accessibilityAction(.escape) { close() }
                }
            }
        }
    }

    private func close() {
        withAnimation(.snappy) { open = false }
    }
}

/// The panel the picker opens: a header with Edit, each space with its
/// color and its projects, the one shown marked, and New Space last.
/// Its metrics keep to a 4 pt grid, and its corners nest: a row's face
/// sits `inset` in from the panel's edge, so its radius is the panel's
/// less that inset.
struct SpacePanel: View {
    let spaces: [String]
    let current: String
    let color: (String) -> Color?
    let count: (String) -> Int
    let choose: (String) -> Void
    let add: () -> Void
    let edit: () -> Void
    /// The width of the color squares' column, which the plus of New
    /// Space shares; it grows with the text.
    @ScaledMetric(relativeTo: .body) private var mark: CGFloat = 16

    static let radius: CGFloat = 28
    /// The panel's padding on every side.
    static let inset: CGFloat = 8
    /// The padding inside a row, and in the header.
    static let pad: CGFloat = 12

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
                .accessibilityLabel("Edit Spaces")
                .accessibilityInputLabels([Text("Edit"), Text("Edit Spaces")])
            }
            .padding(.horizontal, Self.pad)
            .frame(minHeight: 44)
            // The spaces scroll when the screen has no room for them all, at
            // a large text size or with many spaces; the header and New
            // Space stay in view.
            ViewThatFits(in: .vertical) {
                rows
                ScrollView { rows }
                    .scrollBounceBehavior(.basedOnSize)
            }
            Divider()
                .padding(.horizontal, Self.pad)
                .padding(.vertical, 4)
            Button(action: add) {
                HStack(spacing: Self.pad) {
                    Image(systemName: "plus")
                        .font(.subheadline.weight(.semibold))
                        .frame(width: mark)
                    Text("New Space")
                        .lineLimit(1)
                    Spacer()
                }
                .padding(.horizontal, Self.pad)
                .frame(minHeight: 44)
                .contentShape(.rect)
            }
            .buttonStyle(.plain)
            .foregroundStyle(.tint)
        }
        .padding(Self.inset)
        .frame(minWidth: 288, idealWidth: 288)
    }

    private var rows: some View {
        VStack(spacing: 4) {
            ForEach(spaces, id: \.self) { s in row(s) }
        }
    }

    /// A space's row: its color, its name and its projects; the one shown
    /// is bold, checked and tinted.
    private func row(_ s: String) -> some View {
        let on = s == current
        return Button { choose(s) } label: {
            HStack(spacing: Self.pad) {
                RoundedRectangle(cornerRadius: 4, style: .continuous)
                    .fill(color(s) ?? Color.secondary.opacity(0.45))
                    .frame(width: mark, height: mark)
                VStack(alignment: .leading, spacing: 0) {
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
                        // The row says Selected already.
                        .accessibilityHidden(true)
                }
            }
            .padding(.horizontal, Self.pad)
            .frame(minHeight: 52)
            .background {
                if on {
                    RoundedRectangle(cornerRadius: Self.radius - Self.inset, style: .continuous)
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
