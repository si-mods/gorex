import Foundation

/// Binds the Live Activities' push tokens at the hosts' relays
/// (docs/NET-003-push-relay.md, Register and bind): each host's target —
/// its push key and relay — arrives when gorex.push.register answers, and
/// each activity's token and the attributes type's push-to-start token
/// arrive from ActivityKit, in any order; a token binds once both are
/// there, the start token to every target. The host never sees a token.
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
    /// The newest push-to-start token of each attributes type, hex. It is
    /// the app's, not an activity's: it binds to every host's target,
    /// once for each, as a host of any version may grow push-to-start.
    private(set) var startTokens: [String: String] = [:]

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
    /// kept, and the tokens that came already bind to it now — the
    /// host's activity's, and each start token, to every target.
    func set(host: UUID, target: PushTarget) {
        targets[host] = target
        let relay = self.relay
        if let token = tokens[host] {
            Task { await relay.bind(token: token, target: target) }
        }
        for (type, token) in startTokens {
            Task { await relay.bindStart(token: token, attributesType: type, target: target) }
        }
    }

    /// A Live Activity of the host got a push token (hex): it binds to
    /// the host's target, once that is there too.
    func bind(tokenHex: String, host: UUID) {
        tokens[host] = tokenHex
        guard let target = targets[host] else { return }
        let relay = self.relay
        Task { await relay.bind(token: tokenHex, target: target) }
    }

    /// A push-to-start token arrived (the driver's onStartToken): of the
    /// attributes type, it binds to every host target there is, and to
    /// each the next register adds (set).
    func bindStart(tokenHex: String, attributesType: String) {
        startTokens[attributesType] = tokenHex
        let relay = self.relay
        for target in targets.values {
            Task { await relay.bindStart(token: tokenHex, attributesType: attributesType, target: target) }
        }
    }

    /// The host is gone (HostStore.remove): its target and token are
    /// forgotten, its bindings unbound when both were known — the
    /// activity's, and the start token's of this pairing alone, the
    /// token being the app's.
    func drop(host: UUID) {
        let target = targets.removeValue(forKey: host)
        let token = tokens.removeValue(forKey: host)
        let relay = self.relay
        if let target, let token {
            Task { await relay.unbind(token: token, target: target) }
        }
        if let target {
            for (type, token) in startTokens {
                Task { await relay.unbindStart(token: token, attributesType: type, target: target) }
            }
        }
    }
}
