import Foundation
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
                    "[fd7a::1", "[fd7a::1]", "[fd7a::1]:x", "u@h:7297"] {
            XCTAssertFalse(HostConfig.SSH.validTCP(bad), bad)
        }
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
        d.set(try JSONEncoder().encode([tcp, unix]), forKey: "hosts")
        let tokens = MemoryLANTokens()
        tokens.set("kept-token", for: tcp.id)
        tokens.set("stray-token", for: unix.id)
        let store = HostStore(tokens: tokens)
        defer { store.hosts.forEach { $0.stop() } }
        XCTAssertEqual(store.host(tcp.id)?.config.lanToken, "kept-token")
        XCTAssertEqual(store.host(tcp.id)?.config.auth, "kept-token")
        XCTAssertNil(store.host(unix.id)?.config.lanToken)
        XCTAssertNil(store.host(unix.id)?.config.auth)
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
}
