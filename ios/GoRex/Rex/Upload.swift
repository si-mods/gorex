import Foundation

/// GoRex's uploads, outside Rex's API: a file posted to /api/upload goes
/// into the temporary folder of the server's machine, as the app sends a
/// file dropped on a pane of a host, and the answer is its path there as a
/// word of the block's shell, to paste. A server that takes them lists
/// Upload.feature in its welcome.
enum Upload {
    /// The welcome's feature of a server that takes uploads.
    static let feature = "gorex.upload"

    /// The code of the error of a file sent whose path is not to be
    /// pasted: the block's shell cannot take it. Its message is the
    /// server's reason.
    nonisolated static let notPasted = "upload.notPasted"

    /// Sends the file at file as name, for block, to the server at base
    /// through session, streamed from the disk, and returns the word to
    /// paste.
    nonisolated static func send(_ file: URL, name: String, block: String, to base: URL,
                                 session: URLSession = DeviceAccess.session) async throws -> String {
        var c = URLComponents(url: base.apiURL("/api/upload"), resolvingAgainstBaseURL: false)!
        c.queryItems = [URLQueryItem(name: "block_id", value: block), URLQueryItem(name: "name", value: name)]
        // A query reads "+" as a space.
        c.percentEncodedQuery = c.percentEncodedQuery?.replacingOccurrences(of: "+", with: "%2B")
        var r = URLRequest(url: c.url!).withDeviceCredential(for: base)
        r.httpMethod = "POST"
        r.setValue("application/octet-stream", forHTTPHeaderField: "Content-Type")
        let (data, response) = try await session.upload(for: r, fromFile: file)
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

    /// A photo's file name: the name the library suggests, as IMG_1234, with
    /// the extension of the file it gave, upper case as the camera's are.
    nonisolated static func photoName(suggested: String?, file: URL) -> String {
        let base = suggested.flatMap { $0.isEmpty ? nil : $0 } ?? "Photo"
        let ext = file.pathExtension
        if ext.isEmpty || !(base as NSString).pathExtension.isEmpty { return base }
        return base + "." + ext.uppercased()
    }
}
