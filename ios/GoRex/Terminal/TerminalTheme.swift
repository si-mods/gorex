import Foundation

/// A Ghostty theme, as scripts/ghostty-themes writes it from mygo's: its 16
/// palette colors, then background, foreground, cursor, cursor text,
/// selection and selection text, as 0xRRGGBB.
struct TerminalTheme: Decodable {
    let name: String
    let colors: [Int]

    static let all: [TerminalTheme] = Bundle.main.url(forResource: "ghostty-themes", withExtension: "json")
        .flatMap { try? Data(contentsOf: $0) }
        .flatMap { try? JSONDecoder().decode([TerminalTheme].self, from: $0) } ?? []

    /// The theme until one is chosen.
    static let standard = "Gruvbox Dark"

    /// The name of the theme chosen in Settings; "" keeps the host's colors.
    static var name: String { UserDefaults.standard.string(forKey: "theme") ?? standard }

    /// The theme chosen in Settings; nil keeps the host's colors.
    static var chosen: TerminalTheme? {
        let name = name
        return name.isEmpty ? nil : all.first { $0.name == name }
    }

    /// Whether its background is light.
    var light: Bool { VTerminal.RGB(colors[16]).light }

    /// The theme as a stream's theme message, for TerminalView.setTheme. A
    /// theme message from the server replaces it, as each one replaces the last.
    var message: [String: Any] {
        func hex(_ i: Int) -> String { String(format: "%06x", colors[i]) }
        return ["background": hex(16), "foreground": hex(17), "cursor": hex(18),
                "palette": (0..<16).map { "\($0)=\(hex($0))" }]
    }
}
