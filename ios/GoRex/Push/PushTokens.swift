import Foundation

/// Binds the Live Activities' push tokens at the hosts' relays
/// (docs/NET-003-push-relay.md, Register and bind): each host's target —
/// its push key and relay — arrives when gorex.push.register answers, and
/// each activity's token arrives from ActivityKit, in any order; a token
/// binds once both are there. The host never sees a token.
@MainActor
final class PushTokens {
    /// The app's: the Keychain's device key, the relay over HTTPS.
    static let shared = PushTokens()

    /// A register answer without a relay URL points at this one.
    static let defaultRelay = URL(string: "https://relay.gorex.uk")!

    /// "sandbox" in a debug build, else "production": the APNs
    /// environment the relay sends to.
    static let environment: String = {
        #if DEBUG
        return "sandbox"
        #else
        return "production"
        #endif
    }()

    /// The device key the binds are signed with.
    let keys: PushKeys
    private let relay: PushRelay
    /// Each host's target, by host, as its registers answered.
    private(set) var targets: [UUID: PushTarget] = [:]
    /// The newest activity push token of each host, hex.
    private(set) var tokens: [UUID: String] = [:]

    init(keys: PushKeys, relay: PushRelay) {
        self.keys = keys
        self.relay = relay
    }

    private convenience init() {
        let keys = KeychainPushKeys()
        self.init(keys: keys, relay: HTTPPushRelay(env: Self.environment, keys: keys))
    }

    /// The device key's public part, as a register carries it.
    var deviceKey: String { keys.publicKeyB64 }

    /// A host's register answered (Host.registerPush): the target is
    /// kept, and a token that came already binds to it now.
    func set(host: UUID, target: PushTarget) {
        targets[host] = target
        guard let token = tokens[host] else { return }
        let relay = self.relay
        Task { await relay.bind(token: token, target: target) }
    }

    /// A Live Activity of the host got a push token (hex): it binds to
    /// the host's target, once that is there too.
    func bind(tokenHex: String, host: UUID) {
        tokens[host] = tokenHex
        guard let target = targets[host] else { return }
        let relay = self.relay
        Task { await relay.bind(token: tokenHex, target: target) }
    }

    /// The host is gone (HostStore.remove): its target and token are
    /// forgotten, the token unbound when both were known.
    func drop(host: UUID) {
        let target = targets.removeValue(forKey: host)
        let token = tokens.removeValue(forKey: host)
        guard let target, let token else { return }
        let relay = self.relay
        Task { await relay.unbind(token: token, target: target) }
    }
}
