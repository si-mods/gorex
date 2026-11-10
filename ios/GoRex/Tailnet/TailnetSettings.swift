import Foundation

/// Settings › Tailscale, as UserDefaults keeps them (docs/ios-tailnet.md).
enum TailnetSettings {
    static let onKey = "tailnet"
    static let controlKey = "tailnetControl"
    static let nameKey = "tailnetName"

    /// Whether the built-in Tailscale is on.
    static var isOn: Bool { UserDefaults.standard.bool(forKey: onKey) }

    /// The control server's URL; empty for Tailscale's own.
    static var controlURL: String { UserDefaults.standard.string(forKey: controlKey) ?? "" }

    /// The device's name: the one set, else a new default, kept from then on.
    static var name: String {
        if let n = UserDefaults.standard.string(forKey: nameKey), !n.isEmpty { return n }
        let n = defaultName()
        UserDefaults.standard.set(n, forKey: nameKey)
        return n
    }

    /// rexgo- and four random letters, as rexgo-kqzt. Not the phone's
    /// name: iOS tells every app only "iPhone", and two phones would clash.
    static func defaultName() -> String {
        "rexgo-" + String((0..<4).map { _ in "abcdefghijklmnopqrstuvwxyz".randomElement()! })
    }
}
