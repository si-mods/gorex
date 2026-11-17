import Foundation
import XCTest

/// What a tapped link that is not a web or mail link opens: a host's file
/// by a path Ghostty matched or a file:// URL, or a web address printed
/// without its scheme, by the web client's rules (web/src/links.ts).
nonisolated final class FileLinkTests: XCTestCase {
    private func file(_ text: String, matched: Bool = true) -> String? {
        guard case .file(let p, _) = FileLink.target(text, matched: matched) else { return nil }
        return p
    }

    /// Absolute, from ~, ./ and ../, and relative with a slash, as printed:
    /// the server resolves a relative path.
    func testPaths() {
        for text in ["/Users/demo/proj/shot.png", "~/shots/a.png", "./out/b.png", "../out/b.png", "out/b.png",
                     ".claude/shots/c.png", "/tmp/截图/图.png", "./icon_(copy).png", "/tmp/no-extension"] {
            XCTAssertEqual(file(text), text, text)
        }
    }

    /// The punctuation of the sentence round a path, and the text after a
    /// space, are not part of it.
    func testAround() {
        for text in ["./out/b.png.", "./out/b.png,", "./out/b.png)", "./out/b.png!", "./out/b.png?",
                     "./out/b.png。", "./out/b.png，", "./out/b.png done.", "./out/b.png;"] {
            XCTAssertEqual(file(text), "./out/b.png", text)
        }
    }

    /// Prose in full-width punctuation follows a path without a space.
    func testProseAfterPath() {
        XCTAssertEqual(file("/tmp/x.png，请查看"), "/tmp/x.png")
        XCTAssertEqual(file("./out/b.png。结果"), "./out/b.png")
    }

    /// A place in the file, and the option a path is the value of.
    func testPlaceAndOption() {
        XCTAssertEqual(file("src/main.go:12:3"), "src/main.go")
        XCTAssertEqual(file("src/main.go:12:3:"), "src/main.go")
        XCTAssertEqual(file("src/main.go:12"), "src/main.go")
        XCTAssertEqual(file("ios/a.swift#L12C3"), "ios/a.swift")
        XCTAssertEqual(file("dir/a.cs(12,3)"), "dir/a.cs")
        XCTAssertEqual(file("dir/a.cs(12)"), "dir/a.cs")
        XCTAssertEqual(file("--out=./a.png"), "./a.png")
        XCTAssertEqual(file("./a=b/c.png"), "./a=b/c.png")
    }

    /// A folder, a Windows path, a variable and what holds no letter are
    /// no file.
    func testNotFiles() {
        for text in ["./out/", "~/", "./", "../", "/", "$HOME/a.png", "C:\\dir\\a.png", "./a\\b.png", "./...",
                     "ssh://host/a.png", "tel:123/4"] {
            XCTAssertNil(FileLink.target(text, matched: true), text)
        }
    }

    /// A name without a slash is a path only with a letter and an
    /// extension.
    func testBareName() {
        XCTAssertEqual(FileLink.path("notes.md"), "notes.md")
        XCTAssertNil(FileLink.path("1.2.3"))
        XCTAssertNil(FileLink.path("README"))
        XCTAssertNil(FileLink.path("notes."))
    }

    /// A web address without its scheme opens over https; a registry's own
    /// name is not a host.
    func testWebAddress() {
        XCTAssertEqual(FileLink.target("support.github.com/tickets", matched: true), .web(URL(string: "https://support.github.com/tickets")!))
        XCTAssertEqual(FileLink.target("example.com/a.png.", matched: true), .web(URL(string: "https://example.com/a.png")!))
        XCTAssertEqual(file("co.uk/a.png"), "co.uk/a.png")
        XCTAssertEqual(file("build.out/a.png"), "build.out/a.png")
    }

    /// A file:// URL, printed or a program's OSC 8 link: its host ignored,
    /// its path decoded, a Windows drive's slash taken off.
    func testFileURL() {
        XCTAssertEqual(file("file://demo-mac.local/Users/demo/a.png", matched: false), "/Users/demo/a.png")
        XCTAssertEqual(file("file:///tmp/a%20b.png", matched: false), "/tmp/a b.png")
        XCTAssertEqual(file("file:///tmp/a%20b.png.", matched: true), "/tmp/a b.png")
        XCTAssertEqual(file("FILE:///tmp/a.png", matched: false), "/tmp/a.png")
        XCTAssertEqual(file("file:///C:/dir/a.png", matched: false), "C:/dir/a.png")
        XCTAssertNil(FileLink.target("file:///", matched: false))
        XCTAssertNil(FileLink.target("file://host", matched: false))
    }

    /// A program's OSC 8 link is a file only by a file:// URL.
    func testOSC8() {
        XCTAssertNil(FileLink.target("/tmp/a.png", matched: false))
        XCTAssertNil(FileLink.target("https://example.com/a.png", matched: false))
        XCTAssertNil(FileLink.target("vscode://file/tmp/a.png", matched: false))
    }

    /// A program's link (OSC 8) may print other text over its file, as
    /// chart.png over a private key: the app asks before it downloads. A
    /// path or file:// URL printed as text shows what it opens.
    func testHiddenTarget() {
        XCTAssertEqual(FileLink.target("file:///home/demo/.ssh/id_ed25519", matched: false),
                       .file("/home/demo/.ssh/id_ed25519", hidden: true))
        XCTAssertEqual(FileLink.target("file:///home/demo/chart.png", matched: true), .file("/home/demo/chart.png", hidden: false))
        XCTAssertEqual(FileLink.target("./out/chart.png", matched: true), .file("./out/chart.png", hidden: false))
    }

    /// Text Ghostty matched past the word the path is taken from may be
    /// what the tap was on, after a path printed unseen: the app asks.
    func testMatchSpansWords() {
        XCTAssertEqual(FileLink.target("/home/demo/.ssh/id_ed25519 chart.png", matched: true),
                       .file("/home/demo/.ssh/id_ed25519", hidden: true))
        XCTAssertEqual(FileLink.target("file:///home/demo/.ssh/id_ed25519 chart.png", matched: true),
                       .file("/home/demo/.ssh/id_ed25519", hidden: true))
        XCTAssertEqual(FileLink.target("./out/b.png.", matched: true), .file("./out/b.png", hidden: false))
        XCTAssertEqual(FileLink.target("src/main.go:12:3", matched: true), .file("src/main.go", hidden: false))
    }

    /// A file:// URL's path is cleaned as the host cleans it, so the alert
    /// shows and the app downloads the file the host opens; a path that
    /// cleaning changes asks, even printed. A backslash, a separator to a
    /// Windows host, is no file.
    func testCleanedFileURL() {
        XCTAssertEqual(FileLink.target("file:///home/demo/.ssh/id_ed25519%2Fx%2F..", matched: false),
                       .file("/home/demo/.ssh/id_ed25519", hidden: true))
        XCTAssertEqual(FileLink.target("file:///home/demo/.ssh/id_ed25519%2Fx%2F..", matched: true),
                       .file("/home/demo/.ssh/id_ed25519", hidden: true))
        XCTAssertEqual(FileLink.target("file:///home/demo/Pictures/chart.png%2F%2E%2F..%2F..%2F.ssh%2Fid_ed25519", matched: false),
                       .file("/home/demo/.ssh/id_ed25519", hidden: true))
        XCTAssertNil(FileLink.target("file:///home/demo/a.png%5C..%5C..%5Cid_ed25519", matched: false))
        XCTAssertNil(FileLink.target("file:///tmp%2F..", matched: false))
        XCTAssertEqual(FileLink.clean("/a/./b/"), "/a/b")
        XCTAssertEqual(FileLink.clean("/../a"), "/a")
        XCTAssertEqual(FileLink.clean("a/../../b"), "../b")
        XCTAssertEqual(FileLink.clean("C:/a/../.."), "C:/")
        XCTAssertEqual(FileLink.clean("./a//b"), "a/b")
    }

    /// Another machine's path, which a Windows host would reach over SMB
    /// as it looks the file up, and a device's, are no file: UNC, \\.\,
    /// \\?\ and \??\, with either slash, printed or in a file:// URL.
    func testRemotePaths() {
        for text in ["//attacker/share/x.png", "//attacker/share/x.png.", "///attacker/share/x.png", "//?/UNC/attacker/x.png",
                     "/??/UNC/attacker/x.png"] {
            XCTAssertNil(FileLink.target(text, matched: true), text)
        }
        for url in ["file:////attacker/share/x.png", "file:///%2F%2Fattacker/share/x.png", "file:///%5C%5Cattacker%5Cshare%5Cx.png",
                    "file:///%5C??%5CUNC%5Cattacker%5Cx.png", "file:////./pipe/x"] {
            XCTAssertNil(FileLink.target(url, matched: false), url)
            XCTAssertNil(FileLink.target(url, matched: true), url)
        }
        XCTAssertTrue(FileLink.remote("\\\\attacker\\share\\x.png"))
        XCTAssertTrue(FileLink.remote("\\\\?\\UNC\\attacker\\x.png"))
        XCTAssertTrue(FileLink.remote("\\??\\UNC\\attacker\\x.png"))
        XCTAssertTrue(FileLink.remote("/\\attacker\\share"))
        XCTAssertFalse(FileLink.remote("/tmp/a.png"))
        XCTAssertFalse(FileLink.remote("C:/dir/a.png"))
        XCTAssertFalse(FileLink.remote("./a//b.png"))
    }

    /// The downloaded file's name never leaves its folder.
    func testFileName() {
        XCTAssertEqual(FileLink.fileName("shot.png", path: "./x"), "shot.png")
        XCTAssertEqual(FileLink.fileName("", path: "/tmp/out/b.png"), "b.png")
        XCTAssertEqual(FileLink.fileName("../../etc/x.png", path: "a"), "x.png")
        XCTAssertEqual(FileLink.fileName("..", path: "../"), "download")
    }

    /// download_begin's codes take the web client's words; another error
    /// keeps its message.
    func testReason() {
        XCTAssertEqual(FileLink.reason(code: "no_file", message: "open /tmp/x: no such file or directory"), String(localized: "No such file"))
        XCTAssertEqual(FileLink.reason(code: "is_folder", message: "x"), String(localized: "A folder is not downloaded"))
        XCTAssertEqual(FileLink.reason(code: "internal", message: "disk on fire"), "disk on fire")
    }
}
