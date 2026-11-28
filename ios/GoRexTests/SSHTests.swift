import Foundation
import UIKit
import XCTest

/// The ssh:// grammar (docs/IOS-023-ssh-hosts.md, The address): what it
/// parses, why each malformed one is refused, and the host it makes.
nonisolated final class SSHAddressTests: XCTestCase {
    @MainActor func testSSHAddress() throws {
        let full = try XCTUnwrap(HostConfig(ssh: "ssh://alice@alice-laptop:2222/run/rex/server.sock\n"))
        XCTAssertEqual(full.ssh, HostConfig.SSH(user: "alice", host: "alice-laptop", port: 2222, socket: "/run/rex/server.sock"))
        XCTAssertEqual(full.name, "alice@alice-laptop")
        XCTAssertEqual(full.url.absoluteString, "http://\(full.id.uuidString.lowercased()).ssh.invalid")
        XCTAssertEqual(HostConfig.tailcatID(full.id), full.url.host()?.split(separator: ".").first.map(String.init))
        XCTAssertNil(full.transport)
        XCTAssertNil(full.tailcat)
        // Over SSH whether or not the built-in Tailscale is on.
        XCTAssertEqual(full.reach(tailnetOn: false), .ssh)
        XCTAssertEqual(full.reach(tailnetOn: true), .ssh)

        let minimal = try XCTUnwrap(HostConfig(ssh: "ssh://bob@bob-desktop"))
        XCTAssertEqual(minimal.ssh, HostConfig.SSH(user: "bob", host: "bob-desktop", port: 22))
        XCTAssertNil(minimal.ssh?.socket)
        XCTAssertNil(minimal.ssh?.hostKey)
        XCTAssertNotNil(HostConfig(ssh: "SSH://carol@carol-server"))
        XCTAssertNil(HostConfig(ssh: "ssh://dave@dave-desktop/")?.ssh?.socket)
        XCTAssertEqual(HostConfig(ssh: "ssh://erin@[fd7a::1]:2222")?.ssh?.host, "fd7a::1")
        XCTAssertEqual(HostConfig(ssh: "ssh://erin@[fd7a::1]")?.ssh?.port, 22)

        for bad in ["", "ssh://", "ssh://alice-laptop", "ssh://@alice-laptop",
                    "ssh://alice@", "ssh://alice@:22", "ssh://alice@[fd7a::1",
                    "ssh://alice:secret@alice-laptop", "ssh://alice@alice-laptop:0",
                    "ssh://alice@alice-laptop:65536", "ssh://alice@alice-laptop:abc",
                    "ssh://alice@alice-laptop:", "ssh://alice@alice laptop",
                    "ssh://alice@alice-laptop?x=1", "ssh://alice@alice-laptop#f",
                    "http://alice@alice-laptop", "rexgo://tailcat?a=tcx"] {
            XCTAssertNil(HostConfig(ssh: bad), bad)
        }
    }

    /// The tcp:// endpoint of an ssh:// address, and its #k= token, which
    /// goes to the Keychain and never to the wire or the list saved
    /// (docs/IOS-024-ssh-tcp.md, The address).
    @MainActor func testSSHTCPAddress() throws {
        let tcp = try XCTUnwrap(HostConfig(ssh: "ssh://alice@alice-laptop/tcp://127.0.0.1:7297"))
        XCTAssertEqual(tcp.ssh, HostConfig.SSH(user: "alice", host: "alice-laptop", port: 22, tcp: "127.0.0.1:7297"))
        XCTAssertNil(tcp.ssh?.socket)
        XCTAssertNil(tcp.lanToken)
        XCTAssertNil(tcp.auth)
        XCTAssertEqual(tcp.reach(tailnetOn: true), .ssh)

        let v6 = try XCTUnwrap(HostConfig(ssh: "ssh://alice@alice-laptop:2222/tcp://[fd7a::2]:7297"))
        XCTAssertEqual(v6.ssh?.port, 2222)
        XCTAssertEqual(v6.ssh?.tcp, "[fd7a::2]:7297")

        // The #k= is the endpoint's pairing token, as a LAN code's; an
        // ssh-tcp host's requests carry it, a socket host's never do.
        let keyed = try XCTUnwrap(HostConfig(ssh: "ssh://alice@alice-laptop/tcp://127.0.0.1:7297#k=test-token-AAAA"))
        XCTAssertEqual(keyed.lanToken, "test-token-AAAA")
        XCTAssertEqual(keyed.auth, "test-token-AAAA")
        var socketKeyed = try XCTUnwrap(HostConfig(ssh: "ssh://alice@alice-laptop/run/rex/server.sock"))
        socketKeyed.lanToken = "test-token-AAAA"
        XCTAssertNil(socketKeyed.auth)

        XCTAssertTrue(HostConfig.SSH.validTCP("127.0.0.1:7297"))
        XCTAssertTrue(HostConfig.SSH.validTCP("windows-pc:7297"))
        XCTAssertTrue(HostConfig.SSH.validTCP("[fd7a::1]:7297"))
        for bad in ["ssh://alice@alice-laptop/tcp://", "ssh://alice@alice-laptop/tcp://127.0.0.1",
                    "ssh://alice@alice-laptop/tcp://:7297", "ssh://alice@alice-laptop/tcp://h:abc",
                    "ssh://alice@alice-laptop/tcp://h:0", "ssh://alice@alice-laptop/tcp://h:65536",
                    "ssh://alice@alice-laptop/tcp://fd7a::1:7297",
                    "ssh://alice@alice-laptop/tcp://127.0.0.1:7297/run/rex/server.sock",
                    "ssh://alice@alice-laptop#k=", "ssh://alice@alice-laptop#x=abc",
                    "ssh://alice@alice-laptop#k=a+b", "ssh://alice@alice-laptop#k=a#k=b",
                    "ssh://alice@alice-laptop/tcp://h:1?x=1"] {
            XCTAssertNil(HostConfig(ssh: bad), bad)
        }
        for bad in ["", "h", "h:", ":7297", "h:0", "h:65536", "h:abc", "h:p:7297", "fd7a::1:7297",
                    "[fd7a::1", "[fd7a::1]", "[fd7a::1]:x", "u@h:7297",
                    // As Go's net.SplitHostPort and strconv read them
                    // (#1154): a signed port, a bracket in the host.
                    "h:+7297", "h: 7297", "h[x]:7297", "h]:7297", "[a[b]:7297", "[fd7a::1]:+1"] {
            XCTAssertFalse(HostConfig.SSH.validTCP(bad), bad)
        }
        XCTAssertNil(HostConfig(ssh: "ssh://alice@alice-laptop:+22"))
        XCTAssertNil(HostConfig(ssh: "ssh://alice@alice-laptop/tcp://h:+7297"))
    }

    /// Each malformed ssh:// address names its fault in the field's
    /// footer; a good one has none, and so does a bare host[:port].
    @MainActor func testSSHRefusals() throws {
        for ok in ["ssh://alice@alice-laptop", "ssh://alice@alice-laptop:2222/run/rex/server.sock",
                   " SSH://alice@alice-laptop:22 "] {
            XCTAssertNil(HostConfig.refusal(of: ok), ok)
        }
        XCTAssertEqual(try XCTUnwrap(HostConfig.refusal(of: "ssh://alice:secret@alice-laptop")),
                       "ssh:// addresses don't take a password.")
        XCTAssertTrue(try XCTUnwrap(HostConfig.refusal(of: "ssh://alice-laptop")).contains("user"))
        XCTAssertTrue(try XCTUnwrap(HostConfig.refusal(of: "ssh://@alice-laptop")).contains("user"))
        XCTAssertTrue(try XCTUnwrap(HostConfig.refusal(of: "ssh://alice@")).contains("host"))
        XCTAssertTrue(try XCTUnwrap(HostConfig.refusal(of: "ssh://alice@alice-laptop:abc")).contains("port"))
        XCTAssertTrue(try XCTUnwrap(HostConfig.refusal(of: "ssh://alice@alice-laptop:65536")).contains("65535"))
        XCTAssertTrue(try XCTUnwrap(HostConfig.refusal(of: "ssh://alice@alice laptop")).contains("spaces"))
        XCTAssertTrue(try XCTUnwrap(HostConfig.refusal(of: "ssh://alice@alice-laptop?x=1")).contains("query"))
        // The socket must be an absolute path; the URL grammar's always
        // starts with "/", so a relative one is the parser's own check.
        XCTAssertTrue(HostConfig.SSH.validSocket("/run/rex/server.sock"))
        XCTAssertFalse(HostConfig.SSH.validSocket("run/rex/server.sock"))
        // A tcp:// endpoint is host:port, and never beside a socket; the
        // # takes only the pairing token's k=.
        XCTAssertTrue(try XCTUnwrap(HostConfig.refusal(of: "ssh://alice@alice-laptop/tcp://x")).contains("host:port"))
        XCTAssertTrue(try XCTUnwrap(HostConfig.refusal(of: "ssh://alice@alice-laptop/tcp://h:1/sock")).contains("not both"))
        XCTAssertTrue(try XCTUnwrap(HostConfig.refusal(of: "ssh://alice@alice-laptop#x=1")).contains("#k="))
        for ok in ["ssh://alice@alice-laptop/tcp://127.0.0.1:7297",
                   "ssh://alice@alice-laptop/tcp://127.0.0.1:7297#k=test-token-AAAA"] {
            XCTAssertNil(HostConfig.refusal(of: ok), ok)
        }
    }

    /// A saved list keeps an ssh:// host's ssh member, with the host key
    /// trusted, and never writes a transport of "ssh"; a list saved by a
    /// build without ssh:// still decodes, a system host (Migration).
    @MainActor func testSSHSavedRoundTrip() throws {
        var c = try XCTUnwrap(HostConfig(ssh: "ssh://alice@alice-laptop:2222/tcp://127.0.0.1:7297"))
        c.ssh?.hostKey = "SHA256:AAAA"
        // A #k= token lives in the Keychain, never in the list saved.
        var keyed = try XCTUnwrap(HostConfig(ssh: "ssh://bob@bob-laptop/tcp://10.0.0.1:7297#k=test-token-AAAA"))
        XCTAssertEqual(keyed.lanToken, "test-token-AAAA")
        let lan = HostConfig(name: "lan", url: URL(string: "http://127.0.0.1:7297")!)
        let data = try JSONEncoder().encode([c, keyed, lan])
        XCTAssertFalse(String(decoding: data, as: UTF8.self).contains("test-token-AAAA"))
        let obj = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [[String: Any]])
        let ssh = try XCTUnwrap(obj[0]["ssh"] as? [String: Any])
        XCTAssertEqual(ssh["port"] as? Int, 2222)
        XCTAssertEqual(ssh["tcp"] as? String, "127.0.0.1:7297")
        XCTAssertEqual(ssh["hostKey"] as? String, "SHA256:AAAA")
        XCTAssertNil(obj[0]["transport"])
        let back = try XCTUnwrap(try? JSONDecoder().decode([HostConfig].self, from: data))
        keyed.lanToken = nil
        XCTAssertEqual(back, [c, keyed, lan])
        XCTAssertEqual(back[0].reach(tailnetOn: true), .ssh)
        XCTAssertNil(back[0].transport)
        XCTAssertNil(back[1].lanToken)

        let legacy = Data(#"[{"id":"00000000-0000-0000-0000-000000000001","name":"old","url":"http://127.0.0.1:7297","desktop":false}]"#.utf8)
        let old = try XCTUnwrap(try? JSONDecoder().decode([HostConfig].self, from: legacy))
        XCTAssertEqual(old.count, 1)
        XCTAssertNil(old[0].ssh)
        XCTAssertEqual(old[0].reach(tailnetOn: true), .system)
    }

    /// The engine hears the ssh:// hosts as the list changes, beside the
    /// tailcat ones: at load, and as one is added and removed.
    @MainActor func testSSHHostsTold() throws {
        let d = UserDefaults.standard
        let (hosts, hook) = (d.data(forKey: "hosts"), Host.sshHosts)
        defer {
            d.set(hosts, forKey: "hosts")
            Host.sshHosts = hook
        }
        d.removeObject(forKey: "hosts")
        var told: [[HostConfig]] = []
        Host.sshHosts = { told.append($0) }
        let store = HostStore()
        XCTAssertEqual(told, [[]])
        let c = try XCTUnwrap(HostConfig(ssh: "ssh://alice@alice-laptop"))
        store.add(c)
        defer { store.remove(c.id) }
        XCTAssertEqual(told.last?.count, 1)
        XCTAssertEqual(told.last?.first?.ssh, c.ssh)
        store.remove(c.id)
        XCTAssertEqual(told.last, [])
    }

    /// An ssh-tcp host's token comes back from the Keychain on load, as a
    /// LAN host's does; a socket host's stays unread
    /// (docs/IOS-024-ssh-tcp.md).
    @MainActor func testSSHTCPTokenRestored() throws {
        let d = UserDefaults.standard, saved = d.data(forKey: "hosts")
        defer { d.set(saved, forKey: "hosts") }
        let tcp = try XCTUnwrap(HostConfig(ssh: "ssh://alice@alice-laptop/tcp://127.0.0.1:7297"))
        let unix = try XCTUnwrap(HostConfig(ssh: "ssh://bob@bob-laptop/run/rex/server.sock"))
        // A discovered endpoint may be tcp: the token learned for it
        // comes back too (#1154).
        let found = try XCTUnwrap(HostConfig(ssh: "ssh://carol@carol-pc"))
        d.set(try JSONEncoder().encode([tcp, unix, found]), forKey: "hosts")
        let tokens = MemoryLANTokens()
        tokens.set("kept-token", for: tcp.id)
        tokens.set("stray-token", for: unix.id)
        tokens.set("learned-token", for: found.id)
        let store = HostStore(tokens: tokens)
        defer { store.hosts.forEach { $0.stop() } }
        XCTAssertEqual(store.host(tcp.id)?.config.lanToken, "kept-token")
        XCTAssertEqual(store.host(tcp.id)?.config.auth, "kept-token")
        XCTAssertNil(store.host(unix.id)?.config.lanToken)
        XCTAssertNil(store.host(unix.id)?.config.auth)
        XCTAssertEqual(store.host(found.id)?.config.auth, "learned-token")
    }

    /// A failed connect of an ssh-tcp host with no token pulls the one
    /// the engine learned over SSH, into the Keychain and the host's
    /// config, so the next try sends it (docs/IOS-024-ssh-tcp.md).
    @MainActor func testSSHTCPTokenLearned() async throws {
        let d = UserDefaults.standard, saved = d.data(forKey: "hosts")
        let (hook, proxy) = (Host.sshHostToken, Host.tailcatProxy)
        defer {
            d.set(saved, forKey: "hosts")
            Host.sshHostToken = hook
            Host.tailcatProxy = proxy
        }
        d.removeObject(forKey: "hosts")
        let tokens = MemoryLANTokens()
        let store = HostStore(tokens: tokens)
        Host.sshHostToken = { store.learnToken("learned-token", for: $0) }
        // The proxy answers, but nothing listens on it: the dial fails,
        // and the retry asks the engine for the token it learned.
        Host.tailcatProxy = { LoopbackProxy(port: 1, password: "x", run: 0) }
        let c = try XCTUnwrap(HostConfig(ssh: "ssh://alice@alice-laptop/tcp://127.0.0.1:7297"))
        store.add(c)
        defer { store.remove(c.id) }
        await until("the token the engine learned") { tokens.tokens[c.id] != nil }
        XCTAssertEqual(tokens.tokens[c.id], "learned-token")
        XCTAssertEqual(store.host(c.id)?.config.auth, "learned-token")
    }

    /// HostConfig.SSH.keyID round-trips in JSON, while legacy records without
    /// keyID decode as nil (Automatic, docs/IOS-026-ssh-keys.md).
    @MainActor func testSSHHostKeyIDRoundTrip() throws {
        let keyID = UUID()
        var c = try XCTUnwrap(HostConfig(ssh: "ssh://alice@alice-laptop"))
        c.ssh?.keyID = keyID
        let data = try JSONEncoder().encode(c)
        let decoded = try JSONDecoder().decode(HostConfig.self, from: data)
        XCTAssertEqual(decoded.ssh?.keyID, keyID)

        let legacy = Data(#"{"user":"alice","host":"alice-laptop","port":22}"#.utf8)
        let decodedSSH = try JSONDecoder().decode(HostConfig.SSH.self, from: legacy)
        XCTAssertNil(decodedSSH.keyID)
    }

    /// SSHKeyStore manages local keys: legacy IOS-023 migration, generate,
    /// import, rename, default toggle, delete, and engine JSON sync.
    @MainActor func testSSHKeyStoreLifecycleAndMigration() throws {
        final class MemorySSHKeychain: SSHKeychain {
            var stored: [UUID: String] = [:]
            var legacy: String?
            var unavailableStatus: OSStatus?
            func lookupPrivateKey(for id: UUID) -> SSHKeychainLookup {
                if let status = unavailableStatus { return .unavailable(status) }
                return stored[id].map(SSHKeychainLookup.found) ?? .notFound
            }
            func setPrivateKey(_ pem: String?, for id: UUID) throws {
                if let status = unavailableStatus {
                    throw RexError(code: "keychain", message: "\(status)")
                }
                if let pem { stored[id] = pem } else { stored.removeValue(forKey: id) }
            }
            func lookupLegacyPrivateKey() -> SSHKeychainLookup {
                if let status = unavailableStatus { return .unavailable(status) }
                return legacy.map(SSHKeychainLookup.found) ?? .notFound
            }
        }

        let suiteName = "dev.gorex.tests.sshkeys.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suiteName))
        defer { defaults.removePersistentDomain(forName: suiteName) }

        let kc = MemorySSHKeychain()
        kc.legacy = "LEGACY-PEM"
        var pushedJSONs: [String] = []
        let fakeParse: @MainActor (String, String) throws -> SSHKeyStore.ParsedKey = { pem, _ in
            SSHKeyStore.ParsedKey(
                pem: pem,
                publicKey: "ssh-ed25519 AAAA-\(pem)",
                fingerprint: "SHA256:\(pem)",
                type: "ssh-ed25519"
            )
        }

        // Legacy key migrates automatically on first load.
        let store = SSHKeyStore(
            defaults: defaults,
            keychain: kc,
            parseKey: fakeParse,
            generatePEM: { "GEN-PEM" },
            pushEngine: {
                pushedJSONs.append($0)
                return true
            }
        )
        XCTAssertTrue(store.isEngineSynced)
        XCTAssertEqual(store.keys.count, 1)
        XCTAssertEqual(store.keys[0].name, "This Device")
        XCTAssertEqual(store.keys[0].isDefault, true)
        XCTAssertEqual(kc.privateKey(for: store.keys[0].id), "LEGACY-PEM")
        XCTAssertTrue(pushedJSONs.last?.contains("LEGACY-PEM") == true)

        // Generate a second key, rename it, toggle default, then import and delete.
        let generated = try store.generateKey(name: "Work", isDefault: false)
        XCTAssertEqual(store.keys.count, 2)
        XCTAssertEqual(generated.name, "Work")
        XCTAssertFalse(generated.isDefault)

        store.rename(id: generated.id, to: "Work Laptop")
        XCTAssertEqual(store.key(id: generated.id)?.name, "Work Laptop")

        store.setDefault(true, for: generated.id)
        XCTAssertEqual(store.key(id: generated.id)?.isDefault, true)

        let imported = try store.importKey(name: "Imported", pem: "IMP-PEM", isDefault: true)
        XCTAssertEqual(store.keys.count, 3)
        XCTAssertEqual(kc.privateKey(for: imported.id), "IMP-PEM")

        try store.delete(id: generated.id)
        XCTAssertEqual(store.keys.count, 2)
        XCTAssertNil(kc.privateKey(for: generated.id))
        XCTAssertFalse(pushedJSONs.last?.contains("GEN-PEM") == true)
    }

    /// Cold start while Keychain is unavailable (e.g. iOS prewarming before unlock)
    /// must keep saved SSH key metadata intact, avoid pushing an empty key set to
    /// the engine, and sync once Keychain becomes readable again.
    @MainActor func testSSHKeyStoreColdStartKeychainUnavailableDefersSync() throws {
        final class LockedMemorySSHKeychain: SSHKeychain {
            var stored: [UUID: String] = [:]
            var legacy: String?
            var locked = true
            func lookupPrivateKey(for id: UUID) -> SSHKeychainLookup {
                if locked { return .unavailable(errSecInteractionNotAllowed) }
                return stored[id].map(SSHKeychainLookup.found) ?? .notFound
            }
            func setPrivateKey(_ pem: String?, for id: UUID) throws {
                if locked { throw RexError(code: "keychain", message: "locked") }
                if let pem { stored[id] = pem } else { stored.removeValue(forKey: id) }
            }
            func lookupLegacyPrivateKey() -> SSHKeychainLookup {
                if locked { return .unavailable(errSecInteractionNotAllowed) }
                return legacy.map(SSHKeychainLookup.found) ?? .notFound
            }
        }

        let suiteName = "dev.gorex.tests.sshkeys.locked.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suiteName))
        defer { defaults.removePersistentDomain(forName: suiteName) }

        let keyID = UUID()
        let savedRecord = SSHKeyRecord(
            id: keyID,
            name: "This Device",
            type: "ssh-ed25519",
            publicKey: "ssh-ed25519 AAAA-SAVED",
            fingerprint: "SHA256:SAVED",
            createdAt: Date(),
            isDefault: true
        )
        defaults.set(try JSONEncoder().encode([savedRecord]), forKey: "sshKeys")

        let kc = LockedMemorySSHKeychain()
        kc.stored[keyID] = "SAVED-PEM"
        var pushedJSONs: [String] = []
        let store = SSHKeyStore(
            defaults: defaults,
            keychain: kc,
            generatePEM: { "NEW-PEM" },
            pushEngine: {
                pushedJSONs.append($0)
                return true
            }
        )

        // While Keychain is locked: metadata is preserved, nothing pushed to engine.
        XCTAssertFalse(store.isEngineSynced)
        XCTAssertEqual(store.keys.map(\.id), [keyID])
        XCTAssertTrue(pushedJSONs.isEmpty)
        XCTAssertThrowsError(try store.ensureDefaultKey())
        let persistedAfterLockedLoad = try XCTUnwrap(defaults.data(forKey: "sshKeys"))
        let decodedAfterLockedLoad = try JSONDecoder().decode([SSHKeyRecord].self, from: persistedAfterLockedLoad)
        XCTAssertEqual(decodedAfterLockedLoad.map(\.id), [keyID])

        // Once Keychain unlocks: ensureSynced() loads and pushes the real key.
        kc.locked = false
        XCTAssertTrue(store.ensureSynced())
        XCTAssertTrue(store.isEngineSynced)
        XCTAssertEqual(pushedJSONs.count, 1)
        XCTAssertTrue(pushedJSONs[0].contains("SAVED-PEM"))
    }

    /// SSH hosts gate on Host.sshProxy rather than Host.tailcatProxy so a ready
    /// tailcat key cannot let an SSH host dial before SSH keys are synced.
    @MainActor func testSSHHostUsesDedicatedSSHProxyGate() throws {
        let (oldTailcat, oldSSH) = (Host.tailcatProxy, Host.sshProxy)
        defer {
            Host.tailcatProxy = oldTailcat
            Host.sshProxy = oldSSH
        }
        Host.tailcatProxy = { LoopbackProxy(port: 12345, password: "pw", run: 0) }
        Host.sshProxy = { nil }
        let sshHost = Host(try XCTUnwrap(HostConfig(ssh: "ssh://alice@alice-laptop")))
        XCTAssertThrowsError(try sshHost.session())

        Host.sshProxy = { LoopbackProxy(port: 12345, password: "pw", run: 0) }
        XCTAssertNoThrow(try sshHost.session())
    }
}

/// The review findings of #1154 and #1155 on the app's side that the
/// test target reaches: the Add Host fields' grammar, a refused token
/// and the one-time-code prompts. (TailnetEngine, which holds the cards'
/// dismisses and the install, links the engine and is not in it.)
nonisolated final class SSHReviewTests: XCTestCase {
    /// The Add Host fields take what the ssh:// address takes, and
    /// nothing it refuses (#1155).
    @MainActor func testSSHFields() throws {
        XCTAssertEqual(HostConfig.ssh(user: " alice ", host: "alice-laptop", port: "", endpoint: ""),
                       HostConfig.SSH(user: "alice", host: "alice-laptop", port: 22))
        XCTAssertEqual(HostConfig.ssh(user: "alice", host: "alice-laptop", port: "2222", endpoint: "/run/rex/server.sock"),
                       HostConfig.SSH(user: "alice", host: "alice-laptop", port: 2222, socket: "/run/rex/server.sock"))
        XCTAssertEqual(HostConfig.ssh(user: "alice", host: "alice-laptop", port: "", endpoint: "tcp://127.0.0.1:7297")?.tcp,
                       "127.0.0.1:7297")
        XCTAssertEqual(HostConfig.ssh(user: "alice", host: "alice-laptop", port: "", endpoint: "127.0.0.1:7297")?.tcp,
                       "127.0.0.1:7297")
        XCTAssertEqual(HostConfig.ssh(user: "alice", host: "fd7a::1", port: "", endpoint: "")?.host, "fd7a::1")
        XCTAssertEqual(HostConfig.ssh(user: "alice", host: "[fd7a::1]", port: "", endpoint: "")?.host, "fd7a::1")
        // A Windows domain login, and a socket path with a space or a
        // percent sign, as an address spells them percent-encoded.
        XCTAssertEqual(HostConfig.ssh(user: "alice@example.com", host: "windows-pc", port: "", endpoint: "")?.user,
                       "alice@example.com")
        let mac = "/Users/alice/Library/Application Support/rex/server.sock"
        XCTAssertEqual(HostConfig.ssh(user: "alice", host: "alice-laptop", port: "", endpoint: mac)?.socket, mac)
        XCTAssertEqual(HostConfig.ssh(user: "alice", host: "alice-laptop", port: "", endpoint: "/tmp/a%41")?.socket, "/tmp/a%41")
        XCTAssertEqual(HostConfig.ssh(user: "alice", host: "alice-laptop", port: "", endpoint: "tcp://[fd7a::2]:7297")?.tcp,
                       "[fd7a::2]:7297")
        let bad: [(String, String, String, String)] = [
            ("", "alice-laptop", "", ""), ("alice", "", "", ""),
            ("alice", "alice-laptop:2222", "", ""), ("alice", "alice laptop", "", ""),
            ("alice", "alice-laptop/x", "", ""), ("alice", "bob@alice-laptop", "", ""),
            ("alice", "alice-laptop?x", "", ""), ("alice", "alice-laptop#x", "", ""),
            ("alice", "alice-laptop", "+22", ""), ("alice", "alice-laptop", "0", ""),
            ("alice", "alice-laptop", "65536", ""), ("alice", "alice-laptop", "x", ""),
            ("alice", "alice-laptop", "", "run/rex"), ("alice", "alice-laptop", "", "tcp://h"),
            ("alice", "alice-laptop", "", "tcp://h:7297/sock"),
        ]
        for (u, h, p, e) in bad {
            XCTAssertNil(HostConfig.ssh(user: u, host: h, port: p, endpoint: e), "\(u) \(h) \(p) \(e)")
        }
    }

    /// A refused connect retries with a token it did not send; an ssh
    /// host's token is read again once — a kept one refused, as after a
    /// rotate, or none kept from a read before the gateway had one — and
    /// then it waits for the user (#1154).
    @MainActor func testRefusalStep() {
        XCTAssertEqual(Host.refusalStep(sent: nil, now: "t1", ssh: true, relearned: false), .retry)
        XCTAssertEqual(Host.refusalStep(sent: nil, now: nil, ssh: true, relearned: false), .relearn)
        XCTAssertEqual(Host.refusalStep(sent: nil, now: nil, ssh: true, relearned: true), .settle)
        XCTAssertEqual(Host.refusalStep(sent: nil, now: nil, ssh: false, relearned: false), .settle)
        XCTAssertEqual(Host.refusalStep(sent: "t1", now: "t1", ssh: true, relearned: false), .relearn)
        XCTAssertEqual(Host.refusalStep(sent: "t1", now: "t2", ssh: true, relearned: true), .retry)
        XCTAssertEqual(Host.refusalStep(sent: "t1", now: "t1", ssh: true, relearned: true), .settle)
        // A LAN host's token comes from its pairing code alone.
        XCTAssertEqual(Host.refusalStep(sent: "t1", now: "t1", ssh: false, relearned: false), .settle)
    }

    /// A one-time-code prompt gets the code field whatever its case
    /// (#1155).
    @MainActor func testOTPPromptAnyCase() {
        for p in ["OTP:", "Token:", "2FA Code", "Verification code:", "one-time password"] {
            XCTAssertTrue(HostConfig.SSH.asksOneTimeCode(p), p)
        }
        XCTAssertFalse(HostConfig.SSH.asksOneTimeCode("Password:"))
    }

    /// TopAlertPresenter resolves the topmost presented sheet instead of
    /// the root view controller, so SSH trust/password alerts still
    /// present when Settings › Remote Hosts is open, and defers while a
    /// sheet or alert is already on top or mid-transition.
    @MainActor func testTopAlertPresenterOverSheet() {
        final class FakeVC: UIViewController {
            var fakePresented: UIViewController?
            var fakePresenting = false
            var fakeDismissing = false
            override var presentedViewController: UIViewController? { fakePresented }
            override var isBeingPresented: Bool { fakePresenting }
            override var isBeingDismissed: Bool { fakeDismissing }
        }

        let root = FakeVC()
        // No sheet: root itself is the presenter target.
        XCTAssertTrue(TopAlertPresenter.topViewController(from: root) === root)

        // With a sheet presented (like Settings), the sheet is the target,
        // not the underlying root view controller.
        let settingsSheet = FakeVC()
        root.fakePresented = settingsSheet
        XCTAssertTrue(TopAlertPresenter.topViewController(from: root) === settingsSheet)

        // While a child sheet (like Add Host) is mid-dismissal, defer.
        let addHostSheet = FakeVC()
        addHostSheet.fakeDismissing = true
        settingsSheet.fakePresented = addHostSheet
        XCTAssertNil(TopAlertPresenter.topViewController(from: root))

        // While a sheet is mid-presentation, defer.
        addHostSheet.fakeDismissing = false
        addHostSheet.fakePresenting = true
        XCTAssertNil(TopAlertPresenter.topViewController(from: root))

        // When another UIAlertController is already on top, defer.
        let existingAlert = UIAlertController(title: "Other", message: nil, preferredStyle: .alert)
        settingsSheet.fakePresented = existingAlert
        XCTAssertNil(TopAlertPresenter.topViewController(from: root))

        // Once dismissed, settingsSheet is ready to present again.
        settingsSheet.fakePresented = nil
        XCTAssertTrue(TopAlertPresenter.topViewController(from: root) === settingsSheet)

        // ObservableAlertController reports unexpected disappearances unless handled.
        let unhandled = ObservableAlertController(title: "Trust", message: nil, preferredStyle: .alert)
        var fired = 0
        unhandled.onUnexpectedDismiss = { fired += 1 }
        unhandled.viewDidDisappear(false)
        XCTAssertEqual(fired, 1)

        let handled = ObservableAlertController(title: "Trust", message: nil, preferredStyle: .alert)
        handled.onUnexpectedDismiss = { fired += 1 }
        handled.markHandled()
        handled.viewDidDisappear(false)
        XCTAssertEqual(fired, 1)
    }
}
