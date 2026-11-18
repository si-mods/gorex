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

/// The settings that sync through iCloud (docs/ios-icloud-sync.md): which
/// side wins, what goes each way, and what stays on the device.
nonisolated final class CloudSettingsTests: XCTestCase {
    @MainActor private func make(maple: Bool = false) -> (CloudSettings, FakeCloud, UserDefaults) {
        let suite = "cloud-\(UUID().uuidString)"
        let d = UserDefaults(suiteName: suite)!
        addTeardownBlock { UserDefaults().removePersistentDomain(forName: suite) }
        let cloud = FakeCloud()
        return (CloudSettings(cloud: cloud, defaults: d, installed: { maple }), cloud, d)
    }

    @MainActor func testCloudWinsAtStart() {
        let (s, cloud, d) = make()
        cloud.values = ["theme": "Nord", "fontSize": 15.0, "scheme": "light"]
        d.set("Gruvbox Dark", forKey: "theme")
        d.set(12.0, forKey: "fontSize")
        s.start()
        XCTAssertEqual(d.string(forKey: "theme"), "Nord")
        XCTAssertEqual(d.double(forKey: "fontSize"), 15.0)
        XCTAssertEqual(d.string(forKey: "scheme"), "light")
        XCTAssertEqual(cloud.values["theme"] as? String, "Nord")
        XCTAssertTrue(cloud.writes.isEmpty)
    }

    @MainActor func testLocalPushedWhenCloudEmpty() {
        let (s, cloud, d) = make()
        d.set("Nord", forKey: "theme")
        d.set(14.0, forKey: "fontSize")
        d.set("https://api.example.com", forKey: Polish.baseKey)
        s.start()
        XCTAssertEqual(cloud.values["theme"] as? String, "Nord")
        XCTAssertEqual(cloud.values["fontSize"] as? Double, 14.0)
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
        s.defaultsChanged()
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

    @MainActor func testLocalChangePushedAndRemoved() {
        let (s, cloud, d) = make()
        s.start()
        d.set("light", forKey: "scheme")
        s.defaultsChanged()
        XCTAssertEqual(cloud.values["scheme"] as? String, "light")
        d.removeObject(forKey: "scheme")
        s.defaultsChanged()
        XCTAssertNil(cloud.values["scheme"])
    }

    @MainActor func testUnsyncedKeyStaysLocal() {
        let (s, cloud, d) = make()
        d.set("alice-phone", forKey: "clientLabel")
        cloud.values["clientLabel"] = "bob-phone"
        s.start()
        d.set("small", forKey: "dictationModel")
        s.defaultsChanged()
        s.cloudChanged(keys: ["clientLabel"], reason: nil)
        XCTAssertEqual(d.string(forKey: "clientLabel"), "alice-phone")
        XCTAssertEqual(cloud.values["clientLabel"] as? String, "bob-phone")
        XCTAssertNil(cloud.values["dictationModel"])
    }

    @MainActor func testMapleOnlyWhereInstalled() {
        for installed in [false, true] {
            let (s, cloud, d) = make(maple: installed)
            d.set("Menlo", forKey: "fontFamily")
            cloud.values["fontFamily"] = Maple.family
            s.start()
            XCTAssertEqual(d.string(forKey: "fontFamily"), installed ? Maple.family : "Menlo", "installed \(installed)")
            // The cloud keeps it for the devices that have the font.
            s.defaultsChanged()
            XCTAssertEqual(cloud.values["fontFamily"] as? String, Maple.family)
        }
    }

    /// Deleting Maple Mono on one device leaves it on the others.
    @MainActor func testMapleDeleteStaysLocal() {
        var installed = true
        let suite = "cloud-\(UUID().uuidString)"
        let d = UserDefaults(suiteName: suite)!
        addTeardownBlock { UserDefaults().removePersistentDomain(forName: suite) }
        let cloud = FakeCloud()
        let s = CloudSettings(cloud: cloud, defaults: d, installed: { installed })
        d.set(Maple.family, forKey: "fontFamily")
        s.start()
        XCTAssertEqual(cloud.values["fontFamily"] as? String, Maple.family)
        installed = false
        d.removeObject(forKey: "fontFamily")
        s.defaultsChanged()
        XCTAssertEqual(cloud.values["fontFamily"] as? String, Maple.family)
    }

    @MainActor func testNoWriteWhenEqual() {
        let (s, cloud, d) = make()
        cloud.values = ["theme": "Nord"]
        d.set("Nord", forKey: "theme")
        s.start()
        s.defaultsChanged()
        s.cloudChanged(keys: ["theme"], reason: nil)
        XCTAssertTrue(cloud.writes.isEmpty)
    }

    /// One round trip of the API key through the real Keychain, where the
    /// test runs with one; the key is not left behind.
    @MainActor func testKeyRoundTrip() throws {
        let before = Polish.savedKey
        defer { Polish.savedKey = before }
        Polish.savedKey = "test-key-AAAA"
        if Polish.savedKey == nil { throw XCTSkip("no Keychain in this test process") }
        XCTAssertEqual(Polish.savedKey, "test-key-AAAA")
        Polish.savedKey = "test-key-BBBB"
        XCTAssertEqual(Polish.savedKey, "test-key-BBBB")
        Polish.savedKey = nil
        XCTAssertNil(Polish.savedKey)
    }
}
