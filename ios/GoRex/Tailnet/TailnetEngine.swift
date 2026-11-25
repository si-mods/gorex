import AuthenticationServices
import Foundation
import Network
import Observation
import os
import Tailnet

/// Rex Go's built-in Tailscale: the Go engine of ios/tailnet, a device of
/// its own on the tailnet in the app's process, whose loopback proxy the
/// hosts on `.tailnet` go through (docs/IOS-001-tailnet.md). The same proxy
/// reaches the hosts paired over tailcat, with the device on or off
/// (docs/IOS-002-tailcat.md).
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
    /// The proxy as the tailcat hosts use it, read when first needed and
    /// again after a suspension, which may take its listener.
    @ObservationIgnored private(set) var tailcatProxy: LoopbackProxy?
    /// Whether a host is paired over tailcat: the path monitor then runs
    /// with the device off too.
    @ObservationIgnored private var hasTailcat = false
    /// Whether a host is reached over ssh://: the path monitor then runs
    /// with the device off too, as for tailcat.
    @ObservationIgnored private var hasSSH = false
    /// Whether the device is starting, when it waits for the path monitor.
    @ObservationIgnored private var starting = false
    /// This phone's tailcat key, as a GoRex machine's Devices list shows
    /// it; empty before the first pairing.
    private(set) var tailcatKey = ""
    /// This phone's SSH key, as the public key line an ssh:// host's
    /// authorized_keys asks for; empty before the first ssh:// host.
    private(set) var sshKey = ""
    /// The host key the engine asks about while it dials an ssh:// host,
    /// shown as the app's card and answered by answerSSHHostKey
    /// (docs/IOS-023-ssh-hosts.md, Trust); nil for none.
    private(set) var sshHostKeyQuestion: SSHHostKeyQuestion?
    /// The secret the engine asks for while it logs in to an ssh://
    /// host — a password or a keyboard-interactive answer — shown and
    /// answered as the host-key card is.
    private(set) var sshAuthQuestion: SSHAuthQuestion?
    /// A host that logged in by a password offers to install the phone's
    /// key on it (docs/IOS-023-ssh-hosts.md); nil for none.
    private(set) var sshInstallOffer: HostConfig?
    /// The install's failure, shown in the offer's card.
    private(set) var sshInstallError: String?
    /// The questions that came while another one showed, in order.
    @ObservationIgnored private var sshHostKeyQueue: [SSHHostKeyQuestion] = []
    /// The auth questions that came while another one showed, in order.
    @ObservationIgnored private var sshAuthQueue: [SSHAuthQuestion] = []
    /// The engine's SSH delegate, so Go keeps it; set once, in init.
    @ObservationIgnored private let sshDelegate = SSHDelegate()

    /// The host key the engine asks about while it dials an ssh:// host:
    /// its fingerprint and type — and the one trusted before when the
    /// key changed — which SSHAnswer replies to.
    struct SSHHostKeyQuestion: Equatable, Identifiable {
        let qid: Int64
        let user: String
        let host: String
        let port: Int
        let fingerprint: String
        /// The fingerprint trusted before, for a key that changed;
        /// empty at the first connect.
        let previous: String
        /// The key's algorithm, as authorized_keys names it.
        let keyType: String
        var id: Int64 { qid }
        /// Whether the host's key differs from the one trusted before.
        var changed: Bool { !previous.isEmpty }
    }

    /// The secret the engine asks for while it logs in to an ssh://
    /// host: prompt is what the server asked, retry marks a second ask
    /// after a refused one, secret tells the card to hide the typing.
    /// SSHAuthAnswer replies.
    struct SSHAuthQuestion: Equatable, Identifiable {
        let qid: Int64
        let user: String
        let host: String
        let port: Int
        let prompt: String
        let retry: Bool
        let secret: Bool
        var id: Int64 { qid }
        /// Whether the prompt asks for a one-time code, for the card's
        /// OTP AutoFill, as rootshell's secret kinds classify it.
        var otp: Bool {
            prompt.range(of: "code|otp|verification|2fa|token|one-time", options: .regularExpression) != nil
        }
    }
    @ObservationIgnored private let paths = PathMonitor()
    @ObservationIgnored private let log = Logger(subsystem: "dev.gorex.ios", category: "tailnet")

    /// Hosts reach the proxy once the device runs. Until it has the
    /// network map, the proxy refuses them, and they try again. The
    /// tailcat hosts reach it once the phone has its key. Made before the
    /// hosts, so that they find the engine's hooks set.
    private init() {
        Host.tailnetProxy = {
            let e = TailnetEngine.shared
            return e.status.isRunning ? e.proxy : nil
        }
        Host.tailcatProxy = { TailnetEngine.shared.tailcatProxyNow() }
        Host.tailcatHosts = { TailnetEngine.shared.setTailcatHosts($0) }
        Host.sshHosts = { TailnetEngine.shared.setSSHHosts($0) }
        Host.sshHostToken = { TailnetEngine.shared.learnSSHHostToken($0) }
        Host.proxyFailed = { Task { await TailnetEngine.shared.reproxy() } }
        TailnetSetSSHDelegate(sshDelegate)
        _ = try? useTailcatKey(create: false)
        _ = try? useSSHKey(create: false)
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
    /// them anew, the tailcat clients' and, while it runs, the device's;
    /// when the device's fail, starts it anew, with a new proxy
    /// (docs/IOS-001-tailnet.md, Lifecycle). One that is on but failed to
    /// start starts again. Tells whether the device runs as before. The
    /// proxy may be new, when iOS reclaimed the old one's listener: the
    /// hosts' next session() then makes a new URLSession.
    func resume() async -> Bool {
        var kept = false
        await serial {
            // One start at most: a device whose reset failed starts anew
            // here, as does one that failed to start before.
            var started = false
            // In the chain, no proxy is a start that failed.
            let device = TailnetSettings.isOn && self.proxy != nil && self.status.isRunning
            // The tailcat proxy anew, before the reset's wait: iOS may
            // have taken its listener, and a dial while the reset runs
            // then reaches a live one. Read off the main actor, as
            // tailcatProxyNow() does on a session()'s own read.
            let hadTailcat = self.tailcatProxy != nil
            self.tailcatProxy = nil
            if hadTailcat {
                let p = await Task.detached { try? Self.readProxy(run: 0) }.value
                if self.tailcatProxy == nil { self.tailcatProxy = p }
            }
            do {
                try await Task.detached {
                    var error: NSError?
                    guard TailnetResetSockets(&error) else { throw error ?? RexError(code: "tailnet", message: "reset") }
                }.value
                self.log.notice("sockets reset")
                if device {
                    let run = self.run
                    let p = try await Task.detached { try Self.readProxy(run: run) }.value
                    if p != self.proxy {
                        self.log.notice("proxy on 127.0.0.1:\(p.port), the old one gone")
                        self.proxy = p
                    }
                    kept = true
                }
            } catch let error where device {
                self.log.error("reset: \(error.localizedDescription, privacy: .public); starting anew")
                await self.applyNow()
                started = true
            } catch {
                // The device's state changed meanwhile; its next start
                // opens new sockets.
                self.log.error("reset: \(error.localizedDescription, privacy: .public)")
            }
            if TailnetSettings.isOn && self.proxy == nil && !started { await self.applyNow() }
        }
        return kept
    }

    /// Asks the GoRex machine at a tailcat address to pair with this
    /// phone by its device name, making the phone's key first if it has
    /// none, and returns the fingerprint the machine shows by the request
    /// (docs/IOS-002-tailcat.md, Pairing a phone).
    func pair(address: String) async throws -> String {
        TailnetSettings.makeName()
        let name = TailnetSettings.name
        _ = try useTailcatKey(create: true)
        return try await Task.detached {
            var error: NSError?
            let fingerprint = TailnetTailcatPair(address, name, &error)
            if let error { throw error }
            return fingerprint
        }.value
    }

    /// Asks the GoRex machine at an IP address, IP or IP:PORT, to pair with
    /// this phone through its own relay (docs/NET-002-host-links.md, Pairing by
    /// address), making the phone's key first if it has none, and returns
    /// the code to compare with the one the machine shows.
    func pairByAddress(_ address: String) async throws -> String {
        TailnetSettings.makeName()
        let name = TailnetSettings.name
        _ = try useTailcatKey(create: true)
        return try await Task.detached {
            var error: NSError?
            let code = TailnetPairByAddress(address, name, &error)
            if let error { throw error }
            return code
        }.value
    }

    /// The user's word that the codes match, for the pairing of code:
    /// waits for the machine's approval and returns the tailcat address to
    /// keep.
    func confirmAddressPairing(code: String) async throws -> String {
        try await Task.detached {
            var error: NSError?
            let address = TailnetConfirmAddressPairing(code, &error)
            if let error { throw error }
            return address
        }.value
    }

    /// The user's word that the codes differ: ends the pairing of code.
    func mismatchAddressPairing(code: String) {
        Task.detached { _ = TailnetMismatchAddressPairing(code) }
    }

    /// Gives the pairing by address of code up; another one stays.
    func cancelAddressPairing(code: String) {
        Task.detached { TailnetCancelAddressPairing(code) }
    }

    /// Whether a pairing by address failed as not matching: the machine
    /// may have let in another device.
    func isAddressPairingWarning(_ error: Error) -> Bool {
        error.localizedDescription == TailnetAddressPairingWarning()
    }

    /// The proxy for the tailcat and ssh:// hosts; nil while the phone
    /// has neither a tailcat nor an SSH key, as before it paired or an
    /// ssh:// host was added, or while its Keychain is locked.
    private func tailcatProxyNow() -> LoopbackProxy? {
        guard (try? useTailcatKey(create: false)) == true || (try? useSSHKey(create: false)) == true else { return nil }
        if tailcatProxy == nil { tailcatProxy = try? Self.readProxy(run: 0) }
        return tailcatProxy
    }

    /// A dial through the proxy failed, so it is read again at once —
    /// `Proxy()` starts a new listener for one iOS took back in a
    /// suspension no resume() has seen; without this, every dial until
    /// the next resume() fails against the dead listener
    /// (docs/IOS-002-tailcat.md, the proxy). Read off the caller's actor.
    func reproxy() async {
        guard let p = await Task.detached { try? Self.readProxy(run: 0) }.value else { return }
        tailcatProxy = p
        if let q = proxy {
            let r = LoopbackProxy(port: p.port, password: p.password, run: q.run)
            if r != q { proxy = r }
        }
    }

    /// Tells the engine the hosts paired over tailcat, by id and address.
    private func setTailcatHosts(_ hosts: [HostConfig]) {
        let list = hosts.compactMap { h in h.tailcat.map { ["id": HostConfig.tailcatID(h.id), "address": $0.address] } }
        let json = (try? JSONSerialization.data(withJSONObject: list)).map { String(decoding: $0, as: UTF8.self) } ?? "[]"
        var error: NSError?
        if !TailnetSetTailcatHosts(json, &error) {
            // Not the error: it could quote an address.
            log.error("tailcat hosts not set")
        }
        hasTailcat = !list.isEmpty
        if hasTailcat {
            Task { await paths.start() }
        } else if proxy == nil && !starting && !hasSSH {
            // Neither a device that runs nor one starting needs it.
            paths.stop()
        }
    }

    /// Tells the engine the hosts added by an ssh:// address, by id:
    /// login, port, the remote socket or tcp:// endpoint if the address
    /// named one, and the host key fingerprint trusted, which
    /// SetSSHHosts keeps by id. The device key is made first, so a dial
    /// has it (docs/IOS-023-ssh-hosts.md, docs/IOS-024-ssh-tcp.md).
    private func setSSHHosts(_ hosts: [HostConfig]) {
        let list: [[String: Any]] = hosts.compactMap { h in
            h.ssh.map { [
                "id": HostConfig.tailcatID(h.id), "user": $0.user, "host": $0.host, "port": $0.port,
                "socket": $0.socket ?? "", "tcp": $0.tcp ?? "", "hostKey": $0.hostKey ?? "",
            ] }
        }
        hasSSH = !list.isEmpty
        if hasSSH { _ = try? useSSHKey(create: true) }
        let json = (try? JSONSerialization.data(withJSONObject: list)).map { String(decoding: $0, as: UTF8.self) } ?? "[]"
        var error: NSError?
        if !TailnetSetSSHHosts(json, &error) {
            // Not the error: it could quote an address.
            log.error("ssh hosts not set")
        }
        if hasSSH {
            Task { await paths.start() }
        } else if proxy == nil && !starting && !hasTailcat {
            // Neither a device that runs nor one starting needs it.
            paths.stop()
        }
    }

    /// The LAN pairing token the engine learned over SSH for the host's
    /// tcp:// endpoint goes to the host's config and the Keychain, as a
    /// #k= one's, so its next connect sends it
    /// (docs/IOS-024-ssh-tcp.md); none while the engine knows none.
    private func learnSSHHostToken(_ id: UUID) {
        let token = TailnetSSHHostToken(HostConfig.tailcatID(id))
        guard !token.isEmpty else { return }
        HostStore.current?.learnToken(token, for: id)
    }

    /// The SSH delegate's question, off the dial's goroutine: the card
    /// shows it, and answerSSHHostKey replies. One shows at a time; the
    /// rest wait their turn, as the share's prompts do.
    func askSSHHostKey(_ q: SSHHostKeyQuestion) {
        if sshHostKeyQuestion == nil {
            sshHostKeyQuestion = q
        } else if sshHostKeyQuestion?.qid != q.qid && !sshHostKeyQueue.contains(where: { $0.qid == q.qid }) {
            sshHostKeyQueue.append(q)
        }
    }

    /// The user's answer to the host-key question showing: save answers
    /// the dial and keeps the fingerprint in the host's config, so a
    /// later connect asks no more; once answers it for this run only,
    /// written nowhere; nil is the refuse a cancel sends. An answer for
    /// a question already answered — as a dismiss after a button
    /// answered — is a no-op.
    func answerSSHHostKey(_ q: SSHHostKeyQuestion, save: Bool?) {
        guard sshHostKeyQuestion?.qid == q.qid else { return }
        sshHostKeyQuestion = sshHostKeyQueue.isEmpty ? nil : sshHostKeyQueue.removeFirst()
        Task.detached { TailnetSSHAnswer(q.qid, save != nil) }
        guard save == true,
              var c = HostStore.current?.hosts.first(where: {
                  $0.config.ssh?.user == q.user && $0.config.ssh?.host == q.host && $0.config.ssh?.port == q.port
              })?.config
        else { return }
        c.ssh?.hostKey = q.fingerprint
        HostStore.current?.update(c)
    }

    /// The engine's auth question, off the dial's goroutine: the card
    /// shows it, and answerSSHAuth replies. One shows at a time; the
    /// rest wait their turn.
    func askSSHAuth(_ q: SSHAuthQuestion) {
        if sshAuthQuestion == nil {
            sshAuthQuestion = q
        } else if sshAuthQuestion?.qid != q.qid && !sshAuthQueue.contains(where: { $0.qid == q.qid }) {
            sshAuthQueue.append(q)
        }
    }

    /// The user's answer to the auth question showing: the secret
    /// typed, or nil for the cancel that fails the dial.
    func answerSSHAuth(_ q: SSHAuthQuestion, secret: String?) {
        guard sshAuthQuestion?.qid == q.qid else { return }
        sshAuthQuestion = sshAuthQueue.isEmpty ? nil : sshAuthQueue.removeFirst()
        Task.detached { TailnetSSHAuthAnswer(q.qid, secret ?? "") }
    }

    /// Offers to install the phone's key when the host id's last login
    /// used a password; the engine tells it once per password login
    /// (docs/IOS-023-ssh-hosts.md).
    func offerSSHInstallKey(_ c: HostConfig) {
        Task.detached { [weak self] in
            guard TailnetSSHTakePasswordOffer(HostConfig.tailcatID(c.id)) else { return }
            await MainActor.run {
                self?.sshInstallError = nil
                self?.sshInstallOffer = c
            }
        }
    }

    /// Installs the phone's key on the offered host; the card stays up
    /// on a failure, carrying its error.
    func installSSHKey(_ c: HostConfig) async {
        do {
            try await Task.detached {
                var error: NSError?
                guard TailnetSSHInstallKey(HostConfig.tailcatID(c.id), &error) else {
                    throw error ?? RexError(code: "ssh", message: "install")
                }
            }.value
            sshInstallOffer = nil
            sshInstallError = nil
        } catch {
            sshInstallError = error.localizedDescription
        }
    }

    /// Dismisses the install offer, leaving the host on passwords.
    func skipSSHInstallKey() {
        sshInstallOffer = nil
        sshInstallError = nil
    }

    /// Passes the phone's SSH key from the Keychain to the engine, once;
    /// Go never writes it. With none there, create makes one, kept on
    /// this device only: the ssh:// hosts know the phone by it. Tells
    /// whether the engine has a key.
    private func useSSHKey(create: Bool) throws -> Bool {
        if !TailnetSSHPublicKey().isEmpty { return true }
        let query: [CFString: Any] = [
            kSecClass: kSecClassGenericPassword, kSecAttrService: "dev.gorex.ios.ssh", kSecAttrAccount: "client",
        ]
        var item: CFTypeRef?
        var status = SecItemCopyMatching(query.merging([kSecReturnData: true]) { $1 } as CFDictionary, &item)
        var key = (item as? Data).map { String(decoding: $0, as: UTF8.self) }
        if status == errSecItemNotFound {
            guard create else { return false }
            let k = TailnetNewSSHKey()
            status = SecItemAdd(query.merging([
                kSecValueData: Data(k.utf8), kSecAttrAccessible: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
            ]) { $1 } as CFDictionary, nil)
            key = k
        }
        guard status == errSecSuccess, let key else {
            throw RexError(code: "keychain", message: SecCopyErrorMessageString(status, nil) as String? ?? "\(status)")
        }
        var error: NSError?
        guard TailnetSetSSHKey(key, &error) else { throw error ?? RexError(code: "ssh", message: "key") }
        sshKey = TailnetSSHPublicKey()
        return true
    }

    /// Passes the phone's tailcat key from the Keychain to the engine,
    /// once; Go never writes it. With none there, create makes one, kept
    /// on this device only: the machines paired with know the phone by
    /// it. Tells whether the engine has a key.
    private func useTailcatKey(create: Bool) throws -> Bool {
        if !TailnetTailcatKey().isEmpty { return true }
        let query: [CFString: Any] = [
            kSecClass: kSecClassGenericPassword, kSecAttrService: "dev.gorex.ios.tailcat", kSecAttrAccount: "client",
        ]
        var item: CFTypeRef?
        var status = SecItemCopyMatching(query.merging([kSecReturnData: true]) { $1 } as CFDictionary, &item)
        var key = (item as? Data).map { String(decoding: $0, as: UTF8.self) }
        if status == errSecItemNotFound {
            guard create else { return false }
            let k = TailnetNewTailcatKey()
            status = SecItemAdd(query.merging([
                kSecValueData: Data(k.utf8), kSecAttrAccessible: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
            ]) { $1 } as CFDictionary, nil)
            key = k
        }
        guard status == errSecSuccess, let key else {
            throw RexError(code: "keychain", message: SecCopyErrorMessageString(status, nil) as String? ?? "\(status)")
        }
        var error: NSError?
        guard TailnetSetTailcatKey(key, &error) else { throw error ?? RexError(code: "tailcat", message: "key") }
        tailcatKey = TailnetTailcatKey()
        return true
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
            if !hasTailcat && !hasSSH { paths.stop() }
            failure = error.localizedDescription
            log.error("start: \(error.localizedDescription, privacy: .public)")
        }
    }

    /// Starts the engine as the device hostname, of the control server at
    /// controlURL (empty for Tailscale's own), signing in with authKey
    /// when given. A new run has a new proxy value (LoopbackProxy.run).
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
        starting = true
        defer { starting = false }
        // The phone's network, before the engine's first look at it.
        await paths.start()
        // Starting reads the state and the network; off the main thread.
        // The proxy first: one that fails leaves no node running.
        let p = try await Task.detached {
            let p = try Self.readProxy(run: r)
            var error: NSError?
            guard TailnetStart(config, dir.path, watcher, &error) else { throw error ?? RexError(code: "tailnet", message: "start") }
            return p
        }.value
        // Stopped meanwhile.
        guard r == run else { return }
        proxy = p
        log.notice("proxy on 127.0.0.1:\(p.port)")
    }

    /// The engine's proxy of the moment, for run: a new one when the last
    /// died.
    nonisolated private static func readProxy(run: Int) throws -> LoopbackProxy {
        var error: NSError?
        let json = TailnetProxy(&error)
        guard error == nil, let a = try? JSONSerialization.jsonObject(with: Data(json.utf8)) as? [String: String],
              let port = a["address"]?.split(separator: ":").last.flatMap({ Int($0) }), let password = a["password"]
        else { throw error ?? RexError(code: "tailnet", message: "no proxy") }
        return LoopbackProxy(port: port, password: password, run: run)
    }

    private func stopNow() async {
        run += 1
        proxy = nil
        if !hasTailcat && !hasSSH { paths.stop() }
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

    /// How the device reaches host, a tailnet name or address, as JSON
    /// (PeerPath), for the diagnostics.
    static func path(to host: String) async -> String {
        await Task.detached { TailnetPeerPath(host) }.value
    }

    /// A disco ping of host of size bytes, as JSON (Ping), for the
    /// diagnostics.
    static func ping(_ host: String, size: Int) async -> String {
        await Task.detached { TailnetPing(host, size) }.value
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

    /// The engine's SSH questions, on a dial goroutine's thread, as
    /// rootshell's iosbridge asks its app: the app shows the host key or
    /// the secret's prompt in a card and answers it with SSHAnswer or
    /// SSHAuthAnswer.
    nonisolated private final class SSHDelegate: NSObject, TailnetSSHDelegateProtocol, @unchecked Sendable {
        func onSSHHostKey(_ qid: Int64, user: String?, host: String?, port: Int, fingerprint: String?, previous: String?, keyType: String?) {
            guard let user, let host, let fingerprint else { return }
            Task { @MainActor in
                TailnetEngine.shared.askSSHHostKey(SSHHostKeyQuestion(
                    qid: qid, user: user, host: host, port: port,
                    fingerprint: fingerprint, previous: previous ?? "", keyType: keyType ?? ""))
            }
        }

        func onSSHAuth(_ qid: Int64, user: String?, host: String?, port: Int, prompt: String?, retry: Bool, secret: Bool) {
            guard let user, let host else { return }
            Task { @MainActor in
                TailnetEngine.shared.askSSHAuth(SSHAuthQuestion(
                    qid: qid, user: user, host: host, port: port,
                    prompt: prompt ?? "", retry: retry, secret: secret))
            }
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
