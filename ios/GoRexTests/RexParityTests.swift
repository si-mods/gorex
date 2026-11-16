import XCTest

// The bundle compiles the app's sources itself (ios/project.yml): there is no
// GoRex module to import, and `@testable import GoRex` stopped the target
// building when #612 added it.
nonisolated final class RexParityTests: XCTestCase {
    @MainActor func testProtocolConstantsMatchRev14() {
        XCTAssertEqual(RexProtocol.apiRevision, 14)
        XCTAssertEqual(RexProtocol.downloadRevision, 13)
        XCTAssertEqual(RexProtocol.tunnelRevision, 14)
        XCTAssertEqual(RexProtocol.terminalProtocol, "rex.data.terminal.v1")
        XCTAssertEqual(RexProtocol.bytesProtocol, "rex.data.bytes.v1")
        XCTAssertEqual(RexProtocol.methodTerminalDownloadBegin, "com.superlogical.terminal.download_begin")
        XCTAssertEqual(RexProtocol.methodTunnelInfo, "com.superlogical.tunnel.info")
    }

    @MainActor func testDataHandshakeHelloResumeWelcomeAndReject() {
        let rawTicket = Data(repeating: 0xAA, count: 16)
        let ticket = rawTicket.base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
        let hello = DataHandshake.hello(ticket: ticket)
        XCTAssertEqual(hello, Data([0x01]) + rawTicket)

        let token = Data(repeating: 0xBB, count: 16)
        let resume = DataHandshake.resume(token: token, received: 1234)
        XCTAssertEqual(resume.count, 25)
        XCTAssertEqual(resume[0], 0x02)
        XCTAssertEqual(resume[1..<17], token)

        var welcomeBytes = [UInt8](repeating: 0, count: 32)
        welcomeBytes[0] = RexProtocol.dataWelcomeByte
        for i in 1..<17 { welcomeBytes[i] = 0xCC }
        welcomeBytes[20] = 0x88
        welcomeBytes[21] = 0x13 // 5000 ms LE
        welcomeBytes[24] = 42   // nonce = 42 LE
        let welcome = DataHandshake(Data(welcomeBytes))
        XCTAssertEqual(welcome, .welcome(token: Data(repeating: 0xCC, count: 16), keepMillis: 5000, nonce: 42))

        let reject = DataHandshake(Data([RexProtocol.dataRejectByte, 0x04]))
        XCTAssertEqual(reject, .reject(reason: 0x04))
    }
}
