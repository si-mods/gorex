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

    func testPhotoNameKeepsLibraryNameAndAddsUpperCaseExtension() {
        let heic = URL(fileURLWithPath: "/tmp/x/a.heic")
        XCTAssertEqual(Upload.photoName(suggested: "IMG_1234", file: heic), "IMG_1234.HEIC")
        XCTAssertEqual(Upload.photoName(suggested: "IMG_1234.PNG", file: heic), "IMG_1234.PNG")
        XCTAssertEqual(Upload.photoName(suggested: nil, file: URL(fileURLWithPath: "/tmp/x/y.jpeg")), "Photo.JPEG")
        XCTAssertEqual(Upload.photoName(suggested: "", file: URL(fileURLWithPath: "/tmp/x/y")), "Photo")
    }
}
