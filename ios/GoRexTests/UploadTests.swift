import Foundation
import XCTest

/// Add Photo and Add File: a server lists uploads in its welcome, and a file
/// is posted to /api/upload, its word to paste answered.
nonisolated final class UploadTests: XCTestCase {
    /// A host online at a fake server that lists features.
    @MainActor private func online(_ rex: FakeRex, features: [String]) async throws -> Host {
        rex.features = features
        rex.respond = { method, _ in method == "session.bootstrap" ? ["attach_all": ["sessions": []]] : [:] }
        let h = Host(HostConfig(name: "test", url: try await rex.url()))
        h.connect()
        await until("online") { h.status == .online }
        return h
    }

    /// A file of bytes in a folder of its own, named name.
    private func file(_ name: String, _ bytes: Data) throws -> URL {
        let dir = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: dir) }
        let f = dir.appending(path: name)
        try bytes.write(to: f)
        return f
    }

    /// Only a server whose welcome lists gorex.upload takes uploads, so an
    /// older one leaves Add Photo and Add File off.
    @MainActor func testUploadsFollowTheWelcome() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        let old = try await online(rex, features: [])
        XCTAssertFalse(old.canUpload)
        old.stop()
        let new = try await online(rex, features: ["other", "gorex.upload"])
        XCTAssertTrue(new.canUpload)
        new.stop()
        let offline = Host(HostConfig(name: "offline", url: try await rex.url()))
        XCTAssertFalse(offline.canUpload)
    }

    /// A file is posted as application/octet-stream, of a length, for its
    /// block and by its name, a "+" kept; the word answered is returned.
    @MainActor func testUploadPostsTheFile() async throws {
        let http = try FakeHTTP()
        defer { http.stop() }
        http.respond = { _ in (200, ["path": "/tmp/gorex/a+b c.HEIC", "word": "'/tmp/gorex/a+b c.HEIC'"]) }
        let bytes = Data((0..<300_000).map { UInt8($0 % 251) })
        let f = try file("a+b c.HEIC", bytes)
        let word = try await Upload.send(f, name: f.lastPathComponent, block: "block:1", to: try await http.url())
        XCTAssertEqual(word, "'/tmp/gorex/a+b c.HEIC'")
        let r = try XCTUnwrap(http.requests.first)
        XCTAssertTrue(r.head.hasPrefix("POST /api/upload?"), r.head)
        let target = String(r.head.split(separator: " ")[1])
        let q = URLComponents(string: target)?.queryItems ?? []
        XCTAssertEqual(q.first { $0.name == "block_id" }?.value, "block:1")
        XCTAssertFalse(target.contains("+"), target)
        XCTAssertEqual(q.first { $0.name == "name" }?.value, "a+b c.HEIC")
        XCTAssertEqual(r.header("Content-Type"), "application/octet-stream")
        XCTAssertEqual(r.header("Content-Length"), String(bytes.count))
        XCTAssertEqual(r.body, bytes)
    }

    /// A file sent whose path the block's shell cannot take is not pasted:
    /// the error says the server's reason.
    @MainActor func testUploadNotPastedTellsWhy() async throws {
        let http = try FakeHTTP()
        defer { http.stop() }
        http.respond = { _ in (200, ["path": "/tmp/a%b/c.txt", "word": "", "reason": "/tmp/a%b/c.txt is uploaded, but the shell can't take this path"]) }
        let f = try file("c.txt", Data("hi".utf8))
        do {
            _ = try await Upload.send(f, name: "c.txt", block: "block:1", to: try await http.url())
            XCTFail("an empty word was returned")
        } catch let e as RexError {
            XCTAssertEqual(e.code, Upload.notPasted)
            XCTAssertEqual(e.message, "/tmp/a%b/c.txt is uploaded, but the shell can't take this path")
        }
    }

    /// A refused upload fails with the server's reason.
    @MainActor func testUploadFailureTellsWhy() async throws {
        let http = try FakeHTTP()
        defer { http.stop() }
        http.respond = { _ in (404, ["error": "no block block:gone"]) }
        let f = try file("notes.txt", Data("hi".utf8))
        do {
            _ = try await Upload.send(f, name: "notes.txt", block: "block:gone", to: try await http.url())
            XCTFail("the upload succeeded")
        } catch {
            XCTAssertEqual(error.localizedDescription, "no block block:gone")
        }
    }

    /// To a server that takes it, a file of text is posted compressed: raw
    /// DEFLATE, as Content-Encoding deflate-raw, its size in the query, the
    /// body inflating to the file.
    @MainActor func testUploadCompressesText() async throws {
        let http = try FakeHTTP()
        defer { http.stop() }
        http.respond = { _ in (200, ["path": "/tmp/gorex/app.log", "word": "/tmp/gorex/app.log"]) }
        let bytes = Data(String(repeating: "2026-11-12 12:00:00 INFO a line of a log\n", count: 20_000).utf8)
        let f = try file("app.log", bytes)
        _ = try await Upload.send(f, name: "app.log", block: "block:1", to: try await http.url(), compress: true)
        let r = try XCTUnwrap(http.requests.first)
        let q = URLComponents(string: String(r.head.split(separator: " ")[1]))?.queryItems ?? []
        XCTAssertEqual(q.first { $0.name == "size" }?.value, String(bytes.count))
        XCTAssertEqual(r.header("Content-Encoding"), "deflate-raw")
        XCTAssertLessThan(r.body.count, bytes.count / 10)
        XCTAssertEqual(try (r.body as NSData).decompressed(using: .zlib) as Data, bytes)
    }

    /// A file compressed already, as a photo, or one that gets no smaller,
    /// is posted as it is, as it is to a server that takes no compressed
    /// file.
    @MainActor func testUploadSendsCompressedFilesAsTheyAre() async throws {
        let http = try FakeHTTP()
        defer { http.stop() }
        http.respond = { _ in (200, ["path": "/tmp/gorex/x", "word": "/tmp/gorex/x"]) }
        let text = Data(String(repeating: "hello ", count: 10_000).utf8)
        var noise = Data(count: 100_000)
        noise.withUnsafeMutableBytes { for i in $0.indices { $0[i] = UInt8.random(in: 0...255) } }
        for (name, bytes, compress) in [("IMG_0001.HEIC", text, true), ("noise.bin", noise, true), ("notes.txt", text, false)] {
            let f = try file(name, bytes)
            XCTAssertNil(compress ? try Upload.deflated(f) : nil, name)
            _ = try await Upload.send(f, name: name, block: "block:1", to: try await http.url(), compress: compress)
            let r = try XCTUnwrap(http.requests.last)
            XCTAssertNil(r.header("Content-Encoding"), name)
            XCTAssertFalse(r.head.contains("size="), name)
            XCTAssertEqual(r.body, bytes, name)
        }
    }

    /// The compressed file is a temporary one of its own, beside the size of
    /// the file it is of.
    func testDeflated() throws {
        let bytes = Data(String(repeating: "func f() {}\n", count: 50_000).utf8)
        let f = try file("main.swift", bytes)
        let packed = try XCTUnwrap(Upload.deflated(f))
        defer { try? FileManager.default.removeItem(at: packed.file) }
        XCTAssertEqual(packed.size, bytes.count)
        XCTAssertNotEqual(packed.file, f)
        XCTAssertEqual(try (Data(contentsOf: packed.file) as NSData).decompressed(using: .zlib) as Data, bytes)
    }

    /// A photo is named as the library suggests, with its file's extension
    /// upper case, as the camera names it.
    func testPhotoName() {
        let heic = URL(fileURLWithPath: "/tmp/x/version=1&uuid=ABC.heic")
        XCTAssertEqual(Upload.photoName(suggested: "IMG_1234", file: heic), "IMG_1234.HEIC")
        XCTAssertEqual(Upload.photoName(suggested: "IMG_1234.PNG", file: heic), "IMG_1234.PNG")
        XCTAssertEqual(Upload.photoName(suggested: nil, file: URL(fileURLWithPath: "/tmp/x/y.jpeg")), "Photo.JPEG")
        XCTAssertEqual(Upload.photoName(suggested: "", file: URL(fileURLWithPath: "/tmp/x/y")), "Photo")
    }
}
