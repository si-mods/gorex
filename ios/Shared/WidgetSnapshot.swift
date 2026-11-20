import Foundation

/// What the widgets show: the hosts' inboxes as the app last
/// saw them, a file the app writes and the widget extension reads
/// (docs/ios-widgets.md, Snapshot). It is a saved format another build
/// reads: v is the format, and fields are added as optional only. It is a
/// cache: the next time the app is in front writes it anew.
nonisolated struct WidgetSnapshot: Codable, Equatable, Sendable {
    /// The format this build writes and reads.
    static let format = 1
    /// The App Group the app and the extension share.
    static let group = "group.dev.gorex.ios"
    /// The most items written for one host, and the most characters of a
    /// heading or a detail.
    static let maxItems = 20
    static let maxText = 200

    var v = Self.format
    /// When the app wrote it.
    var written: Date
    var hosts: [Host]

    struct Host: Codable, Equatable, Identifiable, Sendable {
        var id: UUID
        var name: String
        /// Its SF Symbol, as HostConfig.icon.
        var icon: String?
        /// Whether it was online when written; a host that was not has no
        /// items, as its inbox is empty then.
        var online: Bool
        /// Its inbox, as Inbox.groups orders it.
        var items: [Item]
        /// Every window, for the Terminal widget's picker (phase 2); none
        /// written before it.
        var windows: [Window]? = nil
    }

    /// A window in the inbox, as a row of it shows.
    struct Item: Codable, Equatable, Sendable {
        var session: String
        /// The block the row opens.
        var block: String
        /// The window's title.
        var window: String
        /// The program whose tile the row shows, by its id or its process.
        var app: String?
        /// blocked, error, done or working.
        var state: String
        var kind: String?
        var progress: Int?
        var heading: String
        var detail: String
        var updated: Date?
    }

    struct Window: Codable, Equatable, Sendable {
        var session: String
        var block: String
        var title: String
        var app: String?
    }

    /// The text cut to what a snapshot keeps.
    static func cut(_ s: String) -> String {
        s.count > maxText ? String(s.prefix(maxText)) : s
    }

    /// Where the file is: in the App Group's container; nil without one.
    static var url: URL? {
        FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: group)?
            .appending(path: "widgets/snapshot.json")
    }

    /// The snapshot in a file; nil for none, one of another format, or one
    /// that does not decode, for which a widget only asks to open the app.
    static func read(from url: URL) -> WidgetSnapshot? {
        guard let data = try? Data(contentsOf: url) else { return nil }
        return decode(data)
    }

    static func decode(_ data: Data) -> WidgetSnapshot? {
        struct Format: Decodable { var v: Int }
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        guard (try? d.decode(Format.self, from: data))?.v == format else { return nil }
        return try? d.decode(WidgetSnapshot.self, from: data)
    }

    func encoded() throws -> Data {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        e.outputFormatting = .sortedKeys
        return try e.encode(self)
    }

    /// Writes it whole or not at all, readable once the phone was first
    /// unlocked, as the extension reads it on a locked phone.
    func write(to url: URL) throws {
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try encoded().write(to: url, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
    }

    /// The items of every host, the most urgent first, the newest first
    /// within a state, as Inbox.groups orders them, with their hosts.
    var items: [(host: Host, item: Item)] {
        let all = hosts.flatMap { h in h.items.map { (host: h, item: $0) } }
        return Self.states.flatMap { s in
            all.filter { $0.item.state == s }.sorted { ($0.item.updated ?? .distantPast) > ($1.item.updated ?? .distantPast) }
        }
    }

    /// The states an inbox groups, the most urgent first, as Inbox.states.
    static let states = ["blocked", "error", "done", "working"]

    /// How many items are in each state.
    func count(_ state: String) -> Int {
        hosts.reduce(0) { n, h in n + h.items.count(where: { $0.state == state }) }
    }
}

/// What a widget's tap opens in the app: rexgo://open?host=&session=&block=
/// a terminal, rexgo://inbox the status inbox. The pairing code
/// (rexgo://tailcat) is HostConfig(tailcat:)'s.
nonisolated enum WidgetLink: Equatable, Sendable {
    case open(host: UUID, session: String, block: String)
    case inbox

    init?(url: URL) {
        guard let c = URLComponents(url: url, resolvingAgainstBaseURL: false), c.scheme?.lowercased() == "rexgo" else { return nil }
        func value(_ n: String) -> String? { c.queryItems?.first { $0.name == n }?.value.flatMap { $0.isEmpty ? nil : $0 } }
        switch c.host?.lowercased() {
        case "inbox":
            self = .inbox
        case "open":
            guard let host = value("host").flatMap(UUID.init(uuidString:)), let session = value("session"), let block = value("block") else { return nil }
            self = .open(host: host, session: session, block: block)
        default:
            return nil
        }
    }

    var url: URL {
        var c = URLComponents()
        c.scheme = "rexgo"
        switch self {
        case .inbox:
            c.host = "inbox"
        case let .open(host, session, block):
            c.host = "open"
            c.queryItems = [.init(name: "host", value: host.uuidString), .init(name: "session", value: session), .init(name: "block", value: block)]
        }
        return c.url!
    }
}
