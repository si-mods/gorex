import XCTest

/// The URL a port opens (docs/IOS-014-pane-ports.md).
nonisolated final class PortsTests: XCTestCase {
    @MainActor private func port(_ address: String, _ number: Int = 5173) -> Ports.Port {
        Ports.Port(port: number, address: address, `protocol`: "tcp", first_seen_at: nil, pid: nil)
    }

    @MainActor func testReachableAddressesOpen() {
        XCTAssertEqual(Ports.openURL(port: port("192.0.2.10"), hostAddress: nil)?.absoluteString,
                       "http://192.0.2.10:5173/")
        XCTAssertEqual(Ports.openURL(port: port("0.0.0.0"), hostAddress: "studio.example")?.absoluteString,
                       "http://studio.example:5173/")
        XCTAssertEqual(Ports.openURL(port: port("::"), hostAddress: "192.0.2.10")?.absoluteString,
                       "http://192.0.2.10:5173/")
    }

    @MainActor func testLoopbackStaysOnTheHost() {
        XCTAssertNil(Ports.openURL(port: port("127.0.0.1"), hostAddress: "studio.example"))
        XCTAssertNil(Ports.openURL(port: port("::1"), hostAddress: "studio.example"))
    }

    @MainActor func testWildcardWithNoHostAddressDoesNotOpen() {
        XCTAssertNil(Ports.openURL(port: port("0.0.0.0"), hostAddress: nil))
        XCTAssertNil(Ports.openURL(port: port("0.0.0.0"), hostAddress: ""))
    }

    @MainActor func testIPv6AddressIsBracketed() {
        XCTAssertEqual(Ports.openURL(port: port("fe80::1"), hostAddress: nil)?.absoluteString,
                       "http://[fe80::1]:5173/")
    }
}
