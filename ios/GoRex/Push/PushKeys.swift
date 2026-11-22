import CryptoKit
import Foundation
import Security

/// The device key the push relay knows this phone by
/// (docs/NET-003-push-relay.md, Parties and keys): a P-256 pair, its
/// public part the phone's identity in a bind and a push.register. The
/// Keychain in the app, a pair in memory in tests.
protocol PushKeys: AnyObject {
    /// The public key: base64 of the X.509 SubjectPublicKeyInfo DER, as
    /// the host's push key travels too.
    var publicKeyB64: String { get }
    /// A signature over body: base64 of the ECDSA signature's ASN.1 DER
    /// form, which the relay checks over the raw request bytes.
    func sign(_ body: Data) throws -> String
}

/// The device key in the Keychain, a generic password: service
/// dev.gorex.ios.push-device, account "device", the pair's raw bytes.
/// Made at first use.
final class KeychainPushKeys: PushKeys {
    static let service = "dev.gorex.ios.push-device"
    static let account = "device"

    private var item: [String: Any] {
        [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: Self.service,
         kSecAttrAccount as String: Self.account]
    }

    /// The pair read or made so far, for the Keychain to be asked once.
    private var cached: P256.Signing.PrivateKey?

    /// The stored pair; nil for none or one that does not decode.
    func load() -> P256.Signing.PrivateKey? {
        var q = item
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess,
              let d = out as? Data else { return nil }
        return try? P256.Signing.PrivateKey(rawRepresentation: d)
    }

    /// Replaces the item; the status of the last Keychain call, for a
    /// test to tell a simulator without a Keychain.
    @discardableResult
    func store(_ key: P256.Signing.PrivateKey) -> OSStatus {
        SecItemDelete(item as CFDictionary)
        var add = item
        add[kSecValueData as String] = key.rawRepresentation
        // A token's bind runs in the short wake iOS gives it, after the
        // first unlock; the key stays on this device.
        add[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        return SecItemAdd(add as CFDictionary, nil)
    }

    /// Drops the stored pair and the cached one.
    func forget() {
        SecItemDelete(item as CFDictionary)
        cached = nil
    }

    /// The pair: the stored one, else a new one kept; nil where the
    /// Keychain is not there (a test's process without the entitlement).
    private var key: P256.Signing.PrivateKey? {
        if let cached { return cached }
        if let k = load() {
            cached = k
            return k
        }
        let made = P256.Signing.PrivateKey()
        guard store(made) == errSecSuccess else { return nil }
        cached = made
        return made
    }

    var publicKeyB64: String {
        key?.publicKey.derRepresentation.base64EncodedString() ?? ""
    }

    func sign(_ body: Data) throws -> String {
        guard let key else { throw RexError(code: "push", message: "no device key") }
        return try key.signature(for: body).derRepresentation.base64EncodedString()
    }
}
