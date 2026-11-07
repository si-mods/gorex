import Foundation
import UIKit

/// A failed call, as the response's error names it.
struct RexError: LocalizedError {
    let code: String
    let message: String
    var errorDescription: String? { message }
}

/// An event of the control connection. A session's event arrives as
/// `session_event`; it is unwrapped here, with its session in `sessionID`.
struct RexEvent {
    let seq: Int
    let type: String
    let sessionID: String?
    let payload: [String: Any]

    /// Reads an event message, or nil for another message.
    init?(message m: [String: Any]) {
        guard m["type"] as? String == "event", let seq = m["seq"] as? Int,
              let outer = m["payload"] as? [String: Any], var type = outer["type"] as? String
        else { return nil }
        var payload = outer["payload"] as? [String: Any] ?? [:]
        var session: String?
        if type == "session_event", let event = payload["event"] as? [String: Any] {
            session = payload["session_id"] as? String
            type = event["type"] as? String ?? ""
            payload = event["payload"] as? [String: Any] ?? [:]
        }
        self.seq = seq
        self.type = type
        self.sessionID = session
        self.payload = payload
    }
}

/// Holds events until the response they combine with arrives, then lets
/// through those it doesn't include: the ones with a higher seq
/// ("Combining Results With Events").
struct EventGate {
    private var floor: Int?
    private var held: [RexEvent] = []

    /// The events to apply now, given one that arrived.
    mutating func receive(_ e: RexEvent) -> [RexEvent] {
        guard let floor else {
            held.append(e)
            return []
        }
        return e.seq > floor ? [e] : []
    }

    /// The held events to apply after the response with seq.
    mutating func open(at seq: Int) -> [RexEvent] {
        floor = seq
        defer { held = [] }
        return held.filter { $0.seq > seq }
    }
}

/// The control connection to a Rex server: one WebSocket at /api/control
/// with JSON requests, responses and events.
final class ControlConnection {
    /// The API revision this client speaks.
    static let revision = 10

    var onEvent: (RexEvent) -> Void = { _ in }
    /// Called once when the connection ends, by either side.
    var onClose: (Error?) -> Void = { _ in }
    /// How often to ping, how long a pong may take before the connection
    /// counts as dead, and how long a response may take.
    var pingEvery = Duration.seconds(30)
    var pongWait = Duration.seconds(15)
    var callWait = Duration.seconds(30)
    private(set) var clientID = ""
    private(set) var reconnectToken: String?
    /// The optional behaviors the server lists in its welcome.
    private(set) var features: Set<String> = []

    private let task: URLSessionWebSocketTask
    private var nextID = 0
    private var pending: [String: CheckedContinuation<Reply, Error>] = [:]

    /// A response's seq and result. JSON values, which nothing mutates.
    private struct Reply: @unchecked Sendable {
        let seq: Int
        let payload: [String: Any]
    }
    private var closed = false

    init(base: URL) {
        var request = URLRequest(url: base.rexURL("/api/control"))
        request.timeoutInterval = 10
        task = URLSession.shared.webSocketTask(with: request.withSubprotocol("rex.control.v1"))
        task.maximumMessageSize = 1 << 20
    }

    /// This client's label for servers to show, as set in Settings, else
    /// the device's name.
    static var label: String {
        let l = UserDefaults.standard.string(forKey: "clientLabel")?.trimmingCharacters(in: .whitespaces) ?? ""
        return l.isEmpty ? UIDevice.current.name : l
    }

    /// Opens the connection and says hello, continuing the client of token
    /// when the server still has it.
    func open(token: String?) async throws {
        task.resume()
        var hello: [String: Any] = [
            "type": "hello", "api_revision": Self.revision,
            "client": ["kind": "app", "platform": "ios", "name": "GoRex", "version": Bundle.main.version,
                       "label": Self.label],
        ]
        hello["reconnect_token"] = token
        try await task.send(.string(String(decoding: try JSONSerialization.data(withJSONObject: hello), as: UTF8.self)))
        guard case .string(let s) = try await task.receive(),
              let welcome = try JSONSerialization.jsonObject(with: Data(s.utf8)) as? [String: Any],
              welcome["type"] as? String == "welcome", welcome["protocol"] as? String == "rex.control.v1"
        else { throw RexError(code: "protocol", message: "no welcome") }
        let client = welcome["client"] as? [String: Any]
        clientID = client?["id"] as? String ?? ""
        reconnectToken = client?["reconnect_token"] as? String
        features = Set(welcome["features"] as? [String] ?? [])
        Task { await receiveLoop() }
        Task { await pingLoop() }
    }

    /// Calls a method and returns its result with the response's seq.
    /// An "offline" error tells that the request was never sent.
    @discardableResult
    func call(_ method: String, _ payload: [String: Any]? = nil) async throws -> (seq: Int, payload: [String: Any]) {
        if closed { throw RexError(code: "offline", message: "disconnected") }
        nextID += 1
        let id = String(nextID)
        var req: [String: Any] = ["type": "request", "id": id, "method": method]
        req["payload"] = payload
        let text = String(decoding: try JSONSerialization.data(withJSONObject: req), as: UTF8.self)
        let timeout = Task { [weak self, callWait] in
            try await Task.sleep(for: callWait)
            self?.pending.removeValue(forKey: id)?.resume(throwing: RexError(code: "timeout", message: "no response"))
        }
        defer { timeout.cancel() }
        let r = try await withCheckedThrowingContinuation { (c: CheckedContinuation<Reply, Error>) in
            pending[id] = c
            task.send(.string(text)) { [weak self] error in
                guard let error else { return }
                Task { @MainActor in self?.pending.removeValue(forKey: id)?.resume(throwing: error) }
            }
        }
        return (r.seq, r.payload)
    }

    /// Calls a block method, such as com.superlogical.terminal.title.
    @discardableResult
    func callBlock(_ method: String, session: String, block: String, _ args: [String: Any] = [:]) async throws -> [String: Any] {
        try await call("com.superlogical.terminal." + method,
                       ["session_id": session, "block_id": block, "args": args]).payload
    }

    /// Closes the connection, for the reason error when given.
    func close(_ error: Error? = nil) {
        task.cancel(with: .normalClosure, reason: nil)
        finish(error)
    }

    private func receiveLoop() async {
        do {
            while true {
                guard case .string(let s) = try await task.receive(),
                      let m = try? JSONSerialization.jsonObject(with: Data(s.utf8)) as? [String: Any]
                else { continue }
                if m["type"] as? String == "response", let id = m["id"] as? String, let c = pending.removeValue(forKey: id) {
                    if let e = m["error"] as? [String: Any] {
                        c.resume(throwing: RexError(code: e["code"] as? String ?? "", message: e["message"] as? String ?? ""))
                    } else {
                        c.resume(returning: Reply(seq: m["seq"] as? Int ?? 0, payload: m["payload"] as? [String: Any] ?? [:]))
                    }
                } else if let e = RexEvent(message: m) {
                    onEvent(e)
                }
            }
        } catch {
            finish(error)
        }
    }

    /// Pings as the server does, so that a server that is gone, or a path
    /// that died without a word, is noticed: a pong that doesn't come in
    /// time ends the connection.
    private func pingLoop() async {
        while !closed {
            try? await Task.sleep(for: pingEvery)
            if closed { return }
            let deadline = Task { [weak self, pongWait] in
                try await Task.sleep(for: pongWait)
                self?.fail(RexError(code: "timeout", message: "no pong"))
            }
            let error = await withCheckedContinuation { c in task.sendPing { c.resume(returning: $0) } }
            deadline.cancel()
            if let error { fail(error) }
        }
    }

    private func fail(_ error: Error) {
        task.cancel()
        finish(error)
    }

    private func finish(_ error: Error?) {
        if closed { return }
        closed = true
        for c in pending.values { c.resume(throwing: error ?? RexError(code: "closed", message: "disconnected")) }
        pending = [:]
        onClose(error)
    }
}

extension URL {
    /// The WebSocket URL of path on the server at this http(s) address.
    func rexURL(_ path: String) -> URL {
        var c = URLComponents(url: self, resolvingAgainstBaseURL: false)!
        c.scheme = c.scheme == "https" ? "wss" : "ws"
        c.path = path
        return c.url!
    }
}

extension URLRequest {
    func withSubprotocol(_ p: String) -> URLRequest {
        var r = self
        r.setValue(p, forHTTPHeaderField: "Sec-WebSocket-Protocol")
        return r
    }
}

extension Bundle {
    var version: String { infoDictionary?["CFBundleShortVersionString"] as? String ?? "0" }
}
