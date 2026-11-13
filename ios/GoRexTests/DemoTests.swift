import XCTest

/// Demo Mode: the host that runs on the phone, its windows, and the
/// terminal in them.
nonisolated final class DemoTests: XCTestCase {
    /// The demo host is online at once, with the sessions, windows and
    /// records the list and the inbox show.
    @MainActor func testDemoHostRunsOnThePhone() {
        let h = Host(Demo.config())
        h.connect()
        defer { h.stop() }
        XCTAssertTrue(h.isDemo)
        XCTAssertEqual(h.status, .online)
        XCTAssertEqual(h.sessions.map(\.label), ["GoRex", "dotfiles"])
        let windows = h.sessions.flatMap { $0.view?.windows ?? [] }
        XCTAssertEqual(windows.count, 5)
        XCTAssertEqual(h.title(windows[0]), "Claude Code")
        XCTAssertEqual(h.status([Demo.claude])?.state, "blocked")
        XCTAssertEqual(h.status([Demo.dashboard])?.progress, 65)
        XCTAssertEqual(h.status([Demo.tests])?.state, "done")
        XCTAssertEqual(h.inbox.count, 3)
        // Nothing is connected, so nothing can be sent to a machine.
        XCTAssertFalse(h.canUpload)
        XCTAssertNil(h.control)
    }

    /// The window menu's calls change the demo host on the phone, as the
    /// server's would, without a control connection.
    @MainActor func testDemoWindowsChangeWithoutAServer() async throws {
        let h = Host(Demo.config())
        h.connect()
        defer { h.stop() }
        let first = try XCTUnwrap(h.sessions.first)

        let made = try await h.newSession()
        let (session, block) = try XCTUnwrap(made)
        XCTAssertEqual(h.sessions.count, 3)
        XCTAssertNotNil(h.blocks[block])
        XCTAssertEqual(h.sessions.last?.label, "New Session")

        try await h.renameSession(session, to: "Scratch")
        XCTAssertEqual(h.sessions.last?.label, "Scratch")
        let window = try XCTUnwrap(h.sessions.last?.view?.windows?.first?.window_id)
        try await h.renameWindow(window, in: session, to: "My Shell")
        XCTAssertEqual(h.title(try XCTUnwrap(h.sessions.last?.view?.windows?.first)), "My Shell")

        let added = try await h.newWindow(in: first.id)
        let opened = try XCTUnwrap(added)
        XCTAssertNotNil(h.blocks[opened])
        XCTAssertEqual(h.sessions.first?.view?.windows?.count, 4)
        let openedWindow = try XCTUnwrap(h.sessions.first?.view?.windows?.last?.window_id)
        try await h.moveWindow(openedWindow, in: first.id, before: nil)
        XCTAssertEqual(h.sessions.first?.view?.windows?.last?.focused_block_id, opened)
        try await h.closeWindow(openedWindow, in: first.id)
        XCTAssertNil(h.blocks[opened])
        XCTAssertEqual(h.sessions.first?.view?.windows?.count, 3)

        try await h.closeSession(session)
        XCTAssertEqual(h.sessions.count, 2)
        XCTAssertNil(h.blocks[block])
    }

    /// A demo terminal shows its window's screen, and the commands typed
    /// run on the phone.
    @MainActor func testDemoTerminalRunsCommands() {
        let h = Host(Demo.config())
        h.connect()
        defer { h.stop() }
        let s = TerminalSession(host: h, session: "s-dotfiles", block: Demo.shell)
        let sink = Sink()
        s.output = sink
        s.start()
        XCTAssertEqual(s.state, .live)
        XCTAssertTrue(sink.text.contains("demo@demo-mac"))

        s.type(Data("help\r".utf8))
        XCTAssertTrue(sink.text.contains("Rex Go Demo Mode"))
        s.type(Data("ls\r".utf8))
        XCTAssertTrue(sink.text.contains("README.md"))
        s.type(Data("git status\r".utf8))
        XCTAssertTrue(sink.text.contains("On branch main"))
        // A mistyped command, backspaced and finished, runs as typed.
        s.type(Data("whoamj\u{7f}i\r".utf8))
        XCTAssertTrue(sink.text.contains("demo\r\n"))
        s.type(Data("cd ios\r".utf8))
        s.type(Data("pwd\r".utf8))
        XCTAssertTrue(sink.text.contains("/Users/demo/ios"))
        s.close()
    }

    /// Answering the demo's question settles the record that waits: the
    /// orange dot and the inbox's row go.
    @MainActor func testDemoAnswerSettlesTheRecord() {
        let h = Host(Demo.config())
        h.connect()
        defer { h.stop() }
        XCTAssertEqual(h.status([Demo.claude])?.state, "blocked")
        let s = TerminalSession(host: h, session: "s-work", block: Demo.claude)
        let sink = Sink()
        s.output = sink
        s.start()
        XCTAssertTrue(sink.text.contains("Apply the patch?"))
        s.type(Data("y\r".utf8))
        XCTAssertNil(h.status([Demo.claude]))
        XCTAssertTrue(sink.text.contains("Applied the patch"))
        s.close()
    }

    /// The demo host is never saved: it answers nothing on the next
    /// launch, and the real hosts around it are.
    @MainActor func testDemoStaysOutOfSavedHosts() {
        let saved = UserDefaults.standard.data(forKey: "hosts")
        defer {
            if let saved { UserDefaults.standard.set(saved, forKey: "hosts") }
            else { UserDefaults.standard.removeObject(forKey: "hosts") }
        }
        UserDefaults.standard.removeObject(forKey: "hosts")

        let store = HostStore()
        store.enterDemo()
        XCTAssertEqual(store.hosts.count, 1)
        XCTAssertNotNil(store.demo)
        let real = HostConfig(name: "real", url: URL(string: "http://127.0.0.1:1")!)
        store.add(real)
        defer { store.hosts.forEach { $0.stop() } }
        XCTAssertEqual(store.hosts.count, 2)

        let data = UserDefaults.standard.data(forKey: "hosts")
        let configs = data.flatMap { try? JSONDecoder().decode([HostConfig].self, from: $0) } ?? []
        XCTAssertEqual(configs.map(\.id), [real.id])

        // The demo host reorders nothing of the real list.
        store.move(from: IndexSet(integer: 0), to: 1)
        XCTAssertEqual(store.hosts.filter { !$0.isDemo }.map(\.id), [real.id])

        store.exitDemo()
        XCTAssertNil(store.demo)
        XCTAssertEqual(store.hosts.map(\.id), [real.id])
    }
}

/// What a demo terminal writes, kept for the test to read.
@MainActor
private final class Sink: TerminalOutput {
    private var data = Data()

    var text: String { String(decoding: data, as: UTF8.self) }

    func write(_ bytes: Data) { data.append(bytes) }
    func setTheme(_ theme: [String: Any]) {}
}
