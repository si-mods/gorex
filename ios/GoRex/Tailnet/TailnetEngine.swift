import AuthenticationServices
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
    }

    /// A device on the tailnet; path is "direct", "derp" or "peer-relay"
    /// while the engine talks to it.
    struct Peer: Decodable, Equatable {
        var name: String
        var ips: [String]?
        var online: Bool
        var path: String?
    }

    private(set) var status = Status(state: "Stopped")
    /// The proxy of the engine's current run; nil while it isn't running.
    private(set) var proxy: LoopbackProxy?
    /// Why the engine last failed to start.
    private(set) var failure: String?
    /// Whether the login page shows, or will once the engine has one.
    private(set) var signingIn = false
    @ObservationIgnored private var login: ASWebAuthenticationSession?
    @ObservationIgnored private let anchor = Anchor()
    /// The run whose status counts; a stopped run's late news doesn't.
    @ObservationIgnored private var run = 0
    @ObservationIgnored private let log = Logger(subsystem: "dev.gorex.ios", category: "tailnet")

    /// Hosts reach the proxy once the device runs. Until it has the
    /// network map, the proxy refuses them, and they try again.
    private init() {
        Host.tailnetProxy = {
            let e = TailnetEngine.shared
            return e.status.state == "Running" ? e.proxy : nil
        }
    }

    /// Starts the engine as Settings › Tailscale has it, signing in with
    /// authKey when given; stops it when off.
    func apply(authKey: String = "") async {
        guard TailnetSettings.isOn else { return await stop() }
        do {
            failure = nil
            try await start(hostname: TailnetSettings.name, controlURL: TailnetSettings.controlURL, authKey: authKey)
        } catch {
            failure = error.localizedDescription
            log.error("start: \(error.localizedDescription, privacy: .public)")
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
        run += 1
        let watcher = Watcher(run: run)
        proxy = nil
        status = Status(state: "Starting")
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

    /// Stops the engine and its proxy.
    func stop() async {
        run += 1
        proxy = nil
        status = Status(state: "Stopped")
        cancelLogin()
        await Task.detached { TailnetStop() }.value
    }

    /// Reads the status again, for what the engine doesn't announce, as
    /// how it reaches each peer.
    func refresh() async {
        guard proxy != nil else { return }
        let r = run
        let json = await Task.detached { TailnetStatus() }.value
        if r == run { changed(json) }
    }

    /// Renames the running device; the name is kept for the next start.
    func setHostname(_ name: String) async {
        guard proxy != nil else { return }
        do {
            try await Task.detached {
                var error: NSError?
                guard TailnetSetHostname(name, &error) else { throw error ?? RexError(code: "tailnet", message: "rename") }
            }.value
        } catch {
            log.error("rename: \(error.localizedDescription, privacy: .public)")
        }
    }

    /// Opens the login page, now or once the engine has one, and closes it
    /// when the device runs. A stopped engine starts first.
    func signIn() async {
        signingIn = true
        if proxy == nil { await apply() }
        guard proxy != nil else { signingIn = false; return }
        if status.auth_url == nil {
            // The engine asks for a login at its start; this one is for a
            // page that expired or was closed.
            _ = await Task.detached { TailnetLogin(nil) }.value
        }
        present()
    }

    /// Signs the device out and forgets it: its state goes. Still on, the
    /// engine starts again as a new device, to sign in anew.
    func signOut() async {
        _ = await Task.detached { TailnetLogout(nil) }.value
        await stop()
        if let dir = try? Self.stateDir() { try? FileManager.default.removeItem(at: dir) }
        await apply()
    }

    /// The transport of a host added now (HostConfig.transport(of:)).
    func transport(of url: URL) -> HostConfig.Transport? {
        HostConfig.transport(of: url, tailnetOn: TailnetSettings.isOn, suffix: status.suffix)
    }

    static func validName(_ s: String) -> Bool { TailnetValidHostname(s) }

    /// A control server's URL as the gateway checks one; empty is Tailscale's.
    static func validControlURL(_ s: String) -> Bool { s.isEmpty || TailnetValidControlURL(s) }

    /// Takes a status the engine reported.
    fileprivate func changed(_ json: String, run r: Int? = nil) {
        guard r == nil || r == run,
              let s = try? JSONDecoder().decode(Status.self, from: Data(json.utf8)), s != status else { return }
        status = s
        log.notice("status \(s.state, privacy: .public) \(s.name ?? "", privacy: .public) \(s.ips ?? [], privacy: .public)")
        if s.state == "Running" { cancelLogin() } else { present() }
    }

    /// Shows the login page while signing in. Tailscale's page never calls
    /// back: the device running closes it, or the user does.
    private func present() {
        guard signingIn, login == nil, let s = status.auth_url, let url = URL(string: s) else { return }
        let session = ASWebAuthenticationSession(url: url, callback: .customScheme("rexgo")) { [weak self] _, _ in
            self?.login = nil
            self?.signingIn = false
        }
        session.presentationContextProvider = anchor
        login = session
        if !session.start() {
            login = nil
            signingIn = false
        }
    }

    private func cancelLogin() {
        signingIn = false
        login?.cancel()
        login = nil
    }

    /// Where the device's state lives: Application Support, out of backups,
    /// readable from the first unlock on, as the app may start locked.
    private static func stateDir() throws -> URL {
        var dir = URL.applicationSupportDirectory.appending(path: "tailnet")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        try FileManager.default.setAttributes([.protectionKey: FileProtectionType.completeUntilFirstUserAuthentication], ofItemAtPath: dir.path)
        var v = URLResourceValues()
        v.isExcludedFromBackup = true
        try dir.setResourceValues(v)
        return dir
    }

    /// The window the login page shows over.
    private final class Anchor: NSObject, ASWebAuthenticationPresentationContextProviding {
        func presentationAnchor(for session: ASWebAuthenticationSession) -> ASPresentationAnchor {
            let scene = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first!
            return scene.keyWindow ?? UIWindow(windowScene: scene)
        }
    }

    /// The engine's callback, on a goroutine's thread, for one run.
    nonisolated private final class Watcher: NSObject, TailnetCallbackProtocol, @unchecked Sendable {
        let run: Int
        init(run: Int) { self.run = run }

        func changed(_ status: String?) {
            guard let status else { return }
            Task { @MainActor in TailnetEngine.shared.changed(status, run: run) }
        }
    }
}
