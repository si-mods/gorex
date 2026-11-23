import XCTest

/// A cloud store in memory; the last writer wins, as in iCloud.
final class FakeCloud: CloudStore {
    var values: [String: Any] = [:]
    var writes: [String] = []
    func object(forKey key: String) -> Any? { values[key] }
    func set(_ value: Any?, forKey key: String) {
        values[key] = value
        writes.append(key)
    }
    func removeObject(forKey key: String) {
        values[key] = nil
        writes.append(key)
    }
    func synchronize() -> Bool { true }
}

/// The settings that sync through iCloud (docs/IOS-008-icloud-sync.md): which
/// side wins, what goes each way, and what stays on the device.
nonisolated final class CloudSettingsTests: XCTestCase {
    @MainActor private func make() -> (CloudSettings, FakeCloud, UserDefaults) {
        let suite = "cloud-\(UUID().uuidString)"
        let d = UserDefaults(suiteName: suite)!
        addTeardownBlock { UserDefaults().removePersistentDomain(forName: suite) }
        let cloud = FakeCloud()
        return (CloudSettings(cloud: cloud, defaults: d), cloud, d)
    }

    @MainActor func testCloudWinsAtStart() {
        let (s, cloud, d) = make()
        cloud.values = ["theme": "Nord", "fontSize": 15.0, "scheme": "light", "fontLigatures": 1.0]
        d.set("Gruvbox Dark", forKey: "theme")
        d.set(12.0, forKey: "fontSize")
        s.start()
        XCTAssertEqual(d.string(forKey: "theme"), "Nord")
        XCTAssertEqual(d.double(forKey: "fontSize"), 15.0)
        XCTAssertTrue(d.bool(forKey: "fontLigatures"))
        XCTAssertEqual(d.string(forKey: "scheme"), "light")
        XCTAssertEqual(cloud.values["theme"] as? String, "Nord")
        XCTAssertTrue(cloud.writes.isEmpty)
    }

    @MainActor func testLocalPushedWhenCloudEmpty() {
        let (s, cloud, d) = make()
        d.set("Nord", forKey: "theme")
        d.set(14.0, forKey: "fontSize")
        d.set(false, forKey: "fontLigatures")
        d.set("https://api.example.com", forKey: Polish.baseKey)
        s.start()
        XCTAssertEqual(cloud.values["theme"] as? String, "Nord")
        XCTAssertEqual(cloud.values["fontSize"] as? Double, 14.0)
        XCTAssertEqual(cloud.values["fontLigatures"] as? Double, 0.0)
        XCTAssertEqual(cloud.values[Polish.baseKey] as? String, "https://api.example.com")
        XCTAssertNil(cloud.values["scheme"])
    }

    @MainActor func testExternalChangeAppliedAndTold() {
        let (s, cloud, d) = make()
        d.set(12.0, forKey: "fontSize")
        s.start()
        cloud.writes = []
        let told = expectation(forNotification: .terminalFont, object: nil)
        cloud.values["fontSize"] = 18.0
        cloud.values["minimumContrast"] = 4.5
        s.cloudChanged(keys: ["fontSize"], reason: nil)
        XCTAssertEqual(d.double(forKey: "fontSize"), 18.0)
        wait(for: [told], timeout: 1)
        // Only the keys named.
        XCTAssertNil(d.object(forKey: "minimumContrast"))
        // What came from the cloud does not go back.
        s.flush()
        XCTAssertTrue(cloud.writes.isEmpty)
    }

    @MainActor func testAccountChangeAppliesAll() {
        let (s, cloud, d) = make()
        s.start()
        cloud.values = ["theme": "Nord", "minimumContrast": 4.5]
        s.cloudChanged(keys: [], reason: NSUbiquitousKeyValueStoreAccountChange)
        XCTAssertEqual(d.string(forKey: "theme"), "Nord")
        XCTAssertEqual(d.double(forKey: "minimumContrast"), 4.5)
    }

    /// The first download may lack what this device wrote before it: the
    /// local value is sent again, and kept.
    @MainActor func testInitialSyncRepushesLocal() {
        let (s, cloud, d) = make()
        d.set("Nord", forKey: "theme")
        s.start()
        cloud.values = [:]
        cloud.values["scheme"] = "light"
        s.cloudChanged(keys: ["scheme"], reason: NSUbiquitousKeyValueStoreInitialSyncChange)
        XCTAssertEqual(d.string(forKey: "theme"), "Nord")
        XCTAssertEqual(cloud.values["theme"] as? String, "Nord")
        XCTAssertEqual(d.string(forKey: "scheme"), "light")
    }

    /// Another device removing a key removes it here.
    @MainActor func testServerChangeRemovesLocal() {
        let (s, cloud, d) = make()
        cloud.values = ["theme": "Nord"]
        s.start()
        cloud.values["theme"] = nil
        s.cloudChanged(keys: ["theme"], reason: NSUbiquitousKeyValueStoreServerChange)
        XCTAssertNil(d.object(forKey: "theme"))
    }

    @MainActor func testLocalChangePushedAndRemoved() {
        let (s, cloud, d) = make()
        s.start()
        d.set("light", forKey: "scheme")
        s.flush()
        XCTAssertEqual(cloud.values["scheme"] as? String, "light")
        d.removeObject(forKey: "scheme")
        s.flush()
        XCTAssertNil(cloud.values["scheme"])
    }

    @MainActor func testUnsyncedKeyStaysLocal() {
        let (s, cloud, d) = make()
        d.set("alice-phone", forKey: "clientLabel")
        cloud.values["clientLabel"] = "bob-phone"
        s.start()
        d.set("small", forKey: "dictationModel")
        s.flush()
        s.cloudChanged(keys: ["clientLabel"], reason: nil)
        XCTAssertEqual(d.string(forKey: "clientLabel"), "alice-phone")
        XCTAssertEqual(cloud.values["clientLabel"] as? String, "bob-phone")
        XCTAssertNil(cloud.values["dictationModel"])
    }

    /// A change waits to settle: nothing is sent at once, and the last value is.
    @MainActor func testChangeIsSentWhenSettled() {
        let (s, cloud, d) = make()
        s.start()
        d.set(13.0, forKey: "fontSize")
        s.defaultsChanged()
        d.set(14.0, forKey: "fontSize")
        s.defaultsChanged()
        XCTAssertNil(cloud.values["fontSize"])
        s.flush()
        XCTAssertEqual(cloud.values["fontSize"] as? Double, 14.0)
        XCTAssertEqual(cloud.writes, ["fontSize"])
    }

    @MainActor func testNoWriteWhenEqual() {
        let (s, cloud, d) = make()
        cloud.values = ["theme": "Nord"]
        d.set("Nord", forKey: "theme")
        s.start()
        s.flush()
        s.cloudChanged(keys: ["theme"], reason: nil)
        XCTAssertTrue(cloud.writes.isEmpty)
    }
}
