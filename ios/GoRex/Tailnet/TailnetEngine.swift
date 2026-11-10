import AuthenticationServices
import Foundation
import Network
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

        var isRunning: Bool { state == "Running" }
        var needsLogin: Bool { state == "NeedsLogin" }
        var awaitingApproval: Bool { state == "NeedsMachineAuth" }
        /// A device with state of its own, signed in or waiting for approval.
        var hasDevice: Bool { name != nil || isRunning || awaitingApproval }
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
    /// Why the device's last rename failed.
    private(set) var renameFailure: String?
    /// Whether the login page shows, or will once the engine has one.
    private(set) var signingIn = false
    @ObservationIgnored private var login: ASWebAuthenticationSession?
    /// The login page's window, kept while it shows: the session holds it weakly.
    @ObservationIgnored private var anchor: Anchor?
    /// The login page last shown, not shown again by itself.
    @ObservationIgnored private var shown: String?
    /// The engine's operations, one at a time: each waits for the one before.
    @ObservationIgnored private var chain: Task<Void, Never>?
    /// The run whose status counts; a stopped run's late news doesn't.
    @ObservationIgnored private var run = 0
    @ObservationIgnored private let paths = PathMonitor()
    @ObservationIgnored private let log = Logger(subsystem: "dev.gorex.ios", category: "tailnet")

    /// Hosts reach the proxy once the device runs. Until it has the
    /// network map, the proxy refuses them, and they try again.
    private init() {
        Host.tailnetProxy = {
            let e = TailnetEngine.shared
            return e.status.isRunning ? e.proxy : nil
        }
    }

    /// Starts the engine as Settings › Tailscale has it, signing in with
    /// authKey when given; stops it when off.
    func apply(authKey: String = "") async {
        await serial { await self.applyNow(authKey: authKey) }
    }

    /// Stops the engine and its proxy.
    func stop() async {
        await serial { await self.stopNow() }
    }

    /// Signs the device out and forgets it: its state goes. Still on, the
    /// engine starts again as a new device, to sign in anew, as after a
    /// change of control server.
    func signOut() async {
        await serial {
            _ = await Task.detached { TailnetLogout(nil) }.value
            await self.stopNow()
            if let dir = try? Self.stateDir() { try? FileManager.default.removeItem(at: dir) }
            await self.applyNow()
        }
    }

    /// Back from a suspension, which reclaims the engine's sockets: opens
    /// them anew while it runs, or, when that fails, starts it anew, with
    /// a new proxy (docs/ios-tailnet.md, Lifecycle). One that is on but
    /// failed to start starts again. Tells whether the engine runs as
    /// before. Its proxy may be new, when iOS reclaimed the old one's
    /// listener: the hosts' next session() then makes a new URLSession.
    func resume() async -> Bool {
        var kept = false
        await serial {
            guard TailnetSettings.isOn else { return }
            // In the chain, no proxy is a start that failed.
            guard self.proxy != nil else { return await self.applyNow() }
            guard self.status.isRunning else { return }
            do {
                try await Task.detached {
                    var error: NSError?
                    guard TailnetResetSockets(&error) else { throw error ?? RexError(code: "tailnet", message: "reset") }
                }.value
                self.log.notice("sockets reset")
                let p = try await Task.detached { try Self.readProxy() }.value
                if p != self.proxy {
                    self.log.notice("proxy on 127.0.0.1:\(p.port), the old one gone")
                    self.proxy = p
                }
                kept = true
            } catch {
                self.log.error("reset: \(error.localizedDescription, privacy: .public); starting anew")
                await self.applyNow()
            }
        }
        return kept
    }

    /// Runs op once the operations before it end.
    private func serial(_ op: @escaping @MainActor () async -> Void) async {
        let before = chain
        let t = Task {
            await before?.value
            await op()
        }
        chain = t
        await t.value
    }

    private func applyNow(authKey: String = "") async {
        TailnetSettings.makeName()
        guard TailnetSettings.isOn else {
            if proxy != nil || status.state != "Stopped" { await stopNow() }
            return
        }
        do {
            failure = nil
            renameFailure = nil
            try await start(hostname: TailnetSettings.name, controlURL: TailnetSettings.controlURL, authKey: authKey)
        } catch {
            paths.stop()
            failure = error.localizedDescription
            log.error("start: \(error.localizedDescription, privacy: .public)")
        }
    }

    /// Starts the engine as the device hostname, of the control server at
    /// controlURL (empty for Tailscale's own), signing in with authKey
    /// when given. A new run has a new proxy.
    private func start(hostname: String, controlURL: String, authKey: String) async throws {
        let config = String(decoding: try JSONSerialization.data(withJSONObject: [
            "hostname": hostname, "control_url": controlURL, "auth_key": authKey,
        ]), as: UTF8.self)
        let dir = try Self.stateDir()
        run += 1
        let r = run
        let watcher = Watcher(run: r)
        proxy = nil
        status = Status(state: "Starting")
        // The phone's network, before the engine's first look at it.
        await paths.start()
        // Starting reads the state and the network; off the main thread.
        let p = try await Task.detached {
            var error: NSError?
            guard TailnetStart(config, dir.path, watcher, &error) else { throw error ?? RexError(code: "tailnet", message: "start") }
            return try Self.readProxy()
        }.value
        // Stopped meanwhile.
        guard r == run else { return }
        proxy = p
        log.notice("proxy on 127.0.0.1:\(p.port)")
    }

    /// The engine's proxy of the moment: a new one when the last died.
    nonisolated private static func readProxy() throws -> LoopbackProxy {
        var error: NSError?
        let json = TailnetProxy(&error)
        guard error == nil, let a = try? JSONSerialization.jsonObject(with: Data(json.utf8)) as? [String: String],
              let port = a["address"]?.split(separator: ":").last.flatMap({ Int($0) }), let password = a["password"]
        else { throw error ?? RexError(code: "tailnet", message: "no proxy") }
        return LoopbackProxy(port: port, password: password)
    }

    private func stopNow() async {
        run += 1
        proxy = nil
        paths.stop()
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
        renameFailure = nil
        do {
            try await Task.detached {
                var error: NSError?
                guard TailnetSetHostname(name, &error) else { throw error ?? RexError(code: "tailnet", message: "rename") }
            }.value
        } catch {
            renameFailure = String(localized: "The new name applies at the next start: \(error.localizedDescription)")
            log.error("rename: \(error.localizedDescription, privacy: .public)")
        }
    }

    /// Opens the login page, now or once the engine has one, and closes it
    /// when the device runs. A stopped engine starts first.
    func signIn() async {
        signingIn = true
        if proxy == nil { await apply() }
        guard proxy != nil else { signingIn = false; return }
        if status.auth_url == nil || status.auth_url == shown {
            // The engine asks for a login at its start; this one is for a
            // page shown before. Tailscale keeps that page while control
            // holds it valid, else asks control for a new one, which comes
            // as the status changes.
            _ = await Task.detached { TailnetLogin(nil) }.value
            await refresh()
            if status.auth_url == shown { shown = nil }
        }
        present()
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
        if s.isRunning { cancelLogin() } else { present() }
    }

    /// Shows the login page while signing in. Tailscale's page never calls
    /// back: the device running closes it, or the user does.
    private func present() {
        guard signingIn, login == nil, let s = status.auth_url, s != shown, let url = URL(string: s) else { return }
        guard let window = Anchor.window() else { signingIn = false; return }
        shown = s
        let session = ASWebAuthenticationSession(url: url, callback: .customScheme("rexgo")) { [weak self] _, _ in
            self?.login = nil
            self?.anchor = nil
            self?.signingIn = false
        }
        anchor = Anchor(window)
        session.presentationContextProvider = anchor
        login = session
        if !session.start() {
            login = nil
            anchor = nil
            signingIn = false
        }
    }

    private func cancelLogin() {
        signingIn = false
        login?.cancel()
        login = nil
        anchor = nil
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
        let window: UIWindow
        init(_ window: UIWindow) { self.window = window }

        func presentationAnchor(for session: ASWebAuthenticationSession) -> ASPresentationAnchor { window }

        /// The key window of the scene in front, else of any; nil with none.
        static func window() -> UIWindow? {
            let scenes = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }
            let s = scenes.first { $0.activationState == .foregroundActive } ?? scenes.first
            return s?.keyWindow ?? s?.windows.first
        }
    }

    /// Tells the engine each change of the phone's network: the interface
    /// that carries it, as Wi-Fi to cellular, or none, and its gateways,
    /// as from one Wi-Fi to another; iOS lets it watch none itself.
    nonisolated private final class PathMonitor: @unchecked Sendable {
        private static let log = Logger(subsystem: "dev.gorex.ios", category: "tailnet")
        private let queue = DispatchQueue(label: "dev.gorex.ios.tailnet.path")
        // On queue.
        private var monitor: NWPathMonitor?
        private var told: String?
        private var waiting: CheckedContinuation<Void, Never>?

        /// Starts watching; returns once the network is told, or after a
        /// second without news.
        func start() async {
            await withCheckedContinuation { (c: CheckedContinuation<Void, Never>) in
                queue.async { [self] in
                    guard monitor == nil else { return c.resume() }
                    waiting = c
                    let m = NWPathMonitor(prohibitedInterfaceTypes: [.other, .loopback])
                    m.pathUpdateHandler = { [weak self] in self?.update($0) }
                    monitor = m
                    m.start(queue: queue)
                    queue.asyncAfter(deadline: .now() + 1) { [self] in ready() }
                }
            }
        }

        func stop() {
            queue.async { [self] in
                monitor?.cancel()
                monitor = nil
                told = nil
                ready()
            }
        }

        private func update(_ path: NWPath) {
            defer { ready() }
            let name = path.status == .satisfied
                ? path.availableInterfaces.first { [.wifi, .wiredEthernet, .cellular].contains($0.type) }?.name ?? ""
                : ""
            let network = "\(name) \(name.isEmpty ? [] : path.gateways)"
            guard network != told else { return }
            told = network
            Self.log.notice("network \(name.isEmpty ? "none" : name, privacy: .public)")
            TailnetDefaultInterfaceChanged(name)
        }

        private func ready() {
            waiting?.resume()
            waiting = nil
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
