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

    /// The device's name; empty until makeName.
    static var name: String { UserDefaults.standard.string(forKey: nameKey) ?? "" }

    /// Gives the device a default name, once: from then on it is kept.
    static func makeName() {
        if name.isEmpty { UserDefaults.standard.set(defaultName(), forKey: nameKey) }
    }

    /// rexgo- and four random letters, as rexgo-kqzt. Not the phone's
    /// name: iOS tells every app only "iPhone", and two phones would clash.
    static func defaultName() -> String {
        "rexgo-" + String((0..<4).map { _ in "abcdefghijklmnopqrstuvwxyz".randomElement()! })
    }
}
