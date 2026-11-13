import Foundation

/// What session.view returns: a session's windows, and in each the blocks
/// of its layers. Only what the app shows is read.
struct SessionView: Decodable {
    struct Window: Decodable {
        var window_id: String
        var label: String?
        var focused_block_id: String?
        var layers: [Layer]?

        init(window_id: String, label: String? = nil, focused_block_id: String? = nil, layers: [Layer]? = nil) {
            self.window_id = window_id
            self.label = label
            self.focused_block_id = focused_block_id
            self.layers = layers
        }
    }

    struct Layer: Decodable {
        var blocks: [Block]?

        init(blocks: [Block]? = nil) {
            self.blocks = blocks
        }
    }

    struct Block: Decodable {
        var block_id: String
        var label: String?

        init(block_id: String, label: String? = nil) {
            self.block_id = block_id
            self.label = label
        }
    }

    var revision: Int
    var session_id: String
    var label: String?
    var windows: [Window]?

    /// A view to show, as Demo Mode makes one; the server's arrive as JSON.
    init(revision: Int, session_id: String, label: String? = nil, windows: [Window]? = nil) {
        self.revision = revision
        self.session_id = session_id
        self.label = label
        self.windows = windows
    }

    private struct Payload<T: Decodable>: Decodable { let payload: T }

    /// The view that session.view answers with, read from the bytes of its response.
    init(response: Data) throws {
        self = try JSONDecoder().decode(Payload<Self>.self, from: response).payload
    }

    /// The view that session.attach answers with.
    static func attached(response: Data) throws -> Self {
        struct Attached: Decodable { let view: SessionView }
        return try JSONDecoder().decode(Payload<Attached>.self, from: response).payload.view
    }

    /// The view of the selected session in a session.bootstrap response, if any.
    static func selected(response: Data) throws -> Self? {
        struct Bootstrap: Decodable { let selected_session: Attached? }
        struct Attached: Decodable { let view: SessionView? }
        return try JSONDecoder().decode(Payload<Bootstrap>.self, from: response).payload.selected_session?.view
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

    /// The name the user gave a block (session.set_block_label), nil while
    /// it has the gateway's default ("shell") or none.
    func blockName(_ blockID: String) -> String? {
        for layer in layers ?? [] {
            for b in layer.blocks ?? [] where b.block_id == blockID {
                guard let l = b.label, !l.isEmpty, l != "shell" else { return nil }
                return l
            }
        }
        return nil
    }

    /// The blocks of its tiled layer, the first.
    var tiled: [String] {
        layers?.first?.blocks?.map(\.block_id) ?? []
    }

    /// Whether the block is in it: the one its row opens, or a tiled one.
    func holds(_ b: String) -> Bool {
        block == b || tiled.contains(b)
    }

    /// The blocks tiled behind the one its row opens.
    var back: [String] {
        tiled.filter { $0 != block }
    }
}
