import Foundation
import Observation
import os
import Tailnet

/// Rex Go's built-in Tailscale: the Go engine of ios/tailnet, a device of
/// its own on the tailnet in the app's process, whose loopback proxy the
/// hosts on `.tailnet` go through (docs/ios-tailnet.md).
@Observable
final class TailnetEngine {
    static let shared = TailnetEngine()

    /// The engine's status, as its Status gives it.
    struct Status: Decodable, Equatable {
        var state: String
        var auth_url: String?
        var name: String?
        var ips: [String]?
        var suffix: String?
        var peers: [Peer]?
        var error: String?

        struct Peer: Decodable, Equatable {
            var name: String
            var ips: [String]
            var online: Bool
        }
    }

    private(set) var status = Status(state: "Stopped")
    /// The proxy of the engine's current run; nil while it isn't running.
    private(set) var proxy: LoopbackProxy?
    @ObservationIgnored private let log = Logger(subsystem: "dev.gorex.ios", category: "tailnet")

    /// Hosts reach the proxy once the device runs: before, it knows no
    /// peer, and dials the tailnet's addresses through the system.
    private init() {
        Host.tailnetProxy = {
            let e = TailnetEngine.shared
            return e.status.state == "Running" ? e.proxy : nil
        }
    }

    /// Starts the engine as the device hostname, of the control server at
    /// controlURL (empty for Tailscale's own), signing in with authKey
    /// when given. A new run has a new proxy.
    func start(hostname: String, controlURL: String = "", authKey: String = "") async throws {
        let config = String(decoding: try JSONSerialization.data(withJSONObject: [
            "hostname": hostname, "control_url": controlURL, "auth_key": authKey,
        ]), as: UTF8.self)
        let dir = try Self.stateDir()
        let watcher = Watcher()
        proxy = nil
        // Starting reads the state and the network; off the main thread.
        let json = try await Task.detached {
            var error: NSError?
            guard TailnetStart(config, dir.path, watcher, &error) else { throw error ?? RexError(code: "tailnet", message: "start") }
            return TailnetProxy(&error)
        }.value
        guard let a = try? JSONSerialization.jsonObject(with: Data(json.utf8)) as? [String: String],
              let port = a["address"]?.split(separator: ":").last.flatMap({ Int($0) }), let password = a["password"]
        else { throw RexError(code: "tailnet", message: "no proxy") }
        proxy = LoopbackProxy(port: port, password: password)
        log.notice("proxy on 127.0.0.1:\(port)")
    }

    /// Takes a status the engine reported.
    fileprivate func changed(_ json: String) {
        guard let s = try? JSONDecoder().decode(Status.self, from: Data(json.utf8)), s != status else { return }
        status = s
        let peers = (s.peers ?? []).map { "\($0.name)\($0.online ? "" : " (offline)")" }
        log.notice("status \(s.state, privacy: .public) \(s.auth_url ?? "", privacy: .public) \(s.name ?? "", privacy: .public) \(s.ips ?? [], privacy: .public) peers: \(peers, privacy: .public)")
    }

    /// Where the device's state lives: Application Support, out of backups.
    private static func stateDir() throws -> URL {
        var dir = URL.applicationSupportDirectory.appending(path: "tailnet")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        var v = URLResourceValues()
        v.isExcludedFromBackup = true
        try dir.setResourceValues(v)
        return dir
    }

    /// The engine's callback, on a goroutine's thread.
    nonisolated private final class Watcher: NSObject, TailnetCallbackProtocol, @unchecked Sendable {
        func changed(_ status: String?) {
            guard let status else { return }
            Task { @MainActor in TailnetEngine.shared.changed(status) }
        }
    }
}

#if DEBUG
extension TailnetEngine {
    /// The transport spike's hook, until Settings has Tailscale: with
    /// GOREX_TAILNET_HOST set to a host's address, starts the engine
    /// (named GOREX_TAILNET_NAME, else rexgo-sim; signed in with the key
    /// in the file GOREX_TAILNET_AUTHKEY_FILE, else by the auth URL it
    /// logs) and reaches that host through it.
    func debugStart(_ store: HostStore) async {
        let env = ProcessInfo.processInfo.environment
        guard let address = env["GOREX_TAILNET_HOST"], var config = HostConfig(pairing: address) else { return }
        let key = env["GOREX_TAILNET_AUTHKEY_FILE"]
            .flatMap { try? String(contentsOfFile: $0, encoding: .utf8) }?
            .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        do {
            try await start(hostname: env["GOREX_TAILNET_NAME"] ?? "rexgo-sim", authKey: key)
        } catch {
            log.error("start: \(error.localizedDescription, privacy: .public)")
            return
        }
        if let h = store.host(at: config.url) {
            config = h.config
            config.transport = .tailnet
            store.update(config)
        } else {
            config.transport = .tailnet
            store.add(config)
        }
    }
}
#endif
