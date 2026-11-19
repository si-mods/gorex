import Foundation

/// Reusable `rex.data.bytes.v1` client stream for file uploads, downloads, and tunnels.
final class BytesStream {
    private let task: URLSessionWebSocketTask

    private init(task: URLSessionWebSocketTask) {
        self.task = task
    }

    /// Connects to `/api/data` with `rex.data.bytes.v1`, sends HELLO(ticket), and awaits WELCOME,
    /// on the host's session (Host.session).
    static func open(ticket: String, to base: URL, auth: String?,
                     urlSession: () throws -> URLSession = { .shared }) async throws -> BytesStream {
        guard let hello = DataHandshake.hello(ticket: ticket) else {
            throw RexError(code: "protocol", message: "invalid data stream ticket")
        }
        var request = URLRequest(url: base.rexURL("/api/data"))
        request.timeoutInterval = 30
        // A session of the built-in Tailscale's proxy is invalid once the
        // proxy changes, as it can during the ticket's round trip, and a
        // task on it aborts the app, so the session is the one of the moment.
        let task = try urlSession().webSocketTask(with: request.withSubprotocol(RexProtocol.bytesProtocol).authorized(auth))
        task.resume()
        do {
            try await task.send(.data(hello))
            let first = try await task.receive()
            guard case .data(let frame) = first, let handshake = DataHandshake(frame) else {
                throw RexError(code: "protocol", message: "unexpected data stream handshake")
            }
            switch handshake {
            case .welcome:
                return BytesStream(task: task)
            case .reject(let reason):
                throw RexError(code: "rejected", message: String(format: "data stream rejected (0x%02x)", reason))
            }
        } catch {
            task.cancel(with: .normalClosure, reason: nil)
            throw error
        }
    }

    func send(_ data: Data) async throws {
        try await task.send(.data(data))
    }

    /// Signals EOF on the outbound half of the byte stream with an empty binary frame.
    func halfClose() async throws {
        try await task.send(.data(Data()))
    }

    /// Reads the next binary payload, or `nil` on half-close EOF (empty frame) or normal closure (1000).
    func receive() async throws -> Data? {
        while true {
            do {
                let message = try await task.receive()
                if case .data(let data) = message {
                    return data.isEmpty ? nil : data
                }
            } catch {
                if task.closeCode == .normalClosure { return nil }
                throw RexError(code: "stream", message: error.localizedDescription)
            }
        }
    }

    /// Writes all remaining bytes, until EOF (empty frame or normal closure
    /// 1000), to a new file at `url`, chunk by chunk: the file's size is
    /// not held in memory, and the writes are off the main actor. A failure
    /// or a cancel removes the file.
    func readAll(to url: URL) async throws {
        let fh: FileHandle
        do {
            fh = try await Task.detached {
                try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
                guard FileManager.default.createFile(atPath: url.path, contents: nil) else {
                    throw CocoaError(.fileWriteUnknown)
                }
                return try FileHandle(forWritingTo: url)
            }.value
        } catch {
            try? FileManager.default.removeItem(at: url)
            throw error
        }
        do {
            while let chunk = try await receive() {
                try Task.checkCancellation()
                try await Task.detached { try fh.write(contentsOf: chunk) }.value
            }
            try await Task.detached { try fh.close() }.value
        } catch {
            try? fh.close()
            try? FileManager.default.removeItem(at: url)
            throw error
        }
    }

    /// Waits for the server to close the stream normally (1000).
    func waitForClose() async throws {
        while true {
            do {
                _ = try await task.receive()
            } catch {
                if task.closeCode == .normalClosure { return }
                throw RexError(code: "stream", message: error.localizedDescription)
            }
        }
    }

    func close() {
        task.cancel(with: .normalClosure, reason: nil)
    }
}
