import Foundation
import Network
import XCTest

nonisolated final class DeviceAccessTests: XCTestCase {
    @MainActor func testSessionPreservesTailnetProxy() {
        let config = URLSessionConfiguration.ephemeral
        var proxy = ProxyConfiguration(socksv5Proxy: .hostPort(host: "127.0.0.1", port: 12345))
        proxy.applyCredential(username: "tsnet", password: "synthetic-test-password")
        config.proxyConfigurations = [proxy]
        let session = DeviceAccess.makeSession(configuration: config)
        defer { session.invalidateAndCancel() }
        XCTAssertEqual(session.configuration.proxyConfigurations.count, 1)
        XCTAssertFalse(session.configuration.httpShouldSetCookies)
        XCTAssertNotNil(session.delegate)
    }

    @MainActor func testInvitationRemovesSecretFromAddress() throws {
        let code = String(repeating: "a", count: 43)
        let pairing = try XCTUnwrap(DevicePairing("https://terminal.example/#gorex-pair=\(code)"))
        XCTAssertEqual(pairing.base.absoluteString, "https://terminal.example")
        XCTAssertNil(pairing.base.fragment)
        XCTAssertEqual(pairing.code, code)
        for bad in [
            "http://terminal.example/#gorex-pair=\(code)",
            "https://user@terminal.example/#gorex-pair=\(code)",
            "https://terminal.example/other#gorex-pair=\(code)",
            "https://terminal.example?x=1#gorex-pair=\(code)",
            "https://terminal.example/#gorex-pair=short",
            "https://terminal.example/#gorex-pair=\(code)+",
        ] { XCTAssertNil(DevicePairing(bad), bad) }
    }

    @MainActor func testPlainHTTPNeverLoadsDeviceCredentials() throws {
        let base = try XCTUnwrap(URL(string: "http://127.0.0.1:7297"))
        XCTAssertNil(DeviceAccess.credential(for: base))
        XCTAssertNil(URLRequest(url: base).withDeviceCredential(for: base).value(forHTTPHeaderField: "Authorization"))
        XCTAssertThrowsError(try DeviceAccess.save("credential", for: base))
    }

    @MainActor func testCredentialsUseCanonicalOrigin() throws {
        let first = try XCTUnwrap(URL(string: "https://terminal.example"))
        XCTAssertTrue(DeviceAccess.sameOrigin(first, try XCTUnwrap(URL(string: "https://TERMINAL.example:443/"))))
        XCTAssertFalse(DeviceAccess.sameOrigin(first, try XCTUnwrap(URL(string: "https://terminal.example:7298"))))
        XCTAssertFalse(DeviceAccess.sameOrigin(first, try XCTUnwrap(URL(string: "http://terminal.example"))))
    }
}
