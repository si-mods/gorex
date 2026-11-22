import SwiftUI

/// The elements the capsule and its panel share as one opens into the
/// other: the glass, and each space's color and name
/// (docs/IOS-011-continuous-transitions.md, the space panel).
enum SpaceMatch: Hashable {
    case chrome
    case chip(String)
    case name(String)
}

/// A shared element of the capsule and its panel, when a namespace joins
/// them; a nil one leaves the view plain.
private struct SpaceMatched: ViewModifier {
    let id: SpaceMatch
    let ns: Namespace.ID?
    let source: Bool

    func body(content: Content) -> some View {
        if let ns {
            content.matchedGeometryEffect(id: id, in: ns, isSource: source)
        } else {
            content
        }
    }
}

/// A part of the panel that appears once the glass is mostly open: the
/// header first, then each row 30 ms after the last.
private struct Reveal: ViewModifier {
    let stagger: Bool
    let revealed: Bool
    let delay: Double

    func body(content: Content) -> some View {
        content
            .opacity(!stagger || revealed ? 1 : 0)
            .animation(.easeIn(duration: 0.15).delay(delay), value: revealed)
    }
}

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
    /// The namespace the list's panel shares with the capsule, so the
    /// capsule's glass and its space's color and name can carry into the
    /// panel. Nil under Reduce Motion, and for the bar's popover.
    var ns: Namespace.ID?
    @State private var popover = false
    /// The capsule's own content while the panel is open: it leaves as the
    /// panel's glass takes the capsule's frame, and returns as the glass
    /// lands back on it.
    @State private var shown = true
    /// VoiceOver comes back to the capsule as the panel closes.
    @AccessibilityFocusState private var focus: Bool
    @Environment(\.accessibilityReduceMotion) private var still

    var body: some View {
        if let panel {
            capsule { withAnimation(still ? .easeInOut(duration: 0.15) : .snappy) { panel.wrappedValue = true } }
                .opacity(shown ? 1 : 0)
                .accessibilityFocused($focus)
                .onChange(of: panel.wrappedValue, initial: true) { was, open in
                    withAnimation(open ? .easeOut(duration: 0.1) : .easeIn(duration: 0.12).delay(0.3)) {
                        shown = still || !open
                    }
                    if was && !open { focus = true }
                }
                .anchorPreference(key: SpacePickerBounds.self, value: .bounds) { $0 }
                // The panel goes with the picker, as Activity takes its place.
                .onDisappear { withAnimation(still ? .easeInOut(duration: 0.15) : .snappy) { panel.wrappedValue = false } }
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
                // The glass is the element the panel grows out of. A bar's
                // own glass is behind it, and two glass effects nest.
                label
                    .glassEffect(.regular.interactive(), in: .capsule)
                    .modifier(SpaceMatched(id: .chrome, ns: ns, source: true))
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
                .modifier(SpaceMatched(id: .chip(current), ns: ns, source: true))
            Text(verbatim: SpaceName.shown(current))
                .font(bar ? .title3.bold() : .title2.bold())
                .lineLimit(1)
                .contentTransition(.opacity)
                .modifier(SpaceMatched(id: .name(current), ns: ns, source: true))
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

/// The panel atop the list, over the page: it is the capsule's glass grown
/// into the panel's, 8 pt under the capsule. Its content stays out of view
/// until the glass is mostly open (the stagger in SpacePanel), so the glass
/// is the only thing that changes shape, and a tap beside it closes it. It
/// keeps 16 pt from the foot of the page and scrolls its spaces past that.
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
    /// The namespace the capsule shares; nil under Reduce Motion, when
    /// nothing moves.
    var ns: Namespace.ID?
    @Environment(\.accessibilityReduceMotion) private var still
    /// A fresh identity on each open: a shared element's slot keeps the
    /// frame it was dismissed at, and a reused one reopens pinned to the
    /// capsule instead of growing out of it.
    @State private var gen = 0

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
                               edit: { close(); edit() },
                               ns: match, stagger: !still)
                        .id(gen)
                        .fixedSize(horizontal: true, vertical: false)
                        // The glass hugs the panel: the cap below only
                        // limits it, and the room under a short panel stays
                        // the page's.
                        .glassEffect(.regular, in: .rect(cornerRadius: SpacePanel.radius, style: .continuous))
                        .shadow(color: .black.opacity(0.16), radius: 24, y: 8)
                        .frame(maxHeight: max(160, g.size.height - r.maxY - 8 - 16), alignment: .top)
                        .modifier(SpaceMatched(id: .chrome, ns: match, source: false))
                        // One element holding the rows, modal, so that
                        // VoiceOver keeps to the panel while it is open.
                        .accessibilityElement(children: .contain)
                        .accessibilityAddTraits(.isModal)
                        .accessibilityAction(.escape) { close() }
                        // Laid out over the whole layer, so the glass can
                        // grow from the capsule's frame: it stays opaque
                        // the whole way, and only on the way back does it
                        // fade as it shrinks.
                        .padding(.leading, r.minX)
                        .padding(.top, r.maxY + 8)
                        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
                        .transition(still ? .opacity : .asymmetric(insertion: .identity, removal: .opacity))
                }
            }
        }
        .onChange(of: open) { _, o in
            if o { gen += 1 }
        }
    }

    /// The shared elements' namespace: nil under Reduce Motion.
    private var match: Namespace.ID? { still ? nil : ns }

    private func close() {
        withAnimation(still ? .easeInOut(duration: 0.15) : .snappy) { open = false }
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
    /// The namespace the rows share with the capsule, so the space shown
    /// trades its color and name with it. Nil for a plain panel, as the
    /// bar's popover's, and under Reduce Motion.
    var ns: Namespace.ID? = nil
    /// Whether the parts come in one by one once the glass is open.
    var stagger: Bool = false
    /// The staggered parts' entrance, replayed on each open.
    @State private var revealed = false
    /// The width of the color squares' column, which the plus of New
    /// Space shares; it grows with the text.
    @ScaledMetric(relativeTo: .body) private var mark: CGFloat = 16
    /// VoiceOver goes to the header as the panel opens.
    @AccessibilityFocusState private var heading: Bool

    static let radius: CGFloat = 28
    /// The panel's padding on every side.
    static let inset: CGFloat = 8
    /// The padding inside a row, and in the header.
    static let pad: CGFloat = 12

    /// The delay of the first row, and the step between them.
    private static let rowDelay = 0.18
    private static let rowStep = 0.03

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text("Spaces")
                    .font(.headline)
                    .accessibilityAddTraits(.isHeader)
                    .accessibilityFocused($heading)
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
            .modifier(Reveal(stagger: stagger, revealed: revealed, delay: 0.12))
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
                .modifier(Reveal(stagger: stagger, revealed: revealed, delay: Self.rowDelay + Double(spaces.count) * Self.rowStep))
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
            .modifier(Reveal(stagger: stagger, revealed: revealed, delay: Self.rowDelay + Double(spaces.count + 1) * Self.rowStep))
        }
        .padding(Self.inset)
        .frame(minWidth: 288, idealWidth: 288)
        .onAppear { revealed = true; heading = true }
    }

    private var rows: some View {
        VStack(spacing: 4) {
            ForEach(Array(spaces.enumerated()), id: \.element) { i, s in row(s, at: i) }
        }
    }

    /// A space's row: its color, its name and its projects; the one shown
    /// is bold, checked and tinted.
    private func row(_ s: String, at i: Int) -> some View {
        let on = s == current
        return Button { choose(s) } label: {
            HStack(spacing: Self.pad) {
                RoundedRectangle(cornerRadius: 4, style: .continuous)
                    .fill(color(s) ?? Color.secondary.opacity(0.45))
                    .frame(width: mark, height: mark)
                    .modifier(SpaceMatched(id: .chip(s), ns: ns, source: false))
                VStack(alignment: .leading, spacing: 0) {
                    Text(verbatim: SpaceName.shown(s))
                        .font(.body.weight(on ? .semibold : .regular))
                        .lineLimit(1)
                        .modifier(SpaceMatched(id: .name(s), ns: ns, source: false))
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
        .modifier(Reveal(stagger: stagger, revealed: revealed, delay: Self.rowDelay + Double(i) * Self.rowStep))
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
