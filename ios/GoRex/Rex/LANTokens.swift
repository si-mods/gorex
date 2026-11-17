import Foundation
import Security

/// The LAN gateway's pairing tokens (HostConfig.lanToken), by host: the
/// Keychain in the app, a dictionary in tests. The hosts' list in
/// UserDefaults never holds one.
protocol LANTokens: AnyObject {
    func token(for host: UUID) -> String?
    /// Keeps the host's token; nil forgets it.
    func set(_ token: String?, for host: UUID)
}

/// The tokens in the Keychain, a generic password per host: service
/// dev.gorex.ios.lan-token, account the host's id.
final class KeychainLANTokens: LANTokens {
    static let service = "dev.gorex.ios.lan-token"

    private func item(_ host: UUID) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: Self.service,
         kSecAttrAccount as String: host.uuidString.lowercased()]
    }

    func token(for host: UUID) -> String? {
        var q = item(host)
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess, let d = out as? Data else { return nil }
        return String(data: d, encoding: .utf8)
    }

    func set(_ token: String?, for host: UUID) {
        store(token, for: host)
    }

    /// Replaces the host's item; the status of the last Keychain call, for
    /// a test to tell a simulator without a Keychain.
    @discardableResult
    func store(_ token: String?, for host: UUID) -> OSStatus {
        let deleted = SecItemDelete(item(host) as CFDictionary)
        guard let token, !token.isEmpty else { return deleted == errSecItemNotFound ? errSecSuccess : deleted }
        var add = item(host)
        add[kSecValueData as String] = Data(token.utf8)
        // Reconnects run in the background, after the first unlock; the
        // token stays on this device.
        add[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        return SecItemAdd(add as CFDictionary, nil)
    }
}
