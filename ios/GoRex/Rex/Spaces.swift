import Foundation

/// How the list groups windows: Settings › Grouping.
enum Grouping: String, CaseIterable {
    /// As the desktop groups them: each host lists its sessions, and a
    /// desktop's space also lists its windows on the other hosts the app
    /// connects to, which leave their own host's list.
    case spaces
    /// Each host lists its own windows only.
    case hosts

    static let key = "grouping"
    /// Spaces: until a desktop tells of a space across hosts it shows
    /// what grouping by host shows.
    static let standard = Grouping.spaces

    init(stored: String?) {
        self = stored.flatMap(Grouping.init(rawValue:)) ?? .standard
    }
}

/// GoRex's spaces across hosts, outside Rex's API (docs/rex-api.md): a
/// server that lists Spaces.feature answers GET /api/spaces with its
/// machine and, for each session whose space holds panes of other hosts,
/// those panes, each by its machine and its block there. The app shows
/// such a pane through its own connection to that machine, and only then.
enum Spaces {
    /// The welcome's feature of a server that answers /api/spaces.
    static let feature = "gorex.spaces"

    /// A pane of another host in a space.
    struct Member: Decodable, Hashable {
        let machine: String
        let block_id: String
    }

    struct Answer: Decodable {
        struct Entry: Decodable {
            let session_id: String
            let remote_blocks: [Member]
        }

        let machine_id: String
        let sessions: [Entry]

        init(json: Data) throws {
            self = try JSONDecoder().decode(Self.self, from: json)
        }
    }

    /// Reads the spaces of the server at base.
    static func read(from base: URL) async throws -> Answer {
        var r = URLRequest(url: base.apiURL("/api/spaces"))
        r.timeoutInterval = 10
        let (data, response) = try await URLSession.shared.data(for: r)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard status == 200 else {
            throw RexError(code: "spaces", message: HTTPURLResponse.localizedString(forStatusCode: status))
        }
        return try Answer(json: data)
    }
}

/// Where each window shows, grouped by the desktop's spaces: a window of
/// one host that a space of another host holds shows in that space's
/// card, and not in its own host's. Each window shows once: the first
/// space to hold it, in the hosts' order, has it.
struct CrossHost {
    /// A host's session.
    struct Place: Hashable {
        let host: UUID
        let session: String
    }

    /// A host's window, and the session it is in there.
    struct Row: Hashable {
        let host: UUID
        let session: String
        let window: String
    }

    /// A host as the grouping reads it: what the list shows of it.
    struct Source {
        struct Session {
            let id: String
            let view: SessionView?
            let remote: [Spaces.Member]
        }

        let host: UUID
        let machine: String?
        let sessions: [Session]
    }

    /// The windows of other hosts each session shows, in its space's order.
    private(set) var joined: [Place: [Row]] = [:]
    /// The windows that show in another host's session instead.
    private(set) var moved: Set<Row> = []

    init(_ sources: [Source] = []) {
        // Where each block is, by machine: the first host of a machine
        // reached by two addresses has it.
        var at: [String: [String: Row]] = [:]
        for h in sources {
            guard let m = h.machine, at[m] == nil else { continue }
            var blocks: [String: Row] = [:]
            for s in h.sessions {
                for w in s.view?.windows ?? [] {
                    for b in w.blocks where blocks[b] == nil {
                        blocks[b] = Row(host: h.host, session: s.id, window: w.window_id)
                    }
                }
            }
            at[m] = blocks
        }
        for h in sources {
            for s in h.sessions {
                for member in s.remote {
                    guard let row = at[member.machine]?[member.block_id], row.host != h.host,
                          moved.insert(row).inserted else { continue }
                    joined[Place(host: h.host, session: s.id), default: []].append(row)
                }
            }
        }
    }

    /// The windows of other hosts a session shows.
    func rows(_ host: UUID, _ session: String) -> [Row] {
        joined[Place(host: host, session: session)] ?? []
    }

    /// Whether a host's window shows in another host's session instead.
    func isMoved(_ host: UUID, _ session: String, _ window: String) -> Bool {
        moved.contains(Row(host: host, session: session, window: window))
    }
}

extension SessionView.Window {
    /// Every block of every layer of it.
    var blocks: [String] {
        (layers ?? []).flatMap { $0.blocks ?? [] }.map(\.block_id)
    }
}
