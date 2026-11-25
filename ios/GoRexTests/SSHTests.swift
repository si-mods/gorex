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
    }

    /// A saved list keeps an ssh:// host's ssh member, with the host key
    /// trusted, and never writes a transport of "ssh"; a list saved by a
    /// build without ssh:// still decodes, a system host (Migration).
    @MainActor func testSSHSavedRoundTrip() throws {
        var c = try XCTUnwrap(HostConfig(ssh: "ssh://alice@alice-laptop:2222/run/rex/server.sock"))
        c.ssh?.hostKey = "SHA256:AAAA"
        let lan = HostConfig(name: "lan", url: URL(string: "http://127.0.0.1:7297")!)
        let data = try JSONEncoder().encode([c, lan])
        let obj = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [[String: Any]])
        let ssh = try XCTUnwrap(obj[0]["ssh"] as? [String: Any])
        XCTAssertEqual(ssh["port"] as? Int, 2222)
        XCTAssertEqual(ssh["hostKey"] as? String, "SHA256:AAAA")
        XCTAssertNil(obj[0]["transport"])
        let back = try XCTUnwrap(try? JSONDecoder().decode([HostConfig].self, from: data))
        XCTAssertEqual(back, [c, lan])
        XCTAssertEqual(back[0].reach(tailnetOn: true), .ssh)
        XCTAssertNil(back[0].transport)

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
}
