import Foundation

/// What com.superlogical.terminal.ports returns: the TCP ports a block's
/// programs listen on, and the processes that hold them.
struct Ports: Decodable, Equatable {
    struct Port: Decodable, Equatable, Identifiable {
        var port: Int
        var address: String
        var `protocol`: String
        var first_seen_at: String?
        var pid: Int?

        var id: String { "\(address):\(port):\(pid ?? 0)" }
    }

    struct Process: Decodable, Equatable {
        var pid: Int
        var user_id: Int?
        var user: String?
        var name: String
        var path: String?
        var argv0: String?
        var script: String?
        var cwd: String?
    }

    /// ok, partial or unsupported.
    var status: String
    var ports: [Port]
    var processes: [Process]

    init(status: String, ports: [Port], processes: [Process]) {
        self.status = status
        self.ports = ports
        self.processes = processes
    }

    private struct Payload<T: Decodable>: Decodable { let payload: T }

    /// The answer of a ports call, read from the bytes of its response.
    init(response: Data) throws {
        self = try JSONDecoder().decode(Payload<Self>.self, from: response).payload
    }

    /// The process that holds a port, if the server described it.
    func process(of port: Port) -> Process? {
        port.pid.flatMap { pid in processes.first { $0.pid == pid } }
    }

    /// The URL to open for a port, when the phone can reach it. A loopback
    /// address is only on the host. A wildcard address uses the host
    /// address the app dialed.
    static func openURL(port: Port, hostAddress: String?) -> URL? {
        var name = port.address
        if name == "127.0.0.1" || name == "::1" { return nil }
        if name == "0.0.0.0" || name == "::" {
            name = hostAddress ?? ""
        }
        if name.isEmpty { return nil }
        if name.contains(":") { name = "[\(name)]" }
        return URL(string: "http://\(name):\(port.port)/")
    }
}
