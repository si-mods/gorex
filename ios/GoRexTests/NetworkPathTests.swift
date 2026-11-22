import XCTest

/// The reconnect on a change of network (docs/IOS-012-background-and-network-changes.md):
/// when NetworkPath asks for one, and what HostStore resumes. The path and
/// the waits are the test's: no time passes by itself.
nonisolated final class NetworkPathTests: XCTestCase {
    typealias Waits = TerminalLiveTests.Waits

    /// A path the test tells networks on.
    @MainActor final class Path {
        var told: (@MainActor @Sendable (String, String) -> Void)?
        func source(_ t: @escaping @MainActor @Sendable (String, String) -> Void) -> AnyObject {
            told = t
            return self
        }
        func tell(_ name: String, gateway: String = "gw") { told?(name, "\(name) \(gateway)") }
    }

    /// The first network is no change; a change reconnects at once; the
    /// changes in the second after it give one more at its end, not one
    /// each; after that second, a change again reconnects at once.
    @MainActor func testChangesReconnectAtOnceThenOnceMore() {
        let p = Path(), w = Waits()
        let n = NetworkPath(source: p.source, schedule: w.schedule)
        n.start()
        p.tell("en0")
        XCTAssertEqual(n.changes, 0, "the first network is no change")
        p.tell("en0")
        XCTAssertEqual(n.changes, 0, "the same network")

        p.tell("pdp_ip0")
        XCTAssertEqual(n.changes, 1, "the first change, at once")
        p.tell("en0")
        p.tell("en0", gateway: "gw2")
        XCTAssertEqual(n.changes, 1, "changes in the second wait for its end")
        w.run()
        XCTAssertEqual(n.changes, 2, "one more for the second's changes")
        w.run()
        XCTAssertEqual(n.changes, 2, "nothing changed in the next second")

        p.tell("")
        XCTAssertEqual(n.changes, 2, "no network is no reconnect: there is nothing to dial")
        p.tell("en0")
        XCTAssertEqual(n.changes, 3, "from no network to a network, at once")
        w.run()
        XCTAssertEqual(n.changes, 3)
    }

    /// A change to no network in the second after a reconnect drops the
    /// change waiting for the second's end; the one back to a network
    /// after it reconnects.
    @MainActor func testNoNetworkDropsPendingChange() {
        let p = Path(), w = Waits()
        let n = NetworkPath(source: p.source, schedule: w.schedule)
        n.start()
        p.tell("en0")
        p.tell("pdp_ip0")
        XCTAssertEqual(n.changes, 1)
        p.tell("en0")
        p.tell("")
        w.run()
        XCTAssertEqual(n.changes, 1, "the pending change to a network is dropped")
        p.tell("en0")
        XCTAssertEqual(n.changes, 2, "back to a network, at once")
    }

    /// A second start watches no second path.
    @MainActor func testStartsOnce() {
        var started = 0
        let n = NetworkPath(source: { _ in started += 1; return NSObject() }, schedule: { _, _ in })
        n.start()
        n.start()
        XCTAssertEqual(started, 1)
    }

    /// Out of the background a change of network resumes the real hosts
    /// the system's way reaches — a connection behind the engine rides
    /// the proxy's loopback, which no path change strands — and those
    /// waiting to retry, whatever their reach; not the demo host nor one
    /// idle; in the background, none. HostStore has no hold of the
    /// tailnet engine (TailnetEngine is not in this target), so the
    /// change cannot reset it.
    @MainActor func testResumedOnNetworkChange() async throws {
        let demo = Host(Demo.config())
        demo.connect()
        defer { demo.stop() }
        let rex = try FakeRex()
        defer { rex.stop() }
        let h = Host(HostConfig(name: "alice-laptop", url: try await rex.url()))
        let idle = Host(HostConfig(name: "bob-desktop", url: try await rex.url()))
        XCTAssertEqual(h.config.reach(tailnetOn: false), .system)
        h.connect()
        defer { h.stop() }
        await until("online") { h.status == .online }

        XCTAssertEqual(HostStore.resumedOnNetworkChange([demo, h, idle], inBackground: false, tailnetOn: true).map(\.id), [h.id])
        XCTAssertTrue(HostStore.resumedOnNetworkChange([demo, h, idle], inBackground: true).isEmpty)

        // A connected host the engine reaches is left to it: the change
        // cannot strand the proxy's loopback connection.
        h.config.transport = .tailnet
        XCTAssertTrue(HostStore.resumedOnNetworkChange([h], inBackground: false, tailnetOn: true).isEmpty)
        XCTAssertEqual(HostStore.resumedOnNetworkChange([h], inBackground: false, tailnetOn: false).map(\.id), [h.id])
        h.config.transport = nil
        h.config.tailcat = HostConfig.Tailcat(address: "tcx", machine: "mac")
        XCTAssertTrue(HostStore.resumedOnNetworkChange([h], inBackground: false, tailnetOn: true).isEmpty)
        XCTAssertTrue(HostStore.resumedOnNetworkChange([h], inBackground: false, tailnetOn: false).isEmpty)

        // A host that cannot connect waits in its retry backoff, and a
        // change dials it at once, whatever its reach.
        let down = Host(HostConfig(name: "carol-server", url: try XCTUnwrap(URL(string: "http://127.0.0.1:1"))))
        down.connect()
        defer { down.stop() }
        await until("retrying") { down.isRetrying }
        XCTAssertEqual(HostStore.resumedOnNetworkChange([idle, down], inBackground: false, tailnetOn: true).map(\.id), [down.id])
    }

    /// The store's hosts connect anew on a change in the foreground, on a
    /// new control connection; in the background they keep theirs.
    @MainActor func testNetworkChangedResumesHosts() async throws {
        let saved = UserDefaults.standard.data(forKey: "hosts")
        defer { UserDefaults.standard.set(saved, forKey: "hosts") }
        UserDefaults.standard.removeObject(forKey: "hosts")
        let r = try FakeRex()
        defer { r.stop() }
        r.respond = { m, _ in m == "session.bootstrap" ? ["attach_all": ["sessions": []]] : [:] }
        let store = HostStore()
        let config = HostConfig(name: "alice-laptop", url: try await r.url())
        store.add(config)
        defer { store.remove(config.id) }
        store.enterDemo()
        defer { store.exitDemo() }
        let h = try XCTUnwrap(store.host(config.id))
        await until("online") { h.status == .online }
        let first = try XCTUnwrap(h.control)

        store.networkChanged(inBackground: true)
        XCTAssertTrue(h.control === first, "the background keeps the connection")

        store.networkChanged(inBackground: false)
        XCTAssertFalse(h.control === first, "a new connection")
        await until("online again") { h.status == .online && r.count("session.bootstrap") == 2 }
    }
}
