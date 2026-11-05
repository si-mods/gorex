import Foundation
import Observation

/// A server the app connects to, as the user added it.
struct HostConfig: Codable, Identifiable, Hashable {
    var id = UUID()
    var name: String
    var url: URL
    var desktop = false
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

    var config: HostConfig
    var id: UUID { config.id }
    private(set) var status = Status.connecting
    private(set) var sessions: [Session] = []
    /// Terminal titles and foreground programs, by block.
    private(set) var titles: [String: String] = [:]
    private(set) var programs: [String: String] = [:]
    private(set) var control: ControlConnection?

    @ObservationIgnored private var gate = EventGate()
    @ObservationIgnored private var token: String?
    @ObservationIgnored private var retry: Task<Void, Never>?
    @ObservationIgnored private var refreshing: Set<String> = []
    @ObservationIgnored private var wanted: [String: Int] = [:]
    @ObservationIgnored private var stopped = false

    init(_ config: HostConfig) {
        self.config = config
    }

    /// Connects, unless connected or connecting.
    func connect() {
        guard control == nil, !stopped else { return }
        retry?.cancel()
        status = .connecting
        let c = ControlConnection(base: config.url)
        control = c
        gate = EventGate()
        c.onEvent = { [weak self, weak c] e in
            guard let self, c === self.control else { return }
            for e in self.gate.receive(e) { self.apply(e) }
        }
        c.onClose = { [weak self, weak c] error in
            guard let self, c === self.control else { return }
            self.control = nil
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
        titles = [:]
        programs = [:]
        sessions = all.compactMap { s in
            (s["session_id"] as? String).map { Session(id: $0, label: s["label"] as? String ?? "") }
        }
        if let selected = r["selected_session"] as? [String: Any], let v = selected["view"] {
            setView(try SessionView(json: v))
        }
        for e in gate.open(at: seq) { apply(e) }
        for s in sessions where s.view == nil { attach(s.id) }
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
            }
        case "session_destroyed":
            let id = e.payload["session_id"] as? String ?? e.sessionID
            sessions.removeAll { $0.id == id }
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
            switch e.payload["name"] as? String {
            case "title_changed": titles[block] = p["title"] as? String
            case "process_changed": programs[block] = Self.program(p["foreground"])
            default: break
            }
        default:
            break
        }
    }

    private func revision(_ id: String) -> Int {
        sessions.first { $0.id == id }?.view?.revision ?? -1
    }

    /// Reads a session's view again; one read at a time, repeated while
    /// a newer revision was announced meanwhile.
    private func refresh(_ id: String) {
        guard refreshing.insert(id).inserted else { return }
        Task {
            defer { refreshing.remove(id) }
            while let want = wanted[id], want > revision(id),
                  let r = try? await control?.call("session.view", ["session_id": id]).payload,
                  let view = try? SessionView(json: r) {
                if view.revision <= revision(id) { break }
                setView(view)
            }
        }
    }

    private func setView(_ v: SessionView) {
        guard let i = sessions.firstIndex(where: { $0.id == v.session_id }) else { return }
        if let old = sessions[i].view, old.revision >= v.revision { return }
        sessions[i].view = v
        if let label = v.label { sessions[i].label = label }
        for w in v.windows ?? [] {
            if let b = w.block, titles[b] == nil { describe(session: v.session_id, block: b) }
        }
    }

    /// Reads a block's title and program, which its events keep up to date.
    private func describe(session: String, block: String) {
        titles[block] = ""
        Task {
            guard let c = control else { return }
            if let t = try? await c.callBlock("title", session: session, block: block)["title"] as? String, titles[block] == "" {
                titles[block] = t
            }
            if let p = try? await c.callBlock("process", session: session, block: block), programs[block] == nil {
                programs[block] = Self.program(p["foreground"])
            }
        }
    }

    /// The name of a foreground process, from process or process_changed.
    private static func program(_ p: Any?) -> String? {
        guard let p = p as? [String: Any] else { return nil }
        let argv0 = (p["argv0"] as? String).map { ($0 as NSString).lastPathComponent }
        return [argv0, p["name"] as? String].compactMap { $0 }.first { !$0.isEmpty }
    }

    /// Opens a new window running the shell, and returns its block.
    func newWindow(in session: String) async throws -> String? {
        let r = try await control?.call("session.new_window", [
            "session_id": session, "focus": false,
            "layout": ["block": ["flavor": "com.superlogical.terminal.shell"]],
        ]).payload
        return (r?["block_ids"] as? [Any])?.first as? String
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

    func host(_ id: UUID) -> Host? { hosts.first { $0.id == id } }

    private func save() {
        UserDefaults.standard.set(try? JSONEncoder().encode(hosts.map(\.config)), forKey: "hosts")
    }
}
