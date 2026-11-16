import Foundation

/// What a window's row opens: the window's block on a host.
struct TerminalTarget: Hashable {
    let host: UUID
    let session: String
    let block: String
    let title: String
    /// A notification's or the inbox's target: open this pane, not the one
    /// this device last viewed of its window.
    var explicit = false
}
