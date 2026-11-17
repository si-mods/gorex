import Foundation
import XCTest

/// Tokens in memory, for a store under test.
final class MemoryLANTokens: LANTokens {
    var tokens: [UUID: String] = [:]
    func token(for host: UUID) -> String? { tokens[host] }
    func set(_ token: String?, for host: UUID) { tokens[host] = token }
}

/// The LAN gateway's pairing token: read from the code's #k=, kept in the
/// Keychain and not in the hosts' list, and sent as a bearer only to the
/// host it came with.
nonisolated final class LANTokenTests: XCTestCase {
    static let token = "test-token-AAAA_bbbb-0123456789abcdefghijklmnop"

    @MainActor func testPairingCodeWithToken() throws {
        for code in ["http://192.0.2.10:7297/#k=\(Self.token)", "http://192.0.2.10:7297#k=\(Self.token)\n"] {
            let c = try XCTUnwrap(HostConfig(pairing: code), code)
            XCTAssertEqual(c.url.absoluteString, "http://192.0.2.10:7297", code)
            XCTAssertEqual(c.name, "192.0.2.10")
            XCTAssertEqual(c.lanToken, Self.token)
            XCTAssertEqual(c.auth, Self.token)
        }
        // An older code has none.
        XCTAssertNil(try XCTUnwrap(HostConfig(pairing: "http://192.0.2.10:7297")).lanToken)

        for bad in ["http://192.0.2.10:7297/#k=", "http://192.0.2.10:7297/#x=abc", "http://192.0.2.10:7297/#k=a+b",
                    "http://192.0.2.10:7297/#k=abc=", "http://192.0.2.10:7297/#k=abc&x=1", "http://192.0.2.10:7297/#abc",
                    "http://192.0.2.10:7297/api#k=abc", "http://192.0.2.10:7297/?k=abc", "http://192.0.2.10:7297/?x=1#k=abc",
                    "http://user@192.0.2.10:7297/#k=abc", "http://192.0.2.10:7297/#k=" + String(repeating: "a", count: 257)] {
            XCTAssertNil(HostConfig(pairing: bad), bad)
        }
    }

    /// The list saved never holds the token, nor reads one back.
    @MainActor func testTokenNotEncoded() throws {
        let c = try XCTUnwrap(HostConfig(pairing: "http://192.0.2.10:7297/#k=\(Self.token)"))
        let json = String(decoding: try JSONEncoder().encode([c]), as: UTF8.self)
        XCTAssertFalse(json.contains(Self.token), json)
        XCTAssertFalse(json.contains("lanToken"), json)
        let back = try JSONDecoder().decode([HostConfig].self, from: Data(json.utf8))
        XCTAssertEqual(back.first?.url, c.url)
        XCTAssertNil(back.first?.lanToken)
    }

    /// The store keeps the token apart, reads it back for the host, and
    /// forgets it with the host; the host's connection carries it.
    @MainActor func testStoreKeepsTokenApart() async throws {
        let d = UserDefaults.standard, saved = d.data(forKey: "hosts")
        defer { d.set(saved, forKey: "hosts") }
        d.removeObject(forKey: "hosts")
        let http = try FakeHTTP()
        defer { http.stop() }
        http.respond = { _ in (401, ["error": "token"]) }
        let tokens = MemoryLANTokens()
        let store = HostStore(tokens: tokens)
        var c = try XCTUnwrap(HostConfig(pairing: "http://192.0.2.10:7297/#k=\(Self.token)"))
        c.url = try await http.url()
        store.add(c)
        XCTAssertEqual(tokens.tokens[c.id], Self.token)
        let list = String(decoding: try XCTUnwrap(d.data(forKey: "hosts")), as: UTF8.self)
        XCTAssertFalse(list.contains(Self.token), list)
        await until("the control connection's upgrade") { !http.requests.isEmpty }
        XCTAssertEqual(http.requests.first?.header("Authorization"), "Bearer \(Self.token)")

        let again = HostStore(tokens: tokens)
        XCTAssertEqual(again.host(c.id)?.config.lanToken, Self.token)
        again.hosts.forEach { $0.stop() }

        store.remove(c.id)
        XCTAssertNil(tokens.tokens[c.id])
    }

    /// The bearer goes on a request only for a host with a token: a
    /// tailcat host's never carries one, even with a token set.
    @MainActor func testBearerOnlyForTokenHost() throws {
        let r = URLRequest(url: URL(string: "http://192.0.2.10:7297/api/spaces")!)
        XCTAssertEqual(r.authorized("test-token-AAAA").value(forHTTPHeaderField: "Authorization"), "Bearer test-token-AAAA")
        XCTAssertNil(r.authorized(nil).value(forHTTPHeaderField: "Authorization"))
        XCTAssertNil(r.authorized("").value(forHTTPHeaderField: "Authorization"))

        var t = try XCTUnwrap(HostConfig(tailcat: "rexgo://tailcat?a=tcx&n=mac"))
        t.lanToken = "test-token-AAAA"
        XCTAssertNil(t.auth)
        XCTAssertNil(r.authorized(t.auth).value(forHTTPHeaderField: "Authorization"))
        XCTAssertNil(try XCTUnwrap(HostConfig(pairing: "https://mac-gorex.example.ts.net")).auth)
    }

    /// HTTP requests (/api/spaces) and WebSocket upgrades (/api/control)
    /// carry the bearer to the server.
    @MainActor func testRequestsCarryBearer() async throws {
        let http = try FakeHTTP()
        defer { http.stop() }
        http.respond = { _ in (401, ["error": "token"]) }
        let base = try await http.url()
        _ = try? await Spaces.read(from: base, auth: "test-token-AAAA")
        let c = ControlConnection(base: base, auth: "test-token-AAAA")
        _ = try? await c.open(token: nil)
        _ = try? await Spaces.read(from: base, auth: nil)
        await until("three requests") { http.requests.count == 3 }
        XCTAssertEqual(http.requests.map { $0.header("Authorization") },
                       ["Bearer test-token-AAAA", "Bearer test-token-AAAA", nil])
        XCTAssertTrue(http.requests[1].head.hasPrefix("GET /api/control"))
    }

    /// One round trip through the real Keychain, where the test runs with
    /// one.
    @MainActor func testKeychainRoundTrip() throws {
        let k = KeychainLANTokens(), id = UUID()
        let status = k.store("test-token-AAAA", for: id)
        if status == errSecMissingEntitlement { throw XCTSkip("no Keychain in this test process (\(status))") }
        XCTAssertEqual(status, errSecSuccess)
        XCTAssertEqual(k.token(for: id), "test-token-AAAA")
        k.set("test-token-BBBB", for: id)
        XCTAssertEqual(k.token(for: id), "test-token-BBBB")
        k.set(nil, for: id)
        XCTAssertNil(k.token(for: id))
    }
}
