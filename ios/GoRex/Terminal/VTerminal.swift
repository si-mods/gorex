import CoreGraphics
import Foundation
import GhosttyVt

/// A libghostty-vt terminal restored from a GHOSTSNP snapshot, which a
/// terminal stream starts with. GhosttyKit cannot read a snapshot, so this
/// one turns it into the escape sequences that draw the same screen.
final class VTerminal {
    struct RGB: Equatable { var r, g, b: UInt8 }

    nonisolated(unsafe) private let term: GhosttyTerminal

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
        guard ok, let t else { return nil }
        term = t
    }

    deinit { ghostty_terminal_free(term) }

    func write(_ d: Data) {
        d.withUnsafeBytes { ghostty_terminal_vt_write(term, $0.bindMemory(to: UInt8.self).baseAddress, $0.count) }
    }

    var size: (cols: Int, rows: Int) {
        var cols: UInt16 = 0, rows: UInt16 = 0
        ghostty_terminal_get(term, GHOSTTY_TERMINAL_DATA_COLS, &cols)
        ghostty_terminal_get(term, GHOSTTY_TERMINAL_DATA_ROWS, &rows)
        return (Int(cols), Int(rows))
    }

    func resize(cols: Int, rows: Int, cellWidth: Int, cellHeight: Int) {
        guard cols > 0, rows > 0 else { return }
        ghostty_terminal_resize(term, UInt16(min(cols, 65535)), UInt16(min(rows, 65535)), UInt32(cellWidth), UInt32(cellHeight))
    }

    /// The escape sequences that draw the screen as it is, from a reset
    /// terminal: the modes, the scrollback and screen with their styles,
    /// the cursor, and the keyboard protocol's state. The default colors
    /// are left out: they are the theme.
    func replay() -> Data { format(GHOSTTY_FORMATTER_FORMAT_VT) }

    /// The screen and its scrollback as plain text.
    var text: String { String(decoding: format(GHOSTTY_FORMATTER_FORMAT_PLAIN), as: UTF8.self) }

    private func format(_ emit: GhosttyFormatterFormat) -> Data {
        var f: GhosttyFormatter?
        guard ghostty_formatter_terminal_new(nil, &f, term) == GHOSTTY_SUCCESS, let f else { return Data() }
        defer { ghostty_formatter_free(f) }
        // Trimming is on unless turned off; the extras are all but the palette.
        var emit = emit
        var trim = emit == GHOSTTY_FORMATTER_FORMAT_PLAIN
        var extras: GhosttyFormatterExtras = GHOSTTY_FORMATTER_EXTRAS_ALL & ~GHOSTTY_FORMATTER_EXTRA_PALETTE
        guard ghostty_formatter_set(f, GHOSTTY_FORMATTER_OPT_EMIT, &emit) == GHOSTTY_SUCCESS,
              ghostty_formatter_set(f, GHOSTTY_FORMATTER_OPT_TRIM, &trim) == GHOSTTY_SUCCESS,
              ghostty_formatter_set(f, GHOSTTY_FORMATTER_OPT_EXTRAS, &extras) == GHOSTTY_SUCCESS
        else { return Data() }
        var p: UnsafeMutablePointer<UInt8>?
        var n = 0
        guard ghostty_formatter_format_alloc(f, nil, &p, &n) == GHOSTTY_SUCCESS, let p else { return Data() }
        defer { ghostty_free(nil, p, n) }
        return Data(bytes: p, count: n)
    }

    /// The default colors, as a stream's theme message gives them; one the
    /// snapshot leaves unset is left out, for Ghostty's own.
    var theme: [String: Any] {
        var m: [String: Any] = [:]
        for (key, data) in [("background", GHOSTTY_TERMINAL_DATA_COLOR_BACKGROUND_DEFAULT),
                            ("foreground", GHOSTTY_TERMINAL_DATA_COLOR_FOREGROUND_DEFAULT),
                            ("cursor", GHOSTTY_TERMINAL_DATA_COLOR_CURSOR_DEFAULT)] {
            var c = GhosttyColorRgb()
            if ghostty_terminal_get(term, data, &c) == GHOSTTY_SUCCESS { m[key] = Self.hex(c) }
        }
        var palette = [GhosttyColorRgb](repeating: GhosttyColorRgb(), count: 256)
        if ghostty_terminal_get(term, GHOSTTY_TERMINAL_DATA_COLOR_PALETTE_DEFAULT, &palette) == GHOSTTY_SUCCESS {
            m["palette"] = palette.enumerated().map { "\($0.offset)=\(Self.hex($0.element))" }
        }
        return m
    }

    private static func hex(_ c: GhosttyColorRgb) -> String { String(format: "%02x%02x%02x", c.r, c.g, c.b) }
}

extension VTerminal.RGB {
    var cgColor: CGColor {
        CGColor(srgbRed: CGFloat(r) / 255, green: CGFloat(g) / 255, blue: CGFloat(b) / 255, alpha: 1)
    }

    static let black = Self(0)

    var packed: Int { Int(r) << 16 | Int(g) << 8 | Int(b) }

    /// Whether it is light, by its luma: dark text goes on it.
    var light: Bool { 0.299 * Double(r) + 0.587 * Double(g) + 0.114 * Double(b) >= 128 }

    init(_ packed: Int) { self.init(r: UInt8(packed >> 16 & 0xff), g: UInt8(packed >> 8 & 0xff), b: UInt8(packed & 0xff)) }

    /// A theme message's color: RRGGBB or #RRGGBB.
    init?(hex s: String) {
        let hex = s.hasPrefix("#") ? String(s.dropFirst()) : s
        guard hex.count == 6, let v = Int(hex, radix: 16) else { return nil }
        self.init(v)
    }
}
