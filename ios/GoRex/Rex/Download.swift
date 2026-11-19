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
                      ifNoneMatch: String? = nil, maxBytes: Int? = nil,
                      control: ControlConnection, to base: URL, auth: String?,
                      urlSession: () throws -> URLSession = { .shared }) async throws -> DownloadResult {
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
        // A file larger than the caller holds in memory is not read; its
        // ticket lapses on the server.
        if let maxBytes, let size = reply.payload["size"] as? Int, size > maxBytes {
            throw RexError(code: "too_large", message: "the file is \(size) bytes, more than \(maxBytes)")
        }
        guard let ticket = reply.payload["ticket"] as? String else {
            throw RexError(code: "protocol", message: "missing download ticket")
        }
        let stream = try await BytesStream.open(ticket: ticket, to: base, auth: auth, urlSession: urlSession)
        defer { stream.close() }
        // A cancelled task closes the stream: a pending read of the socket
        // does not end by itself, and its closing reads as the end of the
        // file, so the task checks again before it keeps the bytes.
        let data = try await withTaskCancellationHandler {
            try Task.checkCancellation()
            return try await stream.readAll()
        } onCancel: {
            Task { @MainActor in stream.close() }
        }
        try Task.checkCancellation()
        return DownloadResult(name: name, version: version, data: data, notModified: false)
    }
}
