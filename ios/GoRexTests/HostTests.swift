import Observation
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
        await until("working") { h.blocks["b"]?.statuses?.first?.state == "working" }
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
        await until("progress") { h.blocks["b"]?.statuses?.first?.progress == 60 }
        // Answered, then blocked again: told again.
        records(["id": "claude", "state": "working", "app": "claude"])
        await until("working again") { h.status(["b"])?.state == "working" }
        records(["id": "claude", "state": "blocked", "app": "claude", "title": "Edit?"])
        await until("blocked again") { told.count == 2 }
        XCTAssertEqual(told[1].2.title, "Edit?")

        send("program_status_removed", ["ids": ["claude"], "reason": "removed"])
        await until("removed") { h.blocks["b"]?.statuses?.isEmpty == true }
        XCTAssertNil(h.status(["b"]))
        XCTAssertEqual(told.count, 2)

        // Done shows until the user sees it.
        records(["id": "claude", "state": "done", "app": "claude"])
        await until("done") { h.status(["b"])?.state == "done" }
        h.see(session: "s", block: "b")
        XCTAssertNil(h.status(["b"]))
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

    /// A block's title change tells only what read that block's title:
    /// not what read another block's, nor the host's lists; a title told
    /// again unchanged tells no one.
    @MainActor func testTitleChangeTellsItsBlockOnly() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        rex.respond = { m, _ in m == "session.bootstrap" ? ["attach_all": ["sessions": []]] : [:] }
        let h = Host(HostConfig(name: "test", url: try await rex.url()))
        h.connect()
        defer { h.stop() }
        await until("online") { h.status == .online }
        func title(_ block: String, _ t: String) {
            rex.event(["type": "session_event", "payload": ["session_id": "s", "event": [
                "type": "block_event", "payload": ["block_id": block, "name": "title_changed", "payload": ["title": t]],
            ]]])
        }
        title("a", "A1")
        title("b", "B1")
        await until("titles") { h.blocks["a"]?.title == "A1" && h.blocks["b"]?.title == "B1" }

        let fired = Fired()
        func watch(_ name: String, _ read: () -> Void) {
            withObservationTracking(read) { fired.names.insert(name) }
        }
        watch("a") { _ = h.blocks["a"]?.title }
        watch("b") { _ = h.blocks["b"]?.title }
        watch("host") { _ = (h.status, h.sessions, h.blocks) }
        title("a", "A2")
        await until("changed") { h.blocks["a"]?.title == "A2" }
        XCTAssertEqual(fired.names, ["a"])

        // The same title again, then b's, which comes after it.
        watch("a again") { _ = h.blocks["a"]?.title }
        title("a", "A2")
        title("b", "B2")
        await until("b changed") { h.blocks["b"]?.title == "B2" }
        XCTAssertEqual(fired.names, ["a", "b"])
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
        await until("read") { h.blocks["b"]?.title == "First" && h.blocks["c"]?.title == "First" && h.blocks["b"]?.program == "zsh" }
        let asked = rex.count("com.superlogical.terminal.title")

        // The app goes to the background and back: the connection dies,
        // and the host connects anew.
        FakeRex.close(rex.controls[0], .protocolCode(.goingAway))
        await until("offline") { h.control == nil }
        title = "Second"
        serveC = false
        h.reconnectIfNeeded()
        await until("online again") { h.status == .online }
        await until("read again") { h.blocks["b"]?.title == "Second" }
        await until("asked again") { rex.count("com.superlogical.terminal.title") > asked }
        // What the first read brought still shows for the block whose
        // second read hasn't answered.
        XCTAssertEqual(h.blocks["c"]?.title, "First")
        XCTAssertEqual(h.blocks["b"]?.program, "zsh")
    }

    /// Blocks no window holds are forgotten: when a window closes, and when
    /// a session is destroyed. Nothing is forgotten while a session has no
    /// view, as after a reconnect, and a block's own changes still tell
    /// only its readers.
    @MainActor func testBlocksForgottenWhenGone() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        func view(_ session: String, _ revision: Int, _ windows: [[String]]) -> [String: Any] {
            ["revision": revision, "session_id": session, "label": session, "windows": windows.enumerated().map { i, bs in
                ["window_id": "\(session)w\(i)", "layers": [["blocks": bs.map { ["block_id": $0] }]]]
            }]
        }
        var s = view("s", 1, [["a", "b"], ["c"]])
        var attachT = false
        rex.respond = { m, p in
            switch m {
            case "session.bootstrap":
                ["attach_all": ["sessions": [["session_id": "s", "label": "s"], ["session_id": "t", "label": "t"]]], "selected_session": ["view": s]]
            case "session.attach":
                attachT ? ["view": view("t", 1, [["d"]])] : nil
            case "session.view":
                s
            case "com.superlogical.terminal.title":
                ["title": p["block_id"] as? String ?? ""]
            default: [:]
            }
        }
        let h = Host(HostConfig(name: "test", url: try await rex.url()))
        h.connect()
        defer { h.stop() }
        await until("s read") { ["a", "b", "c"].allSatisfy { h.blocks[$0]?.title == $0 } }

        // Session t has no view yet: its block, known by an event, stays
        // through s's view and through a reconnect.
        rex.event(["type": "session_event", "payload": ["session_id": "t", "event": [
            "type": "block_event", "payload": ["block_id": "d", "name": "title_changed", "payload": ["title": "D"]],
        ]]])
        await until("d") { h.blocks["d"]?.title == "D" }
        FakeRex.close(rex.controls[0], .protocolCode(.goingAway))
        await until("offline") { h.control == nil }
        h.reconnectIfNeeded()
        await until("online again") { h.status == .online }
        await until("asked") { rex.count("session.attach") > 0 }
        XCTAssertEqual(Set(h.blocks.keys), ["a", "b", "c", "d"])
        XCTAssertEqual(h.blocks["d"]?.title, "D")

        // Both views in: the window that closed takes its blocks.
        attachT = true
        h.reconnect()
        await until("both views") { h.sessions.allSatisfy { $0.view != nil } }
        let fired = Fired()
        let a = try XCTUnwrap(h.blocks["a"])
        withObservationTracking { _ = a.title } onChange: { fired.names.insert("a") }
        s = view("s", 2, [["a", "b"]])
        rex.event(["type": "session_event", "payload": ["session_id": "s", "event": [
            "type": "session_view_changed", "payload": ["revision": 2],
        ]]])
        await until("c gone") { h.blocks["c"] == nil }
        XCTAssertEqual(Set(h.blocks.keys), ["a", "b", "d"])
        XCTAssertTrue(fired.names.isEmpty)

        // A destroyed session takes its blocks.
        rex.event(["type": "session_event", "payload": ["session_id": "t", "event": ["type": "session_destroyed", "payload": ["session_id": "t"]]]])
        await until("d gone") { h.blocks["d"] == nil }
        XCTAssertEqual(Set(h.blocks.keys), ["a", "b"])
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
        XCTAssertNil(again.tailcat)
    }

    /// A tailcat host keeps its address and machine through a save; one
    /// saved before tailcat has none.
    @MainActor func testTailcatHostDecodes() throws {
        let saved = #"[{"id":"8C1F2D3E-4A5B-4C6D-8E7F-9A0B1C2D3E4F","name":"mac","url":"http:\/\/8c1f2d3e-4a5b-4c6d-8e7f-9a0b1c2d3e4f.tailcat.invalid:7297","desktop":false,"tailcat":{"address":"tcx","machine":"mac"}}]"#
        let hosts = try JSONDecoder().decode([HostConfig].self, from: Data(saved.utf8))
        XCTAssertEqual(hosts[0].tailcat, HostConfig.Tailcat(address: "tcx", machine: "mac"))
        XCTAssertEqual(try JSONDecoder().decode([HostConfig].self, from: JSONEncoder().encode(hosts)), hosts)
        // A tailcat entry that doesn't read fails the host, as any other
        // field would; the list is only ever written by the app.
        XCTAssertThrowsError(try JSONDecoder().decode(HostConfig.self, from: Data(#"{"id":"8C1F2D3E-4A5B-4C6D-8E7F-9A0B1C2D3E4F","name":"mac","url":"http:\/\/mac","tailcat":{"address":"tcx"}}"#.utf8)))
    }

    /// A host saved before icons keeps its laptop or desktop; one saved
    /// with an icon keeps it.
    @MainActor func testHostIcon() throws {
        func decode(_ s: String) throws -> HostConfig {
            try JSONDecoder().decode(HostConfig.self, from: Data(#"{"id":"8C1F2D3E-4A5B-4C6D-8E7F-9A0B1C2D3E4F","name":"mac","url":"http:\/\/mac:7297",\#(s)}"#.utf8))
        }
        XCTAssertEqual(try decode(#""desktop":false"#).symbol, "laptopcomputer")
        XCTAssertEqual(try decode(#""desktop":true"#).symbol, "desktopcomputer")
        var h = try decode(#""desktop":false"#)
        h.icon = "macmini"
        XCTAssertEqual(try JSONDecoder().decode(HostConfig.self, from: JSONEncoder().encode(h)).symbol, "macmini")
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

        // A host with a tailcat address goes over it, built-in Tailscale
        // on or off, whatever its transport says.
        var t = try XCTUnwrap(HostConfig(tailcat: "rexgo://tailcat?a=tcx&n=mac"))
        XCTAssertEqual(t.reach(tailnetOn: false), .tailcat)
        XCTAssertEqual(t.reach(tailnetOn: true), .tailcat)
        t.transport = .tailnet
        XCTAssertEqual(t.reach(tailnetOn: true), .tailcat)
    }

    /// An edited host keeps its transport while its address stays; a new
    /// address takes the rule's, and the switch wins over both.
    @MainActor func testEnteredTransport() throws {
        var old = try XCTUnwrap(HostConfig(pairing: "http://192.168.1.2:7297"))
        old.transport = .system
        let same = old.url, moved = URL(string: "http://100.64.0.9:7297")!
        XCTAssertEqual(HostConfig.transport(switched: nil, was: old, at: same, rule: .tailnet), .system)
        XCTAssertEqual(HostConfig.transport(switched: nil, was: old, at: moved, rule: .tailnet), .tailnet)
        XCTAssertNil(HostConfig.transport(switched: nil, was: nil, at: same, rule: nil))
        XCTAssertEqual(HostConfig.transport(switched: true, was: old, at: same, rule: nil), .tailnet)
        XCTAssertEqual(HostConfig.transport(switched: false, was: nil, at: moved, rule: .tailnet), .system)
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
        XCTAssertEqual(TailnetSettings.name, "")
        XCTAssertNil(d.string(forKey: TailnetSettings.nameKey))
        TailnetSettings.makeName()
        let n = TailnetSettings.name
        XCTAssertNotNil(n.wholeMatch(of: /rexgo-[a-z]{4}/))
        TailnetSettings.makeName()
        XCTAssertEqual(TailnetSettings.name, n)
    }

    /// A resumed host connects anew though its connection looks open,
    /// continuing its client; a reconnect starts a new one.
    @MainActor func testResumeKeepsTheClient() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        rex.respond = { m, _ in m == "session.bootstrap" ? ["attach_all": ["sessions": []]] : [:] }
        let h = Host(HostConfig(name: "test", url: try await rex.url()))
        h.connect()
        defer { h.stop() }
        await until("online") { h.status == .online }
        h.resume()
        await until("resumed") { rex.controls.count == 2 && h.status == .online }
        h.reconnect()
        await until("reconnected") { rex.controls.count == 3 && h.status == .online }
        XCTAssertEqual(rex.tokens, [nil, "t1", nil])
    }

    /// A terminal opened as its host connects, as from a notification on a
    /// cold start, asks for its stream once the host is bootstrapped, even
    /// when that takes a reconnect.
    @MainActor func testTerminalWaitsForOnline() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        var answer = false
        rex.respond = { m, _ in m == "session.bootstrap" ? (answer ? ["attach_all": ["sessions": []]] : nil) : [:] }
        let h = Host(HostConfig(name: "test", url: try await rex.url()))
        h.connect()
        defer { h.stop() }
        let s = TerminalSession(host: h, session: "s", block: "b")
        s.start()
        await until("bootstrapping") { rex.count("session.bootstrap") == 1 }
        try await Task.sleep(for: .milliseconds(300))
        XCTAssertEqual(rex.count("data.connect"), 0)
        answer = true
        FakeRex.close(rex.controls[0], .protocolCode(.goingAway))
        await until("asked for the stream") { rex.count("data.connect") == 1 }
        XCTAssertEqual(h.status, .online)
        XCTAssertLessThan(try XCTUnwrap(rex.calls.lastIndex(of: "session.bootstrap")), try XCTUnwrap(rex.calls.firstIndex(of: "data.connect")))
        s.close()
    }

    /// What waits for the host to be online fails when the host is removed.
    @MainActor func testOnlineFailsWhenStopped() async {
        let h = Host(HostConfig(name: "test", url: URL(string: "http://127.0.0.1:1")!))
        h.connect()
        let waiting = Task { try await h.online() }
        try? await Task.sleep(for: .milliseconds(100))
        h.stop()
        do {
            _ = try await waiting.value
            XCTFail("online")
        } catch {
            XCTAssertTrue(error is CancellationError)
        }
        do {
            _ = try await h.online()
            XCTFail("online after stop")
        } catch {
            XCTAssertTrue(error is CancellationError)
        }
    }

    /// What waits for the host to be online fails when the server refuses
    /// the device, as the host then waits for a change.
    @MainActor func testOnlineFailsWhenRefused() async throws {
        let http = try FakeHTTP()
        defer { http.stop() }
        http.respond = { _ in (403, ["error": "refused"]) }
        let h = Host(HostConfig(name: "test", url: try await http.url()))
        defer { h.stop() }
        let waiting = Task { try await h.online() }
        h.connect()
        do {
            _ = try await waiting.value
            XCTFail("online")
        } catch {
            XCTAssertEqual((error as? RexError)?.code, "refused")
        }
    }

    /// A host that can't be reached tries again sooner first, then every
    /// ten seconds, as long as it takes, and shows offline meanwhile.
    @MainActor func testRetryBacksOff() async throws {
        XCTAssertEqual(Host.backoff(after: 1), .milliseconds(500))
        XCTAssertEqual(Host.backoff(after: 2), .seconds(1))
        XCTAssertEqual(Host.backoff(after: 6), .seconds(10))
        XCTAssertEqual(Host.backoff(after: 100), .seconds(10))
        let http = try FakeHTTP()
        defer { http.stop() }
        http.respond = { _ in (500, [:]) }
        let h = Host(HostConfig(name: "test", url: try await http.url()))
        h.connect()
        defer { h.stop() }
        await until("offline") { if case .offline = h.status { true } else { false } }
        let end = ContinuousClock.now + .seconds(2.2)
        while .now < end {
            XCTAssertNotEqual(h.status, .connecting)
            try await Task.sleep(for: .milliseconds(5))
        }
        // At once, then after 0.5 s and 1 s; the next waits 2 s.
        XCTAssertGreaterThanOrEqual(http.requests.count, 3)
        XCTAssertLessThanOrEqual(http.requests.count, 4)
    }

    /// The engine hears of the hosts as their list changes, as it loads
    /// and as one is added or removed; of one added, before it connects.
    @MainActor func testTailcatHostsTold() throws {
        let d = UserDefaults.standard
        let (hosts, hook, proxy) = (d.data(forKey: "hosts"), Host.tailcatHosts, Host.tailcatProxy)
        defer {
            d.set(hosts, forKey: "hosts")
            Host.tailcatHosts = hook
            Host.tailcatProxy = proxy
        }
        d.removeObject(forKey: "hosts")
        var told: [[HostConfig]] = []
        var order: [String] = []
        Host.tailcatHosts = { told.append($0); order.append("told") }
        Host.tailcatProxy = { order.append("connect"); return nil }
        let store = HostStore()
        XCTAssertEqual(told, [[]])
        let t = try XCTUnwrap(HostConfig(tailcat: "rexgo://tailcat?a=tcx&n=mac"))
        order = []
        store.add(t)
        XCTAssertEqual(order, ["told", "connect"])
        XCTAssertEqual(told.last?.compactMap(\.tailcat), [HostConfig.Tailcat(address: "tcx", machine: "mac")])
        XCTAssertEqual(store.host(tailcat: "tcx")?.id, t.id)
        store.remove(t.id)
        XCTAssertEqual(told.last, [])
        XCTAssertNil(store.host(tailcat: "tcx"))
    }

    /// Away more than 5 s, the app was likely suspended.
    @MainActor func testSuspended() {
        let now = Date()
        XCTAssertFalse(HostStore.suspended(since: nil, now: now))
        XCTAssertFalse(HostStore.suspended(since: now.addingTimeInterval(-5), now: now))
        XCTAssertTrue(HostStore.suspended(since: now.addingTimeInterval(-6), now: now))
    }

    /// Back after a suspension, the hosts on the built-in Tailscale connect
    /// anew once it resumes on its proxy, and not when it started anew; the
    /// tailcat hosts connect anew either way; the others only reconnect if
    /// needed. Back sooner, nothing resumes.
    @MainActor func testForeground() async throws {
        let d = UserDefaults.standard
        let (hosts, on, proxy, catProxy) = (d.data(forKey: "hosts"), d.object(forKey: TailnetSettings.onKey), Host.tailnetProxy, Host.tailcatProxy)
        defer {
            d.set(hosts, forKey: "hosts")
            d.set(on, forKey: TailnetSettings.onKey)
            Host.tailnetProxy = proxy
            Host.tailcatProxy = catProxy
        }
        d.removeObject(forKey: "hosts")
        d.set(true, forKey: TailnetSettings.onKey)
        // Each connection the tailnet host tries asks for the proxy; only
        // those of foreground count, not its retries.
        var order: [String] = []
        var counting = false
        Host.tailnetProxy = {
            if counting { order.append("tailnet") }
            return nil
        }
        Host.tailcatProxy = {
            if counting { order.append("tailcat") }
            return nil
        }
        let rex = try FakeRex()
        defer { rex.stop() }
        rex.respond = { m, _ in m == "session.bootstrap" ? ["attach_all": ["sessions": []]] : [:] }
        let store = HostStore()
        let system = HostConfig(name: "system", url: try await rex.url())
        let tailnet = HostConfig(name: "tailnet", url: try await rex.url(), transport: .tailnet)
        let tailcat = try XCTUnwrap(HostConfig(tailcat: "rexgo://tailcat?a=tcx&n=mac"))
        store.add(system)
        store.add(tailnet)
        store.add(tailcat)
        defer { store.remove(system.id); store.remove(tailnet.id); store.remove(tailcat.id) }
        await until("online") { store.host(system.id)?.status == .online }

        func foreground(suspended: Bool, kept: Bool) async -> [String] {
            order = []
            counting = true
            await store.foreground(suspended: suspended) { order.append("resume"); return kept }
            counting = false
            return order
        }
        let shortly = await foreground(suspended: false, kept: true)
        XCTAssertEqual(shortly, ["tailnet", "tailcat"])
        let resumed = await foreground(suspended: true, kept: true)
        XCTAssertEqual(resumed, ["resume", "tailnet", "tailcat"])
        let restarted = await foreground(suspended: true, kept: false)
        XCTAssertEqual(restarted, ["resume", "tailcat"])
        XCTAssertEqual(rex.controls.count, 1, "the system host connected anew")
    }

    /// A title's symbols show as text: iOS draws "✳" as an emoji without U+FE0E.
    func testTitleSymbolsAsText() {
        XCTAssertEqual("✳ sioux/jnj".asText, "✳\u{FE0E} sioux/jnj")
        XCTAssertEqual("✳\u{FE0F} ok".asText, "✳\u{FE0F} ok", "an emoji chosen stays")
        XCTAssertEqual("#1 * 😀 中文".asText, "#1 * 😀 中文", "ASCII and emoji stay")
    }
}

/// The names of the observations that fired, as they fire on the thread
/// that changes what they read.
nonisolated private final class Fired: @unchecked Sendable {
    var names: Set<String> = []
}
