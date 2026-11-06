import Foundation

/// A key press as block.event's key event carries it: the physical key by
/// its W3C code in snake_case, the modifiers, and the text it types. The
/// server encodes it for the program's keyboard modes.
struct KeyEvent: Equatable {
    var key: String
    var text = ""
    var mods: [String] = []
    var consumed: [String] = []
    var unshifted: Character?

    var payload: [String: Any] {
        [
            "key": key, "mods": mods.joined(separator: "+"), "consumed_mods": consumed.joined(separator: "+"),
            "text": text, "unshifted_codepoint": unshifted?.unicodeScalars.first.map { Int($0.value) } ?? 0,
            "composing": false, "action": "press",
        ]
    }

    static func named(_ key: String, shift: Bool = false, ctrl: Bool = false, alt: Bool = false) -> KeyEvent {
        KeyEvent(key: key, mods: [(shift, "shift"), (ctrl, "ctrl"), (alt, "alt")].filter(\.0).map(\.1))
    }

    /// The keys that type text on a US keyboard, with Shift, Ctrl or Alt held
    /// from the accessory bar. Shift capitalizes letters and holds on the keys
    /// that type no text, as Enter and Tab. Alt is sent as Escape first, as
    /// terminals read it.
    static func typing(_ text: String, ctrl: Bool = false, alt: Bool = false, shift: Bool = false) -> [KeyEvent] {
        (shift ? text.uppercased() : text).flatMap { c -> [KeyEvent] in
            var k = key(for: c)
            if shift, k.text.isEmpty { k.mods = ["shift"] }
            if ctrl {
                k.mods.insert("ctrl", at: k.mods.first == "shift" ? 1 : 0)
                if k.key != "unidentified" { k.text = "" }
            }
            return alt ? [.named("escape"), k] : [k]
        }
    }

    private static func key(for c: Character) -> KeyEvent {
        switch c {
        case "\n", "\r": return .named("enter")
        case "\t": return .named("tab")
        case " ": return KeyEvent(key: "space", text: " ", unshifted: " ")
        default: break
        }
        if let a = c.asciiValue {
            if c.isLetter {
                let lower = Character(c.lowercased())
                return c.isUppercase
                    ? KeyEvent(key: "key_\(lower)", text: String(c), mods: ["shift"], consumed: ["shift"], unshifted: lower)
                    : KeyEvent(key: "key_\(lower)", text: String(c), unshifted: lower)
            }
            if c.isNumber { return KeyEvent(key: "digit_\(c)", text: String(c), unshifted: c) }
            if let (name, base) = punctuation[a] {
                return base == c
                    ? KeyEvent(key: name, text: String(c), unshifted: base)
                    : KeyEvent(key: name, text: String(c), mods: ["shift"], consumed: ["shift"], unshifted: base)
            }
        }
        return KeyEvent(key: "unidentified", text: String(c))
    }

    /// The punctuation keys of a US keyboard, by the ASCII they type with
    /// and without Shift: the key's name and its unshifted character.
    private static let punctuation: [UInt8: (String, Character)] = {
        let keys: [(String, Character, Character)] = [
            ("backquote", "`", "~"), ("minus", "-", "_"), ("equal", "=", "+"),
            ("bracket_left", "[", "{"), ("bracket_right", "]", "}"), ("backslash", "\\", "|"),
            ("semicolon", ";", ":"), ("quote", "'", "\""), ("comma", ",", "<"),
            ("period", ".", ">"), ("slash", "/", "?"),
            ("digit_1", "1", "!"), ("digit_2", "2", "@"), ("digit_3", "3", "#"), ("digit_4", "4", "$"),
            ("digit_5", "5", "%"), ("digit_6", "6", "^"), ("digit_7", "7", "&"), ("digit_8", "8", "*"),
            ("digit_9", "9", "("), ("digit_0", "0", ")"),
        ]
        var m: [UInt8: (String, Character)] = [:]
        for (name, base, shifted) in keys {
            m[base.asciiValue!] = (name, base)
            m[shifted.asciiValue!] = (name, base)
        }
        return m
    }()
}
