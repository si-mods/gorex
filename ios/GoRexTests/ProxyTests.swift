import XCTest

/// The proxy's re-read on a failed dial (docs/IOS-002-tailcat.md, the
/// proxy): iOS may take the listener it keeps in a suspension that no
/// resume() has seen yet, and dials through it fail at once until then.
nonisolated final class ProxyTests: XCTestCase {
    /// A tailcat host's dial through a dead proxy fails and asks the
    /// engine for a new one; a system host's failed dial does not.
    @MainActor func testFailedProxyDialAsksForAProxy() async throws {
        let (proxy, failed) = (Host.tailcatProxy, Host.proxyFailed)
        defer {
            Host.tailcatProxy = proxy
            Host.proxyFailed = failed
        }
        var calls = 0
        Host.proxyFailed = { calls += 1 }
        // A proxy that is not there: the dial through it fails at once.
        Host.tailcatProxy = { LoopbackProxy(port: 1, password: "x", run: 0) }
        let tailcat = Host(try XCTUnwrap(HostConfig(tailcat: "rexgo://tailcat?a=tcx&n=mac")))
        tailcat.connect()
        await until("offline") { if case .offline = tailcat.status { true } else { false } }
        tailcat.stop()
        XCTAssertGreaterThanOrEqual(calls, 1)
        calls = 0
        let system = Host(HostConfig(name: "system", url: URL(string: "http://127.0.0.1:1")!))
        system.connect()
        defer { system.stop() }
        await until("offline") { if case .offline = system.status { true } else { false } }
        XCTAssertEqual(calls, 0)
    }
}
