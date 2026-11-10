import Foundation
import XCTest

/// The URL a tapped link opens, from what Ghostty matched in the text or a
/// program's OSC 8 link.
nonisolated final class WebLinkTests: XCTestCase {
    private func opens(_ text: String, matched: Bool = true) -> String? {
        WebLink.url(text, matched: matched)?.absoluteString
    }

    /// Ghostty takes CJK letters for a URL's: the URL ends where they start,
    /// and at full-width punctuation.
    func testCJKAfter() {
        XCTAssertEqual(opens("https://example.com/a的说明"), "https://example.com/a")
        XCTAssertEqual(opens("https://example.com/a，然后"), "https://example.com/a")
        XCTAssertEqual(opens("https://example.com/a。"), "https://example.com/a")
        XCTAssertEqual(opens("https://example.com/a 的"), "https://example.com/a")
    }

    /// Punctuation that ends a sentence, quotes and brackets around the URL.
    func testPunctuationAfter() {
        for text in ["https://example.com/a.", "https://example.com/a,", "https://example.com/a)",
                     "https://example.com/a\"", "https://example.com/a>", "https://example.com/a]",
                     "https://example.com/a'", "https://example.com/a!", "https://example.com/a?",
                     "https://example.com/a:", "https://example.com/a;", "https://example.com/a).", "https://example.com/a\")"] {
            XCTAssertEqual(opens(text), "https://example.com/a", text)
        }
    }

    /// What a URL means by its brackets, query and encoding stays.
    func testWhole() {
        for text in ["https://en.wikipedia.org/wiki/Foo_(bar)", "https://example.com/s?q=a&b=c#frag",
                     "https://example.com/%E4%BD%A0?x=%20", "https://example.com/a/",
                     "mailto:me@example.com", "HTTPS://EXAMPLE.COM/A"] {
            XCTAssertEqual(opens(text), text, text)
        }
        XCTAssertEqual(opens("https://en.wikipedia.org/wiki/Foo_(bar))"), "https://en.wikipedia.org/wiki/Foo_(bar)")
        XCTAssertEqual(WebLink.trimmed("https://example.com/[1]"), "https://example.com/[1]")
    }

    /// A program's OSC 8 link is taken as it is.
    func testOSC8() {
        XCTAssertEqual(opens("https://example.com/osc8?x=1.", matched: false), "https://example.com/osc8?x=1.")
        XCTAssertNotNil(opens("https://example.com/你好", matched: false))
    }

    /// A selection opens only when it is the link alone, the spaces round
    /// it and the text a matched link drops aside.
    func testSelected() {
        for text in ["https://example.com/a", " https://example.com/a\n", "https://example.com/a。", "https://example.com/a的说明"] {
            XCTAssertEqual(WebLink.selected(text)?.absoluteString, "https://example.com/a", text)
        }
        for text in ["https://example.com/a and more", "https://example.com/a\nnext line", "see https://example.com/a", "https://example.com/a\u{3000}的"] {
            XCTAssertNil(WebLink.selected(text), text)
        }
    }

    /// Only web and mail links open.
    func testSchemes() {
        for text in ["file:///etc/hosts", "javascript:alert(1)", "ssh://host", "tel:123", "myapp://x", "/usr/bin", "example.com"] {
            XCTAssertNil(opens(text), text)
            XCTAssertNil(opens(text, matched: false), text)
        }
    }
}
