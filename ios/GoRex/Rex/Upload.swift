import Foundation

/// Uploads a file to a terminal block over Rex's official rev 11
/// com.superlogical.terminal.upload_begin + rex.data.bytes.v1 stream.
enum Upload {
    static func send(_ file: URL, name: String, session: String, block: String,
                     control: ControlConnection, to base: URL, auth: String?,
                     batch: (id: String, index: Int, count: Int)? = nil,
                     urlSession: () throws -> URLSession = { .shared }) async throws {
        let size = try await Task.detached { (try file.resourceValues(forKeys: [.fileSizeKey]).fileSize) ?? 0 }.value
        let ref = "upload:\(UUID().uuidString.lowercased())"
        var args: [String: Any] = ["ref": ref, "name": name, "size": size]
        // A batch's paths are typed together once its files are all up, in
        // index order (docs/IOS-025-composer.md).
        if let batch {
            args["batch"] = batch.id
            args["index"] = batch.index
            args["count"] = batch.count
        }
        let reply = try await control.call(RexProtocol.methodTerminalUploadBegin, [
            "session_id": session,
            "block_id": block,
            "args": args,
        ])
        guard let ticket = reply.payload["ticket"] as? String else {
            throw RexError(code: "protocol", message: "missing upload ticket")
        }

        let stream = try await BytesStream.open(ticket: ticket, to: base, auth: auth, urlSession: urlSession)
        defer { stream.close() }

        let chunkSize = RexProtocol.maxChunk
        // The file is read off the main actor, a chunk at a time.
        let fh = try await Task.detached { try FileHandle(forReadingFrom: file) }.value
        defer { try? fh.close() }
        while true {
            let chunk = try await Task.detached { try fh.read(upToCount: chunkSize) ?? Data() }.value
            if chunk.isEmpty { break }
            try await stream.send(chunk)
        }
        try await stream.halfClose()
        try await stream.waitForClose()
    }

    /// Sends each of files that is not in sent, in order, and adds it to
    /// sent once it is up. A failure at one file leaves those before it in
    /// sent, so a second call sends the rest only: the server types each
    /// file's path as it arrives, and a file sent twice would be typed twice.
    static func sendEach(_ files: [URL], sent: inout Set<URL>, _ send: (URL) async throws -> Void) async throws {
        for f in files where !sent.contains(f) {
            try await send(f)
            sent.insert(f)
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
