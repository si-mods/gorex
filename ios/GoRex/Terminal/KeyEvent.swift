import Foundation

/// A key press as Ghostty's key encoder takes it (ghostty_input_key_s): the
/// physical key by its macOS key code, which Ghostty reads on every Apple
/// platform, the modifiers, those the text used up, and the text it types.
/// Ghostty encodes it for the program's keyboard modes.
struct KeyEvent: Equatable {
    /// Ghostty's modifier bits (ghostty_input_mods_e).
    struct Mods: OptionSet, Equatable {
        let rawValue: UInt32
        static let shift = Mods(rawValue: 1 << 0)
        static let ctrl = Mods(rawValue: 1 << 1)
        static let alt = Mods(rawValue: 1 << 2)
    }

    /// No physical key: text that no US key types.
    static let unidentified: UInt32 = 0xFFFF

    var code: UInt32
    var text = ""
    var mods: Mods = []
    var consumed: Mods = []
    var unshifted: Character?

    /// The keys that type no text, by their names.
    static let codes: [String: UInt32] = [
        "escape": 0x35, "tab": 0x30, "backspace": 0x33, "enter": 0x24, "delete": 0x75,
        "arrow_up": 0x7E, "arrow_down": 0x7D, "arrow_left": 0x7B, "arrow_right": 0x7C,
        "home": 0x73, "end": 0x77, "page_up": 0x74, "page_down": 0x79,
        "f1": 0x7A, "f2": 0x78, "f3": 0x63, "f4": 0x76, "f5": 0x60, "f6": 0x61,
        "f7": 0x62, "f8": 0x64, "f9": 0x65, "f10": 0x6D, "f11": 0x67, "f12": 0x6F,
    ]

    static func named(_ key: String, shift: Bool = false, ctrl: Bool = false, alt: Bool = false) -> KeyEvent {
        KeyEvent(code: codes[key] ?? unidentified, mods: mods(shift: shift, ctrl: ctrl, alt: alt))
    }

    private static func mods(shift: Bool, ctrl: Bool, alt: Bool) -> Mods {
        var m: Mods = []
        if shift { m.insert(.shift) }
        if ctrl { m.insert(.ctrl) }
        if alt { m.insert(.alt) }
        return m
    }

    /// The keys that type text on a US keyboard, with Shift, Ctrl or Alt held
    /// from the accessory bar. Shift capitalizes letters and holds on the keys
    /// that type no text, as Enter and Tab.
    static func typing(_ text: String, ctrl: Bool = false, alt: Bool = false, shift: Bool = false) -> [KeyEvent] {
        (shift ? text.uppercased() : text).map { c in
            var k = key(for: c)
            if shift, k.text.isEmpty { k.mods.insert(.shift) }
            k.mods.formUnion(mods(shift: false, ctrl: ctrl, alt: alt))
            return k
        }
    }

    private static func key(for c: Character) -> KeyEvent {
        switch c {
        case "\n", "\r": return .named("enter")
        case "\t": return .named("tab")
        case " ": return KeyEvent(code: 0x31, text: " ", unshifted: " ")
        default: break
        }
        guard let a = c.asciiValue, let (code, base) = keys[a] else { return KeyEvent(code: unidentified, text: String(c)) }
        return base == c
            ? KeyEvent(code: code, text: String(c), unshifted: base)
            : KeyEvent(code: code, text: String(c), mods: .shift, consumed: .shift, unshifted: base)
    }

    /// The keys of a US keyboard that type ASCII, by what they type with and
    /// without Shift: the key's code and its unshifted character.
    private static let keys: [UInt8: (UInt32, Character)] = {
        let keys: [(UInt32, Character, Character)] = [
            (0x00, "a", "A"), (0x01, "s", "S"), (0x02, "d", "D"), (0x03, "f", "F"), (0x04, "h", "H"),
            (0x05, "g", "G"), (0x06, "z", "Z"), (0x07, "x", "X"), (0x08, "c", "C"), (0x09, "v", "V"),
            (0x0B, "b", "B"), (0x0C, "q", "Q"), (0x0D, "w", "W"), (0x0E, "e", "E"), (0x0F, "r", "R"),
            (0x10, "y", "Y"), (0x11, "t", "T"), (0x1F, "o", "O"), (0x20, "u", "U"), (0x22, "i", "I"),
            (0x23, "p", "P"), (0x25, "l", "L"), (0x26, "j", "J"), (0x28, "k", "K"), (0x2D, "n", "N"),
            (0x2E, "m", "M"),
            (0x12, "1", "!"), (0x13, "2", "@"), (0x14, "3", "#"), (0x15, "4", "$"), (0x17, "5", "%"),
            (0x16, "6", "^"), (0x1A, "7", "&"), (0x1C, "8", "*"), (0x19, "9", "("), (0x1D, "0", ")"),
            (0x32, "`", "~"), (0x1B, "-", "_"), (0x18, "=", "+"), (0x21, "[", "{"), (0x1E, "]", "}"),
            (0x2A, "\\", "|"), (0x29, ";", ":"), (0x27, "'", "\""), (0x2B, ",", "<"), (0x2F, ".", ">"),
            (0x2C, "/", "?"),
        ]
        var m: [UInt8: (UInt32, Character)] = [:]
        for (code, base, shifted) in keys {
            m[base.asciiValue!] = (code, base)
            m[shifted.asciiValue!] = (code, base)
        }
        return m
    }()
}
