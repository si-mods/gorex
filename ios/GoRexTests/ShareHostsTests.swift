import Foundation
import XCTest

/// The hosts HotspotShare sends the share server (ShareHosts): each one's
/// transport is the way the app reaches it now, not the stored switch —
/// a tailcat host's is nil there, and the demo host is left out.
nonisolated final class ShareHostsTests: XCTestCase {
    private struct Row: Decodable {
        let id: String
        let name: String
        let icon: String
        let url: String
        let transport: String
        let token: String?
    }

    @MainActor
    private func rows(_ configs: [HostConfig], tailnetOn: Bool = false) throws -> [Row] {
        let json = try XCTUnwrap(ShareHosts.json(configs, tailnetOn: tailnetOn))
        return try JSONDecoder().decode([Row].self, from: Data(json.utf8))
    }

    /// A host paired by its Rex Go code reaches the share over tailcat, as
    /// the app reaches it; its stored transport is nil, which read as
    /// "system" sent the proxy's dial to <id>.tailcat.invalid (#556).
    @MainActor
    func testTailcatHostSharesAsTailcat() throws {
        let c = try XCTUnwrap(HostConfig(tailcat: "rexgo://tailcat?a=tcABC123&n=mac"))
        let r = try rows([c])
        XCTAssertEqual(r.count, 1)
        XCTAssertEqual(r[0].id, c.id.uuidString)
        XCTAssertEqual(r[0].transport, "tailcat")
        XCTAssertEqual(r[0].url, c.url.absoluteString)
        XCTAssertNil(r[0].token)
    }

    /// A host marked tailnet goes through the engine only while the built-in
    /// Tailscale is on; with it off the app and so the share go the system's
    /// way (the Tailscale app's VPN, or a LAN address).
    @MainActor
    func testTailnetHostFollowsTheSwitch() throws {
        let c = HostConfig(name: "mac", url: URL(string: "https://mac.tail1234.ts.net")!, transport: .tailnet)
        XCTAssertEqual(try rows([c], tailnetOn: true)[0].transport, "tailnet")
        XCTAssertEqual(try rows([c], tailnetOn: false)[0].transport, "system")
    }

    /// A LAN host keeps its pairing token for the proxy to send as Bearer;
    /// the browser never sees it.
    @MainActor
    func testLanHostKeepsItsToken() throws {
        let c = HostConfig(name: "mac", url: URL(string: "http://192.168.1.2:7297")!, lanToken: "tok-abc_123")
        let r = try rows([c])
        XCTAssertEqual(r[0].transport, "system")
        XCTAssertEqual(r[0].token, "tok-abc_123")
    }

    /// The demo host answers nothing off the phone, so the shared list
    /// leaves it out.
    @MainActor
    func testDemoHostIsLeftOut() throws {
        let demo = HostConfig(name: "Demo", url: URL(string: "http://demo-mac.gorex.invalid:7297")!, demo: true)
        let real = HostConfig(name: "mac", url: URL(string: "http://192.168.1.2:7297")!)
        XCTAssertEqual(try rows([demo, real]).map(\.name), ["mac"])
    }
}
