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
        /// The tiled layer's tree of splits, which its blocks' places are in.
        var layout: PaneLayout?

        init(blocks: [Block]? = nil, layout: PaneLayout? = nil) {
            self.blocks = blocks
            self.layout = layout
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

    /// A session's project, GoRex's own (docs/MOD-002-projects.md, Rex API): its
    /// folder on the host, the space it shows in ("" is Default), when a
    /// tab of it was last used, and its color. A host's tabs with no
    /// project have a project with no folder. Read leniently: a malformed
    /// field reads as empty rather than failing the view.
    struct Project: Decodable, Hashable {
        var dir: String
        var space: String
        var used: Date?
        var color: String?
        /// Pinned agent CLI IDs for this project's quick-launcher bar; nil
        /// means default (all detected agent CLIs on the host), empty means none.
        var launchers: [String]?

        init(dir: String, space: String = "", used: Date? = nil, color: String? = nil, launchers: [String]? = nil) {
            self.dir = dir
            self.space = space
            self.used = used
            self.color = color
            self.launchers = launchers
        }

        private enum CodingKeys: String, CodingKey { case dir, space, used, color, launchers }

        init(from decoder: Decoder) throws {
            let c = try? decoder.container(keyedBy: CodingKeys.self)
            dir = (try? c?.decodeIfPresent(String.self, forKey: .dir)) ?? ""
            space = (try? c?.decodeIfPresent(String.self, forKey: .space)) ?? ""
            used = Self.time((try? c?.decodeIfPresent(String.self, forKey: .used)) ?? nil)
            color = ((try? c?.decodeIfPresent(String.self, forKey: .color)) ?? nil).flatMap { $0.isEmpty ? nil : $0 }
            launchers = (try? c?.decodeIfPresent([String].self, forKey: .launchers)) ?? nil
        }

        /// The project of a session list's entry, as session.bootstrap and
        /// session_created give it; nil for a session of no project.
        static func of(_ entry: Any?) -> Project? {
            guard let p = entry as? [String: Any], let dir = p["dir"] as? String, !dir.isEmpty else { return nil }
            return Project(dir: dir, space: p["space"] as? String ?? "", used: time(p["used"] as? String),
                           color: (p["color"] as? String).flatMap { $0.isEmpty ? nil : $0 },
                           launchers: p["launchers"] as? [String])
        }

        /// A time as Go writes one, RFC 3339 with or without a fraction of
        /// a second; nil for none, and for Go's zero time.
        static func time(_ s: String?) -> Date? {
            guard let s, !s.isEmpty else { return nil }
            let f = ISO8601DateFormatter()
            f.formatOptions = [.withInternetDateTime]
            let whole = s.replacing(/\.[0-9]+/, with: "", maxReplacements: 1)
            guard let d = f.date(from: whole), d.timeIntervalSince1970 > 0 else { return nil }
            return d
        }
    }

    var revision: Int
    var session_id: String
    var label: String?
    var windows: [Window]?
    /// Left out by servers of before projects; with no folder for the
    /// host's tabs with no project.
    var project: Project?

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

    /// The blocks of its tiled layer, the first, in layout order.
    var panes: [String] {
        layers?.first?.blocks?.map(\.block_id) ?? []
    }

    /// The tree the tiled layer's pane places are in, nil when the view
    /// has none.
    var layout: PaneLayout? {
        layers?.first?.layout
    }

    /// Whether the block is in it: the one its row opens, or a tiled one.
    func holds(_ b: String) -> Bool {
        block == b || panes.contains(b)
    }

    /// The blocks it holds, each once: the one its row opens, then the
    /// tiled ones in layout order.
    var blockIDs: [String] {
        var seen = Set<String>()
        return ([block].compactMap(\.self) + panes).filter { seen.insert($0).inserted }
    }

    /// The blocks tiled behind the one its row opens.
    var back: [String] {
        panes.filter { $0 != block }
    }
}

/// A pane's place in a tiled layer, or a split of two such places, as
/// session.view gives it: normalized to the window's room.
final class PaneLayout: Decodable {
    /// [x, y, w, h] of the pane or split, fractions of the window's room.
    var rect: [Double]?
    /// The block of a leaf; nil in a split.
    var block_id: String?
    /// The split, nil in a leaf.
    var split: PaneSplit?

    init(rect: [Double]? = nil, block_id: String? = nil, split: PaneSplit? = nil) {
        self.rect = rect
        self.block_id = block_id
        self.split = split
    }
}

/// A split of two pane layouts: horizontal puts before at the left, else
/// on top; ratio is before's share.
final class PaneSplit: Decodable {
    var split_id: String?
    var direction: String
    var ratio: Double
    var before: PaneLayout
    var after: PaneLayout

    init(split_id: String? = nil, direction: String, ratio: Double, before: PaneLayout, after: PaneLayout) {
        self.split_id = split_id
        self.direction = direction
        self.ratio = ratio
        self.before = before
        self.after = after
    }
}
