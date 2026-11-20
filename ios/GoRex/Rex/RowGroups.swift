import Foundation

/// How a card's rows group by session.
enum RowGroups {
    /// The window IDs of each session among a card's rows, in the rows' order.
    static func shown(_ rows: [(session: String, window: String)]) -> [String: [String]] {
        rows.reduce(into: [:]) { $0[$1.session, default: []].append($1.window) }
    }
}
