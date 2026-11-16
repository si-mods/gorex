import SwiftUI

/// A window's pane layout as a small diagram: the current pane is lit, the
/// others dim. Interactive, a tap selects the pane it hits; not, it only
/// shows.
struct PaneMiniMap: View {
    let cells: [PaneCell]
    let current: String
    var interactive = false
    var select: ((String) -> Void)?

    var body: some View {
        GeometryReader { g in
            let inset: CGFloat = 3
            let room = CGRect(x: inset, y: inset, width: max(0, g.size.width - inset * 2), height: max(0, g.size.height - inset * 2))
            ZStack(alignment: .topLeading) {
                ForEach(cells) { cell in
                    let r = CGRect(x: room.minX + room.width * cell.rect.minX,
                                   y: room.minY + room.height * cell.rect.minY,
                                   width: max(1, room.width * cell.rect.width),
                                   height: max(1, room.height * cell.rect.height))
                    RoundedRectangle(cornerRadius: interactive ? 5 : 3, style: .continuous)
                        .fill(cell.block == current ? AnyShapeStyle(.primary.opacity(0.9)) : AnyShapeStyle(.primary.opacity(0.18)))
                        .overlay {
                            RoundedRectangle(cornerRadius: interactive ? 5 : 3, style: .continuous)
                                .strokeBorder(.primary.opacity(cell.block == current ? 0 : 0.08), lineWidth: 0.5)
                        }
                        .frame(width: max(1, r.width - 1.5), height: max(1, r.height - 1.5))
                        .offset(x: r.minX, y: r.minY)
                        .contentShape(.rect)
                        .onTapGesture { if interactive { select?(cell.block) } }
                        .accessibilityLabel(Text(cell.name))
                        .accessibilityValue(Text(cell.block == current ? "Current" : ""))
                        .accessibilityAddTraits(interactive ? (cell.block == current ? [.isButton, .isSelected] : .isButton) : .isImage)
                }
            }
        }
        .accessibilityElement(children: interactive ? .contain : .combine)
        .background {
            RoundedRectangle(cornerRadius: interactive ? 8 : 5, style: .continuous)
                .fill(.primary.opacity(0.08))
        }
        .overlay {
            RoundedRectangle(cornerRadius: interactive ? 8 : 5, style: .continuous)
                .strokeBorder(.primary.opacity(0.12), lineWidth: 0.5)
        }
        .clipShape(RoundedRectangle(cornerRadius: interactive ? 8 : 5, style: .continuous))
    }
}

/// The enlarged map, from the toolbar's small one.
struct PaneMapSheet: View {
    let cells: [PaneCell]
    let current: String
    let select: (String) -> Void
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(spacing: 16) {
            Text("Panes").font(.headline)
            PaneMiniMap(cells: cells, current: current, interactive: true) { block in
                select(block)
                dismiss()
            }
            .frame(width: 220, height: 150)
            .padding(12)
            .glassEffect(.regular, in: .rect(cornerRadius: 16))
            Text("Tap a pane to switch. The list at the bottom switches too.")
                .font(.footnote)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
        }
        .padding(24)
        .presentationDetents([.height(300)])
        .presentationDragIndicator(.visible)
    }
}

/// The panes of the open window, at the bottom: each pane's program, the
/// current one ringed, and a pane's record as a dot. A tap switches; the
/// plus opens a new window, as Rex Go's bottom bar does.
struct PaneBar: View {
    let cells: [PaneCell]
    let current: String
    let program: (String) -> Program
    let select: (String) -> Void
    let newTab: () -> Void

    var body: some View {
        HStack(spacing: 2) {
            ForEach(cells) { cell in
                Button { select(cell.block) } label: {
                    ProgramTile(program: program(cell.block))
                        .frame(width: 24, height: 18)
                        .padding(6)
                        .background {
                            RoundedRectangle(cornerRadius: 8, style: .continuous)
                                .fill(cell.block == current ? AnyShapeStyle(.primary.opacity(0.16)) : AnyShapeStyle(.clear))
                        }
                        .overlay {
                            RoundedRectangle(cornerRadius: 8, style: .continuous)
                                .strokeBorder(.primary.opacity(cell.block == current ? 0.5 : 0), lineWidth: 1)
                        }
                        .overlay(alignment: .topTrailing) {
                            if let record = cell.record {
                                ProgramStatusMark(state: record.state, progress: record.progress, dotSize: 6)
                                    .offset(x: 2, y: -2)
                            }
                        }
                }
                .buttonStyle(.plain)
                .accessibilityLabel(Text(cell.name))
                .accessibilityValue(Text(cell.block == current ? "Current" : ""))
                .accessibilityAddTraits(cell.block == current ? [.isButton, .isSelected] : .isButton)
                .accessibilityHint(Text("Switch to this pane."))
            }
            Divider().frame(height: 20).padding(.horizontal, 2)
            Button(action: newTab) {
                Image(systemName: "plus")
                    .font(.footnote.weight(.semibold))
                    .frame(width: 24, height: 24)
                    .contentShape(.circle)
            }
            .buttonStyle(.plain)
            .accessibilityLabel("New Tab")
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 6)
        .glassEffect(.regular, in: .capsule)
        .animation(.snappy, value: current)
    }
}
