import Foundation

/// Creates, inspects, and connects to rev 14 `com.superlogical.tunnel` blocks.
enum Tunnel {
    static let flavorDial = "com.superlogical.tunnel.dial"
    static let flavorListen = "com.superlogical.tunnel.listen"

    struct Info: Equatable {
        let blockID: String
        let flavor: String
        let target: String?
        let address: String?
        let clientAddress: String?
        let clientTarget: String?
        let connections: Int
        let lastOwner: String?
    }

    static func openDial(session: String, host: String, port: Int,
                         clientAddress: String? = nil,
                         control: ControlConnection) async throws -> String {
        var options: [String: Any] = ["host": host, "port": port]
        if let clientAddress {
            options["client_address"] = clientAddress
        }
        let reply = try await control.call("session.new_block", [
            "session_id": session,
            "flavor": flavorDial,
            "options": options,
        ])
        guard let blockID = reply.payload["block_id"] as? String, !blockID.isEmpty else {
            throw RexError(code: "protocol", message: "missing tunnel block_id")
        }
        return blockID
    }

    static func openListen(session: String, address: String = "127.0.0.1", port: Int = 0,
                           clientTarget: String? = nil,
                           control: ControlConnection) async throws -> String {
        var options: [String: Any] = ["address": address, "port": port]
        if let clientTarget {
            options["client_target"] = clientTarget
        }
        let reply = try await control.call("session.new_block", [
            "session_id": session,
            "flavor": flavorListen,
            "options": options,
        ])
        guard let blockID = reply.payload["block_id"] as? String, !blockID.isEmpty else {
            throw RexError(code: "protocol", message: "missing tunnel block_id")
        }
        return blockID
    }

    static func info(session: String, block: String, control: ControlConnection) async throws -> Info {
        let reply = try await control.call(RexProtocol.methodTunnelInfo, [
            "session_id": session,
            "block_id": block,
        ])
        return Info(
            blockID: block,
            flavor: reply.payload["flavor"] as? String ?? "",
            target: reply.payload["target"] as? String,
            address: reply.payload["address"] as? String,
            clientAddress: reply.payload["client_address"] as? String,
            clientTarget: reply.payload["client_target"] as? String,
            connections: reply.payload["connections"] as? Int ?? 0,
            lastOwner: reply.payload["last_owner"] as? String
        )
    }

    static func connect(session: String, block: String,
                        dataRefID: String? = nil,
                        control: ControlConnection, to base: URL,
                        urlSession: URLSession = .shared) async throws -> BytesStream {
        let ref = dataRefID ?? "conn:\(UUID().uuidString.lowercased())"
        let reply = try await control.call("data.connect", [
            "session_id": session,
            "block_id": block,
            "data_ref_id": ref,
            "subprotocol": RexProtocol.bytesProtocol,
        ])
        guard let ticket = reply.payload["ticket"] as? String else {
            throw RexError(code: "protocol", message: "missing tunnel ticket")
        }
        return try await BytesStream.open(ticket: ticket, to: base, urlSession: urlSession)
    }
}
