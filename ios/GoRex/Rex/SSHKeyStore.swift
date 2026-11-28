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

/// Result of reading a private key from Keychain: distinguishes a missing item
/// (`notFound`) from a transient/locked Keychain error (`unavailable`), so cold
/// starts before Keychain is accessible never wipe saved key metadata.
enum SSHKeychainLookup: Equatable {
    case found(String)
    case notFound
    case unavailable(OSStatus)
}

/// Private-key storage for SSHKeyStore: Keychain in the app, memory in tests.
protocol SSHKeychain: AnyObject {
    func lookupPrivateKey(for id: UUID) -> SSHKeychainLookup
    func setPrivateKey(_ pem: String?, for id: UUID) throws
    /// Reads the single-key legacy item from IOS-023, if present.
    func lookupLegacyPrivateKey() -> SSHKeychainLookup
}

extension SSHKeychain {
    func privateKey(for id: UUID) -> String? {
        if case .found(let pem) = lookupPrivateKey(for: id) { return pem }
        return nil
    }

    func legacyPrivateKey() -> String? {
        if case .found(let pem) = lookupLegacyPrivateKey() { return pem }
        return nil
    }
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

    func lookupPrivateKey(for id: UUID) -> SSHKeychainLookup {
        var q = query(for: id)
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        let status = SecItemCopyMatching(q as CFDictionary, &out)
        if status == errSecSuccess, let data = out as? Data {
            return .found(String(decoding: data, as: UTF8.self))
        }
        if status == errSecItemNotFound { return .notFound }
        return .unavailable(status)
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

    func lookupLegacyPrivateKey() -> SSHKeychainLookup {
        let q: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: Self.legacyService,
            kSecAttrAccount as String: Self.legacyAccount,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var out: CFTypeRef?
        let status = SecItemCopyMatching(q as CFDictionary, &out)
        if status == errSecSuccess, let data = out as? Data {
            return .found(String(decoding: data, as: UTF8.self))
        }
        if status == errSecItemNotFound { return .notFound }
        return .unavailable(status)
    }
}

/// Manages local SSH keys: Keychain storage, legacy IOS-023 migration,
/// metadata persistence in UserDefaults, and pushing keys to the Go engine.
@Observable
final class SSHKeyStore {
    static let shared = SSHKeyStore()

    private static let defaultsKey = "sshKeys"

    private(set) var keys: [SSHKeyRecord] = []
    /// Whether the current key set has been read from Keychain and pushed to
    /// the Go SSH engine. False when a cold-start Keychain read was blocked
    /// (e.g. prewarming before first unlock) or engine sync failed.
    private(set) var isEngineSynced = false

    /// Primary default key's public line, or the first key's, or "".
    var primaryPublicKey: String {
        (keys.first(where: \.isDefault) ?? keys.first)?.publicKey ?? ""
    }

    private let defaults: UserDefaults
    private let keychain: SSHKeychain
    private let parseKey: @MainActor (String, String) throws -> ParsedKey
    private let generatePEM: @MainActor () -> String
    private let pushEngine: @MainActor (String) -> Bool
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
        pushEngine: @escaping @MainActor (String) -> Bool = { json in
            var error: NSError?
            guard TailnetSetSSHKeys(json, &error) else {
                ConnectionLog.note("ssh keys: engine sync failed: \(error?.localizedDescription ?? "unknown")")
                return false
            }
            return true
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
    /// pushes the loaded keys to the engine. Transient Keychain errors keep
    /// saved metadata intact and defer engine sync until the Keychain is ready.
    func load() {
        if let data = defaults.data(forKey: Self.defaultsKey),
           let decoded = try? JSONDecoder().decode([SSHKeyRecord].self, from: data) {
            hasInitialized = true
            var kept: [SSHKeyRecord] = []
            var payload: [[String: Any]] = []
            var pruned = false
            var unavailableStatus: OSStatus?
            for record in decoded {
                switch keychain.lookupPrivateKey(for: record.id) {
                case .found(let pem):
                    kept.append(record)
                    payload.append([
                        "id": record.id.uuidString.lowercased(),
                        "pem": pem,
                        "isDefault": record.isDefault,
                    ])
                case .notFound:
                    pruned = true
                case .unavailable(let status):
                    kept.append(record)
                    if unavailableStatus == nil { unavailableStatus = status }
                }
            }
            keys = kept
            if pruned { save() }
            if let status = unavailableStatus {
                isEngineSynced = false
                ConnectionLog.note("ssh keys: keychain unavailable on load (status \(status)); deferring engine sync")
                return
            }
            pushPayload(payload)
            return
        }
        switch keychain.lookupLegacyPrivateKey() {
        case .found(let legacy):
            let parsed: ParsedKey
            do {
                parsed = try parseKey(legacy, "")
            } catch {
                // An unparseable legacy item will never parse on retry; log and
                // sync an empty key set so SSH hosts are not permanently blocked.
                ConnectionLog.note("ssh keys: legacy key unparseable: \(error.localizedDescription)")
                pushPayload([])
                return
            }
            let id = UUID()
            do {
                try keychain.setPrivateKey(parsed.pem, for: id)
            } catch {
                isEngineSynced = false
                ConnectionLog.note("ssh keys: legacy key migration save failed: \(error.localizedDescription)")
                return
            }
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
            pushPayload([[
                "id": id.uuidString.lowercased(),
                "pem": parsed.pem,
                "isDefault": true,
            ]])
        case .notFound:
            pushPayload([])
        case .unavailable(let status):
            isEngineSynced = false
            ConnectionLog.note("ssh keys: legacy keychain unavailable on load (status \(status)); deferring engine sync")
        }
    }

    /// Ensures local SSH keys have been loaded from Keychain and pushed to the
    /// Go engine. Retries loading if an earlier cold-start attempt found the
    /// Keychain unavailable.
    @discardableResult
    func ensureSynced() -> Bool {
        if isEngineSynced { return true }
        load()
        return isEngineSynced
    }

    /// Creates a default Ed25519 key when the user adds their first SSH host
    /// before ever visiting Settings > SSH Keys. Does nothing if the user
    /// already initialized (e.g. explicitly deleted all keys).
    @discardableResult
    func ensureDefaultKey() throws -> SSHKeyRecord? {
        guard ensureSynced() else {
            throw RexError(code: "keychain", message: String(localized: "Rex Go's SSH connection is not ready"))
        }
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

    /// Sends all saved keys to the Go SSH engine. Returns false without pushing
    /// a partial list if the Keychain is temporarily unavailable.
    @discardableResult
    func syncToEngine() -> Bool {
        var payload: [[String: Any]] = []
        for k in keys {
            switch keychain.lookupPrivateKey(for: k.id) {
            case .found(let pem):
                payload.append([
                    "id": k.id.uuidString.lowercased(),
                    "pem": pem,
                    "isDefault": k.isDefault,
                ])
            case .notFound:
                continue
            case .unavailable(let status):
                isEngineSynced = false
                ConnectionLog.note("ssh keys: keychain unavailable on sync (status \(status)); deferring engine sync")
                return false
            }
        }
        return pushPayload(payload)
    }

    @discardableResult
    private func pushPayload(_ payload: [[String: Any]]) -> Bool {
        let json = (try? JSONSerialization.data(withJSONObject: payload))
            .map { String(decoding: $0, as: UTF8.self) } ?? "[]"
        let ok = pushEngine(json)
        isEngineSynced = ok
        return ok
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
