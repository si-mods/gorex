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

    /// A host given a new address keeps its open terminals working: they
    /// write through the new connection.
    @MainActor func testNewAddressKeepsTerminalsWorking() async throws {
        let saved = UserDefaults.standard.data(forKey: "hosts")
        defer { UserDefaults.standard.set(saved, forKey: "hosts") }
        UserDefaults.standard.removeObject(forKey: "hosts")
        let (a, b) = (try FakeRex(), try FakeRex())
        defer { a.stop(); b.stop() }
        for r in [a, b] { r.respond = { m, _ in m == "session.bootstrap" ? ["attach_all": ["sessions": []]] : [:] } }
        let store = HostStore()
        var config = HostConfig(name: "test", url: try await a.url())
        store.add(config)
        let h = try XCTUnwrap(store.host(config.id))
        await until("online") { h.status == .online }
        let s = TerminalSession(host: h, session: "s", block: "b")
        config.url = try await b.url()
        store.update(config)
        s.type(Data("ls\r".utf8))
        await until("written at the new address") { b.count("com.superlogical.terminal.write") == 1 }
        XCTAssertTrue(store.host(config.id) === h)
        s.close()
        store.remove(config.id)
    }
}
