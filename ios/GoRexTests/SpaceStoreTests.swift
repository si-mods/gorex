import XCTest

/// Centralized cross-host space store tests (SpaceStore.swift).
nonisolated final class SpaceStoreTests: XCTestCase {
    @MainActor private func tempURL() -> URL {
        FileManager.default.temporaryDirectory.appendingPathComponent("spaces-\(UUID().uuidString).json")
    }

    @MainActor private func view(_ session: String, _ windows: [(String, [String])]) -> SessionView {
        SessionView(
            revision: 1,
            session_id: session,
            windows: windows.map { id, blocks in
                .init(window_id: id, focused_block_id: blocks.first, layers: [.init(blocks: blocks.map { .init(block_id: $0) })])
            }
        )
    }

    @MainActor private func member(_ machine: String, _ block: String) -> Spaces.Member {
        try! JSONDecoder().decode(Spaces.Member.self, from: Data(#"{"machine":"\#(machine)","block_id":"\#(block)"}"#.utf8))
    }

    /// Persists spaces and members to disk and reloads them on init.
    @MainActor func testPersistenceRoundTrip() {
        let url = tempURL()
        defer { try? FileManager.default.removeItem(at: url) }

        let store = SpaceStore(fileURL: url)
        let work = store.createSpace(name: "Work", color: "blue")
        store.assign(machine: "M1", block: "b1", window: "w1", to: work.id)

        let reloaded = SpaceStore(fileURL: url)
        XCTAssertEqual(reloaded.orderedSpaces.map(\.name), ["Work"])
        XCTAssertEqual(reloaded.space(for: "M1", block: "b1")?.id, work.id)
    }

    /// Same-name spaces across multiple hosts merge into a single CentralSpace.
    @MainActor func testSameNameSpacesMergeAcrossHosts() {
        let url = tempURL()
        defer { try? FileManager.default.removeItem(at: url) }

        let store = SpaceStore(fileURL: url)
        store.sync(from: [
            .init(machine: "M1", online: true, sessions: [
                .init(id: "s1", label: "Work", view: view("s1", [("w1", ["b1"])]), remote: [member("L1", "lb1")]),
            ]),
            .init(machine: "M2", online: true, sessions: [
                .init(id: "s2", label: "work", view: view("s2", [("w2", ["b2"])]), remote: []),
            ]),
            .init(machine: "L1", online: true, sessions: [
                .init(id: SpaceStore.desktopTerminalsSessionID, label: "Desktop terminals", view: view(SpaceStore.desktopTerminalsSessionID, [("lw1", ["lb1"])]), remote: []),
            ]),
        ])

        XCTAssertEqual(store.orderedSpaces.count, 1)
        let work = store.orderedSpaces[0]
        XCTAssertEqual(store.space(for: "M1", block: "b1")?.id, work.id)
        XCTAssertEqual(store.space(for: "M2", block: "b2")?.id, work.id)
        XCTAssertEqual(store.space(for: "L1", block: "lb1")?.id, work.id)
    }

    /// When the desktop host goes offline, the space and its online remote
    /// host members remain intact.
    @MainActor func testOfflineHostKeepsSpaceAndOnlineMembers() {
        let url = tempURL()
        defer { try? FileManager.default.removeItem(at: url) }

        let store = SpaceStore(fileURL: url)
        store.sync(from: [
            .init(machine: "MAC", online: true, sessions: [
                .init(id: "s1", label: "Infra", view: view("s1", [("w1", ["mb1"])]), remote: [member("LINUX", "lb1")]),
            ]),
            .init(machine: "LINUX", online: true, sessions: [
                .init(id: SpaceStore.desktopTerminalsSessionID, label: "Desktop terminals", view: view(SpaceStore.desktopTerminalsSessionID, [("lw1", ["lb1"])]), remote: []),
            ]),
        ])

        // Mac goes offline; Linux stays online.
        store.sync(from: [
            .init(machine: "MAC", online: false, sessions: []),
            .init(machine: "LINUX", online: true, sessions: [
                .init(id: SpaceStore.desktopTerminalsSessionID, label: "Desktop terminals", view: view(SpaceStore.desktopTerminalsSessionID, [("lw1", ["lb1"])]), remote: []),
            ]),
        ])

        XCTAssertEqual(store.orderedSpaces.map(\.name), ["Infra"])
        XCTAssertNotNil(store.space(for: "MAC", block: "mb1"))
        XCTAssertNotNil(store.space(for: "LINUX", block: "lb1"))
    }

    /// Unassigned terminals in `gorex-desktop-terminals` do not spawn a space.
    @MainActor func testDesktopTerminalsGoToUnassigned() {
        let url = tempURL()
        defer { try? FileManager.default.removeItem(at: url) }

        let store = SpaceStore(fileURL: url)
        let w = SessionView.Window(window_id: "lw1", focused_block_id: "lb1", layers: [.init(blocks: [.init(block_id: "lb1")])])
        store.sync(from: [
            .init(machine: "LINUX", online: true, sessions: [
                .init(id: SpaceStore.desktopTerminalsSessionID, label: "Desktop terminals", view: SessionView(revision: 1, session_id: SpaceStore.desktopTerminalsSessionID, windows: [w]), remote: []),
            ]),
        ])

        XCTAssertTrue(store.orderedSpaces.isEmpty)
        XCTAssertTrue(store.isUnassigned(machine: "LINUX", window: w))
    }

    /// Safe GC removes dead blocks only for online, fully attached hosts.
    @MainActor func testSafeGC() {
        let url = tempURL()
        defer { try? FileManager.default.removeItem(at: url) }

        let store = SpaceStore(fileURL: url)
        let sp = store.createSpace(name: "Work")
        store.assign(machine: "M1", block: "alive", to: sp.id)
        store.assign(machine: "M1", block: "dead", to: sp.id)
        store.assign(machine: "M2", block: "unattached", to: sp.id)

        store.sync(from: [
            .init(machine: "M1", online: true, sessions: [
                .init(id: "s1", label: "Work", view: view("s1", [("w1", ["alive"])]), remote: []),
            ]),
            // M2 has a session whose view is nil (not yet attached) -> do not GC M2.
            .init(machine: "M2", online: true, sessions: [
                .init(id: "s2", label: "Work", view: nil, remote: []),
            ]),
        ])

        XCTAssertNotNil(store.space(for: "M1", block: "alive"))
        XCTAssertNil(store.space(for: "M1", block: "dead"))
        XCTAssertNotNil(store.space(for: "M2", block: "unattached"))
    }

    /// Deleting a space or unassigning a block records tombstones that prevent
    /// immediate re-import on subsequent syncs.
    @MainActor func testTombstonesPreventResurrection() {
        let url = tempURL()
        defer { try? FileManager.default.removeItem(at: url) }

        let store = SpaceStore(fileURL: url)
        let host = SpaceStore.HostSnapshot(machine: "M1", online: true, sessions: [
            .init(id: "s1", label: "Temp", view: view("s1", [("w1", ["b1"])]), remote: []),
        ])
        store.sync(from: [host])
        let sp = store.orderedSpaces.first!

        store.unassign(machine: "M1", block: "b1")
        store.sync(from: [host])
        XCTAssertNil(store.space(for: "M1", block: "b1"))

        store.deleteSpace(sp.id)
        store.sync(from: [host])
        XCTAssertTrue(store.orderedSpaces.isEmpty)
    }

    /// LWW merge resolves concurrent space edits and single-owner block moves.
    @MainActor func testLWWMerge() {
        let s1 = CentralSpace(
            id: "sp1", name: "Old", color: nil, order: 0, updated_at: 100,
            members: [.init(machine_id: "M1", block_id: "b1", window_id: "w1", order: 0, added_at: 100)]
        )
        let s2 = CentralSpace(
            id: "sp1", name: "Renamed", color: nil, order: 0, updated_at: 200,
            members: []
        )
        let s3 = CentralSpace(
            id: "sp2", name: "Other", color: nil, order: 1, updated_at: 200,
            members: [.init(machine_id: "M1", block_id: "b1", window_id: "w1", order: 0, added_at: 200)]
        )

        let merged = SpaceStore.merged(
            local: SpaceManifest(spaces: [s1]),
            remote: SpaceManifest(spaces: [s2, s3])
        )

        XCTAssertEqual(merged.spaces.first(where: { $0.id == "sp1" })?.name, "Renamed")
        XCTAssertEqual(merged.spaces.first(where: { $0.id == "sp1" })?.members.count, 0)
        XCTAssertEqual(merged.spaces.first(where: { $0.id == "sp2" })?.members.map(\.block_id), ["b1"])
    }

    /// Mutations push to iCloud KVS and a second device pulls them on init
    /// and on external-change notifications without ping-ponging.
    @MainActor func testICloudKVSSyncAcrossDevices() {
        final class MockKVS: SpaceKeyValueStore {
            var storage: [String: Data] = [:]
            var setCount = 0
            func data(forKey aKey: String) -> Data? { storage[aKey] }
            func set(_ aData: Data?, forKey aKey: String) {
                storage[aKey] = aData
                setCount += 1
            }
            func synchronize() -> Bool { true }
        }

        let urlA = tempURL(), urlB = tempURL()
        defer {
            try? FileManager.default.removeItem(at: urlA)
            try? FileManager.default.removeItem(at: urlB)
        }

        let kvs = MockKVS()
        let deviceA = SpaceStore(fileURL: urlA, kvs: kvs, observeCloud: true)
        let work = deviceA.createSpace(name: "Work")
        deviceA.assign(machine: "M1", block: "b1", to: work.id)
        let setsAfterA = kvs.setCount
        XCTAssertGreaterThan(setsAfterA, 0)

        // Device B initializes with the same KVS and pulls Work + M1/b1 without pushing back.
        let deviceB = SpaceStore(fileURL: urlB, kvs: kvs, observeCloud: true)
        XCTAssertEqual(deviceB.orderedSpaces.map(\.name), ["Work"])
        XCTAssertEqual(deviceB.space(for: "M1", block: "b1")?.id, work.id)
        XCTAssertEqual(kvs.setCount, setsAfterA)

        // Device B creates a second space; Device A receives external change notification.
        let personal = deviceB.createSpace(name: "Personal")
        deviceB.assign(machine: "M2", block: "b2", to: personal.id)
        NotificationCenter.default.post(name: NSUbiquitousKeyValueStore.didChangeExternallyNotification, object: kvs)

        XCTAssertEqual(deviceA.orderedSpaces.map(\.name), ["Work", "Personal"])
        XCTAssertEqual(deviceA.space(for: "M2", block: "b2")?.id, personal.id)
    }
}
