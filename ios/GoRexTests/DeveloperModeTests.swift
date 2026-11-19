import Foundation
import XCTest

nonisolated final class DeveloperModeTests: XCTestCase {
    @MainActor func testConnectionLogNoteAndClear() {
        ConnectionLog.clear()
        XCTAssertTrue(ConnectionLog.entries.isEmpty)
        XCTAssertTrue(ConnectionLog.lines.isEmpty)

        ConnectionLog.note("first event")
        ConnectionLog.note("second event")
        XCTAssertEqual(ConnectionLog.entries.count, 2)
        XCTAssertEqual(ConnectionLog.entries[0].message, "first event")
        XCTAssertEqual(ConnectionLog.entries[1].message, "second event")
        XCTAssertEqual(ConnectionLog.lines.count, 2)
        XCTAssertTrue(ConnectionLog.lines[0].hasSuffix(" first event"))

        ConnectionLog.clear()
        XCTAssertTrue(ConnectionLog.entries.isEmpty)
        XCTAssertTrue(ConnectionLog.lines.isEmpty)
    }

    @MainActor func testNetworkSummaryLineVariants() {
        let base = Date(timeIntervalSince1970: 1_700_000_000)
        let now = base.addingTimeInterval(81.34)
        var snap = NetworkDiagnostics.Snapshot(
            isSatisfied: true,
            transportLabel: "Wi-Fi + VPN",
            interfaces: ["en0 Wi-Fi", "pdp_ip0 cellular", "utun5 tunnel"],
            supportsIPv4: true,
            supportsIPv6: false,
            tailscaleInterface: nil,
            changedAt: base
        )

        XCTAssertEqual(
            snap.summaryLine(now: now, tailnetOn: false, tailnetState: "Stopped", tailnetIP: nil),
            "Wi-Fi + VPN · en0 Wi-Fi, pdp_ip0 cellular, utun5 tunnel · IPv4 · for 81.3s · no Tailscale interface"
        )

        snap.tailscaleInterface = "utun4"
        XCTAssertEqual(
            snap.summaryLine(now: now, tailnetOn: false, tailnetState: "Stopped", tailnetIP: nil),
            "Wi-Fi + VPN · en0 Wi-Fi, pdp_ip0 cellular, utun5 tunnel · IPv4 · for 81.3s · Tailscale utun4"
        )

        snap.supportsIPv6 = true
        XCTAssertEqual(
            snap.summaryLine(now: now, tailnetOn: true, tailnetState: "Running", tailnetIP: "100.64.1.2"),
            "Wi-Fi + VPN · en0 Wi-Fi, pdp_ip0 cellular, utun5 tunnel · IPv4 + IPv6 · for 81.3s · Tailnet Running (100.64.1.2)"
        )
    }

    @MainActor func testSummaryEmptyAndDemoReady() {
        let now = Date.now
        let empty = DeveloperModeSummary.compute(hosts: [], now: now)
        XCTAssertEqual(empty.tone, .idle)
        XCTAssertEqual(empty.primaryText, "No hosts")
        XCTAssertNil(empty.secondaryText)
        XCTAssertFalse(empty.hasActiveTimer)

        let demo = Host(Demo.config())
        demo.connect()
        let ready = DeveloperModeSummary.compute(hosts: [demo], now: now)
        XCTAssertEqual(ready.tone, .ready)
        XCTAssertEqual(ready.primaryText, "1 ready")
        XCTAssertFalse(ready.hasActiveTimer)
    }

    @MainActor func testHostTelemetryAndPingAgainstFakeRex() async throws {
        ConnectionLog.clear()
        let rex = try FakeRex(pongs: true)
        defer { rex.stop() }
        rex.respond = { method, _ in method == "session.bootstrap" ? ["attach_all": ["sessions": []]] : [:] }

        let host = Host(HostConfig(name: "Test Mac", url: try await rex.url()))
        defer { host.stop() }
        host.connect()

        let control = try await host.online()
        XCTAssertEqual(host.devStatus, .ready)
        XCTAssertNotNil(host.connectDurationMs)
        XCTAssertNil(host.reconnectSince)

        let rtt = await control.measurePing()
        XCTAssertNotNil(rtt)
        XCTAssertNotNil(host.pingMs)

        let summary = DeveloperModeSummary.compute(hosts: [host], now: .now)
        XCTAssertEqual(summary.tone, .ready)
        XCTAssertEqual(summary.primaryText, "1 ready")
        XCTAssertEqual(summary.secondaryText, "\(host.pingMs!) ms")

        let report = DeveloperModeSummary.diagnosticReport(
            version: "1.1",
            build: "0",
            system: "iOS 26.0",
            networkLine: "Wi-Fi · en0 Wi-Fi · IPv4 · for 1.0s · no Tailscale interface",
            hosts: [host],
            entries: ConnectionLog.entries
        )
        XCTAssertTrue(report.contains("Test Mac"))
        XCTAssertTrue(report.contains("connect \(host.connectDurationMs!) ms"))
        XCTAssertTrue(report.contains("ping \(host.pingMs!) ms"))
        XCTAssertTrue(report.contains("Network\nWi-Fi"))
    }
}
