import Foundation

struct DownloadResult: Equatable {
    let name: String
    let version: String
    let data: Data?
    let notModified: Bool
}

/// Downloads a file from a terminal block over Rex's official rev 13
/// `com.superlogical.terminal.download_begin` + `rex.data.bytes.v1` stream.
enum Download {
    static func fetch(path: String, session: String, block: String,
                      ifNoneMatch: String? = nil,
                      control: ControlConnection, to base: URL, auth: String?,
                      urlSession: URLSession = .shared) async throws -> DownloadResult {
        var args: [String: Any] = [
            "ref": "download:\(UUID().uuidString.lowercased())",
            "path": path,
        ]
        if let ifNoneMatch {
            args["if_none_match"] = ifNoneMatch
        }
        let reply = try await control.call(RexProtocol.methodTerminalDownloadBegin, [
            "session_id": session,
            "block_id": block,
            "args": args,
        ])
        let name = reply.payload["name"] as? String ?? (path as NSString).lastPathComponent
        let version = reply.payload["version"] as? String ?? ""
        if reply.payload["not_modified"] as? Bool == true {
            return DownloadResult(name: name, version: version, data: nil, notModified: true)
        }
        guard let ticket = reply.payload["ticket"] as? String else {
            throw RexError(code: "protocol", message: "missing download ticket")
        }
        let stream = try await BytesStream.open(ticket: ticket, to: base, auth: auth, urlSession: urlSession)
        defer { stream.close() }
        let data = try await stream.readAll()
        return DownloadResult(name: name, version: version, data: data, notModified: false)
    }
}
