import Compression
import Foundation
import UniformTypeIdentifiers

/// GoRex's uploads, outside Rex's API: a file posted to /api/upload goes
/// into the temporary folder of the server's machine, as the app sends a
/// file dropped on a pane of a host, and the answer is its path there as a
/// word of the block's shell, to paste. A server that takes them lists
/// Upload.feature in its welcome. One that lists Upload.deflateRaw too
/// takes the file compressed, which a file of text gets much smaller by.
enum Upload {
    /// The welcome's feature of a server that takes uploads.
    static let feature = "gorex.upload"

    /// The welcome's feature of a server that takes an upload's body as raw
    /// DEFLATE (RFC 1951, with no zlib or gzip wrapper), as Content-Encoding
    /// deflate-raw, with the file's size in the query's size.
    static let deflateRaw = "gorex.upload.deflate-raw"

    /// The code of the error of a file sent whose path is not to be
    /// pasted: the block's shell cannot take it. Its message is the
    /// server's reason.
    nonisolated static let notPasted = "upload.notPasted"

    /// Sends the file at file as name, for block, to the server at base
    /// through session, streamed from the disk, and returns the word to
    /// paste. With compress, the server takes the file compressed, which
    /// it is sent when it gets smaller.
    nonisolated static func send(_ file: URL, name: String, block: String, to base: URL,
                                 compress: Bool = false, session: URLSession = .shared) async throws -> String {
        var c = URLComponents(url: base.apiURL("/api/upload"), resolvingAgainstBaseURL: false)!
        c.queryItems = [URLQueryItem(name: "block_id", value: block), URLQueryItem(name: "name", value: name)]
        // Compressing is only a saving: the file goes as it is when it fails.
        let packed = compress ? (try? deflated(file)) : nil
        defer { if let packed { try? FileManager.default.removeItem(at: packed.file) } }
        if let packed { c.queryItems?.append(URLQueryItem(name: "size", value: String(packed.size))) }
        // A query reads "+" as a space.
        c.percentEncodedQuery = c.percentEncodedQuery?.replacingOccurrences(of: "+", with: "%2B")
        var r = URLRequest(url: c.url!)
        r.httpMethod = "POST"
        r.setValue("application/octet-stream", forHTTPHeaderField: "Content-Type")
        if packed != nil { r.setValue("deflate-raw", forHTTPHeaderField: "Content-Encoding") }
        let (data, response) = try await session.upload(for: r, fromFile: packed?.file ?? file)
        let answer = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard status == 200, let word = answer?["word"] as? String else {
            throw RexError(code: "upload", message: answer?["error"] as? String
                ?? HTTPURLResponse.localizedString(forStatusCode: status))
        }
        if word.isEmpty {
            throw RexError(code: notPasted, message: answer?["reason"] as? String ?? "")
        }
        return word
    }

    /// The file at file compressed as raw DEFLATE into a temporary file, a
    /// piece at a time, as a file may be large, and the size of the file
    /// itself; nil when its type is compressed already, as photos, video,
    /// sound, archives and PDFs are, or when it gets no smaller. The caller
    /// removes the file.
    nonisolated static func deflated(_ file: URL) throws -> (file: URL, size: Int)? {
        if let type = UTType(filenameExtension: file.pathExtension),
           [UTType.image, .audiovisualContent, .archive, .pdf].contains(where: type.conforms(to:)) { return nil }
        let input = try FileHandle(forReadingFrom: file)
        defer { try? input.close() }
        let out = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString + ".deflate")
        FileManager.default.createFile(atPath: out.path, contents: nil)
        var size = 0, packed = 0
        do {
            let output = try FileHandle(forWritingTo: out)
            defer { try? output.close() }
            // Compression's zlib is raw DEFLATE.
            let filter = try OutputFilter(.compress, using: .zlib) { data in
                if let data {
                    packed += data.count
                    try output.write(contentsOf: data)
                }
            }
            while let piece = try input.read(upToCount: 1 << 20), !piece.isEmpty {
                size += piece.count
                try filter.write(piece)
            }
            try filter.finalize()
        } catch {
            try? FileManager.default.removeItem(at: out)
            throw error
        }
        if packed >= size {
            try? FileManager.default.removeItem(at: out)
            return nil
        }
        return (out, size)
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
