import Foundation
import Network
import XCTest

/// A Rex server on a local port, for tests of what the app does with the
/// connection: it welcomes control connections and answers their requests
/// as the test says, and hands data connections to the test.
@MainActor final class FakeRex {
    /// A request's answer: its payload, or nil to leave it unanswered.
    var respond: (_ method: String, _ payload: [String: Any]) -> [String: Any]? = { _, _ in [:] }
    /// A data connection, with its handshake message.
    var data: (_ conn: NWConnection, _ first: Data) -> Void = { c, _ in c.cancel() }
    /// The features the welcome lists.
    var features: [String] = []
    /// The methods called, in order.
    private(set) var calls: [String] = []
    private(set) var payloads: [[String: Any]] = []
    private(set) var controls: [NWConnection] = []
    /// The reconnect token of each hello, in order; each welcome gives
    /// "t" and its connection's number.
    private(set) var tokens: [String?] = []

    private let listener: NWListener
    private var seq = 0

    /// A server that answers pings unless told not to.
    init(pongs: Bool = true) throws {
        let ws = NWProtocolWebSocket.Options()
        ws.autoReplyPing = pongs
        ws.maximumMessageSize = 1 << 20
        ws.setClientRequestHandler(.main) { protocols, _ in
            NWProtocolWebSocket.Response(status: .accept, subprotocol: protocols.first)
        }
        let params = NWParameters.tcp
        params.defaultProtocolStack.applicationProtocols.insert(ws, at: 0)
        listener = try NWListener(using: params, on: .any)
        listener.newConnectionHandler = { [weak self] c in
            MainActor.assumeIsolated { self?.accept(c) }
        }
        listener.start(queue: .main)
    }

    /// The server's address, once it listens.
    func url() async throws -> URL {
        for _ in 0..<250 {
            if let p = listener.port, p.rawValue != 0 { return URL(string: "http://127.0.0.1:\(p.rawValue)")! }
            try await Task.sleep(for: .milliseconds(20))
        }
        throw URLError(.cannotConnectToHost)
    }

    func stop() {
        listener.cancel()
        controls.forEach { $0.cancel() }
    }

    func count(_ method: String) -> Int { calls.filter { $0 == method }.count }

    /// Sends an event on every control connection.
    func event(_ payload: [String: Any]) {
        seq += 1
        for c in controls { Self.send(c, json: ["type": "event", "seq": seq, "payload": payload]) }
    }

    /// Sends a message on a connection, as text or binary.
    static func send(_ c: NWConnection, json: [String: Any]) {
        send(c, try! JSONSerialization.data(withJSONObject: json), opcode: .text)
    }

    static func send(_ c: NWConnection, _ d: Data, opcode: NWProtocolWebSocket.Opcode = .binary) {
        let m = NWProtocolWebSocket.Metadata(opcode: opcode)
        c.send(content: d, contentContext: .init(identifier: "m", metadata: [m]), isComplete: true, completion: .idempotent)
    }

    /// Closes a connection with a status, as the server's ws.Close does.
    static func close(_ c: NWConnection, _ code: NWProtocolWebSocket.CloseCode) {
        let m = NWProtocolWebSocket.Metadata(opcode: .close)
        m.closeCode = code
        c.send(content: nil, contentContext: .init(identifier: "close", metadata: [m]), isComplete: true,
               completion: .contentProcessed { _ in c.cancel() })
    }

    /// A WELCOME of a data handshake.
    static let welcome = Data([0x81] + [UInt8](repeating: 7, count: 19) + [0x60, 0xEA, 0, 0] + [1, 0, 0, 0, 0, 0, 0, 0])

    private func accept(_ c: NWConnection) {
        c.start(queue: .main)
        receive(c, first: true)
    }

    private func receive(_ c: NWConnection, first: Bool) {
        c.receiveMessage { [weak self] content, _, _, error in
            MainActor.assumeIsolated {
                guard let self, error == nil, let content else { return }
                if first, content.first == 0x01 || content.first == 0x02 {
                    self.data(c, content)
                    return
                }
                self.control(c, content)
                self.receive(c, first: false)
            }
        }
    }

    private func control(_ c: NWConnection, _ d: Data) {
        guard let m = try? JSONSerialization.jsonObject(with: d) as? [String: Any] else { return }
        switch m["type"] as? String {
        case "hello":
            controls.append(c)
            tokens.append(m["reconnect_token"] as? String)
            Self.send(c, json: ["type": "welcome", "protocol": "rex.control.v1",
                                "client": ["id": "c\(controls.count)", "reconnect_token": "t\(controls.count)"],
                                "features": features])
        case "request":
            let method = m["method"] as? String ?? ""
            let payload = m["payload"] as? [String: Any] ?? [:]
            calls.append(method)
            payloads.append(payload)
            guard let r = respond(method, payload) else { return }
            seq += 1
            Self.send(c, json: ["type": "response", "id": m["id"] ?? "", "seq": seq, "payload": r])
        default:
            break
        }
    }
}

/// A plain HTTP server, as the gateway's /api/upload: it reads one request
/// of a Content-Length on each connection and answers it as the test says.
@MainActor final class FakeHTTP {
    struct Request {
        /// The request line and the headers.
        let head: String
        let body: Data

        /// A header's value, by its name in any case.
        func header(_ name: String) -> String? {
            head.split(separator: "\r\n").dropFirst().lazy.compactMap { line in
                let kv = line.split(separator: ":", maxSplits: 1)
                return kv.count == 2 && kv[0].lowercased() == name.lowercased() ? kv[1].trimmingCharacters(in: .whitespaces) : nil
            }.first
        }
    }

    var respond: (Request) -> (status: Int, json: [String: Any]) = { _ in (200, [:]) }
    private(set) var requests: [Request] = []
    private let listener: NWListener

    init() throws {
        listener = try NWListener(using: .tcp, on: .any)
        listener.newConnectionHandler = { [weak self] c in
            MainActor.assumeIsolated { self?.accept(c) }
        }
        listener.start(queue: .main)
    }

    func url() async throws -> URL {
        for _ in 0..<250 {
            if let p = listener.port, p.rawValue != 0 { return URL(string: "http://127.0.0.1:\(p.rawValue)")! }
            try await Task.sleep(for: .milliseconds(20))
        }
        throw URLError(.cannotConnectToHost)
    }

    func stop() { listener.cancel() }

    private func accept(_ c: NWConnection) {
        c.start(queue: .main)
        read(c, Data())
    }

    private func read(_ c: NWConnection, _ got: Data) {
        c.receive(minimumIncompleteLength: 1, maximumLength: 1 << 16) { [weak self] d, _, done, error in
            MainActor.assumeIsolated {
                guard let self else { return }
                var got = got
                if let d { got.append(d) }
                if let end = got.range(of: Data("\r\n\r\n".utf8)) {
                    let r = Request(head: String(decoding: got[..<end.lowerBound], as: UTF8.self), body: Data(got[end.upperBound...]))
                    // A GET, as a WebSocket's upgrade, has no body.
                    guard let length = r.header("Content-Length").flatMap({ Int($0) }) ?? (r.head.hasPrefix("GET ") ? 0 : nil) else {
                        return self.answer(c, 411, ["error": "an upload has a Content-Length"])
                    }
                    if r.body.count >= length {
                        self.requests.append(r)
                        let (status, json) = self.respond(r)
                        return self.answer(c, status, json)
                    }
                }
                if done || error != nil { return c.cancel() }
                self.read(c, got)
            }
        }
    }

    private func answer(_ c: NWConnection, _ status: Int, _ json: [String: Any]) {
        let body = try! JSONSerialization.data(withJSONObject: json)
        let head = "HTTP/1.1 \(status) X\r\nContent-Type: application/json\r\nContent-Length: \(body.count)\r\nConnection: close\r\n\r\n"
        c.send(content: Data(head.utf8) + body, completion: .contentProcessed { _ in c.cancel() })
    }
}

extension XCTestCase {
    /// Waits on the main actor until a condition holds, or fails after a while.
    @MainActor func until(_ what: String, timeout: TimeInterval = 5, _ cond: @escaping () -> Bool) async {
        let end = Date().addingTimeInterval(timeout)
        while !cond() {
            if Date() > end { return XCTFail("timed out waiting for \(what)") }
            try? await Task.sleep(for: .milliseconds(20))
        }
    }
}
