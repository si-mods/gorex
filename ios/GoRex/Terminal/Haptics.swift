import UIKit

/// The light tap of the hand the key bar's keys give, for the screen's
/// controls that sit in the bar but are SwiftUI's own: the keys tick
/// themselves, and the bar watches the mic as it goes down.
enum Haptics {
    private static let tap = UIImpactFeedbackGenerator(style: .light)

    /// A tap, as a key gives one.
    static func tapped() {
        tap.impactOccurred()
    }
}
