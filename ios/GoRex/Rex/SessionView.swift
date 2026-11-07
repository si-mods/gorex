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

    /// The name the user gave it; nil when its label is the server's
    /// default, "Window " and the end of its ID.
    var name: String? {
        guard let label, !label.isEmpty, label != "Window " + window_id.suffix(8) else { return nil }
        return label
    }

    /// The blocks of its tiled layer, the first.
    var tiled: [String] {
        layers?.first?.blocks?.map(\.block_id) ?? []
    }

    /// The blocks tiled behind the one its row opens.
    var back: [String] {
        tiled.filter { $0 != block }
    }
}
