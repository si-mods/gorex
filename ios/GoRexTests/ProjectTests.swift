import XCTest

/// Projects: a space's home in the manifest, its merge and sync, and where
/// a project's new window opens (docs/projects.md, Rex Go).
nonisolated final class ProjectTests: XCTestCase {
    @MainActor private func tempURL() -> URL {
        FileManager.default.temporaryDirectory.appendingPathComponent("projects-\(UUID().uuidString).json")
    }

    @MainActor private func view(_ session: String, _ blocks: [String]) -> SessionView {
        SessionView(revision: 1, session_id: session, windows: [
            .init(window_id: "w-" + session, focused_block_id: blocks.first, layers: [.init(blocks: blocks.map { .init(block_id: $0) })]),
        ])
    }

    // MARK: Manifest

    /// A manifest of an app of before projects reads with no home known.
    @MainActor func testManifestWithoutHomeDecodes() throws {
        let json = #"{"version":1,"revision":3,"tombstones":[],"spaces":[{"id":"sp_a","name":"A","order":0,"updated_at":100,"members":[]}]}"#
        let m = try JSONDecoder().decode(SpaceManifest.self, from: Data(json.utf8))
        XCTAssertNil(m.spaces[0].home_machine)
        XCTAssertNil(m.spaces[0].dir)
        XCTAssertNil(m.spaces[0].folder)
    }

    /// The home decodes; an unknown type reads as nil and fails nothing.
    @MainActor func testManifestHomeDecodesLeniently() throws {
        let url = tempURL()
        defer { try? FileManager.default.removeItem(at: url) }
        let json = #"""
        {"version":1,"revision":1,"tombstones":[],"spaces":[
          {"id":"sp_a","name":"A","order":0,"updated_at":100,"members":[],"home_machine":"m-office","dir":"/home/demo/code/windflow"},
          {"id":"sp_b","name":"B","order":1,"updated_at":100,"members":[],"home_machine":7,"dir":{"path":"/x"}},
          {"id":"sp_c","name":"C","order":2,"updated_at":100,"members":[],"home_machine":"","dir":""}
        ]}
        """#
        try Data(json.utf8).write(to: url)
        let store = SpaceStore(fileURL: url)
        let spaces = store.orderedSpaces
        XCTAssertEqual(spaces.map(\.id), ["sp_a", "sp_b", "sp_c"])
        XCTAssertEqual(spaces[0].home_machine, "m-office")
        XCTAssertEqual(spaces[0].folder, "/home/demo/code/windflow")
        XCTAssertNil(spaces[1].home_machine)
        XCTAssertNil(spaces[1].dir)
        XCTAssertEqual(spaces[2].dir, "")
        XCTAssertNil(spaces[2].folder)
    }

    /// The home round-trips; a space with none known writes no keys, as an
    /// older app's manifest has none.
    @MainActor func testManifestHomeRoundTrips() throws {
        let url = tempURL()
        defer { try? FileManager.default.removeItem(at: url) }
        let store = SpaceStore(fileURL: url)
        let a = store.createSpace(name: "windflow", homeMachine: "m-office", dir: "/home/demo/code/windflow")
        store.createSpace(name: "Loose")
        let reloaded = SpaceStore(fileURL: url)
        XCTAssertEqual(reloaded.space(id: a.id)?.home_machine, "m-office")
        XCTAssertEqual(reloaded.space(id: a.id)?.dir, "/home/demo/code/windflow")
        let text = String(decoding: try Data(contentsOf: url), as: UTF8.self)
        XCTAssertEqual(text.components(separatedBy: "\"dir\"").count - 1, 1)
    }

    /// setHome sets the home and moves updated_at; a dir of "" clears both.
    @MainActor func testSetHome() {
        let url = tempURL()
        defer { try? FileManager.default.removeItem(at: url) }
        let store = SpaceStore(fileURL: url)
        let s = store.createSpace(name: "windflow", now: Date(timeIntervalSince1970: 100))
        store.setHome(s.id, machine: "m-office", dir: "/srv/windflow", now: Date(timeIntervalSince1970: 200))
        XCTAssertEqual(store.space(id: s.id)?.folder, "/srv/windflow")
        XCTAssertEqual(store.space(id: s.id)?.updated_at, 200)
        store.setHome(s.id, machine: "m-office", dir: "", now: Date(timeIntervalSince1970: 300))
        XCTAssertEqual(store.space(id: s.id)?.home_machine, "")
        XCTAssertEqual(store.space(id: s.id)?.dir, "")
    }

    // MARK: Merge

    /// An older app's newer copy drops the home: the local one stays.
    @MainActor func testMergeKeepsLocalHomeDroppedByOlderApp() {
        let local = CentralSpace(id: "sp1", name: "windflow", color: nil, order: 0, updated_at: 100, members: [],
                                 home_machine: "m-office", dir: "/srv/windflow")
        var remote = local
        remote.name = "Windflow"
        remote.updated_at = 200
        remote.home_machine = nil
        remote.dir = nil
        let merged = SpaceStore.merged(local: SpaceManifest(spaces: [local]), remote: SpaceManifest(spaces: [remote]))
        XCTAssertEqual(merged.spaces.first?.name, "Windflow")
        XCTAssertEqual(merged.spaces.first?.home_machine, "m-office")
        XCTAssertEqual(merged.spaces.first?.dir, "/srv/windflow")
    }

    /// A newer copy's home wins, a cleared one ("") too.
    @MainActor func testMergeTakesNewerHome() {
        let local = CentralSpace(id: "sp1", name: "w", color: nil, order: 0, updated_at: 100, members: [],
                                 home_machine: "m-office", dir: "/srv/windflow")
        var moved = local
        moved.updated_at = 200
        moved.home_machine = "m-laptop"
        moved.dir = "/Users/demo/windflow"
        var merged = SpaceStore.merged(local: SpaceManifest(spaces: [local]), remote: SpaceManifest(spaces: [moved]))
        XCTAssertEqual(merged.spaces.first?.home_machine, "m-laptop")
        XCTAssertEqual(merged.spaces.first?.dir, "/Users/demo/windflow")

        var cleared = local
        cleared.updated_at = 200
        cleared.home_machine = ""
        cleared.dir = ""
        merged = SpaceStore.merged(local: SpaceManifest(spaces: [local]), remote: SpaceManifest(spaces: [cleared]))
        XCTAssertEqual(merged.spaces.first?.dir, "")
        XCTAssertNil(merged.spaces.first?.folder)
    }

    /// A home reaches a second device over iCloud.
    @MainActor func testHomeSyncsOverICloud() {
        final class MockKVS: SpaceKeyValueStore {
            var storage: [String: Data] = [:]
            func data(forKey aKey: String) -> Data? { storage[aKey] }
            func set(_ aData: Data?, forKey aKey: String) { storage[aKey] = aData }
            func synchronize() -> Bool { true }
        }
        let urlA = tempURL(), urlB = tempURL()
        defer {
            try? FileManager.default.removeItem(at: urlA)
            try? FileManager.default.removeItem(at: urlB)
        }
        let kvs = MockKVS()
        let a = SpaceStore(fileURL: urlA, kvs: kvs)
        let s = a.createSpace(name: "windflow", homeMachine: "m-office", dir: "/srv/windflow")
        let b = SpaceStore(fileURL: urlB, kvs: kvs)
        XCTAssertEqual(b.space(id: s.id)?.home_machine, "m-office")
        XCTAssertEqual(b.space(id: s.id)?.folder, "/srv/windflow")
    }

    // MARK: Desktop sync

    @MainActor private func host(_ machine: String, project: SessionView.Project?, projects: Bool = true) -> SpaceStore.HostSnapshot {
        .init(machine: machine, online: true, sessions: [
            .init(id: "s1", label: "windflow", view: view("s1", ["b1"]), remote: [], project: project),
        ], projects: projects)
    }

    /// A desktop's home fills a space whose home is not known.
    @MainActor func testSyncAdoptsDesktopHome() {
        let url = tempURL()
        defer { try? FileManager.default.removeItem(at: url) }
        let store = SpaceStore(fileURL: url)
        store.sync(from: [host("m-office", project: .init(home_machine: "m-office", dir: "/srv/windflow"))])
        let s = store.orderedSpaces.first
        XCTAssertEqual(s?.home_machine, "m-office")
        XCTAssertEqual(s?.folder, "/srv/windflow")
    }

    /// A desktop with no folder, or of before projects, leaves an unknown
    /// home unknown, so that the sync does not clear other desktops' homes.
    @MainActor func testSyncWithoutDesktopHomeKeepsUnknown() {
        let url = tempURL()
        defer { try? FileManager.default.removeItem(at: url) }
        let store = SpaceStore(fileURL: url)
        store.sync(from: [host("m-office", project: nil)])
        XCTAssertNil(store.orderedSpaces.first?.dir)
        store.sync(from: [host("m-old", project: .init(home_machine: "m-old", dir: "/x"), projects: false)])
        XCTAssertNil(store.orderedSpaces.first?.dir)
    }

    /// A home set here stays while a desktop has not caught up; a home the
    /// desktop changes afterwards is taken.
    @MainActor func testSyncKeepsLocalHomeUntilDesktopChangesIt() {
        let url = tempURL()
        defer { try? FileManager.default.removeItem(at: url) }
        let store = SpaceStore(fileURL: url)
        let s = store.createSpace(name: "windflow", homeMachine: "m-office", dir: "/srv/new")
        let old = SessionView.Project(home_machine: "m-office", dir: "/srv/old")
        store.sync(from: [host("m-office", project: old)])
        XCTAssertEqual(store.space(id: s.id)?.folder, "/srv/new")
        store.sync(from: [host("m-office", project: old)])
        XCTAssertEqual(store.space(id: s.id)?.folder, "/srv/new")

        // The desktop's Change Folder.
        store.sync(from: [host("m-office", project: .init(home_machine: "m-office", dir: "/srv/other"))])
        XCTAssertEqual(store.space(id: s.id)?.folder, "/srv/other")

        // The desktop's Remove Folder.
        store.sync(from: [host("m-office", project: nil)])
        XCTAssertEqual(store.space(id: s.id)?.dir, "")
    }

    /// A desktop's Change Folder and Remove Folder made while the app was
    /// closed are taken at the next launch, not sent back over them.
    @MainActor func testDesktopChangeWhileClosedIsAdopted() {
        let url = tempURL()
        defer {
            try? FileManager.default.removeItem(at: url)
            try? FileManager.default.removeItem(at: SpaceStore.reportedURL(for: url))
        }
        let first = SpaceStore(fileURL: url)
        first.sync(from: [host("m-office", project: .init(home_machine: "m-office", dir: "/srv/old"))])
        let id = first.orderedSpaces.first?.id ?? ""
        XCTAssertEqual(first.space(id: id)?.folder, "/srv/old")

        // The app relaunches; the desktop changed the folder meanwhile.
        let second = SpaceStore(fileURL: url)
        second.sync(from: [host("m-office", project: .init(home_machine: "m-office", dir: "/srv/new"))])
        XCTAssertEqual(second.space(id: id)?.folder, "/srv/new")

        // Again, and the desktop removed the folder meanwhile.
        let third = SpaceStore(fileURL: url)
        third.sync(from: [host("m-office", project: nil)])
        XCTAssertEqual(third.space(id: id)?.dir, "")
    }

    /// A home set on the phone that the desktop has not caught up with
    /// stays after a relaunch too.
    @MainActor func testLocalHomeStaysAfterRelaunch() {
        let url = tempURL()
        defer {
            try? FileManager.default.removeItem(at: url)
            try? FileManager.default.removeItem(at: SpaceStore.reportedURL(for: url))
        }
        let old = SessionView.Project(home_machine: "m-office", dir: "/srv/old")
        let first = SpaceStore(fileURL: url)
        first.sync(from: [host("m-office", project: old)])
        let id = first.orderedSpaces.first?.id ?? ""
        first.setHome(id, machine: "m-office", dir: "/srv/phone")
        let second = SpaceStore(fileURL: url)
        second.sync(from: [host("m-office", project: old)])
        XCTAssertEqual(second.space(id: id)?.folder, "/srv/phone")
    }

    // MARK: Home host offline

    /// H1: a host keeps the machine it was last seen to be while offline
    /// and after a relaunch, so that a project's home host offline reads
    /// as offline, not as not added.
    @MainActor func testHostKeepsLastMachineWhileOffline() throws {
        let config = HostConfig(name: "office", url: URL(string: "http://127.0.0.1:1")!)
        defer { Host.forgetMachine(config.id) }
        let h = Host(config)
        XCTAssertEqual(h.homeMachineKey, "host:\(config.id.uuidString)")
        h.take(try Spaces.Answer(json: Data(#"{"machine_id":"m-office","sessions":[]}"#.utf8)))
        XCTAssertEqual(h.homeMachineKey, "m-office")
        h.take(nil)
        XCTAssertNil(h.machine)
        XCTAssertEqual(h.homeMachineKey, "m-office")
        let relaunched = Host(config)
        XCTAssertEqual(relaunched.homeMachineKey, "m-office")
        let candidate = ProjectWindow.Candidate(id: relaunched.id, name: "office", machine: relaunched.homeMachineKey,
                                                online: false, sessions: [])
        XCTAssertEqual(ProjectWindow.plan(project(), hosts: [candidate]), .offline(host: config.id, name: "office"))
        Host.forgetMachine(config.id)
        XCTAssertEqual(Host(config).homeMachineKey, "host:\(config.id.uuidString)")
    }

    /// M1: a host whose address now leads to another computer is online
    /// before /api/spaces tells its machine. Until then its sync is not
    /// keyed by the machine it was last seen to be: the old computer's
    /// members stay, and nothing is reported under its machine. A new
    /// address forgets the machine the host was.
    @MainActor func testHostNotReadYetIsNotTheLastMachine() async throws {
        let saved = UserDefaults.standard.data(forKey: "hosts")
        defer { UserDefaults.standard.set(saved, forKey: "hosts") }
        UserDefaults.standard.removeObject(forKey: "hosts")
        let url = tempURL()
        defer {
            try? FileManager.default.removeItem(at: url)
            try? FileManager.default.removeItem(at: SpaceStore.reportedURL(for: url))
        }
        let (old, new) = (try FakeRex(), try FakeRex())
        defer { old.stop(); new.stop() }
        for r in [old, new] { r.respond = { m, _ in m == "session.bootstrap" ? ["attach_all": ["sessions": []]] : [:] } }
        var config = HostConfig(name: "office", url: try await old.url())
        defer { Host.forgetMachine(config.id) }
        // The host was m-office, as a launch before read it.
        Host(config).take(try Spaces.Answer(json: Data(#"{"machine_id":"m-office","sessions":[]}"#.utf8)))

        let spaces = SpaceStore(fileURL: url)
        let ops = spaces.createSpace(name: "acme-ops")
        spaces.assign(machine: "m-office", block: "block:74", window: "w1", to: ops.id)

        let store = HostStore()
        store.add(config)
        let h = try XCTUnwrap(store.host(config.id))
        await until("online") { h.status == .online }
        XCTAssertNil(h.machine)
        XCTAssertFalse(h.spaceSnapshot.known)
        XCTAssertNotEqual(h.spaceSnapshot.machine, "m-office")
        spaces.sync(from: [h.spaceSnapshot])
        XCTAssertEqual(spaces.space(id: ops.id)?.members.map(\.block_id), ["block:74"])
        XCTAssertEqual(h.lastMachine, "m-office")

        config.url = try await new.url()
        store.update(config)
        XCTAssertNil(h.lastMachine)
        XCTAssertNil(Host(config).lastMachine)
        store.remove(config.id)
    }

    // MARK: Rename and reports of other sessions

    @MainActor private func session(_ id: String, _ label: String, _ blocks: [String], project: SessionView.Project?) -> SpaceStore.HostSnapshot.Session {
        .init(id: id, label: label, view: view(id, blocks), remote: [], project: project)
    }

    @MainActor private func snapshot(_ machine: String, known: Bool = true, _ sessions: [SpaceStore.HostSnapshot.Session]) -> SpaceStore.HostSnapshot {
        .init(machine: machine, online: true, sessions: sessions, projects: true, known: known)
    }

    /// H2: renamed here, a project's windows go to a new desktop space of
    /// the new name, which has no folder until gorex.spaces_apply gives it
    /// one. That space's first report is not the desktop removing the
    /// project's folder: the folder stays.
    @MainActor func testNewSessionWithoutFolderKeepsProjectFolder() {
        let url = tempURL()
        defer {
            try? FileManager.default.removeItem(at: url)
            try? FileManager.default.removeItem(at: SpaceStore.reportedURL(for: url))
        }
        let home = SessionView.Project(home_machine: "m-office", dir: "/tmp/demo/acme-api")
        let store = SpaceStore(fileURL: url)
        let s = store.createSpace(name: "acme-api", homeMachine: "m-office", dir: home.dir)
        store.sync(from: [snapshot("m-office", [session("s1", "acme-api", ["b1"], project: home)])])
        store.renameSpace(s.id, to: "acme-web")
        store.sync(from: [snapshot("m-office", [
            session("s1", "acme-api", [], project: home),
            session("s2", "acme-web", ["b1"], project: nil),
        ])])
        XCTAssertEqual(store.space(id: s.id)?.folder, "/tmp/demo/acme-api")
        XCTAssertEqual(store.space(id: s.id)?.home_machine, "m-office")
        // A relaunch, with the host back.
        let again = SpaceStore(fileURL: url)
        again.sync(from: [snapshot("m-office", [session("s2", "acme-web", ["b1"], project: nil)])])
        XCTAssertEqual(again.space(id: s.id)?.folder, "/tmp/demo/acme-api")
    }

    /// H2: a host whose machine is not known yet, as just after it connects,
    /// keys its reports by its host ID: they are not taken as the home's
    /// changes, nor kept.
    @MainActor func testReportsOfUnknownMachineAreIgnored() {
        let url = tempURL()
        defer {
            try? FileManager.default.removeItem(at: url)
            try? FileManager.default.removeItem(at: SpaceStore.reportedURL(for: url))
        }
        let home = SessionView.Project(home_machine: "m-office", dir: "/tmp/demo/acme-api")
        let store = SpaceStore(fileURL: url)
        let s = store.createSpace(name: "acme-api", homeMachine: "m-office", dir: home.dir)
        let unknown = "host:" + UUID().uuidString
        store.sync(from: [snapshot(unknown, known: false, [session("s1", "acme-api", ["b1"], project: home)])])
        store.sync(from: [snapshot(unknown, known: false, [session("s1", "acme-api", ["b1"], project: nil)])])
        XCTAssertEqual(store.space(id: s.id)?.folder, "/tmp/demo/acme-api")
        store.sync(from: [snapshot("m-office", [session("s1", "acme-api", ["b1"], project: home)])])
        XCTAssertEqual(store.space(id: s.id)?.folder, "/tmp/demo/acme-api")
    }

    /// H3: renamed here, the project's desktop session still reports the
    /// old name until it is renamed there: no project of the old name is
    /// made, and the session is renamed to the new name.
    @MainActor func testRenameHereDoesNotRecreateOldName() {
        let url = tempURL()
        defer {
            try? FileManager.default.removeItem(at: url)
            try? FileManager.default.removeItem(at: SpaceStore.reportedURL(for: url))
        }
        let home = SessionView.Project(home_machine: "m-office", dir: "/tmp/demo/acme-api")
        let store = SpaceStore(fileURL: url)
        let s = store.createSpace(name: "acme-api", homeMachine: "m-office", dir: home.dir)
        let before = snapshot("m-office", [session("s1", "acme-api", ["b1"], project: home)])
        store.sync(from: [before])
        XCTAssertEqual(store.renames(for: before).map(\.name), [])
        store.renameSpace(s.id, to: "acme-web")
        XCTAssertEqual(store.renames(for: before).map(\.session), ["s1"])
        XCTAssertEqual(store.renames(for: before).map(\.name), ["acme-web"])

        // The host has not caught up yet, as offline meanwhile.
        store.sync(from: [before])
        XCTAssertEqual(store.orderedSpaces.map(\.name), ["acme-web"])
        XCTAssertEqual(store.space(id: s.id)?.members.map(\.block_id), ["b1"])
        // Nor after a relaunch.
        let again = SpaceStore(fileURL: url)
        again.sync(from: [before])
        XCTAssertEqual(again.orderedSpaces.map(\.name), ["acme-web"])
        XCTAssertEqual(again.renames(for: before).map(\.name), ["acme-web"])

        // Renamed there, it is the same project.
        let after = snapshot("m-office", [session("s1", "acme-web", ["b1"], project: home)])
        again.sync(from: [after])
        XCTAssertEqual(again.orderedSpaces.map(\.name), ["acme-web"])
        XCTAssertEqual(again.renames(for: after).map(\.name), [])
    }

    /// A project the desktop renames keeps its ID and folder here: the
    /// manifest takes the new name, and makes no second project.
    @MainActor func testRenameOnDesktopRenamesProject() {
        let url = tempURL()
        defer {
            try? FileManager.default.removeItem(at: url)
            try? FileManager.default.removeItem(at: SpaceStore.reportedURL(for: url))
        }
        let home = SessionView.Project(home_machine: "m-office", dir: "/tmp/demo/acme-api")
        let store = SpaceStore(fileURL: url)
        let s = store.createSpace(name: "acme-api", homeMachine: "m-office", dir: home.dir)
        store.sync(from: [snapshot("m-office", [session("s1", "acme-api", ["b1"], project: home)])])
        let after = snapshot("m-office", [session("s1", "acme-web", ["b1"], project: home)])
        store.sync(from: [after])
        XCTAssertEqual(store.orderedSpaces.map(\.id), [s.id])
        XCTAssertEqual(store.space(id: s.id)?.name, "acme-web")
        XCTAssertEqual(store.space(id: s.id)?.folder, "/tmp/demo/acme-api")
        XCTAssertEqual(store.renames(for: after).map(\.name), [])
    }

    /// A session is not renamed to a name another session of its host has.
    @MainActor func testNoRenameOntoAnotherSessionsName() {
        let url = tempURL()
        defer {
            try? FileManager.default.removeItem(at: url)
            try? FileManager.default.removeItem(at: SpaceStore.reportedURL(for: url))
        }
        let store = SpaceStore(fileURL: url)
        let s = store.createSpace(name: "acme-api")
        let before = snapshot("m-office", [session("s1", "acme-api", ["b1"], project: nil)])
        store.sync(from: [before])
        store.renameSpace(s.id, to: "acme-web")
        let both = snapshot("m-office", [
            session("s1", "acme-api", ["b1"], project: nil),
            session("s2", "acme-web", ["b2"], project: nil),
        ])
        XCTAssertEqual(store.renames(for: both).map(\.name), [])
    }

    // MARK: New Window

    @MainActor private func candidate(_ name: String, machine: String, online: Bool = true, sessions: [Host.Session] = []) -> ProjectWindow.Candidate {
        .init(id: UUID(), name: name, machine: machine, online: online, sessions: sessions)
    }

    @MainActor private func project(dir: String? = "/srv/windflow") -> CentralSpace {
        CentralSpace(id: "sp1", name: "windflow", color: nil, order: 0, updated_at: 0, members: [],
                     home_machine: dir == nil ? nil : "m-office", dir: dir)
    }

    @MainActor func testPlanWithoutFolder() {
        XCTAssertEqual(ProjectWindow.plan(project(dir: nil), hosts: [candidate("office", machine: "m-office")]), .noFolder)
        XCTAssertEqual(ProjectWindow.plan(project(dir: ""), hosts: [candidate("office", machine: "m-office")]), .noFolder)
    }

    /// No host of the home's machine: no window, and none elsewhere.
    @MainActor func testPlanUnknownHomeHost() {
        XCTAssertEqual(ProjectWindow.plan(project(), hosts: [candidate("laptop", machine: "m-laptop")]), .unknownHost)
    }

    /// The home host offline: no window elsewhere.
    @MainActor func testPlanOfflineHomeHost() {
        let office = candidate("office", machine: "m-office", online: false)
        let laptop = candidate("laptop", machine: "m-laptop")
        XCTAssertEqual(ProjectWindow.plan(project(), hosts: [office, laptop]), .offline(host: office.id, name: "office"))
    }

    /// Of two entries of the home's machine, the one online opens it.
    @MainActor func testPlanPrefersOnlineEntryOfHomeMachine() {
        let lan = candidate("office (LAN)", machine: "m-office", online: false)
        let tailnet = candidate("office", machine: "m-office")
        XCTAssertEqual(ProjectWindow.plan(project(), hosts: [lan, tailnet]),
                       .session(host: tailnet.id, label: "windflow", cwd: "/srv/windflow"))
    }

    /// The session that keeps the folder as its home: the server starts the
    /// window there and checks the folder, so no cwd is sent.
    @MainActor func testPlanSessionKeepingHome() {
        let s = Host.Session(id: "s1", label: "Windflow", view: nil, project: .init(home_machine: "m-office", dir: "/srv/windflow"))
        let office = candidate("office", machine: "m-office", sessions: [s])
        XCTAssertEqual(ProjectWindow.plan(project(), hosts: [office]), .window(host: office.id, session: "s1", cwd: nil))
    }

    /// A session that does not keep the home, as of an older GoRex: the
    /// window gets the folder as its cwd.
    @MainActor func testPlanSessionWithoutHome() {
        let s = Host.Session(id: "s1", label: "windflow", view: nil, project: nil)
        let other = Host.Session(id: "s2", label: "windflow", view: nil, project: .init(home_machine: "m-office", dir: "/srv/old"))
        let office = candidate("office", machine: "m-office", sessions: [s])
        XCTAssertEqual(ProjectWindow.plan(project(), hosts: [office]), .window(host: office.id, session: "s1", cwd: "/srv/windflow"))
        let office2 = candidate("office", machine: "m-office", sessions: [other])
        XCTAssertEqual(ProjectWindow.plan(project(), hosts: [office2]), .window(host: office2.id, session: "s2", cwd: "/srv/windflow"))
    }

    /// The desktop's loose terminals are no project's session.
    @MainActor func testPlanSkipsDesktopTerminals() {
        let loose = Host.Session(id: SpaceStore.desktopTerminalsSessionID, label: "windflow", view: nil, project: nil)
        let office = candidate("office", machine: "m-office", sessions: [loose])
        XCTAssertEqual(ProjectWindow.plan(project(), hosts: [office]), .session(host: office.id, label: "windflow", cwd: "/srv/windflow"))
    }

    /// A cwd the server would leave for home when it is gone is checked
    /// first: a folder not there, or not readable, fails as folder_missing.
    @MainActor func testEnsureFolder() async throws {
        try await ProjectWindow.ensureFolder("/srv/w") { Host.Folders(path: $0, parent: "/srv", home: "/home/demo", dirs: []) }
        for code in ["not_found", "denied"] {
            do {
                try await ProjectWindow.ensureFolder("/srv/gone") {
                    Host.Folders(path: $0, parent: "/srv", home: "/home/demo", dirs: [], code: code)
                }
                XCTFail("a \(code) folder passed")
            } catch {
                XCTAssertTrue(ProjectWindow.folderMissing(error), "\(error)")
            }
        }
        // Demo Mode's tree: a folder gone fails, one there passes.
        try await ProjectWindow.ensureFolder("~/code/windflow") { Demo.folders($0) }
        do {
            try await ProjectWindow.ensureFolder("~/code/gone") { Demo.folders($0) }
            XCTFail("a gone demo folder passed")
        } catch {
            XCTAssertTrue(ProjectWindow.folderMissing(error))
        }
    }

    @MainActor func testFolderMissing() {
        XCTAssertTrue(ProjectWindow.folderMissing(RexError(code: "conflict", message: "folder_missing: /srv/windflow is not a folder")))
        XCTAssertFalse(ProjectWindow.folderMissing(RexError(code: "conflict", message: "Open the GoRex window on office")))
        XCTAssertFalse(ProjectWindow.folderMissing(RexError(code: "not_found", message: "folder_missing")))
    }

    @MainActor func testShortAndName() {
        XCTAssertEqual(ProjectWindow.short("/Users/demo/code/gorex"), "~/code/gorex")
        XCTAssertEqual(ProjectWindow.short("/home/demo"), "~")
        XCTAssertEqual(ProjectWindow.short("/srv/windflow"), "/srv/windflow")
        XCTAssertEqual(ProjectWindow.name(of: "/Users/demo/code/gorex/"), "gorex")
        XCTAssertEqual(ProjectWindow.name(of: #"C:\src\windflow"#), "windflow")
    }

    // MARK: Wire

    /// A view's project reads; a malformed one reads as no folder.
    @MainActor func testSessionViewProject() throws {
        let good = #"{"payload":{"revision":1,"session_id":"s1","project":{"home_machine":"m","dir":"/srv/w"}}}"#
        XCTAssertEqual(try SessionView(response: Data(good.utf8)).project, .init(home_machine: "m", dir: "/srv/w"))
        let bad = #"{"payload":{"revision":1,"session_id":"s1","project":"nope"}}"#
        XCTAssertEqual(try SessionView(response: Data(bad.utf8)).project?.dir, "")
        let none = #"{"payload":{"revision":1,"session_id":"s1"}}"#
        XCTAssertNil(try SessionView(response: Data(none.utf8)).project)
        XCTAssertNil(SessionView.Project.of(["home_machine": "m", "dir": ""]))
        XCTAssertEqual(SessionView.Project.of(["home_machine": "m", "dir": "/a"])?.dir, "/a")
    }

    /// gorex.dir.list's answer, and a child's path.
    @MainActor func testFolders() {
        let f = Host.Folders(["path": "/home/demo", "parent": "/home", "home": "/home/demo", "dirs": ["code"], "more": true])
        XCTAssertEqual(f.child("code"), "/home/demo/code")
        XCTAssertTrue(f.more)
        XCTAssertNil(f.code)
        XCTAssertEqual(Host.Folders(path: "/", parent: "", home: "/", dirs: []).child("Users"), "/Users")
        XCTAssertEqual(Host.Folders(path: "", parent: "", home: "", dirs: []).child(#"C:\"#), #"C:\"#)
        XCTAssertEqual(Host.Folders(["path": "/x", "code": "denied"]).code, "denied")
    }

    /// Demo Mode's folders browse as a host's do.
    @MainActor func testDemoFolders() {
        XCTAssertEqual(Demo.folders("").path, Demo.home)
        XCTAssertTrue(Demo.folders("~/code").dirs.contains("gorex"))
        XCTAssertEqual(Demo.folders("~/code/..").path, Demo.home)
        XCTAssertNil(Demo.folders("~/code/gorex/cmd").code)
        XCTAssertEqual(Demo.folders("~/nowhere").code, "not_found")
        XCTAssertEqual(Demo.folders("/").parent, "")
    }
}
