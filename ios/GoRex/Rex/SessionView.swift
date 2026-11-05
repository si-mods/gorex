import Foundation

/// What session.view returns: a session's windows, and in each the blocks
/// of its layers. Only what the app shows is read.
struct SessionView: Decodable {
    struct Window: Decodable {
        let window_id: String
        let label: String?
        let focused_block_id: String?
        let layers: [Layer]?
    }

    struct Layer: Decodable {
        let blocks: [Block]?
    }

    struct Block: Decodable {
        let block_id: String
    }

    let revision: Int
    let session_id: String
    let label: String?
    let windows: [Window]?

    init(json: Any) throws {
        self = try JSONDecoder().decode(Self.self, from: JSONSerialization.data(withJSONObject: json))
    }
}

extension SessionView.Window {
    /// The block a window's row opens: its focused one, else its first.
    var block: String? {
        focused_block_id ?? layers?.lazy.compactMap { $0.blocks?.first?.block_id }.first
    }
}
