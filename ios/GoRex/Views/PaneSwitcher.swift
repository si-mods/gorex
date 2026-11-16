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
            let hInset: CGFloat = 6
            let vInset: CGFloat = 3.5
            let room = CGRect(x: hInset, y: vInset,
                              width: max(0, g.size.width - hInset * 2),
                              height: max(0, g.size.height - vInset * 2))
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
        HStack(spacing: 0) {
            ForEach(cells) { cell in
                Button { select(cell.block) } label: {
                    ProgramTile(program: program(cell.block))
                        .frame(width: 21 * ProgramTiles.scale, height: 16 * ProgramTiles.scale)
                        .overlay {
                            if cell.block == current {
                                RoundedRectangle(cornerRadius: 8.5, style: .continuous)
                                    .strokeBorder(.primary.opacity(0.55), lineWidth: 1.3)
                                    .padding(-2)
                            }
                        }
                        .overlay(alignment: .topTrailing) {
                            if let record = cell.record {
                                PaneStatus(record: record)
                                    .offset(x: 4, y: -4)
                            }
                        }
                        .padding(5)
                        .contentShape(.rect)
                }
                .buttonStyle(.plain)
                .accessibilityLabel(Text(cell.name))
                .accessibilityValue(Text(cell.block == current ? "Current" : ""))
                .accessibilityAddTraits(cell.block == current ? [.isButton, .isSelected] : .isButton)
                .accessibilityHint(Text("Switch to this pane."))
            }
            Divider().frame(height: 22).padding(.horizontal, 4)
            Button(action: newTab) {
                Image(systemName: "plus")
                    .font(.footnote.weight(.semibold))
                    .frame(width: 30, height: 30)
                    .contentShape(.circle)
            }
            .buttonStyle(.plain)
            .accessibilityLabel("New Tab")
            .padding(.horizontal, 3)
        }
        .padding(.horizontal, 5)
        .padding(.vertical, 3)
        .glassEffect(.regular, in: .capsule)
        .animation(.snappy, value: current)
    }
}


/// A pane's record in the bar, small enough not to cover its tile: a dot
/// in the color of the state, or a quiet one while its program works.
private struct PaneStatus: View {
    let record: PaneRecord

    var body: some View {
        switch record.state {
        case "blocked":
            Circle().fill(Color.attention).frame(width: 7, height: 7)
        case "error":
            Circle().fill(.red).frame(width: 7, height: 7)
        case "done":
            Circle().fill(Color.done).frame(width: 7, height: 7)
        case "working":
            Circle().fill(.secondary).frame(width: 6, height: 6)
        default:
            EmptyView()
        }
    }
}
