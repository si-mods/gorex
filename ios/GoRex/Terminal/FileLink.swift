import Foundation

/// What a tapped link that is not a web or mail link opens, as the desktop
/// and the web client find it (web/src/links.ts, docs/web-open-files.md): a
/// file of the host, by a path Ghostty matched in the text or by a file://
/// URL, or a web address printed without its scheme. Ghostty matches only
/// paths with a slash, so a bare file name, as notes.md, is no link here.
nonisolated enum FileLink {
    enum Target: Equatable {
        /// The path to ask the host for, as printed: the server resolves a
        /// relative one against the directory of the session's program.
        case file(String)
        case web(URL)
    }

    /// The target of a link Ghostty found in the text, else of a program's
    /// OSC 8 link, which only a file:// URL makes a file's.
    static func target(_ text: String, matched: Bool) -> Target? {
        guard matched else { return fileURLPath(text).map(Target.file) }
        let word = trimmed(String(text.prefix { !$0.isWhitespace }))
        if word.lowercased().hasPrefix("file:") { return fileURLPath(word).map(Target.file) }
        guard !word.contains("://"), let path = path(word) else { return nil }
        if webPath(path), let url = URL(string: "https://" + path) { return .web(url) }
        return .file(path)
    }

    /// The path a matched word prints, without the place in the file
    /// (main.go:12:3, a.swift#L12C3) and the name of the option or
    /// variable it is the value of (--out=a/b.png); nil for a folder's (a
    /// trailing /), one written with backslashes, a variable's ($HOME/x,
    /// which the host does not expand) and what holds no letter or digit.
    /// A name without a slash needs a letter and an extension, so that
    /// version 1.2.3 is none.
    static func path(_ word: String) -> String? {
        var s = Substring(word)
        if let eq = s.firstIndex(of: "="), !s[..<eq].contains("/") { s = s[s.index(after: eq)...] }
        var p = String(s).replacing(#/(?::\d+(?::\d+)?|#L\d+(?:C\d+)?)$/#, with: "")
        p = trimmed(p)
        guard !p.isEmpty, !p.hasSuffix("/"), !p.hasPrefix("$"),
              !p.contains(where: { $0 == "\\" || $0 == ":" || $0 == "\0" }),
              p.contains(where: { $0.isLetter || $0.isNumber }) else { return nil }
        if !p.contains("/") {
            guard p.contains(where: \.isLetter), let dot = p.lastIndex(of: "."), p.index(after: dot) != p.endIndex else { return nil }
        }
        return p
    }

    /// The path a file:// URL names on the host, as the desktop's download
    /// reads it: the host part ignored, the path decoded, and a Windows
    /// drive's leading slash taken off (file:///C:/x is C:/x); nil for
    /// another URL, or one without a path.
    static func fileURLPath(_ text: String) -> String? {
        guard text.lowercased().hasPrefix("file://"), let url = URL(string: text) else { return nil }
        var p = url.path(percentEncoded: false)
        let chars = Array(p)
        if chars.count > 2, chars[2] == ":" { p.removeFirst() }
        return p.isEmpty || p == "/" ? nil : p
    }

    /// A word without what likely ends the sentence around it rather than
    /// it: punctuation, as the 。 of prose, and a closing bracket it did
    /// not open.
    static func trimmed(_ text: String) -> String {
        var s = text
        let pairs: [Character: Character] = [")": "(", "]": "[", "}": "{"]
        while let c = s.last {
            if ".,;:!?'\"".contains(c) || (!c.isASCII && c.isPunctuation) {
                s.removeLast()
            } else if let open = pairs[c], s.count(where: { $0 == c }) > s.count(where: { $0 == open }) {
                s.removeLast()
            } else {
                break
            }
        }
        return s
    }

    /// Whether a path reads as a web address without its scheme, as
    /// support.github.com/tickets: its first part a host name under a
    /// top-level domain, and not a registry's own name such as co.uk.
    static func webPath(_ path: String) -> Bool {
        guard let slash = path.firstIndex(of: "/") else { return false }
        let name = path[..<slash].lowercased()
        let labels = name.split(separator: ".", omittingEmptySubsequences: false)
        guard labels.count > 1, let tld = labels.last, tlds.contains(String(tld)) else { return false }
        return !(labels.count == 2 && tld.count == 2 && registryNames.contains(String(labels[0])))
    }

    /// The name a downloaded file takes in its own folder: the host's name
    /// for it, else the path's last part, never one that leaves the folder.
    static func fileName(_ name: String, path: String) -> String {
        for n in [name, path] {
            let last = n.split(whereSeparator: { $0 == "/" || $0 == "\\" }).last.map(String.init) ?? ""
            if !last.isEmpty, last != ".", last != ".." { return last }
        }
        return "download"
    }

    /// The words for download_begin's error codes, as the web client's
    /// (web/src/files.ts errorText); else the error's own message.
    static func reason(code: String, message: String) -> String {
        switch code {
        case "no_file": String(localized: "No such file")
        case "is_folder": String(localized: "A folder is not downloaded")
        case "not_regular": String(localized: "Not a regular file")
        case "denied": String(localized: "Permission denied")
        case "too_large": String(localized: "Too large")
        case "not_supported": String(localized: "The host does not send files")
        default: message
        }
    }

    /// The top-level domains a web address without its scheme may end in,
    /// as the web client's (web/src/links.ts tlds): every two-letter
    /// country code of the public suffix list, and common generic ones.
    private static let tlds: Set<String> = Set((
        "ac ad ae af ag ai al am ao aq ar as at au aw ax az ba bb bd be bf bh bi bj bm bn bo br bs bt bv bw by bz ca cc cd cf cg ch ci " +
        "cl cm cn co cr cu cv cw cx cy cz de dj dk dm do dz ec ee eg es et eu fi fj fm fo fr ga gb gd ge gf gg gh gi gl gm gn gp gq gr gs " +
        "gt gu gw gy hk hm hn hr ht hu id ie il im in io iq ir is it je jo jp ke kg ki km kn kp kr kw ky kz la lb lc li lk lr ls lt lu lv " +
        "ly ma mc md me mg mh mk ml mn mo mp mq mr ms mt mu mv mw mx my mz na nc ne nf ng ni nl no nr nu nz om pa pe pf ph pk pl pm pn pr " +
        "ps pt pw py qa re ro rs ru rw sa sb sc sd sg sh si sj sk sl sm sn so sr ss st su sv sx sy sz tc td tf tg th tj tk tl tm tn to tr " +
        "tt tv tw tz ua ug uk us uy uz va vc ve vg vi vn vu wf ws ye yt za zm zw " +
        "com net org edu gov mil int info biz name pro mobi aero asia cat coop jobs museum tel travel post " +
        "dev app page blog cloud tech online site store shop xyz top live news ink wiki world space website club design digital " +
        "email link fun art icu vip work today run rocks one zone systems software solutions network media agency studio team " +
        "tools codes engineering services academy center chat social group global company host hosting moe google"
    ).split(separator: " ").map(String.init))

    /// Second-level names a country's registry keeps, as co.uk: a public
    /// suffix itself, not a host.
    private static let registryNames: Set<String> = ["ac", "co", "com", "edu", "go", "gob", "gov", "mil", "ne", "net", "nic", "or", "org"]
}
