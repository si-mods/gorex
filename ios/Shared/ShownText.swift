import Foundation

extension String {
    /// The text as a Live Activity and a notification show it: without
    /// what a terminal's escapes leave of it, and without the characters
    /// iOS draws as nothing or as a box. Gone are the escape sequences
    /// (CSI, OSC, DCS and the like, and a lone ESC), the C0 and C1
    /// controls but tab and line breaks, the bidi controls and zero-width
    /// characters, private-use glyphs (a Nerd Font icon, a picture's
    /// placeholder), noncharacters and U+FFFD. Emoji, CJK text and the
    /// joiners of emoji sequences stay. A character that can be an emoji
    /// but whose default style is text (⏺, ❤) gets U+FE0E behind it:
    /// iOS draws the emoji style where Unicode asks for text, and a
    /// terminal's ⏺ is a bullet, not a button.
    nonisolated var shownText: String {
        let scalars = Array(unicodeScalars)
        // The common case, plain ASCII: nothing to drop.
        if scalars.allSatisfy({ $0.value == 0x09 || $0.value == 0x0A || $0.value >= 0x20 && $0.value < 0x7F }) {
            return self
        }
        var out = String.UnicodeScalarView()
        var i = 0
        while i < scalars.count {
            let v = scalars[i].value
            if Shown.starts(v) {
                i = Shown.end(scalars, at: i)
                continue
            }
            if Shown.keeps(v) {
                out.append(scalars[i])
                if Shown.textStyled(scalars, at: i) { out.append("\u{FE0E}") }
            }
            i += 1
        }
        return String(out)
    }
}

/// shownText's rules.
nonisolated private enum Shown {
    /// Whether a scalar is shown: not a control, a bidi control, a
    /// private-use glyph, a noncharacter or U+FFFD.
    static func keeps(_ v: UInt32) -> Bool {
        switch v {
        case 0x09, 0x0A: return true
        case 0x00...0x1F, 0x7F, 0x80...0x9F: return false
        // Bidi controls, which turn what follows around, and the
        // characters that draw nothing.
        case 0x061C, 0x180E, 0x200B, 0x200E, 0x200F, 0x202A...0x202E,
             0x2060...0x2064, 0x2066...0x206F, 0xFEFF: return false
        // Private use: a Nerd Font icon, the placeholder of a picture.
        case 0xE000...0xF8FF, 0xF0000...0xFFFFD, 0x100000...0x10FFFD: return false
        // Noncharacters, and the replacement character of broken UTF-8.
        case 0xFDD0...0xFDEF, 0xFFFD: return false
        default: return v & 0xFFFE != 0xFFFE
        }
    }

    /// Whether the scalar at i needs U+FE0E to draw the text style that
    /// is its default: an emoji-capable character iOS would draw as an
    /// emoji where Unicode says text (⏺, ❤, ☀), but not one that is
    /// already part of an emoji sequence — a char with its own FE0E or
    /// FE0F, one a ZWJ joins, a keycap's, or one a skin tone modifies.
    static func textStyled(_ s: [Unicode.Scalar], at i: Int) -> Bool {
        let p = s[i].properties
        guard s[i].value > 0x7F, p.isEmoji, !p.isEmojiPresentation else { return false }
        if i > 0, s[i - 1].value == 0x200D { return false }
        guard i + 1 < s.count else { return true }
        switch s[i + 1].value {
        case 0xFE0E, 0xFE0F, 0x200D, 0x20E3, 0x1F3FB...0x1F3FF: return false
        default: return true
        }
    }

    /// Whether a scalar starts an escape sequence: ESC, or one of the C1
    /// introducers, as a program of the 8-bit controls sends.
    static func starts(_ v: UInt32) -> Bool {
        v == 0x1B || v == 0x9B || v == 0x9D || v == 0x90 || v == 0x98 || v == 0x9E || v == 0x9F
    }

    /// Where the escape sequence at start ends: after CSI's final byte,
    /// after OSC's, DCS's, SOS's, PM's and APC's terminator, and after a
    /// two-character escape.
    static func end(_ s: [Unicode.Scalar], at start: Int) -> Int {
        let v = s[start].value
        let i = start + 1
        switch v {
        case 0x9B:
            return csi(s, from: i)
        case 0x9D, 0x90, 0x98, 0x9E, 0x9F:
            return text(s, from: i)
        default:  // ESC.
            guard i < s.count else { return i }
            switch s[i].value {
            case 0x5B: return csi(s, from: i + 1)
            case 0x5D, 0x50, 0x58, 0x5E, 0x5F: return text(s, from: i + 1)
            case 0x30...0x7E: return i + 1  // A two-character escape, such as ESC c.
            default:  // Intermediates, then a final byte; another escape starts its own.
                guard !starts(s[i].value) else { return i }
                var j = i
                while j < s.count, (0x20...0x2F).contains(s[j].value) { j += 1 }
                return min(j + 1, s.count)
            }
        }
    }

    /// After CSI's parameters, its final byte in 0x40–0x7E.
    private static func csi(_ s: [Unicode.Scalar], from start: Int) -> Int {
        var i = start
        while i < s.count, !(0x40...0x7E).contains(s[i].value) { i += 1 }
        return min(i + 1, s.count)
    }

    /// After OSC's, DCS's, SOS's, PM's and APC's text: its BEL, its
    /// 8-bit ST or its ESC \.
    private static func text(_ s: [Unicode.Scalar], from start: Int) -> Int {
        var i = start
        while i < s.count {
            if s[i].value == 0x07 || s[i].value == 0x9C { return i + 1 }
            if s[i].value == 0x1B, i + 1 < s.count, s[i + 1].value == 0x5C { return i + 2 }
            i += 1
        }
        return i
    }
}
