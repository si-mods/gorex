import Foundation
import Network
import Observation
import SwiftUI

/// A server the app connects to, as the user added it.
struct HostConfig: Codable, Identifiable, Hashable {
    var id = UUID()
    var name: String
    var url: URL
    var desktop = false
    /// The program and arguments new windows run, as typed; nil or empty
    /// runs the host's login shell.
    var command: String?
    /// How the app reaches the host; nil is through the system. Optional,
    /// as a list saved without it must still decode.
    var transport: Transport?

    enum Transport: String, Codable {
        /// The phone's own network, or the Tailscale app's VPN.
        case system
        /// The built-in Tailscale's proxy (docs/ios-tailnet.md).
        case tailnet
    }
}

/// The built-in Tailscale's SOCKS5 proxy on loopback, of one run of the
/// engine: user name "tsnet" and a password of that run.
struct TailnetProxy: Equatable {
    let port: Int
    let password: String

    /// A session whose connections go through the proxy, which resolves
    /// the hosts' names on the tailnet itself.
    func session() -> URLSession {
        let c = URLSessionConfiguration.default
        var p = ProxyConfiguration(socksv5Proxy: .hostPort(host: "127.0.0.1", port: NWEndpoint.Port(integerLiteral: UInt16(port))))
        p.applyCredential(username: "tsnet", password: password)
        c.proxyConfigurations = [p]
        return URLSession(configuration: c)
    }
}

extension HostConfig {
    /// The host of a pairing QR code. GoRex and Rex show a plain address,
    /// http(s)://host[:port], with no path, query or user; nil for anything else.
    init?(pairing code: String) {
        guard var c = URLComponents(string: code.trimmingCharacters(in: .whitespacesAndNewlines)),
              let scheme = c.scheme?.lowercased(), ["http", "https"].contains(scheme),
              let host = c.host, !host.isEmpty,
              c.user == nil, c.password == nil, c.path.isEmpty, c.query == nil, c.fragment == nil
        else { return nil }
        c.scheme = scheme
        guard let url = c.url else { return nil }
        self.init(name: Self.name(host), url: url)
    }

    /// A host's name by its address, as Rex's API tells no machine name: an
    /// IP address as is, else the first label of the DNS name, less the
    /// "-gorex" that GoRex's built-in Tailscale adds.
    static func name(_ host: String) -> String {
        let h = host.trimmingCharacters(in: ["[", "]"])
        if IPv4Address(h) != nil || IPv6Address(h) != nil { return h }
        let label = h.split(separator: ".").first.map(String.init) ?? h
        return label.hasSuffix("-gorex") && label.count > 6 ? String(label.dropLast(6)) : label
    }

    /// Splits a command shell-style: white space separates words, quotes
    /// group them, and a backslash outside single quotes takes the next
    /// character as is.
    static func argv(_ command: String) -> [String] {
        var words: [String] = [], word = "", inWord = false, quote: Character?, escaped = false
        for ch in command {
            if escaped {
                word.append(ch); escaped = false
            } else if ch == "\\" && quote != "'" {
                escaped = true; inWord = true
            } else if let q = quote {
                if ch == q { quote = nil } else { word.append(ch) }
            } else if ch == "\"" || ch == "'" {
                quote = ch; inWord = true
            } else if ch.isWhitespace {
                if inWord { words.append(word); word = ""; inWord = false }
            } else {
                word.append(ch); inWord = true
            }
        }
        if inWord { words.append(word) }
        return words
    }
}

/// One host's control connection and what it shows: its sessions, their
/// windows, and each window's title and program.
@Observable
final class Host: Identifiable {
    enum Status: Equatable {
        case connecting
        case online
        case offline(String)
    }

    struct Session: Identifiable {
        let id: String
        var label: String
        var view: SessionView?
    }

    /// A record a program in a terminal reported (OSC 7501), as
    /// program_status_changed and com.superlogical.terminal.program_status
    /// give it: its state is idle, working, blocked, done or error.
    struct ProgramStatus: Equatable {
        let id: String
        var state: String
        var kind: String?
        var progress: Int?
        var app: String?
        var title: String?
        var msg: String?
        /// The last message the record carried, kept when a later report
        /// carries none.
        var lastMsg: String?
        /// When it last changed; nil when the server doesn't say.
        var updated: Date?

        init?(json: Any) {
            guard let j = json as? [String: Any], let state = j["state"] as? String else { return nil }
            func text(_ k: String) -> String? { (j[k] as? String).flatMap { $0.isEmpty ? nil : $0 } }
            id = j["id"] as? String ?? ""
            self.state = state
            kind = text("kind")
            progress = j["progress"] as? Int
            app = text("effective_app") ?? text("app")
            title = text("title")
            msg = text("msg")
            lastMsg = text("last_msg")
            // The server writes Go's zero time for none.
            updated = text("updated_at").flatMap { try? Date($0, strategy: .iso8601) }.flatMap { $0.timeIntervalSince1970 > 0 ? $0 : nil }
        }

        /// How much the record needs the user, as the desktop orders them.
        var rank: Int { ["idle": 1, "working": 2, "done": 3, "error": 4, "blocked": 5][state] ?? 0 }
    }

    /// Called when a program turns blocked, as it waits for the user: the
    /// host, the session, the block and the record.
    static var blocked: ((Host, String, String, ProgramStatus) -> Void)?
    /// Whether a block of the host is on screen, where the user sees its
    /// records as they come.
    static var shown: ((Host, String) -> Bool)?

    var config: HostConfig
    var id: UUID { config.id }
    private(set) var status = Status.connecting
    private(set) var sessions: [Session] = []
    /// Terminal titles and foreground programs, by block.
    private(set) var titles: [String: String] = [:]
    private(set) var programs: [String: String] = [:]
    /// What the programs in each block reported, by block.
    private(set) var statuses: [String: [ProgramStatus]] = [:]
    /// The done and error records the user saw, by block, as their keys
    /// (Inbox.swift).
    private(set) var seen: [String: Set<String>] = [:]
    /// The machine the host is, and the panes of other hosts in each of
    /// its sessions' spaces, by session, as /api/spaces tells (Spaces);
    /// nil and none from a server that doesn't.
    private(set) var machine: String?
    private(set) var remote: [String: [Spaces.Member]] = [:]
    private(set) var control: ControlConnection?

    @ObservationIgnored private var gate = EventGate()
    @ObservationIgnored private var token: String?
    @ObservationIgnored private var retry: Task<Void, Never>?
    @ObservationIgnored private var refreshing: Set<String> = []
    /// Sessions whose read titles, programs and records are being read
    /// again, as after a reconnect.
    @ObservationIgnored private var reread: Set<String> = []
    @ObservationIgnored private var wanted: [String: Int] = [:]
    @ObservationIgnored private var stopped = false
    @ObservationIgnored private var readingSpaces = false
    @ObservationIgnored private var spacesAgain = false
    /// Reads a server's spaces; tests replace it.
    @ObservationIgnored var spacesReader: (URL, URLSession) async throws -> Spaces.Answer = Spaces.read
    /// The built-in Tailscale's proxy of the moment; nil while it isn't
    /// running. The app sets it.
    static var tailnetProxy: () -> TailnetProxy? = { nil }
    @ObservationIgnored private var tailnetSession: (proxy: TailnetProxy, session: URLSession)?

    init(_ config: HostConfig) {
        self.config = config
    }

    /// The session the host's connections go through: the system's, or
    /// one through the built-in Tailscale's proxy of the moment, made anew
    /// for each run of it.
    func session() throws -> URLSession {
        guard config.transport == .tailnet else { return .shared }
        guard let p = Self.tailnetProxy() else {
            throw RexError(code: "offline", message: String(localized: "Tailscale is not running"))
        }
        if let s = tailnetSession, s.proxy == p { return s.session }
        tailnetSession?.session.invalidateAndCancel()
        let s = p.session()
        tailnetSession = (p, s)
        return s
    }

    /// Connects, unless connected or connecting.
    func connect() {
        guard control == nil, !stopped else { return }
        retry?.cancel()
        status = .connecting
        let session: URLSession
        do {
            session = try self.session()
        } catch {
            status = .offline(error.localizedDescription)
            scheduleRetry()
            return
        }
        let c = ControlConnection(base: config.url, session: session)
        control = c
        gate = EventGate()
        c.onEvent = { [weak self, weak c] e in
            guard let self, c === self.control else { return }
            for e in self.gate.receive(e) { self.apply(e) }
        }
        c.onClose = { [weak self, weak c] error in
            guard let self, c === self.control else { return }
            self.control = nil
            // What the connection said of the spaces goes with it.
            self.take(nil)
            self.status = .offline(error?.localizedDescription ?? "")
            self.scheduleRetry()
        }
        Task {
            do {
                try await c.open(token: token)
                token = c.reconnectToken
                try await bootstrap(c)
                status = .online
            } catch {
                c.close(error)
            }
        }
    }

    /// Disconnects for good, as when the host is removed.
    func stop() {
        stopped = true
        retry?.cancel()
        control?.close()
    }

    /// Connects anew, as to a new address: the old connection and its
    /// client end.
    func reconnect() {
        token = nil
        control?.close()
        connect()
    }

    /// Reconnects now, as after the app returns to the foreground.
    func reconnectIfNeeded() {
        if control == nil { connect() }
    }

    private func scheduleRetry() {
        retry?.cancel()
        retry = Task {
            try? await Task.sleep(for: .seconds(3))
            if !Task.isCancelled { connect() }
        }
    }

    /// session.bootstrap for every session, then session.attach for those
    /// it didn't attach, so that every window can be opened.
    private func bootstrap(_ c: ControlConnection) async throws {
        let (seq, r) = try await c.call("session.bootstrap", ["event_scope": "all"])
        let all = (r["attach_all"] as? [String: Any])?["sessions"] as? [[String: Any]] ?? []
        sessions = all.compactMap { s in
            (s["session_id"] as? String).map { Session(id: $0, label: s["label"] as? String ?? "") }
        }
        // What was read before keeps showing, as after a reconnect from
        // the background; each session's view reads it again in place.
        reread = Set(sessions.map(\.id))
        if let selected = r["selected_session"] as? [String: Any], let v = selected["view"] {
            setView(try SessionView(json: v))
        }
        for e in gate.open(at: seq) { apply(e) }
        for s in sessions where s.view == nil { attach(s.id) }
        readSpaces()
    }

    /// Reads the host's spaces again; one read at a time, repeated when
    /// asked again meanwhile, as a session's view changed or a new
    /// connection opened. Each read is of the connection of the moment,
    /// and an answer applies only while that connection lasts.
    private func readSpaces() {
        guard !readingSpaces else {
            spacesAgain = true
            return
        }
        readingSpaces = true
        Task {
            defer { readingSpaces = false }
            repeat {
                spacesAgain = false
                guard let c = control else { break }
                if !c.features.contains(Spaces.feature) {
                    take(nil)
                // A read that fails keeps what was read before.
                } else if let s = try? session(), let a = try? await spacesReader(config.url, s), c === control {
                    take(a)
                }
            } while spacesAgain
        }
    }

    /// Keeps what /api/spaces answered; nil for a server that doesn't.
    func take(_ a: Spaces.Answer?) {
        let m = a.flatMap { $0.machine_id.isEmpty ? nil : $0.machine_id }
        let r = Dictionary((a?.sessions ?? []).map { ($0.session_id, $0.remote_blocks) }) { first, _ in first }
        if machine != m { machine = m }
        if remote != r { remote = r }
    }

    /// The host as grouping by the desktop's spaces reads it; nil while
    /// offline, when the list shows none of its sessions.
    var crossHost: CrossHost.Source? {
        if case .offline = status { return nil }
        return CrossHost.Source(host: id, machine: machine, sessions: sessions.map {
            .init(id: $0.id, view: $0.view, remote: remote[$0.id] ?? [])
        })
    }

    private func attach(_ id: String) {
        Task {
            guard let r = try? await control?.call("session.attach", ["session_id": id]).payload,
                  let v = r["view"], let view = try? SessionView(json: v) else { return }
            setView(view)
        }
    }

    private func apply(_ e: RexEvent) {
        switch e.type {
        case "session_created":
            if let s = e.payload["session"] as? [String: Any], let id = s["session_id"] as? String,
               !sessions.contains(where: { $0.id == id }) {
                sessions.append(Session(id: id, label: s["label"] as? String ?? ""))
                attach(id)
                readSpaces()
            }
        case "session_destroyed":
            let id = e.payload["session_id"] as? String ?? e.sessionID
            sessions.removeAll { $0.id == id }
            id.map { reread.remove($0) }
        case "session_label_changed":
            if let i = sessions.firstIndex(where: { $0.id == e.sessionID }) {
                sessions[i].label = e.payload["label"] as? String ?? sessions[i].label
            }
        case "session_view_changed":
            if let id = e.sessionID, let rev = e.payload["revision"] as? Int, rev > revision(id) {
                wanted[id] = max(wanted[id] ?? 0, rev)
                refresh(id)
                readSpaces()
            }
        case "block_event":
            guard let block = e.payload["block_id"] as? String, let p = e.payload["payload"] as? [String: Any] else { return }
            switch e.payload["name"] as? String {
            case "title_changed": titles[block] = p["title"] as? String
            case "process_changed": programs[block] = Self.program(p["foreground"])
            case "program_status_changed":
                let was = statuses[block] ?? []
                let now = (p["records"] as? [Any] ?? []).compactMap(ProgramStatus.init)
                statuses[block] = now
                if Self.shown?(self, block) == true { see(block) }
                for r in now where r.state == "blocked" && !was.contains(where: { $0.id == r.id && $0.state == "blocked" }) {
                    if let s = e.sessionID { Self.blocked?(self, s, block, r) }
                }
            case "program_status_removed":
                let ids = p["ids"] as? [String] ?? []
                statuses[block]?.removeAll { ids.contains($0.id) }
            default: break
            }
        default:
            break
        }
    }

    /// A window's title, as the desktop names a tab: its name when it has
    /// one, else its terminal's title.
    func title(_ w: SessionView.Window) -> String {
        w.name ?? w.block.flatMap { titles[$0] }.flatMap { $0.isEmpty ? nil : $0 } ?? w.label ?? ""
    }

    /// The record of blocks that most needs the user, as the desktop's dot
    /// shows it: one blocked first.
    func status(_ blocks: [String]) -> ProgramStatus? {
        blocks.flatMap { statuses[$0] ?? [] }.max { $0.rank < $1.rank }
    }

    /// Marks a block's done and error records seen, as the desktop's pane
    /// does when it shows.
    func see(_ block: String) {
        let keys = (statuses[block] ?? []).filter { $0.state == "done" || $0.state == "error" }.map(\.seenKey)
        if !keys.isEmpty { seen[block, default: []].formUnion(keys) }
    }

    private func revision(_ id: String) -> Int {
        sessions.first { $0.id == id }?.view?.revision ?? -1
    }

    /// Reads a session's view again; one read at a time, repeated while
    /// a newer revision was announced meanwhile. A view that changes
    /// nothing, as of a session the host doesn't have, ends it.
    private func refresh(_ id: String) {
        guard refreshing.insert(id).inserted else { return }
        Task {
            defer { refreshing.remove(id) }
            while let want = wanted[id], want > revision(id),
                  let r = try? await control?.call("session.view", ["session_id": id]).payload,
                  let view = try? SessionView(json: r) {
                let had = revision(id)
                setView(view)
                if revision(id) == had { break }
            }
        }
    }

    private func setView(_ v: SessionView) {
        guard let i = sessions.firstIndex(where: { $0.id == v.session_id }) else { return }
        if let old = sessions[i].view, old.revision >= v.revision { return }
        sessions[i].view = v
        if let label = v.label { sessions[i].label = label }
        let again = reread.remove(v.session_id) != nil
        for w in v.windows ?? [] {
            for b in [w.block].compactMap(\.self) + w.tiled where titles[b] == nil || again { describe(session: v.session_id, block: b) }
        }
    }

    /// Reads a block's title, program and status, which its events keep up
    /// to date. When the block's values were read before, as after a
    /// reconnect, they keep showing and this read refreshes them in place:
    /// an answer applies when nothing has changed what it read since, so
    /// an event that arrived while it was in flight wins.
    private func describe(session: String, block: String) {
        let hadTitle = titles[block]
        let hadProgram = programs[block]
        let hadRecords = statuses[block]
        if hadTitle == nil { titles[block] = "" }
        Task {
            guard let c = control else { return }
            if let t = try? await c.callBlock("title", session: session, block: block)["title"] as? String,
               titles[block] == "" || titles[block] == hadTitle {
                titles[block] = t
            }
            if let p = try? await c.callBlock("process", session: session, block: block) {
                let program = Self.program(p["foreground"])
                if let program, programs[block] == nil || programs[block] == hadProgram {
                    programs[block] = program
                }
            }
            if let r = try? await c.callBlock("program_status", session: session, block: block)["records"] as? [Any] {
                if statuses[block] == nil || statuses[block] == hadRecords {
                    statuses[block] = r.compactMap(ProgramStatus.init)
                    if Self.shown?(self, block) == true { see(block) }
                }
            }
        }
    }

    /// The name of a foreground process, from process or process_changed.
    private static func program(_ p: Any?) -> String? {
        guard let p = p as? [String: Any] else { return nil }
        let argv0 = (p["argv0"] as? String).map { ($0 as NSString).lastPathComponent }
        return [argv0, p["name"] as? String].compactMap { $0 }.first { !$0.isEmpty }
    }

    /// Whether the server takes files into its machine's temporary folder
    /// (Upload).
    var canUpload: Bool { control?.features.contains(Upload.feature) == true }

    /// Sends a file into the temporary folder of the host's machine, for
    /// block, and returns its path there as a word of the block's shell.
    func upload(_ file: URL, block: String) async throws -> String {
        _ = try live()
        return try await Upload.send(file, name: file.lastPathComponent, block: block, to: config.url, session: try session())
    }

    /// The control connection; offline, an error to tell.
    func live() throws -> ControlConnection {
        guard let control else { throw RexError(code: "offline", message: String(localized: "Offline")) }
        return control
    }

    /// Opens a new window running the shell, and returns its block.
    func newWindow(in session: String) async throws -> String? {
        let r = try await live().call("session.new_window", [
            "session_id": session, "focus": false, "layout": ["block": shell],
        ]).payload
        return (r["block_ids"] as? [Any])?.first as? String
    }

    /// Makes a session, named by the server, with a window running the
    /// shell, and returns the session and the window's block.
    func newSession() async throws -> (String, String)? {
        let r = try await live().call("session.create", ["initial_windows": [["layout": ["block": shell]]]]).payload
        guard let s = r["session_id"] as? String,
              let w = (r["initial_windows"] as? [[String: Any]])?.first,
              let b = (w["block_ids"] as? [Any])?.first as? String else { return nil }
        return (s, b)
    }

    /// The block a new window runs: the shell, or the command of the host.
    private var shell: [String: Any] {
        var block: [String: Any] = ["flavor": "com.superlogical.terminal.shell"]
        let argv = HostConfig.argv(config.command ?? "")
        if !argv.isEmpty { block["options"] = ["command": argv] }
        return block
    }

    /// Names a window; an empty name gives back its default.
    func renameWindow(_ window: String, in session: String, to label: String) async throws {
        try await live().call("session.set_window_label", ["session_id": session, "window_id": window, "label": label])
    }

    /// Moves a window before another, or to the end when before is nil.
    func moveWindow(_ window: String, in session: String, before: String?) async throws {
        var p: [String: Any] = ["session_id": session, "window_id": window]
        if let before { p["before_window_id"] = before }
        try await live().call("session.move_window", p)
    }

    /// The window to move a window before, for it to take the place of
    /// another row shown: before that row when it is above, else just
    /// after it, before the next window in the server's order, nil at the
    /// end. The server's order also has windows the list leaves out.
    static func before(_ window: String, onto target: String, shown: [String], all: [String]) -> String? {
        guard let from = shown.firstIndex(of: window), let to = shown.firstIndex(of: target), to > from else { return target }
        let after = all.firstIndex(of: target).map { $0 + 1 } ?? all.count
        return after < all.count ? all[after] : nil
    }

    /// Names a session; an empty name gives back its default.
    func renameSession(_ session: String, to label: String) async throws {
        try await live().call("session.set_label", ["session_id": session, "label": label])
    }

    /// Closes a session, its windows and their panes.
    func closeSession(_ session: String) async throws {
        try await live().call("session.destroy", ["session_id": session])
    }

    /// Closes a window and its panes.
    func closeWindow(_ window: String, in session: String) async throws {
        try await live().call("session.close_window", ["session_id": session, "window_id": window])
    }
}

/// The hosts the user added, kept in UserDefaults.
@Observable
final class HostStore {
    private(set) var hosts: [Host]

    init() {
        let saved = UserDefaults.standard.data(forKey: "hosts")
            .flatMap { try? JSONDecoder().decode([HostConfig].self, from: $0) } ?? []
        hosts = saved.map(Host.init)
        hosts.forEach { $0.connect() }
    }

    func add(_ config: HostConfig) {
        let h = Host(config)
        hosts.append(h)
        h.connect()
        save()
    }

    func remove(_ id: UUID) {
        hosts.first { $0.id == id }?.stop()
        hosts.removeAll { $0.id == id }
        save()
    }

    /// Changes a host; one with a new address or transport connects
    /// again. The host stays the same, for the terminals open on it.
    func update(_ config: HostConfig) {
        guard let h = host(config.id) else { return }
        let moved = h.config.url != config.url || h.config.transport != config.transport
        h.config = config
        if moved { h.reconnect() }
        save()
    }

    func move(from source: IndexSet, to destination: Int) {
        hosts.move(fromOffsets: source, toOffset: destination)
        save()
    }

    func host(_ id: UUID) -> Host? { hosts.first { $0.id == id } }

    func host(at url: URL) -> Host? { hosts.first { $0.config.url == url } }

    private func save() {
        UserDefaults.standard.set(try? JSONEncoder().encode(hosts.map(\.config)), forKey: "hosts")
    }
}
