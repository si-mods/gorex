import SwiftUI

/// A window's pane layout as a small diagram: the current pane is lit, the
/// others dim. It only shows, floating briefly after a pane switch, as Rex
/// Go's does, and takes no touches: they go to the terminal under it.
struct PaneMiniMap: View {
    let cells: [PaneCell]
    let current: String
    /// The current pane's color, from its program's tile, as Rex Go tints
    /// the pane it shows.
    let currentColor: Color
    @Environment(\.colorScheme) private var scheme

    /// Explicit ink, so the current pane keeps a clear contrast in both
    /// schemes and does not depend on a hierarchical foreground style.
    private var ink: Color { scheme == .dark ? .white : .black }

    var body: some View {
        GeometryReader { g in
            let inset: CGFloat = 3
            let gap: CGFloat = 3
            let room = CGRect(x: inset, y: inset,
                              width: max(0, g.size.width - inset * 2),
                              height: max(0, g.size.height - inset * 2))
            ZStack(alignment: .topLeading) {
                ForEach(cells) { cell in
                    let r = CGRect(x: room.minX + room.width * cell.rect.minX,
                                   y: room.minY + room.height * cell.rect.minY,
                                   width: max(1, room.width * cell.rect.width),
                                   height: max(1, room.height * cell.rect.height))
                    RoundedRectangle(cornerRadius: 3, style: .continuous)
                        .fill(cell.block == current ? AnyShapeStyle(currentColor) : AnyShapeStyle(ink.opacity(0.18)))
                        .overlay {
                            RoundedRectangle(cornerRadius: 3, style: .continuous)
                                .strokeBorder(ink.opacity(cell.block == current ? 0 : 0.08), lineWidth: 0.5)
                        }
                        .frame(width: max(1, r.width - gap), height: max(1, r.height - gap))
                        .offset(x: r.minX + gap / 2, y: r.minY + gap / 2)
                        .accessibilityLabel(Text(cell.name))
                        .accessibilityValue(cell.block == current ? Text("Current") : Text(""))
                        .accessibilityAddTraits(.isImage)
                }
            }
        }
        .accessibilityElement(children: .combine)
        .allowsHitTesting(false)
        .background {
            RoundedRectangle(cornerRadius: 5, style: .continuous)
                .fill(ink.opacity(0.08))
        }
        .overlay {
            RoundedRectangle(cornerRadius: 5, style: .continuous)
                .strokeBorder(ink.opacity(0.12), lineWidth: 0.5)
        }
        .clipShape(RoundedRectangle(cornerRadius: 5, style: .continuous))
        .animation(.easeInOut(duration: 0.25), value: current)
    }
}

/// The panes of the open window, at the bottom: each pane's program, the
/// current one filled, and a pane's record as a dot. A tap switches; the
/// plus splits the current pane in place.
struct PaneBar: View {
    /// The bar's height: a tile and its padding, in the capsule's.
    static let height: CGFloat = 32

    let cells: [PaneCell]
    let current: String
    let program: (String) -> Program
    let select: (String) -> Void
    let newSplit: () -> Void

    var body: some View {
        HStack(spacing: 8) {
            HStack(spacing: 0) {
                ForEach(cells) { cell in
                    Button { select(cell.block) } label: {
                        ProgramTile(program: program(cell.block), scale: 1)
                            .frame(width: 21, height: 16)
                            .overlay(alignment: .topTrailing) {
                                if let record = cell.record {
                                    PaneStatus(record: record)
                                        .offset(x: 3, y: -3)
                                }
                            }
                            .padding(4)
                            .background {
                                if cell.block == current {
                                    RoundedRectangle(cornerRadius: 6, style: .continuous)
                                        .fill(.primary.opacity(0.16))
                                }
                            }
                            .contentShape(.rect)
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel(Text(cell.name))
                    .accessibilityValue(cell.block == current ? Text("Current") : Text(""))
                    .accessibilityAddTraits(cell.block == current ? [.isButton, .isSelected] : .isButton)
                    .accessibilityHint(Text("Switch to this pane."))
                }
            }
            .padding(.horizontal, 6)
            .padding(.vertical, 4)
            .glassEffect(.regular, in: .capsule)
            Button(action: newSplit) {
                Image(systemName: "plus")
                    .font(.footnote.weight(.semibold))
                    .frame(width: 30, height: 30)
                    .contentShape(.circle)
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Split Pane")
            .glassEffect(.regular, in: .circle)
        }
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
