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
            ("rename session", { try await h.renameSession("s", to: "x") }),
            ("close session", { try await h.closeSession("s") }),
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

    /// A session's menu renames and closes the session by its id.
    @MainActor func testSessionMenuCalls() async throws {
        let saved = UserDefaults.standard.data(forKey: "hosts")
        defer { UserDefaults.standard.set(saved, forKey: "hosts") }
        UserDefaults.standard.removeObject(forKey: "hosts")
        let r = try FakeRex()
        defer { r.stop() }
        r.respond = { m, _ in m == "session.bootstrap" ? ["attach_all": ["sessions": []]] : [:] }
        let store = HostStore()
        let config = HostConfig(name: "test", url: try await r.url())
        store.add(config)
        let h = try XCTUnwrap(store.host(config.id))
        await until("online") { h.status == .online }
        try await h.renameSession("s1", to: "work")
        try await h.closeSession("s1")
        let i = try XCTUnwrap(r.calls.firstIndex(of: "session.set_label"))
        XCTAssertEqual(r.payloads[i]["session_id"] as? String, "s1")
        XCTAssertEqual(r.payloads[i]["label"] as? String, "work")
        let j = try XCTUnwrap(r.calls.firstIndex(of: "session.destroy"))
        XCTAssertEqual(r.payloads[j]["session_id"] as? String, "s1")
        store.remove(config.id)
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

    /// A record reads as program_status_changed gives it: the effective
    /// app first, empty text as none.
    @MainActor func testProgramStatusParses() throws {
        let r = try XCTUnwrap(Host.ProgramStatus(json: [
            "id": "claude", "state": "blocked", "kind": "question", "progress": 40,
            "app": "", "effective_app": "claude", "title": "", "msg": "Allow?", "last_msg": "", "updated_at": "",
        ]))
        XCTAssertEqual(r.id, "claude")
        XCTAssertEqual(r.state, "blocked")
        XCTAssertEqual(r.kind, "question")
        XCTAssertEqual(r.progress, 40)
        XCTAssertEqual(r.app, "claude")
        XCTAssertNil(r.title)
        XCTAssertEqual(r.msg, "Allow?")
        XCTAssertNil(r.lastMsg)
        XCTAssertEqual(Host.ProgramStatus(json: ["state": "done", "last_msg": "Testing"])?.lastMsg, "Testing")
        XCTAssertNil(Host.ProgramStatus(json: ["id": "x"]))
    }

    /// A host keeps each block's records from the events, tells once when
    /// a program turns blocked, shows the one that most needs the user,
    /// and drops what is removed.
    @MainActor func testProgramStatusEvents() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        rex.respond = { m, _ in m == "session.bootstrap" ? ["attach_all": ["sessions": []]] : [:] }
        var told: [(String, String, Host.ProgramStatus)] = []
        Host.blocked = { _, s, b, r in told.append((s, b, r)) }
        defer { Host.blocked = nil }
        let h = Host(HostConfig(name: "test", url: try await rex.url()))
        h.connect()
        defer { h.stop() }
        await until("online") { h.status == .online }
        func send(_ name: String, _ payload: [String: Any], block: String = "b") {
            rex.event(["type": "session_event", "payload": ["session_id": "s", "event": [
                "type": "block_event", "payload": ["block_id": block, "name": name, "payload": payload],
            ]]])
        }
        func records(_ rs: [String: Any]...) { send("program_status_changed", ["records": rs]) }

        records(["id": "omp", "state": "working", "progress": 40])
        await until("working") { h.statuses["b"]?.first?.state == "working" }
        XCTAssertEqual(h.status(["b"])?.progress, 40)
        XCTAssertTrue(told.isEmpty)

        records(["id": "omp", "state": "working", "progress": 50],
                ["id": "claude", "state": "blocked", "kind": "question", "app": "claude", "msg": "Allow?"])
        await until("blocked told") { told.count == 1 }
        XCTAssertEqual(told[0].0, "s")
        XCTAssertEqual(told[0].1, "b")
        XCTAssertEqual(told[0].2.app, "claude")
        XCTAssertEqual(told[0].2.msg, "Allow?")
        XCTAssertEqual(h.status(["a", "b"])?.state, "blocked")
        XCTAssertNil(h.status(["a"]))

        // Still blocked, as another record changes: told once.
        records(["id": "omp", "state": "working", "progress": 60],
                ["id": "claude", "state": "blocked", "kind": "question", "app": "claude", "msg": "Allow?"])
        await until("progress") { h.statuses["b"]?.first?.progress == 60 }
        // Answered, then blocked again: told again.
        records(["id": "claude", "state": "working", "app": "claude"])
        await until("working again") { h.status(["b"])?.state == "working" }
        records(["id": "claude", "state": "blocked", "app": "claude", "title": "Edit?"])
        await until("blocked again") { told.count == 2 }
        XCTAssertEqual(told[1].2.title, "Edit?")

        send("program_status_removed", ["ids": ["claude"], "reason": "removed"])
        await until("removed") { h.statuses["b"]?.isEmpty == true }
        XCTAssertNil(h.status(["b"]))
        XCTAssertEqual(told.count, 2)
    }

    /// A window's records are read as it shows, without telling: the
    /// program blocked before the app looked.
    @MainActor func testProgramStatusRead() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        let view: [String: Any] = ["revision": 1, "session_id": "s", "label": "s", "windows": [
            ["window_id": "w", "layers": [["blocks": [["block_id": "b"]]]]],
        ]]
        rex.respond = { m, _ in
            switch m {
            case "session.bootstrap":
                ["attach_all": ["sessions": [["session_id": "s", "label": "s"]]], "selected_session": ["view": view]]
            case "com.superlogical.terminal.program_status":
                ["records": [["id": "claude", "state": "blocked", "app": "claude"]]]
            default: [:]
            }
        }
        var told = 0
        Host.blocked = { _, _, _, _ in told += 1 }
        defer { Host.blocked = nil }
        let h = Host(HostConfig(name: "test", url: try await rex.url()))
        h.connect()
        defer { h.stop() }
        await until("read") { h.status(["b"])?.state == "blocked" }
        XCTAssertEqual(told, 0)
    }

    /// A reconnect, as after the background, keeps the titles and programs
    /// it had, and reads them again in place: nothing blanks while the
    /// reads run, and what changed while away arrives.
    @MainActor func testReconnectKeepsTitles() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        let view: [String: Any] = ["revision": 1, "session_id": "s", "label": "s", "windows": [
            ["window_id": "w", "layers": [["blocks": [["block_id": "b"], ["block_id": "c"]]]]],
        ]]
        // Block b's title changes while away; block c's second read
        // answers without a title, so what the reconnect kept is what the
        // test still sees.
        var title = "First"
        var serveC = true
        rex.respond = { m, p in
            switch m {
            case "session.bootstrap":
                ["attach_all": ["sessions": [["session_id": "s", "label": "s"]]], "selected_session": ["view": view]]
            case "com.superlogical.terminal.title":
                switch p["block_id"] as? String {
                case "b": ["title": title]
                case "c": serveC ? ["title": title] : [:]
                default: [:]
                }
            case "com.superlogical.terminal.process":
                ["foreground": ["name": "zsh"]]
            default: [:]
            }
        }
        let h = Host(HostConfig(name: "test", url: try await rex.url()))
        h.connect()
        defer { h.stop() }
        await until("read") { h.titles["b"] == "First" && h.titles["c"] == "First" && h.programs["b"] == "zsh" }
        let asked = rex.count("com.superlogical.terminal.title")

        // The app goes to the background and back: the connection dies,
        // and the host connects anew.
        FakeRex.close(rex.controls[0], .protocolCode(.goingAway))
        await until("offline") { h.control == nil }
        title = "Second"
        serveC = false
        h.reconnectIfNeeded()
        await until("online again") { h.status == .online }
        await until("read again") { h.titles["b"] == "Second" }
        await until("asked again") { rex.count("com.superlogical.terminal.title") > asked }
        // What the first read brought still shows for the block whose
        // second read hasn't answered.
        XCTAssertEqual(h.titles["c"], "First")
        XCTAssertEqual(h.programs["b"], "zsh")
    }

    /// A row moved onto another lands where it is: before a row above,
    /// after a row below, past the windows the list leaves out.
    @MainActor func testMoveBefore() {
        // b and d are left out of the list.
        let all = ["a", "b", "c", "d", "e"], shown = ["a", "c", "e"]
        XCTAssertEqual(Host.before("e", onto: "a", shown: shown, all: all), "a")
        XCTAssertEqual(Host.before("c", onto: "a", shown: shown, all: all), "a")
        XCTAssertEqual(Host.before("a", onto: "c", shown: shown, all: all), "d")
        XCTAssertNil(Host.before("a", onto: "e", shown: shown, all: all))
    }

    /// A host list saved before hosts had a transport still decodes, each
    /// host through the system; one saved with it keeps it.
    @MainActor func testHostsSavedWithoutTransportDecode() throws {
        let old = #"[{"id":"8C1F2D3E-4A5B-4C6D-8E7F-9A0B1C2D3E4F","name":"mac","url":"https:\/\/mac-gorex.example.ts.net","desktop":false}]"#
        let hosts = try JSONDecoder().decode([HostConfig].self, from: Data(old.utf8))
        XCTAssertEqual(hosts.map(\.name), ["mac"])
        XCTAssertNil(hosts[0].transport)
        var h = hosts[0]
        h.transport = .tailnet
        let again = try JSONDecoder().decode(HostConfig.self, from: JSONEncoder().encode(h))
        XCTAssertEqual(again.transport, .tailnet)
    }

    /// A host added with the built-in Tailscale on goes through it when
    /// its name is under the tailnet's suffix or its address is
    /// Tailscale's; with it off, every host goes the system's way.
    @MainActor func testTransportRule() throws {
        func t(_ s: String, on: Bool = true) -> HostConfig.Transport? {
            HostConfig.transport(of: URL(string: s)!, tailnetOn: on, suffix: "example.ts.net")
        }
        XCTAssertEqual(t("https://mac-gorex.example.ts.net"), .tailnet)
        XCTAssertEqual(t("https://MAC-gorex.Example.ts.net."), .tailnet)
        XCTAssertNil(t("https://mac-gorex.other.ts.net"))
        XCTAssertNil(t("https://example.ts.net"))
        XCTAssertNil(t("https://notexample.ts.net"))
        XCTAssertEqual(t("http://100.64.0.1:7297"), .tailnet)
        XCTAssertEqual(t("http://100.127.255.254:7297"), .tailnet)
        XCTAssertNil(t("http://100.128.0.1:7297"))
        XCTAssertNil(t("http://100.63.255.255:7297"))
        XCTAssertNil(t("http://192.168.1.2:7297"))
        XCTAssertEqual(t("http://[fd7a:115c:a1e0::1]:7297"), .tailnet)
        XCTAssertEqual(t("http://[fd7a:115c:a1e0:ab12::42]"), .tailnet)
        XCTAssertNil(t("http://[fd7a:115c:a1e1::1]:7297"))
        XCTAssertNil(t("http://[fe80::1]:7297"))
        XCTAssertNil(t("http://studio.local:7297"))
        XCTAssertNil(HostConfig.transport(of: URL(string: "https://mac-gorex.example.ts.net")!, tailnetOn: true, suffix: nil))
        XCTAssertNil(t("https://mac-gorex.example.ts.net", on: false))
        XCTAssertNil(t("http://100.64.0.1:7297", on: false))

        // A host on the tailnet goes the system's way while it is off.
        var h = try XCTUnwrap(HostConfig(pairing: "https://mac-gorex.example.ts.net"))
        XCTAssertEqual(h.reach(tailnetOn: true), .system)
        h.transport = .tailnet
        XCTAssertEqual(h.reach(tailnetOn: true), .tailnet)
        XCTAssertEqual(h.reach(tailnetOn: false), .system)
    }

    /// The device's default name is rexgo- and four lowercase letters,
    /// made once and kept.
    @MainActor func testDeviceName() {
        for _ in 0..<50 {
            XCTAssertNotNil(TailnetSettings.defaultName().wholeMatch(of: /rexgo-[a-z]{4}/))
        }
        let d = UserDefaults.standard, saved = d.string(forKey: TailnetSettings.nameKey)
        defer { d.set(saved, forKey: TailnetSettings.nameKey) }
        d.removeObject(forKey: TailnetSettings.nameKey)
        let n = TailnetSettings.name
        XCTAssertNotNil(n.wholeMatch(of: /rexgo-[a-z]{4}/))
        XCTAssertEqual(TailnetSettings.name, n)
        XCTAssertEqual(d.string(forKey: TailnetSettings.nameKey), n)
    }
}
