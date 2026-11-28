import CoreGraphics

/// A pane's most urgent record, as the map and the bar show it: the state,
/// and a working program's progress when it reports one.
struct PaneRecord: Equatable {
    let state: String
}

/// A pane's place in a window, normalized to the window's room: the block,
/// its box, its name and its record.
struct PaneCell: Identifiable {
    let block: String
    let rect: CGRect
    let name: String
    let record: PaneRecord?
    var agentActive: Bool = false

    var id: String { block }
}

/// The cells of a window's tiled panes, in layout order, from the view's
/// split tree. A leaf the window's panes do not hold is skipped, so the map
/// and the pane bar always show the same set. Without a tree, the panes go
/// in a row, as they do before a window is laid out.
func paneCells(
    layout: PaneLayout?,
    panes: [String],
    name: (String) -> String,
    record: (String) -> PaneRecord?,
    agentActive: (String) -> Bool = { _ in false }
) -> [PaneCell] {
    guard !panes.isEmpty else { return [] }
    let held = Set(panes)
    var out: [PaneCell] = []
    func walk(_ n: PaneLayout, _ rect: CGRect) {
        if let block = n.block_id {
            guard held.contains(block) else { return }
            out.append(PaneCell(block: block, rect: norm(n.rect) ?? rect, name: name(block), record: record(block), agentActive: agentActive(block)))
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
            PaneCell(block: block, rect: CGRect(x: CGFloat(i) / n, y: 0, width: 1 / n, height: 1), name: name(block), record: record(block), agentActive: agentActive(block))
        }
    }
    return out
}

/// A rect from the view's [x, y, w, h], normalized; nil when it is absent.
private func norm(_ r: [Double]?) -> CGRect? {
    guard let r, r.count == 4, r[2] > 0, r[3] > 0 else { return nil }
    return CGRect(x: r[0], y: r[1], width: r[2], height: r[3])
}

/// Which way a pane switch goes from the current pane.
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

/// The axis a two-finger drag between panes moves along.
enum PaneAxis {
    case horizontal, vertical
}

/// The axis a pane drag locks to once its fingers have gone a few points:
/// the one they went farther along. Nil until then.
func paneDragAxis(_ translation: CGPoint, lockAfter: CGFloat = 10) -> PaneAxis? {
    guard max(abs(translation.x), abs(translation.y)) >= lockAfter else { return nil }
    return abs(translation.x) >= abs(translation.y) ? .horizontal : .vertical
}

/// The pane a drag brings, as the content follows the fingers: a drag
/// left (sign -1) brings the pane on the right, a drag up the one below.
func paneDragDirection(_ axis: PaneAxis, sign: CGFloat) -> PaneDirection {
    switch axis {
    case .horizontal: sign < 0 ? .right : .left
    case .vertical: sign < 0 ? .down : .up
    }
}

/// How far a drag has moved the panes along its way, 0 to 1: the fingers'
/// way over the page's size, no farther back or on than the panes go.
func paneDragProgress(_ along: CGFloat, sign: CGFloat, size: CGFloat) -> CGFloat {
    guard size > 0 else { return 0 }
    return min(max(along * sign / size, 0), 1)
}

/// How far the pane moves under a drag with no pane to bring: a fifth of
/// the fingers' way at first, less as they go, never 40 points.
func paneRubberBand(_ along: CGFloat, resistance: CGFloat = 0.2, limit: CGFloat = 40) -> CGFloat {
    let d = abs(along) * resistance
    let band = limit * d / (d + limit)
    return along < 0 ? -band : band
}

/// Whether a pane drag switches as the fingers lift: past 35% of the way,
/// or flung its way at 500 points a second. One flung back stays.
/// The velocity is along the drag's way, negative when flung back.
func paneDragCommits(progress: CGFloat, velocity: CGFloat) -> Bool {
    guard velocity > -200 else { return false }
    return progress >= 0.35 || velocity >= 500
}

/// The panes to open and to close so that every pane of the window stays
/// open: open, the blocks with a terminal (on the page or kept off it);
/// kept, those kept off it, which alone close.
func paneKeepChanges(open: [String], kept: [String], panes: [String]) -> (open: [String], close: [String]) {
    var seen = Set(open)
    let opening = panes.filter { seen.insert($0).inserted }
    let closing = kept.filter { !panes.contains($0) }
    return (opening, closing)
}
