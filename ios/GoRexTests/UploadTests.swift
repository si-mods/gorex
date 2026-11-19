import Foundation
import XCTest

/// Add Photo and Add File: servers at rev 11+ accept uploads via upload_begin +
/// rex.data.bytes.v1.
nonisolated final class UploadTests: XCTestCase {
    @MainActor private func online(_ rex: FakeRex, revision: Int) async throws -> Host {
        rex.revision = revision
        rex.respond = { method, _ in method == "session.bootstrap" ? ["attach_all": ["sessions": []]] : [:] }
        let h = Host(HostConfig(name: "test", url: try await rex.url()))
        h.connect()
        await until("online") { h.status == .online }
        return h
    }

    /// Only servers negotiating rev 11+ advertise uploads.
    @MainActor func testUploadsFollowAPIRevision() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        let old = try await online(rex, revision: RexProtocol.uploadRevision - 1)
        XCTAssertFalse(old.canUpload)
        old.stop()
        let new = try await online(rex, revision: RexProtocol.uploadRevision)
        XCTAssertTrue(new.canUpload)
        new.stop()
        let offline = Host(HostConfig(name: "offline", url: try await rex.url()))
        XCTAssertFalse(offline.canUpload)
    }

    private func downloadBegin(_ rex: FakeRex) {
        rex.respond = { method, _ in
            method == RexProtocol.methodTerminalDownloadBegin
                ? ["ticket": "uH4mJKwzMarlTNT-UiyMog", "name": "a.bin", "version": "v1", "size": 6] : [:]
        }
    }

    private func fetch(_ rex: FakeRex) async throws -> DownloadResult {
        let control = ControlConnection(base: try await rex.url(), auth: nil)
        try await control.open(token: nil)
        defer { control.close() }
        return try await Download.fetch(path: "a.bin", session: "s", block: "b",
                                        control: control, to: try await rex.url(), auth: nil)
    }

    private func parts() -> Set<String> {
        Set((try? FileManager.default.contentsOfDirectory(atPath: Download.folder.path)) ?? [])
    }

    /// A download goes to a temporary file, chunk by chunk, not to memory.
    @MainActor func testDownloadWritesChunksToAFile() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        downloadBegin(rex)
        rex.data = { c, _ in
            FakeRex.send(c, FakeRex.welcome)
            FakeRex.send(c, Data([1, 2, 3]))
            FakeRex.send(c, Data([4, 5, 6]))
            FakeRex.send(c, Data())
        }
        let r = try await fetch(rex)
        let file = try XCTUnwrap(r.file)
        defer { try? FileManager.default.removeItem(at: file) }
        XCTAssertEqual(try Data(contentsOf: file), Data([1, 2, 3, 4, 5, 6]))
        XCTAssertEqual(r.name, "a.bin")
        XCTAssertFalse(r.notModified)
    }

    /// A stream that fails midway leaves no temporary file.
    @MainActor func testFailedDownloadLeavesNoFile() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        downloadBegin(rex)
        rex.data = { c, _ in
            FakeRex.send(c, FakeRex.welcome)
            FakeRex.send(c, Data([1, 2, 3]))
            FakeRex.close(c, .protocolCode(.policyViolation))
        }
        let before = parts()
        do {
            _ = try await fetch(rex)
            XCTFail("the download should fail")
        } catch {
            XCTAssertEqual((error as? RexError)?.code, "stream")
        }
        XCTAssertEqual(parts(), before)
    }

    func testPhotoNameKeepsLibraryNameAndAddsUpperCaseExtension() {
        let heic = URL(fileURLWithPath: "/tmp/x/a.heic")
        XCTAssertEqual(Upload.photoName(suggested: "IMG_1234", file: heic), "IMG_1234.HEIC")
        XCTAssertEqual(Upload.photoName(suggested: "IMG_1234.PNG", file: heic), "IMG_1234.PNG")
        XCTAssertEqual(Upload.photoName(suggested: nil, file: URL(fileURLWithPath: "/tmp/x/y.jpeg")), "Photo.JPEG")
        XCTAssertEqual(Upload.photoName(suggested: "", file: URL(fileURLWithPath: "/tmp/x/y")), "Photo")
    }
}
