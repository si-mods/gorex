import Foundation

/// Uploads a file to a terminal block over Rex's official rev 11
/// com.superlogical.terminal.upload_begin + rex.data.bytes.v1 stream.
enum Upload {
    static func send(_ file: URL, name: String, session: String, block: String,
                     control: ControlConnection, to base: URL,
                     urlSession: URLSession = .shared) async throws {
        let size = (try file.resourceValues(forKeys: [.fileSizeKey]).fileSize) ?? 0
        let ref = "upload:\(UUID().uuidString.lowercased())"
        let reply = try await control.call(RexProtocol.methodTerminalUploadBegin, [
            "session_id": session,
            "block_id": block,
            "args": ["ref": ref, "name": name, "size": size],
        ])
        guard let ticket = reply.payload["ticket"] as? String,
              let hello = DataHandshake.hello(ticket: ticket)
        else { throw RexError(code: "protocol", message: "missing upload ticket") }

        var request = URLRequest(url: base.rexURL("/api/data"))
        request.timeoutInterval = 30
        let task = urlSession.webSocketTask(with: request.withSubprotocol(RexProtocol.bytesProtocol))
        task.resume()
        defer { task.cancel(with: .normalClosure, reason: nil) }

        try await task.send(.data(hello))
        let first = try await task.receive()
        guard case .data(let welcomeData) = first,
              welcomeData.count == 32, welcomeData[0] == RexProtocol.dataWelcomeByte
        else { throw RexError(code: "protocol", message: "upload stream rejected") }

        let fh = try FileHandle(forReadingFrom: file)
        defer { try? fh.close() }
        let chunkSize = RexProtocol.maxChunk
        while true {
            let chunk = try fh.read(upToCount: chunkSize) ?? Data()
            if chunk.isEmpty { break }
            try await task.send(.data(chunk))
        }
        try await task.send(.data(Data()))
        // Wait for server close ("ok" = 1000).
        while true {
            do {
                _ = try await task.receive()
            } catch {
                if task.closeCode == .normalClosure { return }
                throw RexError(code: "upload", message: error.localizedDescription)
            }
        }
    }

    /// A photo's file name: the name the library suggests, as IMG_1234, with
    /// the extension of the file it gave, upper case as the camera's are.
    nonisolated static func photoName(suggested: String?, file: URL) -> String {
        let base = suggested.flatMap { $0.isEmpty ? nil : $0 } ?? "Photo"
        let ext = file.pathExtension
        if ext.isEmpty || !(base as NSString).pathExtension.isEmpty { return base }
        return base + "." + ext.uppercased()
    }
}
