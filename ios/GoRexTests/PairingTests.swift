import Foundation
import XCTest

nonisolated final class PairingTests: XCTestCase {
    @MainActor func testPairingCodes() throws {
        let ts = try XCTUnwrap(HostConfig(pairing: "https://yibo-macbook-gorex.tail1234.ts.net"))
        XCTAssertEqual(ts.url.absoluteString, "https://yibo-macbook-gorex.tail1234.ts.net")
        XCTAssertEqual(ts.name, "yibo-macbook")
        XCTAssertFalse(ts.desktop)

        let lan = try XCTUnwrap(HostConfig(pairing: "http://192.168.88.119:7297\n"))
        XCTAssertEqual(lan.url.absoluteString, "http://192.168.88.119:7297")
        XCTAssertEqual(lan.name, "192.168.88.119")

        XCTAssertEqual(HostConfig(pairing: "HTTPS://studio.local:7297")?.url.absoluteString, "https://studio.local:7297")
        XCTAssertEqual(HostConfig(pairing: "HTTPS://studio.local:7297")?.name, "studio")
        XCTAssertEqual(HostConfig(pairing: "https://rex.tail1234.ts.net")?.name, "rex")

        for bad in ["", "hello", "192.168.88.119:7297", "ftp://host", "http://", "http://:7297",
                    "http://host/", "http://host/api", "http://host?x=1", "http://host?", "http://host#a",
                    "http://user@host", "http://user:pw@host", "http://host:port"] {
            XCTAssertNil(HostConfig(pairing: bad), bad)
        }
    }

    @MainActor func testNames() {
        XCTAssertEqual(HostConfig.name("yibo-macbook-gorex.tail1234.ts.net"), "yibo-macbook")
        XCTAssertEqual(HostConfig.name("studio.tail1234.ts.net"), "studio")
        XCTAssertEqual(HostConfig.name("studio"), "studio")
        XCTAssertEqual(HostConfig.name("-gorex.example"), "-gorex")
        XCTAssertEqual(HostConfig.name("10.0.0.2"), "10.0.0.2")
        XCTAssertEqual(HostConfig.name("[fd7a:115c::1]"), "fd7a:115c::1")
        XCTAssertEqual(HostConfig.name("fd7a:115c::1"), "fd7a:115c::1")
    }
}

nonisolated final class CommandTests: XCTestCase {
    @MainActor func testArgv() {
        XCTAssertEqual(HostConfig.argv(""), [])
        XCTAssertEqual(HostConfig.argv("   "), [])
        XCTAssertEqual(HostConfig.argv("tmux new -A -s main"), ["tmux", "new", "-A", "-s", "main"])
        XCTAssertEqual(HostConfig.argv(#"zsh -c "echo hi; exec zsh""#), ["zsh", "-c", "echo hi; exec zsh"])
        XCTAssertEqual(HostConfig.argv(#"a 'b "c" d' "e 'f'""#), ["a", #"b "c" d"#, "e 'f'"])
        XCTAssertEqual(HostConfig.argv(#"a "" b"#), ["a", "", "b"])
        XCTAssertEqual(HostConfig.argv(#"one\ word 'it\s'"#), ["one word", #"it\s"#])
        XCTAssertEqual(HostConfig.argv(#"x"y z"w"#), ["xy zw"])
    }
}

nonisolated final class ThemeTests: XCTestCase {
    @MainActor func testMessage() throws {
        let json = #"{"name":"T","colors":[0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,16777215,1193046,6636321,11259375,0,0,0]}"#
        let t = try JSONDecoder().decode(TerminalTheme.self, from: Data(json.utf8))
        let m = t.message
        XCTAssertEqual(m["background"] as? String, "123456")
        XCTAssertEqual(m["foreground"] as? String, "654321")
        XCTAssertEqual(m["cursor"] as? String, "abcdef")
        let palette = try XCTUnwrap(m["palette"] as? [String])
        XCTAssertEqual(palette.count, 16)
        XCTAssertEqual(palette.first, "0=000000")
        XCTAssertEqual(palette.last, "15=ffffff")
    }

    /// Gruvbox Dark until a theme is chosen; the host's colors once chosen.
    @MainActor func testName() {
        let d = UserDefaults.standard, saved = d.object(forKey: "theme")
        defer { d.set(saved, forKey: "theme") }
        d.removeObject(forKey: "theme")
        XCTAssertEqual(TerminalTheme.name, "Gruvbox Dark")
        d.set("", forKey: "theme")
        XCTAssertEqual(TerminalTheme.name, "")
        XCTAssertNil(TerminalTheme.chosen)
        d.set("Nord", forKey: "theme")
        XCTAssertEqual(TerminalTheme.name, "Nord")
    }

    /// The default is one of the themes the app ships.
    @MainActor func testStandardShips() throws {
        let url = URL(fileURLWithPath: #filePath).deletingLastPathComponent().appending(path: "../GoRex/ghostty-themes.json")
        let all = try JSONDecoder().decode([TerminalTheme].self, from: Data(contentsOf: url))
        XCTAssertTrue(all.contains { $0.name == TerminalTheme.standard })
    }

    @MainActor func testLight() {
        XCTAssertTrue(VTerminal.RGB(0xfbf1c7).light)
        XCTAssertFalse(VTerminal.RGB(0x282828).light)
    }
}
