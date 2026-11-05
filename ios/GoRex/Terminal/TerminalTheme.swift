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

    /// The theme chosen in Settings; nil keeps the terminal's own colors.
    static var chosen: TerminalTheme? {
        let name = UserDefaults.standard.string(forKey: "theme") ?? ""
        return name.isEmpty ? nil : all.first { $0.name == name }
    }

    /// The theme as a stream's theme message, for VTerminal.setTheme. A
    /// theme message from the server replaces it, as each one replaces the last.
    var message: [String: Any] {
        func hex(_ i: Int) -> String { String(format: "%06x", colors[i]) }
        return ["background": hex(16), "foreground": hex(17), "cursor": hex(18),
                "palette": (0..<16).map { "\($0)=\(hex($0))" }]
    }
}
