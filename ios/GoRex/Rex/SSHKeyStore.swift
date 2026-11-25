import Foundation
import Observation
import Security
import Tailnet

/// Non-secret metadata for a local SSH key (docs/IOS-026-ssh-keys.md).
/// The private key PEM lives only in the Keychain under SSHKeychain.
struct SSHKeyRecord: Codable, Identifiable, Hashable {
    var id: UUID
    var name: String
    /// Algorithm as OpenSSH names it (e.g. "ssh-ed25519", "ssh-rsa").
    var type: String
    /// Authorized-keys line ("<type> <base64>").
    var publicKey: String
    /// SHA256 fingerprint ("SHA256:...").
    var fingerprint: String
    var createdAt: Date
    var isDefault: Bool

    /// Short uppercase badge label for list rows (e.g. "ED25519", "RSA", "ECDSA").
    var badge: String {
        switch type {
        case "ssh-ed25519": return "ED25519"
        case "ssh-rsa": return "RSA"
        case let t where t.hasPrefix("ecdsa-"): return "ECDSA"
        default: return type.replacingOccurrences(of: "ssh-", with: "").uppercased()
        }
    }

    /// Shortened SHA256 fingerprint for compact rows.
    var shortFingerprint: String {
        guard fingerprint.count > 22 else { return fingerprint }
        return String(fingerprint.prefix(19)) + "…"
    }

    /// Authorized-keys line with the key's name as comment, for copying.
    var authorizedKeyLine: String {
        let comment = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !comment.isEmpty else { return publicKey }
        return "\(publicKey) \(comment)"
    }
}

/// Private-key storage for SSHKeyStore: Keychain in the app, memory in tests.
protocol SSHKeychain: AnyObject {
    func privateKey(for id: UUID) -> String?
    func setPrivateKey(_ pem: String?, for id: UUID) throws
    /// Reads the single-key legacy item from IOS-023, if present.
    func legacyPrivateKey() -> String?
}

/// Stores each SSH private key as a device-only Keychain generic password.
final class KeychainSSHKeys: SSHKeychain {
    static let service = "dev.gorex.ios.ssh.keys"
    static let legacyService = "dev.gorex.ios.ssh"
    static let legacyAccount = "client"

    private func query(for id: UUID) -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: Self.service,
            kSecAttrAccount as String: id.uuidString.lowercased(),
        ]
    }

    func privateKey(for id: UUID) -> String? {
        var q = query(for: id)
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess,
              let data = out as? Data else { return nil }
        return String(decoding: data, as: UTF8.self)
    }

    func setPrivateKey(_ pem: String?, for id: UUID) throws {
        let deleted = SecItemDelete(query(for: id) as CFDictionary)
        guard let pem, !pem.isEmpty else {
            if deleted != errSecSuccess && deleted != errSecItemNotFound {
                throw RexError(code: "keychain", message: SecCopyErrorMessageString(deleted, nil) as String? ?? "\(deleted)")
            }
            return
        }
        var add = query(for: id)
        add[kSecValueData as String] = Data(pem.utf8)
        add[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let status = SecItemAdd(add as CFDictionary, nil)
        guard status == errSecSuccess else {
            throw RexError(code: "keychain", message: SecCopyErrorMessageString(status, nil) as String? ?? "\(status)")
        }
    }

    func legacyPrivateKey() -> String? {
        let q: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: Self.legacyService,
            kSecAttrAccount as String: Self.legacyAccount,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var out: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess,
              let data = out as? Data else { return nil }
        return String(decoding: data, as: UTF8.self)
    }
}

/// Manages local SSH keys: Keychain storage, legacy IOS-023 migration,
/// metadata persistence in UserDefaults, and pushing keys to the Go engine.
@Observable
final class SSHKeyStore {
    static let shared = SSHKeyStore()

    private static let defaultsKey = "sshKeys"

    private(set) var keys: [SSHKeyRecord] = []

    /// Primary default key's public line, or the first key's, or "".
    var primaryPublicKey: String {
        (keys.first(where: \.isDefault) ?? keys.first)?.publicKey ?? ""
    }

    private let defaults: UserDefaults
    private let keychain: SSHKeychain
    private let parseKey: @MainActor (String, String) throws -> ParsedKey
    private let generatePEM: @MainActor () -> String
    private let pushEngine: @MainActor (String) -> Void
    private var hasInitialized = false

    struct ParsedKey: Decodable {
        let pem: String
        let publicKey: String
        let fingerprint: String
        let type: String
    }

    init(
        defaults: UserDefaults = .standard,
        keychain: SSHKeychain = KeychainSSHKeys(),
        parseKey: @escaping @MainActor (String, String) throws -> ParsedKey = SSHKeyStore.engineParseKey,
        generatePEM: @escaping @MainActor () -> String = { TailnetNewSSHKey() },
        pushEngine: @escaping @MainActor (String) -> Void = { json in
            var error: NSError?
            _ = TailnetSetSSHKeys(json, &error)
        }
    ) {
        self.defaults = defaults
        self.keychain = keychain
        self.parseKey = parseKey
        self.generatePEM = generatePEM
        self.pushEngine = pushEngine
        load()
    }

    static func engineParseKey(_ pem: String, passphrase: String) throws -> ParsedKey {
        var error: NSError?
        let json = TailnetParseSSHKey(pem, passphrase, &error)
        if let error { throw error }
        guard let data = json.data(using: .utf8) else {
            throw RexError(code: "ssh", message: "Invalid key response")
        }
        return try JSONDecoder().decode(ParsedKey.self, from: data)
    }

    /// Loads saved metadata or migrates the legacy single IOS-023 key, then
    /// pushes the loaded keys to the engine.
    func load() {
        if let data = defaults.data(forKey: Self.defaultsKey),
           let decoded = try? JSONDecoder().decode([SSHKeyRecord].self, from: data) {
            hasInitialized = true
            // Drop any stale metadata whose private key is no longer in Keychain.
            keys = decoded.filter { keychain.privateKey(for: $0.id) != nil }
            if keys.count != decoded.count { save() }
        } else if let legacy = keychain.legacyPrivateKey(),
                  let parsed = try? parseKey(legacy, "") {
            let id = UUID()
            if (try? keychain.setPrivateKey(parsed.pem, for: id)) != nil {
                let record = SSHKeyRecord(
                    id: id,
                    name: String(localized: "This Device"),
                    type: parsed.type,
                    publicKey: parsed.publicKey,
                    fingerprint: parsed.fingerprint,
                    createdAt: Date(),
                    isDefault: true
                )
                keys = [record]
                hasInitialized = true
                save()
            }
        }
        syncToEngine()
    }

    /// Creates a default Ed25519 key when the user adds their first SSH host
    /// before ever visiting Settings > SSH Keys. Does nothing if the user
    /// already initialized (e.g. explicitly deleted all keys).
    @discardableResult
    func ensureDefaultKey() throws -> SSHKeyRecord? {
        if let existing = keys.first { return existing }
        guard !hasInitialized else { return nil }
        return try generateKey(name: String(localized: "This Device"), isDefault: true)
    }

    /// Generates a new Ed25519 key and saves it to the Keychain.
    @discardableResult
    func generateKey(name: String, isDefault: Bool = true) throws -> SSHKeyRecord {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        let label = trimmed.isEmpty ? defaultNewKeyName() : trimmed
        let pem = generatePEM()
        guard !pem.isEmpty else {
            throw RexError(code: "ssh", message: String(localized: "Could not generate SSH key."))
        }
        let parsed = try parseKey(pem, "")
        let id = UUID()
        try keychain.setPrivateKey(parsed.pem, for: id)
        let record = SSHKeyRecord(
            id: id,
            name: label,
            type: parsed.type,
            publicKey: parsed.publicKey,
            fingerprint: parsed.fingerprint,
            createdAt: Date(),
            isDefault: isDefault
        )
        keys.append(record)
        hasInitialized = true
        save()
        syncToEngine()
        return record
    }

    /// Imports an OpenSSH or PEM private key, decrypting it once with
    /// passphrase if encrypted, and saves the unencrypted PEM to the Keychain.
    @discardableResult
    func importKey(name: String, pem: String, passphrase: String = "", isDefault: Bool = true) throws -> SSHKeyRecord {
        let trimmedPEM = pem.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedPEM.isEmpty else {
            throw RexError(code: "ssh", message: String(localized: "Paste or select a private key."))
        }
        let parsed = try parseKey(trimmedPEM, passphrase)
        let trimmedName = name.trimmingCharacters(in: .whitespacesAndNewlines)
        let label = trimmedName.isEmpty ? defaultNewKeyName() : trimmedName
        let id = UUID()
        try keychain.setPrivateKey(parsed.pem, for: id)
        let record = SSHKeyRecord(
            id: id,
            name: label,
            type: parsed.type,
            publicKey: parsed.publicKey,
            fingerprint: parsed.fingerprint,
            createdAt: Date(),
            isDefault: isDefault
        )
        keys.append(record)
        hasInitialized = true
        save()
        syncToEngine()
        return record
    }

    func rename(id: UUID, to name: String) {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, let i = keys.firstIndex(where: { $0.id == id }) else { return }
        keys[i].name = trimmed
        save()
    }

    func setDefault(_ isDefault: Bool, for id: UUID) {
        guard let i = keys.firstIndex(where: { $0.id == id }) else { return }
        keys[i].isDefault = isDefault
        save()
        syncToEngine()
    }

    func delete(id: UUID) throws {
        try keychain.setPrivateKey(nil, for: id)
        keys.removeAll { $0.id == id }
        hasInitialized = true
        save()
        syncToEngine()
    }

    func key(id: UUID?) -> SSHKeyRecord? {
        guard let id else { return nil }
        return keys.first { $0.id == id }
    }

    /// Sends all saved keys to the Go SSH engine.
    func syncToEngine() {
        let payload: [[String: Any]] = keys.compactMap { k in
            guard let pem = keychain.privateKey(for: k.id) else { return nil }
            return [
                "id": k.id.uuidString.lowercased(),
                "pem": pem,
                "isDefault": k.isDefault,
            ]
        }
        let json = (try? JSONSerialization.data(withJSONObject: payload))
            .map { String(decoding: $0, as: UTF8.self) } ?? "[]"
        pushEngine(json)
    }

    private func save() {
        if let data = try? JSONEncoder().encode(keys) {
            defaults.set(data, forKey: Self.defaultsKey)
        }
    }

    private func defaultNewKeyName() -> String {
        let base = String(localized: "This Device")
        if !keys.contains(where: { $0.name == base }) { return base }
        for n in 2...99 {
            let candidate = "\(base) \(n)"
            if !keys.contains(where: { $0.name == candidate }) { return candidate }
        }
        return base
    }
}
