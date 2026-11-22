import Foundation
import XCTest

/// The push tokens' coordinator (docs/NET-003-push-relay.md, Register
/// and bind): a token binds at the host's relay once both the token and
/// the register's target are there, in either order, and a token binds
/// only to its own host's target.
nonisolated final class PushTokensTests: XCTestCase {
    /// The bind and unbind calls the coordinator made, in order.
    @MainActor final class FakeRelay: PushRelay {
        var binds: [(token: String, target: PushTarget)] = []
        var unbinds: [(token: String, target: PushTarget)] = []
        func bind(token: String, target: PushTarget) async { binds.append((token, target)) }
        func unbind(token: String, target: PushTarget) async { unbinds.append((token, target)) }
    }

    private static let defaultRelay = URL(string: "https://relay.gorex.uk")!

    private static func target(_ key: String, relay: URL = defaultRelay) -> PushTarget {
        PushTarget(key: key, relay: relay)
    }

    /// The token came first: the register's target binds it now, once.
    @MainActor func testTokenBeforeTargetBindsOnRegister() async {
        let r = FakeRelay(), p = PushTokens(keys: MemoryPushKeys(), relay: r)
        let h = UUID()
        p.bind(tokenHex: "aabb", host: h)
        XCTAssertTrue(r.binds.isEmpty, "no target yet")
        p.set(host: h, target: Self.target("hk"))
        await until("the bind") { r.binds.count == 1 }
        XCTAssertEqual(r.binds.first?.token, "aabb")
        XCTAssertEqual(r.binds.first?.target, Self.target("hk"))
    }

    /// The target came first: a token binds as it arrives, once.
    @MainActor func testTargetBeforeTokenBindsOnToken() async {
        let r = FakeRelay(), p = PushTokens(keys: MemoryPushKeys(), relay: r)
        let h = UUID()
        p.set(host: h, target: Self.target("hk"))
        XCTAssertTrue(r.binds.isEmpty, "no token yet")
        p.bind(tokenHex: "aabb", host: h)
        await until("the bind") { r.binds.count == 1 }
        // A rotated token binds again, to the same target.
        p.bind(tokenHex: "ccdd", host: h)
        await until("the next token's bind") { r.binds.count == 2 }
        XCTAssertEqual(r.binds.map(\.token), ["aabb", "ccdd"])
    }

    /// A token binds to its own host's target alone: another host's is
    /// not it, and dropping a host unbinds its token.
    @MainActor func testTokenBindsToOwnHost() async {
        let r = FakeRelay(), p = PushTokens(keys: MemoryPushKeys(), relay: r)
        let a = UUID(), b = UUID()
        p.set(host: b, target: Self.target("b-key"))
        p.bind(tokenHex: "aa", host: a)
        try? await Task.sleep(for: .milliseconds(100))
        XCTAssertTrue(r.binds.isEmpty, "host A's token is not host B's to bind")
        p.set(host: a, target: Self.target("a-key"))
        await until("the bind") { r.binds.count == 1 }
        XCTAssertEqual(r.binds.first?.target.key, "a-key")

        p.drop(host: a)
        await until("the unbind") { r.unbinds.count == 1 }
        XCTAssertEqual(r.unbinds.first?.token, "aa")
        XCTAssertEqual(r.unbinds.first?.target.key, "a-key")
        XCTAssertNil(p.targets[a])
        XCTAssertNil(p.tokens[a])
    }

    /// Over a real control connection: the register carries the device
    /// key and the target, and its answer — the host's push key and the
    /// relay — reaches the coordinator. A server without gorex.push gets
    /// no call.
    @MainActor func testRegisterOverControl() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        rex.features = ["gorex.push"]
        rex.respond = { m, _ in
            switch m {
            case "session.bootstrap": ["attach_all": ["sessions": []]]
            case "gorex.push.register": ["host": "aG9zdA", "relay": "https://relay.example"]
            default: [:]
            }
        }
        let keys = MemoryPushKeys(), r = FakeRelay()
        let p = PushTokens(keys: keys, relay: r)
        let was = Host.pushTokens
        Host.pushTokens = { p }
        defer { Host.pushTokens = was }
        let h = Host(HostConfig(name: "test", url: try await rex.url()))
        h.connect()
        defer { h.stop() }
        await until("registered") { rex.payload("gorex.push.register") != nil }
        let args = try XCTUnwrap(rex.payload("gorex.push.register"))
        XCTAssertEqual(args["device"] as? String, keys.publicKeyB64)
        XCTAssertNotNil(args["live_activities"] as? Bool)
        XCTAssertNil(args["session_id"], "no terminal on screen: no target")
        await until("the target") { p.targets[h.id] != nil }
        XCTAssertEqual(p.targets[h.id], PushTarget(key: "aG9zdA", relay: URL(string: "https://relay.example")!))

        // The terminal on screen: session and block go with the register.
        h.setPushTarget(session: "s1", block: "b1")
        await until("the target's register") {
            rex.payloads(of: "gorex.push.register").last?["session_id"] as? String == "s1"
        }
        XCTAssertEqual(rex.payloads(of: "gorex.push.register").last?["block_id"] as? String, "b1")

        // A token binds to this host's target, at the relay it gave.
        p.bind(tokenHex: "ee", host: h.id)
        await until("the bind") { r.binds.count == 1 }
        XCTAssertEqual(r.binds.first?.target.relay.absoluteString, "https://relay.example")
    }

    /// A server without gorex.push gets no register, and the demo host
    /// none either.
    @MainActor func testNoFeatureNoRegister() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        rex.respond = { m, _ in m == "session.bootstrap" ? ["attach_all": ["sessions": []]] : [:] }
        let keys = MemoryPushKeys(), r = FakeRelay()
        let p = PushTokens(keys: keys, relay: r)
        let was = Host.pushTokens
        Host.pushTokens = { p }
        defer { Host.pushTokens = was }
        let h = Host(HostConfig(name: "test", url: try await rex.url()))
        h.connect()
        defer { h.stop() }
        await until("online") { h.status == .online }
        h.setPushTarget(session: "s1", block: "b1")
        try? await Task.sleep(for: .milliseconds(100))
        XCTAssertEqual(rex.count("gorex.push.register"), 0)
        XCTAssertNil(p.targets[h.id])
    }
}
