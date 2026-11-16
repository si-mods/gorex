import CoreGraphics

/// A pane's most urgent record, as the map and the bar show it: the state,
/// and a working program's progress when it reports one.
struct PaneRecord: Equatable {
    let state: String
    let progress: Int?
}

/// A pane's place in a window, normalized to the window's room: the block,
/// its box, its name and its record.
struct PaneCell: Identifiable {
    let block: String
    let rect: CGRect
    let name: String
    let record: PaneRecord?

    var id: String { block }
}

/// The cells of a window's tiled panes, in layout order, from the view's
/// split tree. A leaf the window's panes do not hold is skipped, so the map
/// and the pane bar always show the same set. Without a tree, the panes go
/// in a row, as they do before a window is laid out.
func paneCells(layout: PaneLayout?, panes: [String], name: (String) -> String, record: (String) -> PaneRecord?) -> [PaneCell] {
    guard !panes.isEmpty else { return [] }
    let held = Set(panes)
    var out: [PaneCell] = []
    func walk(_ n: PaneLayout, _ rect: CGRect) {
        if let block = n.block_id {
            guard held.contains(block) else { return }
            out.append(PaneCell(block: block, rect: norm(n.rect) ?? rect, name: name(block), record: record(block)))
            return
        }
        guard let split = n.split else { return }
        let r = norm(n.rect) ?? rect
        let ratio = min(max(split.ratio, 0.05), 0.95)
        let (a, b): (CGRect, CGRect)
        if split.direction == "vertical" {
            a = CGRect(x: r.minX, y: r.minY, width: r.width, height: r.height * ratio)
            b = CGRect(x: r.minX, y: r.minY + a.height, width: r.width, height: r.height - a.height)
        } else {
            a = CGRect(x: r.minX, y: r.minY, width: r.width * ratio, height: r.height)
            b = CGRect(x: r.minX + a.width, y: r.minY, width: r.width - a.width, height: r.height)
        }
        walk(split.before, a)
        walk(split.after, b)
    }
    if let layout {
        walk(layout, CGRect(x: 0, y: 0, width: 1, height: 1))
    }
    guard out.count == panes.count else {
        let n = CGFloat(panes.count)
        return panes.enumerated().map { i, block in
            PaneCell(block: block, rect: CGRect(x: CGFloat(i) / n, y: 0, width: 1 / n, height: 1), name: name(block), record: record(block))
        }
    }
    return out
}

/// A rect from the view's [x, y, w, h], normalized; nil when it is absent.
private func norm(_ r: [Double]?) -> CGRect? {
    guard let r, r.count == 4, r[2] > 0, r[3] > 0 else { return nil }
    return CGRect(x: r[0], y: r[1], width: r[2], height: r[3])
}

/// The pane one step along the window's order, for a swipe: nil at the
/// first or the last, as a swipe stops there and does not cross a tab.
func paneStep(_ panes: [String], from current: String, by delta: Int) -> String? {
    guard panes.count > 1, let i = panes.firstIndex(of: current) else { return nil }
    let next = i + delta
    return panes.indices.contains(next) ? panes[next] : nil
}

/// Which way a swipe on the layout map points.
enum PaneDirection {
    case left, right, up, down
}

/// The pane nearest the current one in a direction, by their places: a
/// candidate must lie mostly that way, so a swipe down from a pane that
/// spans the height does not jump to a diagonal. Nil when none is there.
func paneNeighbor(_ cells: [PaneCell], from current: String, direction: PaneDirection) -> String? {
    guard let here = cells.first(where: { $0.block == current }) else { return nil }
    let c = CGPoint(x: here.rect.midX, y: here.rect.midY)
    var best: (block: String, score: CGFloat)?
    for cell in cells where cell.block != current {
        let p = CGPoint(x: cell.rect.midX, y: cell.rect.midY)
        let dx = p.x - c.x, dy = p.y - c.y
        let primary: CGFloat, secondary: CGFloat
        switch direction {
        case .left: primary = -dx; secondary = abs(dy)
        case .right: primary = dx; secondary = abs(dy)
        case .up: primary = -dy; secondary = abs(dx)
        case .down: primary = dy; secondary = abs(dx)
        }
        guard primary > 0.001, primary >= secondary else { continue }
        let score = primary + secondary * 1.5
        if best == nil || score < best!.score { best = (cell.block, score) }
    }
    return best?.block
}
