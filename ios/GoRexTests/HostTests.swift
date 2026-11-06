import XCTest

/// What a host's window menu does with no control connection.
nonisolated final class HostTests: XCTestCase {
    /// Offline, a change to the windows fails, for the alert to tell,
    /// instead of doing nothing.
    @MainActor func testOfflineChangesFail() async {
        let h = Host(HostConfig(name: "test", url: URL(string: "http://127.0.0.1:1")!))
        let changes: [(String, () async throws -> Void)] = [
            ("new", { _ = try await h.newWindow(in: "s") }),
            ("rename", { try await h.renameWindow("w", in: "s", to: "x") }),
            ("move", { try await h.moveWindow("w", in: "s", before: nil) }),
            ("close", { try await h.closeWindow("w", in: "s") }),
        ]
        for (name, change) in changes {
            do {
                try await change()
                XCTFail("\(name) did not fail")
            } catch {
                XCTAssertEqual((error as? RexError)?.code, "offline", name)
            }
        }
    }
}
