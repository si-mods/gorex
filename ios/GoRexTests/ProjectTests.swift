import XCTest

/// Projects on each host, inside a space (docs/projects.md, Rex Go): what
/// the host's welcome and sessions say, the Rex API calls the list makes,
/// the spaces this device keeps, and the copy of a host's projects shown
/// while it is away.
nonisolated final class ProjectTests: XCTestCase {
    // MARK: Welcome

    /// A feature's version reads from each form a welcome may give it; a
    /// feature with none has no version.
    @MainActor func testFeatureVersions() {
        func version(_ welcome: [String: Any]) -> Int? {
            ControlConnection.features(of: welcome).1[Host.projectsFeature]
        }
        XCTAssertNil(version(["features": ["gorex.projects", "gorex.dir_list"]]))
        XCTAssertTrue(ControlConnection.features(of: ["features": ["gorex.projects"]]).0.contains(Host.projectsFeature))
        XCTAssertEqual(version(["features": ["gorex.projects"], "feature_versions": ["gorex.projects": 2]]), 2)
        XCTAssertEqual(version(["features": [["name": "gorex.projects", "version": 2], "gorex.dir_list"]]), 2)
        XCTAssertEqual(version(["features": ["gorex.projects.v2"]]), 2)
        let (names, _) = ControlConnection.features(of: ["features": [["name": "gorex.projects", "version": 2], "gorex.dir_list"]])
        XCTAssertEqual(names, [Host.projectsFeature, Host.dirListFeature])
    }

    /// Only a host whose welcome gives gorex.projects version 2 lists
    /// projects; one of before keeps its sessions as before.
    @MainActor func testOnlyVersionTwoHasProjects() async throws {
        let cases: [([String], [String: Int], Bool)] = [
            ([], [:], false), (["gorex.projects"], [:], false),
            (["gorex.projects"], ["gorex.projects": 1], false), (["gorex.projects"], ["gorex.projects": 2], true),
        ]
        for (features, versions, want) in cases {
            let (rex, h) = try await connect(features: features, versions: versions)
            XCTAssertEqual(h.hasProjects, want, "\(features) \(versions)")
            h.stop()
            rex.stop()
        }
    }

    // MARK: Sessions

    /// A session's project reads its folder, space, last use and color; a
    /// session of no project has none, and a malformed field reads empty.
    @MainActor func testSessionViewProject() throws {
        let good = #"{"payload":{"revision":1,"session_id":"s1","project":{"dir":"/srv/acme-api","space":"Work","used":"2026-11-18T09:30:00.123456789+08:00","color":"teal"}}}"#
        let p = try XCTUnwrap(try SessionView(response: Data(good.utf8)).project)
        XCTAssertEqual(p.dir, "/srv/acme-api")
        XCTAssertEqual(p.space, "Work")
        XCTAssertEqual(p.color, "teal")
        XCTAssertEqual(p.used, Date(timeIntervalSince1970: 1_794_965_400))
        let loose = #"{"payload":{"revision":1,"session_id":"s1","project":{}}}"#
        XCTAssertEqual(try SessionView(response: Data(loose.utf8)).project?.dir, "")
        let bad = #"{"payload":{"revision":1,"session_id":"s1","project":"nope"}}"#
        XCTAssertEqual(try SessionView(response: Data(bad.utf8)).project?.dir, "")
        XCTAssertNil(SessionView.Project.of([:]))
        XCTAssertNil(SessionView.Project.of(["dir": ""]))
        let listed = SessionView.Project.of(["dir": "/a", "space": "Life", "used": "2026-11-18T01:30:00Z"])
        XCTAssertEqual(listed, .init(dir: "/a", space: "Life", used: Date(timeIntervalSince1970: 1_794_965_400)))
        XCTAssertNil(SessionView.Project.time("0001-01-01T00:00:00Z"))
        XCTAssertNil(SessionView.Project.time("yesterday"))
    }

    /// A host with projects lists its sessions with a folder as projects,
    /// in their spaces, and the tabs of the others as tabs with no project.
    @MainActor func testHostListsProjectsAndLooseTabs() async throws {
        let (rex, h) = try await connect(features: [Host.projectsFeature], versions: [Host.projectsFeature: 2], sessions: [
            session("session:api", "acme-api", project: ["dir": "/tmp/demo/acme-api", "space": "Work", "used": "2026-11-18T01:00:00Z"],
                    tabs: [("window:dev", "dev"), ("window:logs", "logs")]),
            session("session:site", "site", project: ["dir": "/tmp/demo/site", "space": ""], tabs: []),
            session("session:loose", "", project: [:], tabs: [("window:scratch", "scratch")]),
        ], spaceList: ["Work", "", "Old"])
        defer { h.stop(); rex.stop() }
        await until("the views") { h.sessions.allSatisfy { $0.view != nil } }
        // The old space no project carries is offered, for the picker.
        await until("the spaces offered") { h.offeredSpaces == ["Old"] }
        let l = ProjectListing(host: h)
        XCTAssertEqual(l.projects.map(\.name), ["acme-api", "site"])
        XCTAssertEqual(l.projects[0].space, "Work")
        XCTAssertEqual(l.projects[0].tabs.map(\.title), ["dev", "logs"])
        XCTAssertEqual(l.projects[1].space, "")
        XCTAssertEqual(l.loose.map(\.title), ["scratch"])
        XCTAssertEqual(l.spaces, ["Work", ""])
        XCTAssertEqual(l.pickerSpaces, ["Work", "", "Old"])
    }

    // MARK: Rex API calls

    /// gorex.project.create sends the name, folder and space, and gives the
    /// project and its first tab; the tab's block reads from its view.
    @MainActor func testCreateProject() async throws {
        let (rex, h) = try await connect(features: [Host.projectsFeature], versions: [Host.projectsFeature: 2])
        defer { h.stop(); rex.stop() }
        rex.respond = { method, _ in
            switch method {
            case "gorex.project.create": ["project_id": "session:api", "tab_id": "window:dev"]
            case "session.view": ["revision": 1, "session_id": "session:api", "label": "acme-api",
                                  "windows": [["window_id": "window:dev", "focused_block_id": "block:dev"]]]
            default: [:]
            }
        }
        let made = try await h.createProject(name: "acme-api", dir: "/tmp/demo/acme-api", space: "Work")
        XCTAssertEqual(made, .made(project: "session:api", tab: "window:dev"))
        let p = try XCTUnwrap(rex.payload("gorex.project.create"))
        XCTAssertEqual(p["name"] as? String, "acme-api")
        XCTAssertEqual(p["dir"] as? String, "/tmp/demo/acme-api")
        XCTAssertEqual(p["space"] as? String, "Work")
        let block = try await h.block(ofTab: "window:dev", in: "session:api")
        XCTAssertEqual(block, "block:dev")
    }

    /// A folder that is a project already gives that project, told in the
    /// answer or as an error.
    @MainActor func testCreateProjectThatExists() async throws {
        let (rex, h) = try await connect(features: [Host.projectsFeature], versions: [Host.projectsFeature: 2], sessions: [
            session("session:api", "acme-api", project: ["dir": "/tmp/demo/acme-api", "space": "Life"], tabs: []),
        ])
        defer { h.stop(); rex.stop() }
        rex.respond = { method, _ in method == "gorex.project.create" ? ["code": "exists", "project_id": "session:api"] : [:] }
        let told = try await h.createProject(name: "api", dir: "/tmp/demo/acme-api", space: "Work")
        XCTAssertEqual(told, .exists(project: "session:api"))
        rex.respond = { method, _ in
            method == "gorex.project.create" ? ["error": ["code": "exists", "message": "a project of this folder exists"]] : [:]
        }
        let failed = try await h.createProject(name: "api", dir: "/tmp/demo/acme-api", space: "Work")
        XCTAssertEqual(failed, .exists(project: "session:api"))
        rex.respond = { method, _ in
            method == "gorex.project.create" ? ["error": ["code": "exists", "message": "exists", "project_id": "session:other"]] : [:]
        }
        let named = try await h.createProject(name: "api", dir: "/tmp/demo/acme-api/", space: "Work")
        XCTAssertEqual(named, .exists(project: "session:other"))
        rex.respond = { method, _ in
            method == "gorex.project.create" ? ["error": ["code": "exists", "message": "a project of this folder exists"]] : [:]
        }
        do {
            _ = try await h.createProject(name: "x", dir: "/tmp/demo/other", space: "")
            XCTFail("an exists of an unknown folder gave a project")
        } catch {
            XCTAssertEqual((error as? RexError)?.code, "exists")
        }
    }

    /// Move to Project, a project's update and a space's rename send what
    /// the doc names, and leave out what does not change.
    @MainActor func testMoveUpdateAndRenameSpace() async throws {
        let (rex, h) = try await connect(features: [Host.projectsFeature], versions: [Host.projectsFeature: 2])
        defer { h.stop(); rex.stop() }
        try await h.moveTab("window:dev", to: "session:site")
        try await h.moveTab("window:dev", to: "")
        try await h.updateProject("session:api", space: "Life")
        try await h.updateProject("session:api", name: "API", color: "teal")
        try await h.renameSpace(from: "Work", to: "Office")
        let moves = rex.payloads(of: "gorex.project.move")
        XCTAssertEqual(moves.map { $0["tab_id"] as? String }, ["window:dev", "window:dev"])
        XCTAssertEqual(moves.map { $0["project_id"] as? String }, ["session:site", ""])
        let updates = rex.payloads(of: "gorex.project.update")
        XCTAssertEqual(Set(updates[0].keys), ["project_id", "space"])
        XCTAssertEqual(updates[0]["space"] as? String, "Life")
        XCTAssertEqual(Set(updates[1].keys), ["project_id", "name", "color"])
        let rename = try XCTUnwrap(rex.payload("gorex.space.rename"))
        XCTAssertEqual(rename["from"] as? String, "Work")
        XCTAssertEqual(rename["to"] as? String, "Office")
    }

    /// A new tab of a project opens with its folder as cwd.
    @MainActor func testNewTabInFolder() async throws {
        let (rex, h) = try await connect(features: [Host.projectsFeature], versions: [Host.projectsFeature: 2])
        defer { h.stop(); rex.stop() }
        rex.respond = { method, _ in method == "session.new_window" ? ["block_ids": ["block:new"]] : [:] }
        let b = try await h.newWindow(in: "session:api", cwd: "/tmp/demo/acme-api")
        XCTAssertEqual(b, "block:new")
        let layout = try XCTUnwrap(rex.payload("session.new_window")?["layout"] as? [String: Any])
        let options = (layout["block"] as? [String: Any])?["options"] as? [String: Any]
        XCTAssertEqual(options?["cwd"] as? String, "/tmp/demo/acme-api")
    }

    // MARK: Demo Mode

    /// Demo Mode's projects change on the phone as the server's would.
    @MainActor func testDemoProjects() async throws {
        let h = Host(Demo.config())
        h.connect()
        defer { h.stop() }
        XCTAssertTrue(h.hasProjects)
        let made = try await h.createProject(name: "windflow", dir: Demo.home + "/code/windflow", space: "Work")
        guard case .made(let project, let tab) = made else { return XCTFail("\(made)") }
        let block = try await h.block(ofTab: tab, in: project)
        XCTAssertNotNil(block)
        let again = try await h.createProject(name: "wf", dir: Demo.home + "/code/windflow", space: "")
        XCTAssertEqual(again, .exists(project: project))
        try await h.moveTab("w-shell", to: project)
        XCTAssertEqual(h.sessions.first { $0.id == project }?.view?.windows?.map(\.window_id), [tab, "w-shell"])
        try await h.moveTab("w-shell", to: "")
        XCTAssertEqual(h.sessions.first { $0.id == Demo.loose }?.view?.windows?.map(\.window_id), ["w-shell"])
        try await h.updateProject(project, name: "Windflow", color: "teal", space: "Life")
        let p = try XCTUnwrap(h.sessions.first { $0.id == project })
        XCTAssertEqual(p.label, "Windflow")
        XCTAssertEqual(p.project?.color, "teal")
        XCTAssertEqual(p.project?.space, "Life")
        try await h.renameSpace(from: "Life", to: "Home")
        XCTAssertEqual(Set(h.sessions.compactMap(\.project?.space)), ["", "Home"])
    }

    // MARK: Order

    /// Projects go latest used first; those not used for 30 days go to
    /// Older; one never used counts as recent, after those used.
    @MainActor func testOrderByLastUse() {
        let now = Date(timeIntervalSince1970: 100 * 86400)
        let items: [(String, Date?)] = [
            ("old", now.addingTimeInterval(-40 * 86400)), ("new", now.addingTimeInterval(-60)),
            ("never", nil), ("week", now.addingTimeInterval(-7 * 86400)), ("older", now.addingTimeInterval(-90 * 86400)),
        ]
        let (recent, older) = ProjectOrder.split(items, used: \.1, name: \.0, now: now)
        XCTAssertEqual(recent.map(\.0), ["new", "week", "never"])
        XCTAssertEqual(older.map(\.0), ["old", "older"])
    }

    // MARK: Spaces

    @MainActor private func book() -> (SpaceBook, UserDefaults, URL) {
        let suite = "projects-\(UUID().uuidString)"
        let d = UserDefaults(suiteName: suite)!
        let url = FileManager.default.temporaryDirectory.appendingPathComponent("\(suite).json")
        addTeardownBlock {
            UserDefaults().removePersistentDomain(forName: suite)
            try? FileManager.default.removeItem(at: url)
        }
        return (SpaceBook(defaults: d, listingsURL: url), d, url)
    }

    /// The picker lists Default, the spaces carried and those made here, in
    /// the user's order, then new ones by name; a made space drops once a
    /// project carried it and none does any more.
    @MainActor func testPickerSpaces() {
        let (b, _, _) = book()
        XCTAssertEqual(b.spaces(carried: []), [""])
        XCTAssertEqual(b.spaces(carried: ["Work", "Life"]), ["", "Life", "Work"])
        b.reorder(["Work", "", "Life"])
        XCTAssertEqual(b.spaces(carried: ["Work", "Life", "Lab"]), ["Work", "", "Life", "Lab"])
        XCTAssertEqual(b.add("  Side  ", carried: ["Work"]), "Side")
        XCTAssertEqual(b.chosen, "Side")
        XCTAssertNil(b.add("   ", carried: []))
        XCTAssertEqual(b.spaces(carried: ["Work"]), ["Work", "", "Side"])
        b.settle(carried: ["Work", "Side"])
        XCTAssertEqual(b.made, [])
        XCTAssertEqual(b.spaces(carried: ["Work"]), ["Work", ""])
        // A made space no project carries can go.
        b.add("Empty", carried: [])
        b.remove("Empty")
        XCTAssertEqual(b.spaces(carried: []), [""])
        XCTAssertEqual(b.chosen, "")
    }

    /// A rename moves the space's place, color and choice at once, and
    /// waits for each host until it is sent; a host away shows its last
    /// projects under the new name meanwhile.
    @MainActor func testRenameQueuesForEachHost() {
        let (b, _, _) = book()
        b.reorder(["Work", ""])
        b.setColor("teal", for: "Work")
        b.chosen = "Work"
        b.remember(ProjectListing(projects: [.init(id: "p", name: "acme-api", dir: "/tmp/demo/acme-api", space: "Work", tabs: [])]), for: "away")
        b.rename("Work", to: "Office", on: ["away", "here"])
        XCTAssertEqual(b.order, ["Office", ""])
        XCTAssertEqual(b.colors, ["Office": "teal"])
        XCTAssertEqual(b.chosen, "Office")
        XCTAssertEqual(b.renames["away"], [.init(from: "Work", to: "Office")])
        XCTAssertEqual(b.listing(for: "away")?.spaces, ["Office"])
        XCTAssertEqual(b.space("Work", on: "here"), "Office")
        b.rename("Office", to: "Lab", on: ["away"])
        XCTAssertEqual(b.space("Work", on: "away"), "Lab")
        b.sent(.init(from: "Work", to: "Office"), to: "here")
        XCTAssertNil(b.renames["here"])
        XCTAssertEqual(b.renames["away"]?.count, 2)
        // Default keeps its name.
        b.rename("", to: "Home", on: ["here"])
        XCTAssertNil(b.renames["here"])
        b.forget(host: "away")
        XCTAssertNil(b.renames["away"])
        XCTAssertNil(b.listing(for: "away"))
    }

    /// What the book keeps is there at the next launch.
    @MainActor func testBookPersists() {
        let (b, d, url) = book()
        b.reorder(["Life", ""])
        b.setColor("rose", for: "Life")
        b.add("Lab", carried: [])
        b.rename("Life", to: "Home", on: ["h"])
        b.remember(ProjectListing(projects: [.init(id: "p", name: "site", dir: "/tmp/demo/site", space: "",
                                                   used: Date(timeIntervalSince1970: 1000), tabs: [.init(id: "w", title: "dev")])],
                                  loose: [.init(id: "x", title: "scratch")]), for: "h")
        let again = SpaceBook(defaults: d, listingsURL: url)
        XCTAssertEqual(again.order, ["Home", ""])
        XCTAssertEqual(again.colors, ["Home": "rose"])
        XCTAssertEqual(again.made, ["Lab"])
        XCTAssertEqual(again.chosen, "Lab")
        XCTAssertEqual(again.renames["h"], [.init(from: "Life", to: "Home")])
        XCTAssertEqual(again.listings, b.listings)
    }

    /// The manifest of before, its reports, its settings and each host's
    /// last machine are dropped, not carried over.
    @MainActor func testOldDataDropped() throws {
        let suite = "old-\(UUID().uuidString)"
        let d = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { UserDefaults().removePersistentDomain(forName: suite) }
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent(suite)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }
        for name in ["spaces-manifest.json", "spaces-manifest.reported.json", "projects-cache.json"] {
            try Data("{}".utf8).write(to: dir.appendingPathComponent(name))
        }
        for key in ["spaceSync.icloud", "spaceSync.desktop", "spaceSync.primaryHost", "hostMachines", "grouping", "space", "hosts"] {
            d.set("x", forKey: key)
        }
        SpaceBook.dropOldData(defaults: d, directory: dir)
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: dir.path), ["projects-cache.json"])
        XCTAssertEqual(d.string(forKey: "hosts"), "x")
        for key in ["spaceSync.icloud", "spaceSync.desktop", "spaceSync.primaryHost", "hostMachines", "grouping", "space"] {
            XCTAssertNil(d.object(forKey: key), key)
        }
    }

    // MARK: Folders

    /// A cwd the server would leave for home when it is gone is checked
    /// first: a folder not there, or not readable, fails as folder_missing.
    @MainActor func testEnsureFolder() async throws {
        try await ProjectFolder.ensure("/srv/w") { Host.Folders(path: $0, parent: "/srv", home: "/home/demo", dirs: []) }
        for code in ["not_found", "denied"] {
            do {
                try await ProjectFolder.ensure("/srv/gone") {
                    Host.Folders(path: $0, parent: "/srv", home: "/home/demo", dirs: [], code: code)
                }
                XCTFail("a \(code) folder passed")
            } catch {
                XCTAssertTrue(ProjectFolder.missing(error), "\(error)")
            }
        }
        // Demo Mode's tree: a folder gone fails, one there passes.
        try await ProjectFolder.ensure("~/code/windflow") { Demo.folders($0) }
        do {
            try await ProjectFolder.ensure("~/code/gone") { Demo.folders($0) }
            XCTFail("a gone demo folder passed")
        } catch {
            XCTAssertTrue(ProjectFolder.missing(error))
        }
    }

    @MainActor func testFolderMissing() {
        XCTAssertTrue(ProjectFolder.missing(RexError(code: "conflict", message: "folder_missing: /srv/windflow is not a folder")))
        XCTAssertFalse(ProjectFolder.missing(RexError(code: "conflict", message: "Open the GoRex window on office")))
        XCTAssertFalse(ProjectFolder.missing(RexError(code: "not_found", message: "folder_missing")))
    }

    @MainActor func testShortAndName() {
        XCTAssertEqual(ProjectFolder.short("/Users/demo/code/gorex"), "~/code/gorex")
        XCTAssertEqual(ProjectFolder.short("/home/demo"), "~")
        XCTAssertEqual(ProjectFolder.short("/srv/windflow"), "/srv/windflow")
        XCTAssertEqual(ProjectFolder.name(of: "/Users/demo/code/gorex/"), "gorex")
        XCTAssertEqual(ProjectFolder.name(of: #"C:\src\windflow"#), "windflow")
    }

    @MainActor func testProjectColors() {
        XCTAssertEqual(ProjectColor.hex("teal"), 0x12A594)
        XCTAssertEqual(ProjectColor.hex("#0090ff"), 0x0090FF)
        XCTAssertNil(ProjectColor.hex(""))
        XCTAssertNil(ProjectColor.hex("plaid"))
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

    // MARK: Helpers

    /// A session list entry of the fake server, with its view: tabs by ID
    /// and name, each with one block.
    @MainActor private func session(_ id: String, _ label: String, project: [String: Any], tabs: [(String, String)]) -> [String: Any] {
        ["session_id": id, "label": label, "project": project, "view": [
            "revision": 1, "session_id": id, "label": label, "project": project,
            "windows": tabs.map { w, name in
                ["window_id": w, "label": name, "focused_block_id": "block:" + w, "layers": [["blocks": [["block_id": "block:" + w]]]]] as [String: Any]
            },
        ] as [String: Any]]
    }

    /// A host online against a fake server of the features and versions
    /// given, which lists the sessions given.
    @MainActor private func connect(features: [String], versions: [String: Int], sessions: [[String: Any]] = [],
                                     spaceList: [String] = []) async throws -> (FakeRex, Host) {
        let rex = try FakeRex()
        rex.features = features
        rex.featureVersions = versions
        let views = Dictionary(uniqueKeysWithValues: sessions.map { ($0["session_id"] as! String, $0["view"]!) })
        rex.respond = { method, payload in
            switch method {
            case "session.bootstrap":
                ["attach_all": ["sessions": sessions.map { $0.filter { $0.key != "view" } }]]
            case "gorex.space.list":
                ["spaces": spaceList]
            case "session.attach":
                (views[payload["session_id"] as? String ?? ""]).map { ["view": $0] } ?? [:]
            default: [:]
            }
        }
        let h = Host(HostConfig(name: "office", url: try await rex.url()))
        h.connect()
        await until("online") { h.status == .online }
        return (rex, h)
    }
}
