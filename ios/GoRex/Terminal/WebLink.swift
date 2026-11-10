import Foundation

/// The link a tap opens: only a web or mail link, as the text means it.
nonisolated enum WebLink {
    /// The URL for a link Ghostty found in the text, else for a program's
    /// OSC 8 link, which is taken as it is.
    static func url(_ text: String, matched: Bool) -> URL? {
        guard let url = URL(string: matched ? trimmed(text) : text),
              let scheme = url.scheme?.lowercased(), ["http", "https", "mailto"].contains(scheme) else { return nil }
        return url
    }

    /// The link a selection is, when it is one word: the spaces round it
    /// go, and the text after the URL goes as a matched link's does.
    static func selected(_ text: String) -> URL? {
        let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !t.contains(where: \.isWhitespace) else { return nil }
        return url(t, matched: true)
    }

    /// A matched URL without the text after it. Ghostty takes CJK letters
    /// for a URL's, so it ends at the first character that is not ASCII, as
    /// a URL sends them encoded, and at a space; then without the
    /// punctuation that ends a sentence and a closing bracket it does not
    /// open.
    static func trimmed(_ text: String) -> String {
        var s = text.prefix { $0.isASCII && !$0.isWhitespace }
        let pairs: [Character: Character] = [")": "(", "]": "[", "}": "{", ">": "<"]
        while let c = s.last {
            if ".,;:!?'\"".contains(c) {
                s.removeLast()
            } else if let open = pairs[c], s.count(where: { $0 == c }) > s.count(where: { $0 == open }) {
                s.removeLast()
            } else {
                break
            }
        }
        return String(s)
    }
}
