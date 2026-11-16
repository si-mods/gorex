import Foundation

/// What Settings tells the terminals, on the main thread.
extension Notification.Name {
    /// Settings changed the minimum contrast.
    static let minimumContrast = Notification.Name("minimumContrast")
    /// Settings changed the terminal theme.
    static let terminalTheme = Notification.Name("terminalTheme")
    /// Settings changed the terminal font family or size.
    static let terminalFont = Notification.Name("terminalFont")
}
