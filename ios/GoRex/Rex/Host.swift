import Foundation
import Network
import Observation
import SwiftUI

/// A server the app connects to, as the user added it.
struct HostConfig: Codable, Identifiable, Hashable {
    var id = UUID()
    var name: String
    var url: URL
    /// Set with the icon, for a list saved before the icon to read.
    var desktop = false
    /// The SF Symbol that stands for the host, one of Icon.all; nil is
    /// the laptop or desktop of a list saved before it.
    var icon: String?
    /// The program and arguments new windows run, as typed; nil or empty
    /// runs the host's login shell.
    var command: String?
    /// How the app reaches the host; nil is through the system. Optional,
    /// as a list saved without it must still decode.
    var transport: Transport?
    /// The GoRex machine's tailcat address, for a host paired by its Rex
    /// Go code; optional as transport is.
    var tailcat: Tailcat?

    enum Transport: String, Codable {
        /// The phone's own network, or the Tailscale app's VPN.
        case system
        /// The built-in Tailscale's proxy (docs/ios-tailnet.md).
        case tailnet
        /// The engine's tailcat client, through the same proxy
        /// (docs/ios-tailcat.md).
        case tailcat
    }

    /// A GoRex machine as its Rex Go code names it: its tailcat address, a
    /// secret, and its name.
    struct Tailcat: Codable, Hashable {
        var address: String
        var machine: String
    }
}

/// The built-in Tailscale's SOCKS5 proxy on loopback, as one run of the
/// engine uses it: user name "tsnet" and a password of its own. The
/// proxy lasts until iOS reclaims its listener in a suspension, when the
/// engine reads a new one (TailnetEngine.resume); its tailnet connections
/// end with the run, so another run is another value, even on the same
/// port.
struct LoopbackProxy: Equatable {
    let port: Int
    let password: String
    let run: Int

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
    /// The icons a host can take, as Rex offers them.
    static let icons: [(name: LocalizedStringKey, symbol: String)] = [
        ("Globe", "globe"),
        ("Server", "server.rack"),
        ("Cloud", "cloud"),
        ("MacBook", "laptopcomputer"),
        ("Mac mini", "macmini"),
        ("Mac Studio", "macstudio"),
        ("Desktop", "desktopcomputer"),
    ]

    /// The SF Symbol of the host's icon.
    var symbol: String { icon ?? (desktop ? "desktopcomputer" : "laptopcomputer") }
}

extension HostConfig {
    /// The transport of a host as entered: as switched, else the host's
    /// own while its address stays, else by its address (rule).
    static func transport(switched: Bool?, was old: HostConfig?, at url: URL, rule: @autoclosure () -> Transport?) -> Transport? {
        if let switched { return switched ? .tailnet : .system }
        if let old, old.url == url { return old.transport }
        return rule()
    }

    /// How the app reaches the host: over tailcat when it has a tailcat
    /// address, whether or not the built-in Tailscale is on; through the
    /// built-in Tailscale when the host is on it and it is on; else the
    /// system's way, as through the Tailscale app.
    func reach(tailnetOn: Bool) -> Transport {
        if tailcat != nil { return .tailcat }
        return transport == .tailnet && tailnetOn ? .tailnet : .system
    }

    /// The transport of a host added now: .tailnet while the built-in
    /// Tailscale is on, for a name under the tailnet's MagicDNS suffix or
    /// an address of Tailscale's, in 100.64.0.0/10 or fd7a:115c:a1e0::/48;
    /// else nil, the system's way.
    static func transport(of url: URL, tailnetOn: Bool, suffix: String?) -> Transport? {
        guard tailnetOn, let host = url.host()?.lowercased().trimmingCharacters(in: ["[", "]"]) else { return nil }
        if let s = suffix?.lowercased().trimmingCharacters(in: ["."]), !s.isEmpty,
           host.trimmingCharacters(in: ["."]).hasSuffix("." + s) { return .tailnet }
        if let a = IPv4Address(host) {
            let b = [UInt8](a.rawValue)
            return b[0] == 100 && b[1] & 0xC0 == 64 ? .tailnet : nil
        }
        if let a = IPv6Address(host) {
            return [UInt8](a.rawValue).starts(with: [0xFD, 0x7A, 0x11, 0x5C, 0xA1, 0xE0]) ? .tailnet : nil
        }
        return nil
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

    /// The host of a Rex Go code, rexgo://tailcat?a=<address>&n=<machine>,
    /// as GoRex's Rex Go QR… shows it; nil for anything else. The engine
    /// checks the address in full as it pairs.
    init?(tailcat code: String) {
        guard let c = URLComponents(string: code.trimmingCharacters(in: .whitespacesAndNewlines)),
              c.scheme?.lowercased() == "rexgo", c.host?.lowercased() == "tailcat", c.path.isEmpty,
              c.user == nil, c.password == nil, c.port == nil, c.fragment == nil,
              let a = c.queryItems?.first(where: { $0.name == "a" })?.value,
              a.wholeMatch(of: /tc[A-Za-z0-9_-]+/) != nil
        else { return nil }
        let n = c.queryItems?.first { $0.name == "n" }?.value?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let id = UUID()
        self.init(id: id, name: n.isEmpty ? "GoRex" : n, url: Self.tailcatURL(id), tailcat: Tailcat(address: a, machine: n))
    }

    /// Where the engine's proxy reaches a tailcat host: Rex's API port at
    /// <id>.tailcat.invalid, a name that resolves nowhere else.
    static func tailcatURL(_ id: UUID) -> URL {
        URL(string: "http://\(tailcatID(id)).tailcat.invalid:7297")!
    }

    /// The host's name to the engine (SetTailcatHosts), a DNS label.
    static func tailcatID(_ id: UUID) -> String { id.uuidString.lowercased() }

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
    /// What each block shows, by block. The dictionary changes only as
    /// blocks come; a block's own changes redraw only the views that read
    /// that block, as titles spin many times a second.
    private(set) var blocks: [String: Block] = [:]

    @Observable
    final class Block {
        /// Its terminal's title and foreground program.
        var title: String?
        var program: String?
        /// What its programs reported.
        var statuses: [ProgramStatus]?
        /// The done and error records the user saw, as their keys
        /// (Inbox.swift).
        var seen: Set<String> = []
    }
    /// The machine the host is, and the panes of other hosts in each of
    /// its sessions' spaces, by session, as /api/spaces tells (Spaces);
    /// nil and none from a server that doesn't.
    private(set) var machine: String?
    private(set) var remote: [String: [Spaces.Member]] = [:]
    private(set) var control: ControlConnection?

    @ObservationIgnored private var gate = EventGate()
    @ObservationIgnored private var token: String?
    @ObservationIgnored private var retry: Task<Void, Never>?
    /// Connections that failed in a row, for the wait before the next.
    @ObservationIgnored private var attempts = 0
    /// Those waiting for the host to be online.
    @ObservationIgnored private var waiters: [CheckedContinuation<ControlConnection, Error>] = []
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
    static var tailnetProxy: () -> LoopbackProxy? = { nil }
    /// The proxy the tailcat hosts go through, the same one whether or not
    /// the built-in Tailscale runs; nil when there is none. The app sets it.
    static var tailcatProxy: () -> LoopbackProxy? = { nil }
    /// Tells the engine the hosts, as their list changes, for it to reach
    /// those with a tailcat address. The app sets it.
    static var tailcatHosts: ([HostConfig]) -> Void = { _ in }
    @ObservationIgnored private var proxySession: (proxy: LoopbackProxy, session: URLSession)?

    init(_ config: HostConfig) {
        self.config = config
    }

    /// Whether the host's connections go through the built-in Tailscale.
    var viaTailnet: Bool { config.reach(tailnetOn: TailnetSettings.isOn) == .tailnet }

    /// The session the host's connections go through: the system's, or
    /// one through the engine's proxy of the moment, made anew for each
    /// run of the built-in Tailscale and each new proxy.
    func session() throws -> URLSession {
        let p: LoopbackProxy
        switch config.reach(tailnetOn: TailnetSettings.isOn) {
        case .system:
            return .shared
        case .tailnet:
            guard let t = Self.tailnetProxy() else {
                throw RexError(code: "offline", message: String(localized: "Tailscale is not running"))
            }
            p = t
        case .tailcat:
            guard let t = Self.tailcatProxy() else {
                throw RexError(code: "offline", message: String(localized: "Rex Go's connection to GoRex is not ready"))
            }
            p = t
        }
        if let s = proxySession, s.proxy == p { return s.session }
        proxySession?.session.invalidateAndCancel()
        let s = p.session()
        proxySession = (p, s)
        return s
    }

    /// Connects, unless connected or connecting. Tried again after a
    /// failure, the host stays offline until it is online.
    func connect(again: Bool = false) {
        guard control == nil, !stopped else { return }
        retry?.cancel()
        if !again {
            attempts = 0
            status = .connecting
        }
        let name = "control \(config.url.host() ?? "")"
        let session: URLSession
        do {
            session = try self.session()
        } catch {
            ConnectionLog.note("\(name): \(error.diagnosis)")
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
            ConnectionLog.note("\(name): closed: \(error?.diagnosis ?? "by the app")")
            self.control = nil
            // What the connection said of the spaces goes with it.
            self.take(nil)
            let offline = Status.offline(error?.localizedDescription ?? "")
            if self.status != offline { self.status = offline }
            // Refused, it waits for a change: the engine running again, as
            // after a sign-in, or the app back in the foreground.
            if let error, (error as? RexError)?.code == "refused" {
                self.settle(.failure(error))
            } else {
                self.scheduleRetry()
            }
        }
        let began = ContinuousClock.now
        ConnectionLog.note("\(name): opening via \(config.reach(tailnetOn: TailnetSettings.isOn).rawValue)")
        Task {
            do {
                try await c.open(token: token)
                token = c.reconnectToken
                try await bootstrap(c)
                status = .online
                attempts = 0
                settle(.success(c))
                ConnectionLog.note("\(name): online, \(began.duration(to: .now).ms)")
            } catch let e as RexError where e.code == "refused" && viaTailnet {
                // The gateway lets in only whom it says (docs/ios-tailnet.md, Server).
                c.close(RexError(code: e.code, message: String(localized: "Refused: sign Rex Go in as the Tailscale user who signed in the GoRex machine, not with a tagged key, or turn on --allow-tailnet-members there.")))
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
        settle(.failure(CancellationError()))
    }

    /// Connects anew, as to a new address: the old connection and its
    /// client end.
    func reconnect() {
        token = nil
        resume()
    }

    /// Connects anew, continuing the client, as after a suspension: the
    /// connection can look open while its path died.
    func resume() {
        control?.close()
        connect()
    }

    /// Reconnects now, as after the app returns to the foreground.
    func reconnectIfNeeded() {
        if control == nil { connect() }
    }

    /// Connects again after a failure, waiting longer each time in a row,
    /// up to ten seconds, as long as it takes.
    private func scheduleRetry() {
        retry?.cancel()
        attempts += 1
        let wait = Self.backoff(after: attempts)
        retry = Task {
            try? await Task.sleep(for: wait)
            if !Task.isCancelled { connect(again: true) }
        }
    }

    /// The wait before connecting again after failures in a row: a
    /// terminal's, which never gives up here.
    static func backoff(after failures: Int) -> Duration {
        TerminalSession.backoff(after: failures) ?? .seconds(10)
    }

    /// The control connection, once the host is online: bootstrapped, so
    /// that its blocks can be opened. It waits while the host connects or
    /// is offline, and fails when the host is removed or refused.
    func online() async throws -> ControlConnection {
        if status == .online, let control { return control }
        if stopped { throw CancellationError() }
        return try await withCheckedThrowingContinuation { waiters.append($0) }
    }

    /// Resumes those waiting for the host to be online.
    private func settle(_ r: Result<ControlConnection, Error>) {
        let w = waiters
        waiters = []
        w.forEach { $0.resume(with: r) }
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
            // Observation skips a value set again unchanged.
            switch e.payload["name"] as? String {
            case "title_changed": self.block(block).title = p["title"] as? String
            case "process_changed": self.block(block).program = Self.program(p["foreground"])
            case "program_status_changed":
                let b = self.block(block)
                let was = b.statuses ?? []
                let now = (p["records"] as? [Any] ?? []).compactMap(ProgramStatus.init)
                b.statuses = now
                if Self.shown?(self, block) == true { see(block) }
                for r in now where r.state == "blocked" && !was.contains(where: { $0.id == r.id && $0.state == "blocked" }) {
                    if let s = e.sessionID { Self.blocked?(self, s, block, r) }
                }
            case "program_status_removed":
                let ids = p["ids"] as? [String] ?? []
                if let b = blocks[block] { b.statuses = b.statuses?.filter { !ids.contains($0.id) } }
            default: break
            }
        default:
            break
        }
    }

    /// A window's title, as the desktop names a tab: its name when it has
    /// one, else its terminal's title.
    func title(_ w: SessionView.Window) -> String {
        w.name ?? w.block.flatMap { blocks[$0]?.title }.flatMap { $0.isEmpty ? nil : $0 } ?? w.label ?? ""
    }

    /// The record of blocks that most needs the user, as the desktop's dot
    /// shows it: one blocked first; done and error records seen do not count.
    func status(_ blocks: [String]) -> ProgramStatus? {
        blocks.compactMap { self.blocks[$0].flatMap { ProgramStatus.mostUrgent($0.statuses ?? [], seen: $0.seen) } }.max { $0.rank < $1.rank }
    }

    /// Marks a block's done and error records seen, as the desktop's pane
    /// does when it shows.
    func see(_ block: String) {
        guard let b = blocks[block] else { return }
        let keys = (b.statuses ?? []).filter { $0.state == "done" || $0.state == "error" }.map(\.seenKey)
        if !b.seen.isSuperset(of: keys) { b.seen.formUnion(keys) }
    }

    /// A block's values, made as the block first comes.
    private func block(_ id: String) -> Block {
        if let b = blocks[id] { return b }
        let b = Block()
        blocks[id] = b
        return b
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
            for b in [w.block].compactMap(\.self) + w.tiled where blocks[b]?.title == nil || again { describe(session: v.session_id, block: b) }
        }
    }

    /// Reads a block's title, program and status, which its events keep up
    /// to date. When the block's values were read before, as after a
    /// reconnect, they keep showing and this read refreshes them in place:
    /// an answer applies when nothing has changed what it read since, so
    /// an event that arrived while it was in flight wins.
    private func describe(session: String, block: String) {
        let b = self.block(block)
        let hadTitle = b.title
        let hadProgram = b.program
        let hadRecords = b.statuses
        if hadTitle == nil { b.title = "" }
        Task {
            guard let c = control else { return }
            if let t = try? await c.callBlock("title", session: session, block: block)["title"] as? String,
               b.title == "" || b.title == hadTitle {
                b.title = t
            }
            if let p = try? await c.callBlock("process", session: session, block: block) {
                let program = Self.program(p["foreground"])
                if let program, b.program == nil || b.program == hadProgram {
                    b.program = program
                }
            }
            if let r = try? await c.callBlock("program_status", session: session, block: block)["records"] as? [Any] {
                if b.statuses == nil || b.statuses == hadRecords {
                    b.statuses = r.compactMap(ProgramStatus.init)
                    if Self.shown?(self, block) == true { see(block) }
                }
            }
        }
    }

    /// The name of a foreground process, from process or process_changed.
    /// argv0 is a path by / or, from a Windows host, by \ with .exe.
    static func program(_ p: Any?) -> String? {
        guard let p = p as? [String: Any] else { return nil }
        let argv0 = (p["argv0"] as? String).map { a in
            var name = String(a[(a.lastIndex { $0 == "/" || $0 == "\\" }.map(a.index(after:)) ?? a.startIndex)...])
            if name.lowercased().hasSuffix(".exe") { name.removeLast(4) }
            return name
        }
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
        Host.tailcatHosts(saved)
        hosts.forEach { $0.connect() }
    }

    /// Adds a host; the engine hears of it before it connects.
    func add(_ config: HostConfig) {
        let h = Host(config)
        hosts.append(h)
        save()
        h.connect()
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

    /// Back in the foreground. After a suspension (suspended), resume
    /// gets the engine going again first; while the built-in Tailscale
    /// runs on as before, the hosts on it then connect anew, as their
    /// connections to its proxy can look open while their tailnet side
    /// died. Started anew, its old run's connections end with it, and the
    /// hosts connect, on a new session, once it runs (docs/ios-tailnet.md,
    /// Lifecycle). The tailcat hosts connect anew either way: their
    /// clients outlive the tailnet's runs. The others reconnect if needed,
    /// at once.
    func foreground(suspended: Bool, resume: () async -> Bool) async {
        let forced = { (h: Host) in suspended && h.config.reach(tailnetOn: TailnetSettings.isOn) != .system }
        hosts.filter { !forced($0) }.forEach { $0.reconnectIfNeeded() }
        guard suspended else { return }
        let proxied = hosts.filter(forced)
        let kept = await resume()
        proxied.filter { kept || $0.config.tailcat != nil }.forEach { $0.resume() }
    }

    /// Whether the app, in the background since away, was likely
    /// suspended: more than 5 s ago.
    static func suspended(since away: Date?, now: Date = .now) -> Bool {
        away.map { now.timeIntervalSince($0) > 5 } ?? false
    }

    func move(from source: IndexSet, to destination: Int) {
        hosts.move(fromOffsets: source, toOffset: destination)
        save()
    }

    func host(_ id: UUID) -> Host? { hosts.first { $0.id == id } }

    func host(at url: URL) -> Host? { hosts.first { $0.config.url == url } }

    /// The host of a GoRex machine's tailcat address.
    func host(tailcat address: String) -> Host? { hosts.first { $0.config.tailcat?.address == address } }

    private func save() {
        Host.tailcatHosts(hosts.map(\.config))
        UserDefaults.standard.set(try? JSONEncoder().encode(hosts.map(\.config)), forKey: "hosts")
    }
}

extension String {
    /// The string as text to show, its symbols as text: a symbol that is
    /// text by default but may be an emoji, as the "✳" Claude Code puts
    /// in its title, gets U+FE0E unless it chose a presentation already;
    /// iOS would draw it as an emoji.
    nonisolated var asText: String {
        var out = String.UnicodeScalarView()
        let scalars = Array(unicodeScalars)
        for (i, c) in scalars.enumerated() {
            out.append(c)
            let next = i + 1 < scalars.count ? scalars[i + 1].value : 0
            if c.properties.isEmoji, !c.properties.isEmojiPresentation, c.value > 0x7F,
               next != 0xFE0E, next != 0xFE0F, next != 0x20E3 {
                out.append("\u{FE0E}")
            }
        }
        return String(out)
    }
}
