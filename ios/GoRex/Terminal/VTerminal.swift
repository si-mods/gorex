import Foundation
import GhosttyVt

/// A libghostty-vt terminal restored from a GHOSTSNP snapshot and fed the
/// stream that follows it. The server's terminal answers the program's
/// queries, so this one writes nothing back.
final class VTerminal {
    struct RGB: Equatable { var r, g, b: UInt8 }

    /// One cell to draw.
    struct Cell {
        var x: Int
        var text: String
        var fg: RGB
        var bg: RGB?
        var bold, italic, faint, underline, strike: Bool
    }

    struct Cursor {
        var x, y: Int
        var style: GhosttyRenderStateCursorVisualStyle
    }

    nonisolated(unsafe) private let term: GhosttyTerminal
    nonisolated(unsafe) private let render: GhosttyRenderState
    nonisolated(unsafe) private let rows: GhosttyRenderStateRowIterator
    nonisolated(unsafe) private let cells: GhosttyRenderStateRowCells

    /// Decodes a complete snapshot, or returns nil for a bad one.
    init?(snapshot: Data) {
        var decoder: GhosttySnapshotDecoder?
        var t: GhosttyTerminal?
        let ok = snapshot.withUnsafeBytes { p -> Bool in
            guard ghostty_snapshot_decoder_new_buf(nil, &decoder, p.bindMemory(to: UInt8.self).baseAddress, p.count) == GHOSTTY_SUCCESS
            else { return false }
            defer { ghostty_snapshot_decoder_free(decoder) }
            return ghostty_snapshot_decoder_decode(decoder, &t) == GHOSTTY_SUCCESS
        }
        var r: GhosttyRenderState?, it: GhosttyRenderStateRowIterator?, c: GhosttyRenderStateRowCells?
        guard ok, let t, ghostty_render_state_new(nil, &r) == GHOSTTY_SUCCESS, let r,
              ghostty_render_state_row_iterator_new(nil, &it) == GHOSTTY_SUCCESS, let it,
              ghostty_render_state_row_cells_new(nil, &c) == GHOSTTY_SUCCESS, let c
        else { return nil }
        term = t
        render = r
        rows = it
        cells = c
    }

    deinit {
        ghostty_render_state_row_cells_free(cells)
        ghostty_render_state_row_iterator_free(rows)
        ghostty_render_state_free(render)
        ghostty_terminal_free(term)
    }

    func write(_ d: Data) {
        d.withUnsafeBytes { ghostty_terminal_vt_write(term, $0.bindMemory(to: UInt8.self).baseAddress, $0.count) }
    }

    var size: (cols: Int, rows: Int) {
        (Int(value(GHOSTTY_TERMINAL_DATA_COLS, UInt16(0))), Int(value(GHOSTTY_TERMINAL_DATA_ROWS, UInt16(0))))
    }

    func resize(cols: Int, rows: Int, cellWidth: Int, cellHeight: Int) {
        guard cols > 0, rows > 0 else { return }
        ghostty_terminal_resize(term, UInt16(min(cols, 65535)), UInt16(min(rows, 65535)), UInt32(cellWidth), UInt32(cellHeight))
    }

    /// Whether a drag should go to the program as the wheel: a full-screen
    /// program, or one that reads the mouse.
    var wantsWheel: Bool {
        value(GHOSTTY_TERMINAL_DATA_ACTIVE_SCREEN, GHOSTTY_TERMINAL_SCREEN_PRIMARY) == GHOSTTY_TERMINAL_SCREEN_ALTERNATE
            || value(GHOSTTY_TERMINAL_DATA_MOUSE_TRACKING, false)
    }

    /// Scrolls the viewport by rows (up is negative), or to the bottom.
    func scroll(_ delta: Int?) {
        var s = GhosttyTerminalScrollViewport()
        if let delta {
            s.tag = GHOSTTY_SCROLL_VIEWPORT_DELTA
            s.value.delta = delta
        } else {
            s.tag = GHOSTTY_SCROLL_VIEWPORT_BOTTOM
        }
        ghostty_terminal_scroll_viewport(term, s)
    }

    /// Applies a `theme` message: default colors as RRGGBB or #RRGGBB, and
    /// palette changes as index=RRGGBB. A color left out is the built-in one.
    func setTheme(_ m: [String: Any]) {
        for (key, opt) in [("foreground", GHOSTTY_TERMINAL_OPT_COLOR_FOREGROUND),
                           ("background", GHOSTTY_TERMINAL_OPT_COLOR_BACKGROUND),
                           ("cursor", GHOSTTY_TERMINAL_OPT_COLOR_CURSOR)] {
            if var c = (m[key] as? String).flatMap(Self.color) {
                ghostty_terminal_set(term, opt, &c)
            } else {
                ghostty_terminal_set(term, opt, nil)
            }
        }
        ghostty_terminal_set(term, GHOSTTY_TERMINAL_OPT_COLOR_PALETTE, nil)
        let changes = m["palette"] as? [String] ?? []
        if changes.isEmpty { return }
        var palette = [GhosttyColorRgb](repeating: GhosttyColorRgb(), count: 256)
        ghostty_terminal_get(term, GHOSTTY_TERMINAL_DATA_COLOR_PALETTE_DEFAULT, &palette)
        for entry in changes {
            let parts = entry.split(separator: "=", maxSplits: 1)
            if parts.count == 2, let i = Int(parts[0]), (0..<256).contains(i), let c = Self.color(String(parts[1])) {
                palette[i] = c
            }
        }
        ghostty_terminal_set(term, GHOSTTY_TERMINAL_OPT_COLOR_PALETTE, &palette)
    }

    private static func color(_ s: String) -> GhosttyColorRgb? {
        let hex = s.hasPrefix("#") ? String(s.dropFirst()) : s
        guard hex.count == 6, let v = UInt32(hex, radix: 16) else { return nil }
        return GhosttyColorRgb(r: UInt8(v >> 16 & 0xff), g: UInt8(v >> 8 & 0xff), b: UInt8(v & 0xff))
    }

    /// Takes the screen as it is now and calls row for each row of the
    /// viewport with its cells that have text or a background. Returns the
    /// default colors and the cursor, if it shows.
    @discardableResult
    func draw(row: (Int, [Cell]) -> Void) -> (fg: RGB, bg: RGB, cursor: Cursor?, cursorColor: RGB?) {
        ghostty_render_state_update(render, term)
        var colors = GhosttyRenderStateColors()
        colors.size = MemoryLayout<GhosttyRenderStateColors>.size
        ghostty_render_state_get(render, GHOSTTY_RENDER_STATE_DATA_COLORS, &colors)
        let fg = RGB(colors.foreground), bg = RGB(colors.background)

        var it: GhosttyRenderStateRowIterator? = rows
        ghostty_render_state_get(render, GHOSTTY_RENDER_STATE_DATA_ROW_ITERATOR, &it)
        var y = 0
        var line: [Cell] = []
        var codepoints = [UInt32](repeating: 0, count: 16)
        while ghostty_render_state_row_iterator_next(rows) {
            var c: GhosttyRenderStateRowCells? = cells
            ghostty_render_state_row_get(rows, GHOSTTY_RENDER_STATE_ROW_DATA_CELLS, &c)
            line.removeAll(keepingCapacity: true)
            var x = 0
            while ghostty_render_state_row_cells_next(cells) {
                defer { x += 1 }
                var n: UInt32 = 0
                ghostty_render_state_row_cells_get(cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_GRAPHEMES_LEN, &n)
                var hasBg = GhosttyColorRgb()
                let bgSet = ghostty_render_state_row_cells_get(cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_BG_COLOR, &hasBg) == GHOSTTY_SUCCESS
                var styled = false
                ghostty_render_state_row_cells_get(cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_HAS_STYLING, &styled)
                if n == 0 && !bgSet && !styled { continue }
                var style = GhosttyStyle()
                style.size = MemoryLayout<GhosttyStyle>.size
                ghostty_render_state_row_cells_get(cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_STYLE, &style)
                var fgc = GhosttyColorRgb()
                var cellFg = ghostty_render_state_row_cells_get(cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_FG_COLOR, &fgc) == GHOSTTY_SUCCESS ? RGB(fgc) : fg
                var cellBg: RGB? = bgSet ? RGB(hasBg) : nil
                if style.inverse {
                    (cellFg, cellBg) = (cellBg ?? bg, cellFg)
                }
                var text = ""
                if n > 0 && !style.invisible {
                    if codepoints.count < n { codepoints = [UInt32](repeating: 0, count: Int(n)) }
                    ghostty_render_state_row_cells_get(cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_GRAPHEMES_BUF, &codepoints)
                    text = String(String.UnicodeScalarView(codepoints[0..<Int(n)].compactMap(Unicode.Scalar.init)))
                }
                if text.isEmpty && cellBg == nil { continue }
                line.append(Cell(x: x, text: text, fg: cellFg, bg: cellBg, bold: style.bold, italic: style.italic,
                                 faint: style.faint, underline: style.underline != 0, strike: style.strikethrough))
            }
            row(y, line)
            y += 1
        }

        var cur = GhosttyRenderStateCursor()
        cur.size = MemoryLayout<GhosttyRenderStateCursor>.size
        ghostty_render_state_get(render, GHOSTTY_RENDER_STATE_DATA_CURSOR, &cur)
        let cursor = cur.visible && cur.viewport_has_value
            ? Cursor(x: Int(cur.viewport_x), y: Int(cur.viewport_y), style: cur.visual_style) : nil
        ghostty_render_state_clean(render)
        return (fg, bg, cursor, colors.cursor_has_value ? RGB(colors.cursor) : nil)
    }

    private func value<T>(_ data: GhosttyTerminalData, _ initial: T) -> T {
        var v = initial
        ghostty_terminal_get(term, data, &v)
        return v
    }
}

extension VTerminal.RGB {
    init(_ c: GhosttyColorRgb) { self.init(r: c.r, g: c.g, b: c.b) }
}
