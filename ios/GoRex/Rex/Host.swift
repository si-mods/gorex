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
    /// Whether the host is Demo Mode's: a Mac that answers on the phone,
    /// which the hosts saved never hold. Optional, as a list saved before
    /// Demo Mode must still decode.
    var demo: Bool?
    /// The LAN gateway's pairing token, from the code's #k=; nil for a
    /// code without one. It lives in the Keychain (LANTokens), never in
    /// the list saved, so CodingKeys leaves it out.
    var lanToken: String? = nil

    enum CodingKeys: String, CodingKey {
        case id, name, url, desktop, icon, command, transport, tailcat, demo
    }

    /// The token the host's requests carry: the LAN token of a host the
    /// system's network reaches by its address; none for a tailcat host.
    var auth: String? { tailcat == nil && demo != true ? lanToken : nil }

    enum Transport: String, Codable {
        /// The phone's own network, or the Tailscale app's VPN.
        case system
        /// The built-in Tailscale's proxy (docs/IOS-001-tailnet.md).
        case tailnet
        /// The engine's tailcat client, through the same proxy
        /// (docs/IOS-002-tailcat.md).
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
    /// http(s)://host[:port], with no path, query or user; GoRex's LAN
    /// code adds its token, http://host:port/#k=<token>. nil for anything
    /// else. The address kept has no path and no token.
    init?(pairing code: String) {
        guard var c = URLComponents(string: code.trimmingCharacters(in: .whitespacesAndNewlines)),
              let scheme = c.scheme?.lowercased(), ["http", "https"].contains(scheme),
              let host = c.host, !host.isEmpty,
              c.user == nil, c.password == nil, c.query == nil
        else { return nil }
        var token: String?
        if let f = c.fragment {
            guard f.hasPrefix("k="), Self.validToken(String(f.dropFirst(2))), ["", "/"].contains(c.path) else { return nil }
            token = String(f.dropFirst(2))
        } else if !c.path.isEmpty {
            return nil
        }
        c.scheme = scheme
        c.path = ""
        c.fragment = nil
        guard let url = c.url else { return nil }
        self.init(name: Self.name(host), url: url, lanToken: token)
    }

    /// Whether a LAN token reads as the gateway's: base64url, no padding.
    static func validToken(_ t: String) -> Bool {
        t.count <= 256 && t.wholeMatch(of: /[A-Za-z0-9_-]+/) != nil
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

    /// Why a typed address is refused, when it names a scheme other than
    /// http(s); nil for a bare host[:port], which reads as http, and for
    /// any other fault.
    static func refusal(of address: String) -> String? {
        guard let r = address.range(of: "://") else { return nil }
        let scheme = address[..<r.lowerBound].trimmingCharacters(in: .whitespaces).lowercased()
        if ["", "http", "https"].contains(scheme) { return nil }
        if scheme == "ssh" {
            return String(localized: "SSH hosts aren't supported in Rex Go. Enter the address of a Rex server, like http://192.168.1.2:7297.")
        }
        return String(localized: "Unsupported address. Enter an http:// or https:// address, like http://192.168.1.2:7297.")
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
        /// Its project, as the server reports it; nil for a session of no
        /// project, and on a host of before projects.
        var project: SessionView.Project?
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
            updated = Self.time(j["updated_at"])
        }

        /// A record Demo Mode shows; the server's arrive as JSON.
        init(id: String, state: String, kind: String? = nil, progress: Int? = nil, app: String? = nil,
             title: String? = nil, msg: String? = nil, lastMsg: String? = nil, updated: Date? = nil) {
            self.id = id
            self.state = state
            self.kind = kind
            self.progress = progress
            self.app = app
            self.title = title
            self.msg = msg
            self.lastMsg = lastMsg
            self.updated = updated
        }

        /// A time as the server writes updated_at and seen_at, in whole
        /// seconds; nil for none, and for Go's zero time.
        static func time(_ v: Any?) -> Date? {
            (v as? String).flatMap { try? Date($0, strategy: .iso8601) }.flatMap { $0.timeIntervalSince1970 > 0 ? $0 : nil }
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
    /// Whether the host is Demo Mode's, which runs on the phone.
    var isDemo: Bool { config.demo == true }
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
        /// When the latest done or error record a client showed the user
        /// was updated: those till then were seen. The server keeps
        /// it for every client (seen_at, gorex.see_block); against one
        /// without it, only this app knows, till it quits.
        var seen: Date?

        /// Takes the server's seen_at, which only moves forward.
        func raiseSeen(_ t: Date?) {
            if let t, t > seen ?? .distantPast { seen = t }
        }
    }

    /// The welcome's feature of a server that keeps when a block's status
    /// was seen (gorex.see_block, docs/REF-005-rex-api.md).
    static let seeFeature = "gorex.see_block"

    /// The welcome's feature of a server that keeps projects; at version
    /// 2, as folders on the host in spaces (docs/MOD-002-projects.md, Rex API).
    static let projectsFeature = "gorex.projects"

    /// The welcome's feature of a server that lists its directories
    /// (`gorex.dir.list`).
    static let dirListFeature = "gorex.dir_list"

    private(set) var control: ControlConnection?
    /// Developer Mode telemetry: when the current connect/reconnect cycle began,
    /// how long the last successful connect took, the latest WebSocket ping RTT,
    /// and whether a retry loop is active.
    enum DevStatus: Equatable {
        case ready
        case reconnecting
        case connecting
        case offline
    }
    private(set) var reconnectSince: Date?
    private(set) var connectDurationMs: Int?
    private(set) var pingMs: Int?
    private(set) var isRetrying = false

    var devStatus: DevStatus {
        if status == .online { return .ready }
        if isRetrying { return .reconnecting }
        if status == .connecting { return .connecting }
        return .offline
    }

    @ObservationIgnored private var gate = EventGate()
    @ObservationIgnored private var token: String?
    @ObservationIgnored private var retry: Task<Void, Never>?
    /// Connections that failed in a row, for the wait before the next.
    @ObservationIgnored private var attempts = 0
    /// Those waiting for the host to be online.
    @ObservationIgnored private var waiters: [(id: UUID, continuation: CheckedContinuation<ControlConnection, Error>)] = []
    /// Those waiting for the host to be online, for tests.
    var waitingCount: Int { waiters.count }
    /// The reads of blocks' titles, programs and statuses: a few at a time
    /// on the control connection, so that a reconnect's reads leave room
    /// for the calls of the moment, as opening a terminal.
    let describes = BoundedTasks(limit: 4)
    @ObservationIgnored private var refreshing: Set<String> = []
    /// Sessions whose read titles, programs and records are being read
    /// again, as after a reconnect.
    @ObservationIgnored private var reread: Set<String> = []
    @ObservationIgnored private var wanted: [String: Int] = [:]
    @ObservationIgnored private var stopped = false
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
        if isDemo {
            // Nothing to reach: the demo host's windows and statuses live
            // on the phone. Connecting again, as the app comes back to the
            // foreground, keeps what the user has done since.
            retry?.cancel()
            if sessions.isEmpty { becomeDemo() }
            status = .online
            reconnectSince = nil
            isRetrying = false
            return
        }
        retry?.cancel()
        if reconnectSince == nil {
            reconnectSince = .now
        }
        if !again {
            attempts = 0
            isRetrying = false
            status = .connecting
        } else {
            isRetrying = true
        }
        let name = "control \(config.url.host() ?? "")"
        let session: URLSession
        do {
            session = try self.session()
        } catch {
            ConnectionLog.note("\(name): \(error.diagnosis)")
            status = .offline(error.localizedDescription)
            scheduleRetry(reason: error.localizedDescription)
            return
        }
        let c = ControlConnection(base: config.url, auth: config.auth, session: session)
        control = c
        describes.cancelAll()
        gate = EventGate()
        c.onPing = { [weak self, weak c] rtt in
            guard let self, c === self.control else { return }
            self.pingMs = max(0, Int(rtt / .milliseconds(1)))
        }
        c.onEvent = { [weak self, weak c] e in
            guard let self, c === self.control else { return }
            for e in self.gate.receive(e) { self.apply(e) }
        }
        c.onClose = { [weak self, weak c] error in
            guard let self, c === self.control else { return }
            ConnectionLog.note("\(name): closed: \(error?.diagnosis ?? "by the app")")
            self.control = nil
            self.describes.cancelAll()
            self.pingMs = nil
            let offline = Status.offline(error?.localizedDescription ?? "")
            if self.status != offline { self.status = offline }
            // Refused, it waits for a change: the engine running again, as
            // after a sign-in, or the app back in the foreground.
            if let error, (error as? RexError)?.code == "refused" {
                self.isRetrying = false
                self.settle(.failure(error))
            } else {
                if self.reconnectSince == nil { self.reconnectSince = .now }
                self.scheduleRetry(reason: error?.localizedDescription)
            }
        }
        let began = ContinuousClock.now
        ConnectionLog.note("\(name): opening via \(config.reach(tailnetOn: TailnetSettings.isOn).rawValue)")
        Task {
            do {
                try await c.open(token: token)
                token = c.reconnectToken
                try await bootstrap(c)
                let elapsed = began.duration(to: .now)
                connectDurationMs = max(0, Int(elapsed / .milliseconds(1)))
                reconnectSince = nil
                isRetrying = false
                status = .online
                attempts = 0
                settle(.success(c))
                ConnectionLog.note("\(name): online, \(elapsed.ms)")
            } catch let e as RexError where e.code == "refused" && viaTailnet {
                // The gateway lets in only whom it says (docs/IOS-001-tailnet.md, Server).
                c.close(RexError(code: e.code, message: String(localized: "Refused: sign Rex Go in as the Tailscale user who signed in the GoRex machine, not with a tagged key, or turn on --allow-tailnet-members there.")))
            } catch {
                c.close(error)
            }
        }
    }

    /// Disconnects for good, as when the host is removed.
    func stop() {
        stopped = true
        isRetrying = false
        reconnectSince = nil
        pingMs = nil
        retry?.cancel()
        control?.close()
        describes.cancelAll()
        proxySession?.session.invalidateAndCancel()
        proxySession = nil
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
    private func scheduleRetry(reason: String? = nil) {
        retry?.cancel()
        attempts += 1
        isRetrying = true
        if reconnectSince == nil { reconnectSince = .now }
        if let reason, !reason.isEmpty {
            ConnectionLog.note("\(config.name) reconnecting \(attempts) (\(reason))")
        }
        let wait = Self.backoff(after: attempts)
        retry = Task {
            try? await Task.sleep(for: wait)
            if !Task.isCancelled { connect(again: true) }
        }
    }

    /// The wait before connecting again after failures in a row: a
    /// terminal's, which never gives up here.
    /// The wait before dialing a host again after failures in a row: half a
    /// second, doubling up to ten. A host is dialed again while the app runs.
    static func backoff(after failures: Int) -> Duration {
        let n = min(max(failures, 1), 6)
        return min(.milliseconds(500) * (1 << (n - 1)), .seconds(10))
    }

    /// The control connection, once the host is online: bootstrapped, so
    /// that its blocks can be opened. It waits while the host connects or
    /// is offline, and fails when the host is removed or refused.
    func online() async throws -> ControlConnection {
        // Demo Mode has no server: a caller that wants one fails rather
        // than waiting here for an answer that never comes.
        if isDemo { throw RexError(code: "offline", message: String(localized: "Demo Mode has no server.")) }
        if status == .online, let control { return control }
        if stopped { throw CancellationError() }
        // A waiter cancelled, as when its terminal closes, stops waiting
        // now rather than when the host is next online.
        let id = UUID()
        return try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { c in
                if Task.isCancelled {
                    c.resume(throwing: CancellationError())
                } else {
                    waiters.append((id, c))
                }
            }
        } onCancel: {
            Task { @MainActor [weak self] in self?.cancelWaiter(id) }
        }
    }

    private func cancelWaiter(_ id: UUID) {
        guard let i = waiters.firstIndex(where: { $0.id == id }) else { return }
        waiters.remove(at: i).continuation.resume(throwing: CancellationError())
    }

    /// Resumes those waiting for the host to be online.
    private func settle(_ r: Result<ControlConnection, Error>) {
        let w = waiters
        waiters = []
        w.forEach { $0.continuation.resume(with: r) }
    }

    /// session.bootstrap for every session, then session.attach for those
    /// it didn't attach, so that every window can be opened.
    private func bootstrap(_ c: ControlConnection) async throws {
        let (seq, r, raw) = try await c.call("session.bootstrap", ["event_scope": "all"])
        let all = (r["attach_all"] as? [String: Any])?["sessions"] as? [[String: Any]] ?? []
        sessions = all.compactMap { s in
            (s["session_id"] as? String).map { Session(id: $0, label: s["label"] as? String ?? "", project: .of(s["project"])) }
        }
        // What was read before keeps showing, as after a reconnect from
        // the background; each session's view reads it again in place.
        reread = Set(sessions.map(\.id))
        if let view = try SessionView.selected(response: raw) {
            setView(view)
        }
        for e in gate.open(at: seq) { apply(e) }
        for s in sessions where s.view == nil { attach(s.id) }
        listSpaces()
    }

    /// The spaces the host offers though no project of it carries one, as
    /// those of a layout of before projects, for the picker to list too
    /// (gorex.space.list); none from a host of before projects.
    private(set) var offeredSpaces: [String] = []

    /// Reads the spaces the host offers, on the connection of the moment.
    private func listSpaces() {
        guard hasProjects, !isDemo, let c = control else { return }
        Task {
            guard let r = try? await c.call("gorex.space.list").payload, c === control else { return }
            let all = r["spaces"] as? [String] ?? []
            let carried = Set(sessions.compactMap(\.project?.space))
            let offered = all.filter { !carried.contains($0) }
            if offered != offeredSpaces { offeredSpaces = offered }
        }
    }

    /// The key this device's caches know the host by, as the pane last
    /// viewed of each tab: its ID.
    var cacheKey: String { "host:\(id.uuidString)" }

    private func attach(_ id: String) {
        Task {
            guard let raw = try? await control?.call("session.attach", ["session_id": id]).raw,
                  let view = try? SessionView.attached(response: raw) else { return }
            setView(view)
        }
    }

    private func apply(_ e: RexEvent) {
        switch e.type {
        case "session_created":
            if let s = e.payload["session"] as? [String: Any], let id = s["session_id"] as? String,
               !sessions.contains(where: { $0.id == id }) {
                sessions.append(Session(id: id, label: s["label"] as? String ?? "", project: .of(s["project"])))
                attach(id)
            }
        case "session_destroyed":
            let id = e.payload["session_id"] as? String ?? e.sessionID
            sessions.removeAll { $0.id == id }
            id.map { reread.remove($0) }
            prune()
        case "session_label_changed":
            if let i = sessions.firstIndex(where: { $0.id == e.sessionID }) {
                sessions[i].label = e.payload["label"] as? String ?? sessions[i].label
            }
        case "session_view_changed":
            if let id = e.sessionID, let rev = e.payload["revision"] as? Int, rev > revision(id) {
                wanted[id] = max(wanted[id] ?? 0, rev)
                refresh(id)
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
                b.raiseSeen(ProgramStatus.time(p["seen_at"]))
                if let s = e.sessionID, Self.shown?(self, block) == true { see(session: s, block: block) }
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

    /// A pane's name, as the desktop names it: the label the user gave it,
    /// else its terminal's title.
    func paneTitle(_ block: String, in w: SessionView.Window) -> String? {
        w.blockName(block)
            ?? blocks[block]?.title.flatMap { $0.isEmpty ? nil : $0 }
    }

    /// A window's title, as the desktop names a tab: its name when it has
    /// one, else its focused pane's name, else its terminal's title, else
    /// its project's name, or its folder's name when it has none, else its
    /// program; never the server's "Window <ID>".
    func title(_ w: SessionView.Window) -> String {
        if let t = w.name ?? w.block.flatMap({ paneTitle($0, in: w) }) { return t }
        let session = sessions.first { $0.view?.windows?.contains { $0.window_id == w.window_id } == true }
        if let dir = session?.project?.dir, !dir.isEmpty {
            let name = session?.label ?? ""
            return name.isEmpty ? ProjectFolder.name(of: dir) : name
        }
        if let p = w.block.flatMap({ blocks[$0]?.program }), !p.isEmpty { return p }
        return String(localized: "Shell")
    }

    /// A window's title for the pane shown, as its row names it: the name
    /// the user gave the window, else that pane's name or terminal title;
    /// nil when the pane has none, for `title(_:)`'s fallbacks to run.
    func title(_ w: SessionView.Window, pane block: String) -> String? {
        w.name ?? paneTitle(block, in: w)
    }

    /// The record of blocks that most needs the user, as the desktop's dot
    /// shows it: one blocked first; done and error records seen do not count.
    func status(_ blocks: [String]) -> ProgramStatus? {
        blocks.compactMap { self.blocks[$0].flatMap { ProgramStatus.mostUrgent($0.statuses ?? [], seen: $0.seen) } }.max { $0.rank < $1.rank }
    }

    /// Marks a block's done and error records seen, as the desktop's pane
    /// does when it shows, and tells the server, which tells every client.
    /// A server that keeps it is told first, and its answer marks them, so
    /// that a call that failed is made again as the block is seen next;
    /// against one that doesn't, this app marks them itself.
    func see(session: String, block: String) {
        guard let b = blocks[block] else { return }
        let finished = (b.statuses ?? []).filter { $0.state == "done" || $0.state == "error" }
        guard !finished.isEmpty else { return }
        // A record without a time is seen as any time is.
        let latest = finished.compactMap(\.updated).max() ?? .distantPast
        if let seen = b.seen, latest <= seen { return }
        guard latest != .distantPast, let c = control, c.features.contains(Self.seeFeature) else {
            b.seen = latest
            return
        }
        Task {
            guard (try? await c.call("gorex.see_block", ["session_id": session, "block_id": block, "seen_at": latest.formatted(.iso8601)])) != nil else { return }
            b.raiseSeen(latest)
        }
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
                  let raw = try? await control?.call("session.view", ["session_id": id]).raw,
                  let view = try? SessionView(response: raw) {
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
        // Every view carries the home: one without has no folder.
        let project = v.project.flatMap { $0.dir.isEmpty ? nil : $0 }
        if sessions[i].project != project { sessions[i].project = project }
        let again = reread.remove(v.session_id) != nil
        for w in v.windows ?? [] {
            for b in w.blockIDs where blocks[b]?.title == nil || again { describe(session: v.session_id, block: b) }
        }
        prune()
    }

    /// Forgets the blocks no window holds, as after a tab closes or a
    /// session ends. Not while a session has no view, as after a
    /// reconnect: its blocks would look gone.
    private func prune() {
        guard sessions.allSatisfy({ $0.view != nil }) else { return }
        let held = Set(sessions.flatMap { $0.view?.windows ?? [] }.flatMap(\.blockIDs))
        for id in blocks.keys where !held.contains(id) { blocks[id] = nil }
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
        // A few at a time; a disconnect cancels them, and the reconnect
        // reads every block again.
        describes.add(block) { [weak self] in
            guard let self, let c = control else { return }
            if let t = try? await c.callBlock("title", session: session, block: block)["title"] as? String,
               b.title == "" || b.title == hadTitle {
                b.title = t
            }
            guard !Task.isCancelled else { return }
            if let p = try? await c.callBlock("process", session: session, block: block) {
                let program = Self.program(p["foreground"])
                if let program, b.program == nil || b.program == hadProgram {
                    b.program = program
                }
            }
            guard !Task.isCancelled else { return }
            if let p = try? await c.callBlock("program_status", session: session, block: block), let r = p["records"] as? [Any] {
                b.raiseSeen(ProgramStatus.time(p["seen_at"]))
                if b.statuses == nil || b.statuses == hadRecords {
                    b.statuses = r.compactMap(ProgramStatus.init)
                    if Self.shown?(self, block) == true { see(session: session, block: block) }
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

    /// Opens a relay from the host's own address to a pane's loopback
    /// port, for the phone's browser, and returns the address to open.
    func openPort(session: String, block: String, port: Int) async throws -> URL {
        let r = try await live().call("gorex.port.open",
                                      ["session_id": session, "block_id": block, "port": port]).payload
        guard let text = r["url"] as? String, let url = URL(string: text) else {
            throw RexError(code: "internal", message: String(localized: "The host answered no address."))
        }
        return url
    }

    /// Closes a relay that openPort opened.
    func closePort(session: String, block: String, port: Int) async {
        _ = try? await live().call("gorex.port.close",
                                   ["session_id": session, "block_id": block, "port": port])
    }

    /// The TCP ports a block's programs listen on, with the processes
    /// that hold them.
    func ports(session: String, block: String) async throws -> Ports {
        let raw = try await live().call("com.superlogical.terminal.ports",
                                        ["session_id": session, "block_id": block, "args": [String: Any]()]).raw
        return try Ports(response: raw)
    }

    /// Whether the server supports rev 11+ file uploads.
    var canUpload: Bool {
        (control?.apiRevision ?? 0) >= RexProtocol.uploadRevision
    }

    /// Sends a file into the temporary folder of the host's machine, for
    /// block; the server types its path into the terminal once uploaded.
    func upload(_ file: URL, block: String, session: String? = nil) async throws {
        let c = try live()
        guard c.apiRevision >= RexProtocol.uploadRevision, let sid = session ?? sessionID(forBlock: block) else {
            throw RexError(code: "not_supported", message: String(localized: "Server does not accept uploads"))
        }
        try await Upload.send(file, name: file.lastPathComponent, session: sid, block: block,
                              control: c, to: config.url, auth: config.auth, urlSession: { try self.session() })
    }

    /// Whether the server supports rev 13+ file downloads.
    var canDownload: Bool {
        (control?.apiRevision ?? 0) >= RexProtocol.downloadRevision
    }

    /// Downloads a file from the working directory of block over rev 13 download_begin.
    func download(_ path: String, block: String, session: String? = nil,
                  ifNoneMatch: String? = nil, maxBytes: Int? = nil) async throws -> DownloadResult {
        let c = try live()
        guard c.apiRevision >= RexProtocol.downloadRevision, let sid = session ?? sessionID(forBlock: block) else {
            throw RexError(code: "not_supported", message: String(localized: "Server does not accept downloads"))
        }
        return try await Download.fetch(path: path, session: sid, block: block,
                                        ifNoneMatch: ifNoneMatch, maxBytes: maxBytes,
                                        control: c, to: config.url, auth: config.auth, urlSession: { try self.session() })
    }

    /// Whether the server supports rev 14+ com.superlogical.tunnel blocks.
    var canTunnel: Bool {
        (control?.apiRevision ?? 0) >= RexProtocol.tunnelRevision
    }

    /// Opens a dial tunnel block targeting host:port inside session.
    func openDialTunnel(host: String, port: Int, session: String,
                        clientAddress: String? = nil) async throws -> String {
        let c = try live()
        guard c.apiRevision >= RexProtocol.tunnelRevision else {
            throw RexError(code: "not_supported", message: String(localized: "Server does not support tunnels"))
        }
        return try await Tunnel.openDial(session: session, host: host, port: port,
                                         clientAddress: clientAddress, control: c)
    }

    /// Opens a listen tunnel block on address:port inside session.
    func openListenTunnel(address: String = "127.0.0.1", port: Int = 0, session: String,
                          clientTarget: String? = nil) async throws -> String {
        let c = try live()
        guard c.apiRevision >= RexProtocol.tunnelRevision else {
            throw RexError(code: "not_supported", message: String(localized: "Server does not support tunnels"))
        }
        return try await Tunnel.openListen(session: session, address: address, port: port,
                                           clientTarget: clientTarget, control: c)
    }

    /// Queries metadata for a tunnel block.
    func tunnelInfo(session: String, block: String) async throws -> Tunnel.Info {
        let c = try live()
        guard c.apiRevision >= RexProtocol.tunnelRevision else {
            throw RexError(code: "not_supported", message: String(localized: "Server does not support tunnels"))
        }
        return try await Tunnel.info(session: session, block: block, control: c)
    }

    /// Connects a `rex.data.bytes.v1` stream to a tunnel block.
    func connectTunnel(session: String, block: String, dataRefID: String? = nil) async throws -> BytesStream {
        let c = try live()
        guard c.apiRevision >= RexProtocol.tunnelRevision else {
            throw RexError(code: "not_supported", message: String(localized: "Server does not support tunnels"))
        }
        return try await Tunnel.connect(session: session, block: block, dataRefID: dataRefID,
                                        control: c, to: config.url, auth: config.auth, urlSession: { try self.session() })
    }

    private func sessionID(forBlock block: String) -> String? {
        for s in sessions {
            for w in s.view?.windows ?? [] {
                if (w.layers ?? []).contains(where: { ($0.blocks ?? []).contains(where: { $0.block_id == block }) }) {
                    return s.id
                }
            }
        }
        return nil
    }

    /// The control connection; offline, an error to tell.
    func live() throws -> ControlConnection {
        guard let control else { throw RexError(code: "offline", message: String(localized: "Offline")) }
        return control
    }

    // MARK: Demo Mode

    /// Fills the host as Demo Mode's: the session it shows, its windows
    /// and what runs in them, all on the phone.
    private func becomeDemo() {
        sessions = Demo.sessions()
        blocks = Demo.blocks()
    }

    /// A command typed in a demo terminal changes what the list shows, as
    /// the server's events would: the program's tile, and whether it works
    /// or waits for the user.
    func demoCommand(_ text: String, block: String) {
        guard isDemo else { return }
        let b = self.block(block)
        let first = text.split(separator: " ", maxSplits: 1, omittingEmptySubsequences: true)
            .first.map { $0.lowercased() } ?? ""
        switch first {
        case "claude":
            b.title = "Claude Code"
            b.program = "claude"
            b.statuses = [.init(id: "claude", state: "working", app: "claude", title: "Claude Code",
                                msg: "Reading Tabs.swift", updated: .now)]
        case "swift":
            b.title = "Tests"
            b.program = "swift"
            b.statuses = [.init(id: "swift-test", state: "done", app: "swift", title: "Tests",
                                msg: "312 tests passed in 4.2s", updated: .now)]
        case "npm":
            b.title = "Dashboard"
            b.program = "node"
            b.statuses = [.init(id: "vite", state: "working", progress: 10, app: "node", title: "Dashboard",
                                msg: "Starting Vite", updated: .now)]
        case "y", "yes":
            // The demo's question answered: nothing waits for the user now.
            let rest = (b.statuses ?? []).filter { $0.state != "blocked" }
            b.statuses = rest.isEmpty ? nil : rest
        default:
            break
        }
    }

    private static func demoID(_ prefix: String) -> String {
        prefix + "-" + UUID().uuidString.prefix(8).lowercased()
    }

    /// The blocks a session's windows hold.
    private static func demoBlocks(_ s: Session) -> [String] {
        (s.view?.windows ?? []).flatMap { w in
            [w.focused_block_id].compactMap { $0 } + (w.layers ?? []).flatMap { ($0.blocks ?? []).map(\.block_id) }
        }
    }

    /// Splits a demo window's pane, a new shell to its right, as the server
    /// would: the pane's place halves, the new block after it.
    private func demoAddSplit(beside anchor: String, in session: String) -> String? {
        guard let i = sessions.firstIndex(where: { $0.id == session }),
              var view = sessions[i].view, var windows = view.windows,
              let w = windows.firstIndex(where: { $0.holds(anchor) }) else { return nil }
        let block = Self.demoID("b")
        var layer = windows[w].layers?.first ?? .init(blocks: [.init(block_id: anchor)])
        var tiled = layer.blocks ?? [.init(block_id: anchor)]
        let at = tiled.firstIndex { $0.block_id == anchor }.map { $0 + 1 } ?? tiled.count
        tiled.insert(.init(block_id: block), at: at)
        layer.blocks = tiled
        let root = layer.layout ?? PaneLayout(rect: [0, 0, 1, 1], block_id: anchor)
        func leaf(_ n: PaneLayout) -> PaneLayout? {
            if n.block_id == anchor { return n }
            guard let s = n.split else { return nil }
            return leaf(s.before) ?? leaf(s.after)
        }
        if let n = leaf(root), let r = n.rect, r.count == 4 {
            let half = r[2] / 2
            n.split = PaneSplit(direction: "horizontal", ratio: 0.5,
                                before: PaneLayout(rect: [r[0], r[1], half, r[3]], block_id: anchor),
                                after: PaneLayout(rect: [r[0] + half, r[1], half, r[3]], block_id: block))
            n.block_id = nil
        }
        layer.layout = root
        windows[w].layers = [layer] + (windows[w].layers ?? []).dropFirst()
        view.windows = windows
        view.revision += 1
        sessions[i].view = view
        let b = Block()
        b.title = "Shell"
        b.program = "zsh"
        blocks[block] = b
        return block
    }

    /// Adds a window to a demo session, as the server would.
    @discardableResult
    private func demoAddWindow(_ id: String, block: String, to i: Int) -> String {
        var view = sessions[i].view ?? SessionView(revision: 0, session_id: sessions[i].id)
        view.windows = (view.windows ?? []) + [
            SessionView.Window(window_id: id, focused_block_id: block,
                               layers: [.init(blocks: [.init(block_id: block)])]),
        ]
        view.revision += 1
        sessions[i].view = view
        let b = Block()
        b.title = "Shell"
        b.program = "zsh"
        blocks[block] = b
        return block
    }

    /// Opens a new window running the shell, in cwd when given, and
    /// returns its block. Without a cwd, a session whose project's home is
    /// this host starts it in the folder, or fails with conflict
    /// (folder_missing) when the folder is gone.
    func newWindow(in session: String, cwd: String? = nil) async throws -> String? {
        if isDemo {
            guard let i = sessions.firstIndex(where: { $0.id == session }) else { return nil }
            let window = Self.demoID("w"), block = Self.demoID("b")
            return demoAddWindow(window, block: block, to: i)
        }
        let r = try await live().call("session.new_window", [
            "session_id": session, "focus": false, "layout": ["block": shell(cwd: cwd)],
        ]).payload
        return (r["block_ids"] as? [Any])?.first as? String
    }

    /// Splits a pane of a window where it is: a new shell to its right, as
    /// the desktop's Split Right, and returns its block. The shared layout's
    /// focus stays where it is.
    func newSplit(beside block: String, in session: String) async throws -> String? {
        if isDemo { return demoAddSplit(beside: block, in: session) }
        let r = try await live().call("session.new_split", [
            "session_id": session, "anchor_id": block, "direction": "horizontal", "side": "after",
            "focus": false, "layout": ["block": shell],
        ]).payload
        return (r["block_ids"] as? [Any])?.first as? String
    }

    /// Closes a pane of a window, as the server would: its neighbor takes
    /// its place in the layout, and the window's focus moves off it.
    private func demoClosePane(_ block: String, in session: String) {
        guard let i = sessions.firstIndex(where: { $0.id == session }),
              var view = sessions[i].view, var windows = view.windows,
              let w = windows.firstIndex(where: { $0.holds(block) }),
              var layer = windows[w].layers?.first, var tiled = layer.blocks,
              let at = tiled.firstIndex(where: { $0.block_id == block }), tiled.count > 1 else { return }
        tiled.remove(at: at)
        layer.blocks = tiled
        // The kept side fills the room both had, laid out again from it.
        func place(_ n: PaneLayout, _ r: [Double]) {
            n.rect = r
            guard let s = n.split else { return }
            if s.direction == "horizontal" {
                let a = r[2] * s.ratio
                place(s.before, [r[0], r[1], a, r[3]])
                place(s.after, [r[0] + a, r[1], r[2] - a, r[3]])
            } else {
                let a = r[3] * s.ratio
                place(s.before, [r[0], r[1], r[2], a])
                place(s.after, [r[0], r[1] + a, r[2], r[3] - a])
            }
        }
        func prune(_ n: PaneLayout) {
            guard let s = n.split else { return }
            for (gone, kept) in [(s.before, s.after), (s.after, s.before)] where gone.block_id == block {
                n.block_id = kept.block_id
                n.split = kept.split
                if let r = n.rect, r.count == 4 { place(n, r) }
                return
            }
            prune(s.before)
            prune(s.after)
        }
        if let root = layer.layout { prune(root) }
        windows[w].layers = [layer] + (windows[w].layers ?? []).dropFirst()
        if windows[w].focused_block_id == block { windows[w].focused_block_id = tiled[min(at, tiled.count - 1)].block_id }
        view.windows = windows
        view.revision += 1
        sessions[i].view = view
        blocks[block] = nil
    }

    /// Closes a pane and ends its program; the window's last pane stays.
    func closePane(_ block: String, in session: String) async throws {
        if isDemo { return demoClosePane(block, in: session) }
        try await live().call("block.close", ["session_id": session, "block_id": block, "keep_last": true])
    }

    /// Makes a session, named by `label` or by the server, with a window
    /// running the shell, and returns the session and the window's block.
    func newSession(label: String? = nil, cwd: String? = nil) async throws -> (String, String)? {
        let trimmed = label?.trimmingCharacters(in: .whitespaces)
        let customLabel = (trimmed?.isEmpty == false) ? trimmed : nil
        if isDemo {
            let id = Self.demoID("s"), window = Self.demoID("w"), block = Self.demoID("b")
            let label = customLabel ?? String(localized: "New Session")
            let view = SessionView(revision: 1, session_id: id, label: label, windows: [
                .init(window_id: window, focused_block_id: block, layers: [.init(blocks: [.init(block_id: block)])]),
            ])
            sessions.append(Session(id: id, label: label, view: view))
            let b = Block()
            b.title = "Shell"
            b.program = "zsh"
            blocks[block] = b
            return (id, block)
        }
        var args: [String: Any] = ["initial_windows": [["layout": ["block": shell(cwd: cwd)]]]]
        if let customLabel { args["label"] = customLabel }
        let r = try await live().call("session.create", args).payload
        guard let s = r["session_id"] as? String,
              let w = (r["initial_windows"] as? [[String: Any]])?.first,
              let b = (w["block_ids"] as? [Any])?.first as? String else { return nil }
        return (s, b)
    }

    /// The block a new window runs: the shell, or the command of the host,
    /// in cwd when given.
    private var shell: [String: Any] { shell(cwd: nil) }

    private func shell(cwd: String?) -> [String: Any] {
        var block: [String: Any] = ["flavor": "com.superlogical.terminal.shell"]
        var options: [String: Any] = [:]
        let argv = HostConfig.argv(config.command ?? "")
        if !argv.isEmpty { options["command"] = argv }
        if let cwd, !cwd.isEmpty { options["cwd"] = cwd }
        if !options.isEmpty { block["options"] = options }
        return block
    }

    // MARK: Projects

    /// Whether the host keeps projects in spaces, gorex.projects at version
    /// 2 (docs/MOD-002-projects.md): the list shows its projects and its tabs with
    /// no project. A host of before shows its sessions as before. Demo
    /// Mode's keeps them on the phone.
    var hasProjects: Bool {
        isDemo || (control?.featureVersions[Self.projectsFeature] ?? 0) >= 2
    }

    /// Whether the host lists its folders (gorex.dir.list), for a project's
    /// folder to be chosen there.
    var listsFolders: Bool {
        isDemo || (status == .online && control?.features.contains(Self.dirListFeature) ?? false)
    }

    /// A directory's folders, as gorex.dir.list answers.
    struct Folders: Equatable {
        var path: String
        var parent: String
        var home: String
        var dirs: [String]
        var more = false
        /// "not_found" or "denied", with path, parent and home still set.
        var code: String?

        init(path: String, parent: String, home: String, dirs: [String], more: Bool = false, code: String? = nil) {
            self.path = path
            self.parent = parent
            self.home = home
            self.dirs = dirs
            self.more = more
            self.code = code
        }

        init(_ p: [String: Any]) {
            path = p["path"] as? String ?? ""
            parent = p["parent"] as? String ?? ""
            home = p["home"] as? String ?? ""
            dirs = p["dirs"] as? [String] ?? []
            more = p["more"] as? Bool ?? false
            code = (p["code"] as? String).flatMap { $0.isEmpty ? nil : $0 }
        }

        /// A child's path: path and name joined with "/", which the server
        /// cleans; at Windows' drives each name is a path of its own.
        func child(_ name: String) -> String {
            if path.isEmpty || name.hasSuffix(":\\") || name.hasSuffix(":/") { return name }
            return path.hasSuffix("/") || path.hasSuffix("\\") ? path + name : path + "/" + name
        }
    }

    /// Lists the folders of path on the host: "" is the home directory.
    func folders(_ path: String, hidden: Bool = false) async throws -> Folders {
        if isDemo { return Demo.folders(path) }
        var p: [String: Any] = ["path": path]
        if hidden { p["hidden"] = true }
        return Folders(try await live().call("gorex.dir.list", p).payload)
    }

    /// What gorex.project.create did: made the project with its first tab,
    /// or found the folder a project of the host already.
    enum ProjectCreated: Equatable {
        case made(project: String, tab: String)
        case exists(project: String)
    }

    /// Makes a project of a folder on the host, in a space ("" is Default),
    /// with its first tab in the folder (gorex.project.create). A folder
    /// that is a project already gives that project.
    func createProject(name: String, dir: String, space: String) async throws -> ProjectCreated {
        if isDemo { return demoCreateProject(name: name, dir: dir, space: space) }
        do {
            let r = try await live().call("gorex.project.create", ["name": name, "dir": dir, "space": space]).payload
            guard let project = r["project_id"] as? String, !project.isEmpty else {
                throw RexError(code: "protocol", message: "gorex.project.create gave no project_id")
            }
            if r["code"] as? String == "exists" { return .exists(project: project) }
            guard let tab = r["tab_id"] as? String else {
                throw RexError(code: "protocol", message: "gorex.project.create gave no tab_id")
            }
            return .made(project: project, tab: tab)
        } catch let e as RexError where e.code == "exists" {
            // Told as an error, which names the project, else the project
            // is the one of the folder as the host lists it.
            if let id = e.projectID, !id.isEmpty { return .exists(project: id) }
            guard let s = sessions.first(where: { $0.project?.dir == dir }) else { throw e }
            return .exists(project: s.id)
        }
    }

    /// Moves a tab, with its panes and their programs, to another project
    /// of the host, or with "" to its tabs with no project
    /// (gorex.project.move).
    func moveTab(_ tab: String, to project: String) async throws {
        if isDemo { return demoMoveTab(tab, to: project) }
        try await live().call("gorex.project.move", ["tab_id": tab, "project_id": project])
    }

    /// Changes a project's name, color or space; nil leaves it as it is
    /// (gorex.project.update). The color "default" gives the default one
    /// back: the server takes "" as no change.
    func updateProject(_ project: String, name: String? = nil, color: String? = nil, space: String? = nil) async throws {
        if isDemo { return demoUpdateProject(project, name: name, color: color, space: space) }
        var p: [String: Any] = ["project_id": project]
        p["name"] = name
        p["color"] = color
        p["space"] = space
        try await live().call("gorex.project.update", p)
    }

    /// Renames a space on every project of the host (gorex.space.rename).
    func renameSpace(from: String, to: String) async throws {
        if isDemo {
            for i in sessions.indices where sessions[i].project?.space == from { sessions[i].project?.space = to }
            return
        }
        try await live().call("gorex.space.rename", ["from": from, "to": to])
        listSpaces()
    }

    /// Deletes a space on the host: its projects move to Default and no
    /// tab closes (gorex.space.delete). A server of before it (v0.41.0)
    /// does not know the method; a rename to Default does the same there.
    /// Gives the number of projects moved.
    @discardableResult
    func deleteSpace(_ name: String) async throws -> Int {
        if isDemo {
            let moved = sessions.indices.filter { sessions[$0].project?.space == name }
            for i in moved { sessions[i].project?.space = "" }
            return moved.count
        }
        let c = try live()
        let r: [String: Any]
        do {
            r = try await c.call("gorex.space.delete", ["name": name]).payload
        } catch let e as RexError where e.code == "method_not_found" {
            r = try await c.call("gorex.space.rename", ["from": name, "to": ""]).payload
        }
        listSpaces()
        return r["projects"] as? Int ?? 0
    }

    /// The block a tab's row opens, as the host shows it, else as the
    /// project's view reads it now, as for the tab a call just made.
    func block(ofTab tab: String, in project: String) async throws -> String? {
        if let w = sessions.first(where: { $0.id == project })?.view?.windows?.first(where: { $0.window_id == tab }) {
            return w.block
        }
        if isDemo { return nil }
        let v = try SessionView(response: try await live().call("session.view", ["session_id": project]).raw)
        setView(v)
        return v.windows?.first { $0.window_id == tab }?.block
    }

    /// Opens a tab with no project, in the host's home directory, and
    /// returns its session and block: in the session of the host's tabs
    /// with no project. A host that has none yet, as a fresh one, makes it
    /// with session.create, with the tab in it; the server lets the first
    /// one be made (docs/MOD-002-projects.md, Phases).
    func newLooseTab() async throws -> (String, String)? {
        if let s = sessions.first(where: { $0.project == nil }) {
            return try await newWindow(in: s.id).map { (s.id, $0) }
        }
        return try await newSession()
    }

    private func demoCreateProject(name: String, dir: String, space: String) -> ProjectCreated {
        if let s = sessions.first(where: { $0.project?.dir == dir }) { return .exists(project: s.id) }
        let id = Self.demoID("s"), window = Self.demoID("w"), block = Self.demoID("b")
        sessions.append(Session(id: id, label: name, view: SessionView(revision: 0, session_id: id, label: name),
                                project: .init(dir: dir, space: space, used: .now)))
        demoAddWindow(window, block: block, to: sessions.count - 1)
        return .made(project: id, tab: window)
    }

    private func demoMoveTab(_ tab: String, to project: String) {
        guard let from = sessions.firstIndex(where: { $0.view?.windows?.contains { $0.window_id == tab } == true }),
              var source = sessions[from].view, let j = source.windows?.firstIndex(where: { $0.window_id == tab }),
              let moved = source.windows?.remove(at: j) else { return }
        let to = project.isEmpty ? sessions.firstIndex { $0.project == nil } : sessions.firstIndex { $0.id == project }
        guard let to, to != from else { return }
        source.revision += 1
        sessions[from].view = source
        var target = sessions[to].view ?? SessionView(revision: 0, session_id: sessions[to].id)
        target.windows = (target.windows ?? []) + [moved]
        target.revision += 1
        sessions[to].view = target
    }

    private func demoUpdateProject(_ project: String, name: String?, color: String?, space: String?) {
        guard let i = sessions.firstIndex(where: { $0.id == project }), sessions[i].project != nil else { return }
        if let name, !name.isEmpty { sessions[i].label = name }
        if let color, !color.isEmpty { sessions[i].project?.color = color == "default" ? nil : color }
        if let space { sessions[i].project?.space = space }
    }

    /// Names a window; an empty name gives back its default.
    func renameWindow(_ window: String, in session: String, to label: String) async throws {
        if isDemo {
            guard let i = sessions.firstIndex(where: { $0.id == session }), var view = sessions[i].view,
                  var windows = view.windows, let j = windows.firstIndex(where: { $0.window_id == window })
            else { return }
            windows[j].label = label.isEmpty ? nil : label
            view.windows = windows
            view.revision += 1
            sessions[i].view = view
            return
        }
        try await live().call("session.set_window_label", ["session_id": session, "window_id": window, "label": label])
    }

    /// Moves a window before another, or to the end when before is nil.
    func moveWindow(_ window: String, in session: String, before: String?) async throws {
        if isDemo {
            guard let i = sessions.firstIndex(where: { $0.id == session }), var view = sessions[i].view,
                  var windows = view.windows, let from = windows.firstIndex(where: { $0.window_id == window })
            else { return }
            let moved = windows.remove(at: from)
            if let before, let to = windows.firstIndex(where: { $0.window_id == before }) {
                windows.insert(moved, at: to)
            } else {
                windows.append(moved)
            }
            view.windows = windows
            view.revision += 1
            sessions[i].view = view
            return
        }
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
        if isDemo {
            guard let i = sessions.firstIndex(where: { $0.id == session }) else { return }
            sessions[i].label = label
            if var view = sessions[i].view {
                view.label = label.isEmpty ? nil : label
                view.revision += 1
                sessions[i].view = view
            }
            return
        }
        try await live().call("session.set_label", ["session_id": session, "label": label])
    }

    /// Closes a session, its windows and their panes.
    func closeSession(_ session: String) async throws {
        if isDemo {
            guard let i = sessions.firstIndex(where: { $0.id == session }) else { return }
            let held = Self.demoBlocks(sessions[i])
            sessions.remove(at: i)
            for b in held { blocks[b] = nil }
            return
        }
        try await live().call("session.destroy", ["session_id": session])
    }

    /// Closes a window and its panes.
    func closeWindow(_ window: String, in session: String) async throws {
        if isDemo {
            guard let i = sessions.firstIndex(where: { $0.id == session }), var view = sessions[i].view,
                  var windows = view.windows, let j = windows.firstIndex(where: { $0.window_id == window })
            else { return }
            let closed = windows.remove(at: j)
            view.windows = windows
            view.revision += 1
            sessions[i].view = view
            for b in [closed.focused_block_id].compactMap({ $0 }) + (closed.layers ?? []).flatMap({ ($0.blocks ?? []).map(\.block_id) }) {
                blocks[b] = nil
            }
            return
        }
        try await live().call("session.close_window", ["session_id": session, "window_id": window])
    }
}

/// The hosts the user added, kept in UserDefaults.
@Observable
final class HostStore {
    private(set) var hosts: [Host]
    /// The hosts' LAN tokens, kept apart from the list saved.
    @ObservationIgnored private let tokens: LANTokens

    init(tokens: LANTokens = KeychainLANTokens()) {
        self.tokens = tokens
        // A saved list never holds the demo host; one from an older build
        // that did is dropped, as it answers nothing.
        var saved = (UserDefaults.standard.data(forKey: "hosts")
            .flatMap { try? JSONDecoder().decode([HostConfig].self, from: $0) } ?? []).filter { $0.demo != true }
        for i in saved.indices where saved[i].tailcat == nil {
            saved[i].lanToken = tokens.token(for: saved[i].id)
        }
        hosts = saved.map(Host.init)
        Host.tailcatHosts(saved)
        hosts.forEach { $0.connect() }
    }

    /// The Demo Mode host, while the user explores one.
    var demo: Host? { hosts.first(where: \.isDemo) }

    /// Adds the demo host, once: the list shows its windows as a real
    /// host's, and nothing is saved.
    func enterDemo() {
        guard demo == nil else { return }
        let h = Host(Demo.config())
        hosts.append(h)
        h.connect()
    }

    /// Leaves Demo Mode, closing its windows and their terminals.
    func exitDemo() {
        guard let h = demo else { return }
        h.stop()
        hosts.removeAll { $0 === h }
    }

    /// Adds a host; the engine hears of it before it connects.
    func add(_ config: HostConfig) {
        let h = Host(config)
        hosts.append(h)
        tokens.set(config.auth, for: config.id)
        save()
        h.connect()
    }

    func remove(_ id: UUID) {
        hosts.first { $0.id == id }?.stop()
        hosts.removeAll { $0.id == id }
        tokens.set(nil, for: id)
        save()
    }

    /// Changes a host; one with a new address or transport connects
    /// again. The host stays the same, for the terminals open on it.
    func update(_ config: HostConfig) {
        guard let h = host(config.id) else { return }
        let moved = h.config.url != config.url || h.config.transport != config.transport || h.config.auth != config.auth
        if h.config.auth != config.auth { tokens.set(config.auth, for: config.id) }
        h.config = config
        if moved { h.reconnect() }
        save()
    }

    /// Back in the foreground. After a suspension (suspended), resume
    /// gets the engine going again first; while the built-in Tailscale
    /// runs on as before, the hosts on it then connect anew, as their
    /// connections to its proxy can look open while their tailnet side
    /// died. Started anew, its old run's connections end with it, and the
    /// hosts connect, on a new session, once it runs (docs/IOS-001-tailnet.md,
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

    /// The phone changed network (NetworkPath). Unless in the background
    /// (inactive counts as the foreground), each host with a control
    /// connection connects anew, whatever its reach, as the connection can
    /// look open while its path died; so does one waiting in its retry
    /// backoff, which dials now rather than after up to ten seconds. Unlike
    /// foreground, it leaves the engine be: the engine hears the change
    /// itself, and a host behind it that comes too early tries again
    /// (scheduleRetry). In the background, nothing: coming back to the
    /// foreground sees to it.
    func networkChanged(inBackground: Bool) {
        for h in Self.resumedOnNetworkChange(hosts, inBackground: inBackground) {
            h.resume()
            ConnectionLog.note("\(h.config.name): resumed after network change")
        }
    }

    /// The hosts a change of network resumes: out of the background, the
    /// real ones with a control connection or waiting to retry; in the
    /// background, none.
    static func resumedOnNetworkChange(_ hosts: [Host], inBackground: Bool) -> [Host] {
        inBackground ? [] : hosts.filter { !$0.isDemo && ($0.control != nil || $0.isRetrying) }
    }

    /// A pull to refresh in the host list (docs/IOS-016-pull-refresh.md):
    /// each real host that is not online connects anew at once — one
    /// waiting in its retry backoff dials now rather than after up to
    /// ten seconds, one refused dials again, one mid-dial starts over.
    func reconnectDown() {
        for h in Self.resumedOnRefresh(hosts) { h.resume() }
    }

    /// The hosts a pull to refresh resumes: the real ones not online.
    static func resumedOnRefresh(_ hosts: [Host]) -> [Host] {
        hosts.filter { !$0.isDemo && $0.status != .online }
    }

    /// Whether leaving the app asks iOS for a grace (BackgroundGrace): a
    /// real host has a control connection, or a terminal of one is open
    /// (terminal, its host). Demo Mode alone keeps nothing.
    static func needsGrace(_ hosts: [Host], terminal: UUID?) -> Bool {
        hosts.contains { !$0.isDemo && ($0.control != nil || $0.id == terminal) }
    }

    /// Whether the app, in the background since away, was likely
    /// suspended: more than 5 s ago.
    static func suspended(since away: Date?, now: Date = .now) -> Bool {
        away.map { now.timeIntervalSince($0) > 5 } ?? false
    }

    /// Reorders the real hosts, as Settings shows them: the demo host, not
    /// listed there, keeps its place.
    func move(from source: IndexSet, to destination: Int) {
        var real = hosts.filter { !$0.isDemo }
        real.move(fromOffsets: source, toOffset: destination)
        var next = real.makeIterator()
        hosts = hosts.map { $0.isDemo ? $0 : (next.next() ?? $0) }
        save()
    }

    func host(_ id: UUID) -> Host? { hosts.first { $0.id == id } }

    func host(at url: URL) -> Host? { hosts.first { $0.config.url == url } }

    /// The host of a GoRex machine's tailcat address.
    func host(tailcat address: String) -> Host? { hosts.first { $0.config.tailcat?.address == address } }

    private func save() {
        let saved = hosts.filter { !$0.isDemo }.map(\.config)
        Host.tailcatHosts(saved)
        UserDefaults.standard.set(try? JSONEncoder().encode(saved), forKey: "hosts")
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
