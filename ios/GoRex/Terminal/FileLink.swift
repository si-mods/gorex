import Foundation

/// What a tapped link that is not a web or mail link opens, as the desktop
/// and the web client find it (web/src/links.ts, docs/WEB-001-open-files.md): a
/// file of the host, by a path Ghostty matched in the text or by a file://
/// URL, or a web address printed without its scheme. Ghostty matches only
/// paths with a slash, so a bare file name, as notes.md, is no link here.
nonisolated enum FileLink {
    enum Target: Equatable {
        /// The path to ask the host for, as printed: the server resolves a
        /// relative one against the directory of the session's program.
        /// Hidden when the text tapped need not show it: a program's link
        /// (OSC 8) may print chart.png over file:///home/u/.ssh/id_ed25519,
        /// so the app asks before it downloads.
        case file(String, hidden: Bool)
        case web(URL)
    }

    /// The target of a link Ghostty found in the text, else of a program's
    /// OSC 8 link, which only a file:// URL makes a file's.
    static func target(_ text: String, matched: Bool) -> Target? {
        guard matched else { return fileURLTarget(text, hidden: true) }
        // A word ends at a space or at the full-width punctuation of prose
        // (web/src/links.ts proseDelimiters), which has no space round it.
        let word = trimmed(String(text.prefix { !$0.isWhitespace && !proseDelimiters.contains($0) }))
        // Text Ghostty matched past the word may be what the tap was on: a
        // program can print the path unseen (SGR 8, or in the background's
        // color) and a harmless name after it, so the app asks.
        let spans = word != trimmed(text.trimmingCharacters(in: .whitespacesAndNewlines))
        if word.lowercased().hasPrefix("file:") { return fileURLTarget(word, hidden: spans) }
        guard !word.contains("://"), let path = path(word) else { return nil }
        if webPath(path), let url = URL(string: "https://" + path) { return .web(url) }
        // A path that climbs (/tmp/chart.png/../../home/u/.ssh/id_ed25519,
        // its tail printed unseen) asks, and shows the file the host opens.
        guard climbs(path) else { return .file(path, hidden: spans) }
        let c = clean(path)
        guard c != "/", c != "." else { return nil }
        return .file(c, hidden: true)
    }

    /// Whether a path has a .. segment, after a Windows drive's C: too
    /// (C:../x climbs, and cleaning keeps it).
    static func climbs(_ path: String) -> Bool {
        var rest = Substring(path)
        let chars = Array(path)
        if chars.count >= 2, chars[1] == ":", chars[0].isASCII, chars[0].isLetter { rest = rest.dropFirst(2) }
        return rest.split(separator: "/", omittingEmptySubsequences: false).contains("..")
    }

    /// A file:// URL's file, by its path cleaned (clean): what is shown and
    /// downloaded is the file the host opens, and a path that cleaning
    /// changes (dot segments that push the file out of the alert's view),
    /// or that climbs (a decoded %2F.., C:..), is hidden, so the app asks.
    private static func fileURLTarget(_ url: String, hidden: Bool) -> Target? {
        guard let p = fileURLPath(url) else { return nil }
        let c = clean(p)
        guard c != "/", c != "." else { return nil }
        return .file(c, hidden: hidden || c != p || climbs(p))
    }

    /// A path cleaned lexically as Go's path/filepath.Clean does on the
    /// host: empty and . segments dropped, .. taking the segment before
    /// it off (none above the root), a trailing slash gone; a Windows
    /// drive (C:) stays in front.
    static func clean(_ path: String) -> String {
        var vol = "", rest = Substring(path)
        let chars = Array(path)
        if chars.count >= 2, chars[1] == ":", chars[0].isASCII, chars[0].isLetter {
            vol = String(path.prefix(2))
            rest = rest.dropFirst(2)
        }
        let rooted = rest.hasPrefix("/")
        var out: [Substring] = []
        for seg in rest.split(separator: "/", omittingEmptySubsequences: true) {
            if seg == "." { continue }
            if seg == ".." {
                if let last = out.last, last != ".." { out.removeLast() } else if !rooted { out.append(seg) }
                continue
            }
            out.append(seg)
        }
        let body = (rooted ? "/" : "") + out.joined(separator: "/")
        return vol + (body.isEmpty ? "." : body)
    }

    /// The full-width punctuation of prose, which has no space round a
    /// path; the gallery's scanner splits words at it too.
    static let proseDelimiters = Set("，。；：！？、（）【】《》「」『』")

    /// The path a matched word prints, without the place in the file
    /// (main.go:12:3, main.cs(12,3), a.swift#L12C3) and the name of the option or
    /// variable it is the value of (--out=a/b.png); nil for a folder's (a
    /// trailing /), one written with backslashes, a variable's ($HOME/x,
    /// which the host does not expand), another machine's (remote) and
    /// what holds no letter or digit.
    /// A name without a slash needs a letter and an extension, so that
    /// version 1.2.3 is none.
    static func path(_ word: String) -> String? {
        var s = Substring(word)
        if let eq = s.firstIndex(of: "="), !s[..<eq].contains("/") { s = s[s.index(after: eq)...] }
        var p = String(s).replacing(#/(?::\d+(?::\d+)?|\(\d+(?:,\d+)?\)|#L\d+(?:C\d+)?)$/#, with: "")
        p = trimmed(p)
        guard !p.isEmpty, !p.hasSuffix("/"), !p.hasPrefix("$"), !remote(p),
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
    /// another URL, one without a path, one with a backslash (a separator
    /// to a Windows host, which could climb out unseen) or one of another
    /// machine (remote).
    static func fileURLPath(_ text: String) -> String? {
        guard text.lowercased().hasPrefix("file://"), let url = URL(string: text) else { return nil }
        var p = url.path(percentEncoded: false)
        let chars = Array(p)
        if chars.count > 2, chars[2] == ":" { p.removeFirst() }
        return p.isEmpty || p == "/" || p.contains("\\") || remote(p) ? nil : p
    }

    /// Whether a path names another machine or a device, not a file of the
    /// host's disks, as Windows reads it: a UNC path (//server/share,
    /// \\server\share), a device's (\\.\, \\?\) or an NT one (\??\), with
    /// either slash. A Windows host's stat of //attacker/share/x.png would
    /// reach that SMB server and hand it the user's NTLM hash.
    static func remote(_ path: String) -> Bool {
        let p = path.replacing("\\", with: "/")
        return p.hasPrefix("//") || p.hasPrefix("/??/")
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

    /// Reconstructs a long file path that a TUI (such as Claude Code or
    /// Antigravity CLI) hard-wrapped across consecutive rows on a narrow
    /// screen, given the visible `rows`, the tapped `row`, the `fragment`
    /// Ghostty matched on that row, and the terminal width in `columns`.
    /// Returns `nil` if the fragment does not span multiple wrapped rows.
    static func wrappedPath(rows: [String], row: Int, fragment: String, columns: Int) -> String? {
        guard columns > 0, rows.indices.contains(row), !fragment.isEmpty else { return nil }
        let isDelimiter: (Character) -> Bool = { $0.isWhitespace || proseDelimiters.contains($0) }
        let line = rows[row]
        guard let range = line.range(of: fragment) else { return nil }

        // Expand within `line` to the enclosing non-delimiter token.
        var tokStart = range.lowerBound
        while tokStart > line.startIndex {
            let prev = line.index(before: tokStart)
            if isDelimiter(line[prev]) { break }
            tokStart = prev
        }
        var tokEnd = range.upperBound
        while tokEnd < line.endIndex, !isDelimiter(line[tokEnd]) {
            tokEnd = line.index(after: tokEnd)
        }

        let leadingOnlySpaces: (String, String.Index) -> Bool = { s, idx in
            s[..<idx].allSatisfy { $0 == " " }
        }
        let trailingEmpty: (String, String.Index) -> Bool = { s, idx in
            s[idx...].allSatisfy { $0 == " " }
        }
        let nearFullWidth: (String) -> Bool = { s in
            let stripped = s.drop(while: { _ in false }).reversed().drop(while: { $0 == " " })
            guard let last = stripped.first, !isDelimiter(last) else { return false }
            let w = displayWidth(String(stripped.reversed()))
            return w >= max(columns - 8, 1)
        }
        let startsNewRoot: (Substring) -> Bool = { s in
            s.hasPrefix("~/") || s.hasPrefix("./") || s.hasPrefix("../") || s.hasPrefix("/")
        }

        var joined = String(line[tokStart..<tokEnd])
        var startRow = row
        var endRow = row
        let maxSteps = 6

        // Walk upward while the current token starts at the beginning of its
        // row (after leading TUI indent spaces) and the row above looks wrapped.
        var atRowStart = leadingOnlySpaces(line, tokStart)
        var r = row - 1
        while atRowStart, r >= max(0, row - maxSteps) {
            let prevLine = rows[r]
            guard nearFullWidth(prevLine) else { break }
            let trimmedRight = prevLine.reversed().drop(while: { $0 == " " }).reversed()
            guard !trimmedRight.isEmpty else { break }
            var pStart = trimmedRight.endIndex
            while pStart > trimmedRight.startIndex {
                let before = trimmedRight.index(before: pStart)
                if isDelimiter(trimmedRight[before]) { break }
                pStart = before
            }
            let prevTok = trimmedRight[pStart..<trimmedRight.endIndex]
            guard !prevTok.isEmpty else { break }
            // Two separate paths stacked on consecutive lines do not join.
            if startsNewRoot(Substring(joined)), !prevTok.hasSuffix("/") { break }
            let looksLikePath = prevTok.contains("/") || prevTok.hasPrefix("~") || prevTok.hasPrefix(".") || joined.contains("/")
            guard looksLikePath else { break }
            joined = String(prevTok) + joined
            startRow = r
            atRowStart = trimmedRight[..<pStart].allSatisfy { $0 == " " }
            r -= 1
        }

        // Walk downward while the current token reaches the end of a near-full
        // row and the next row continues the path after optional indent spaces.
        var atRowEnd = trailingEmpty(line, tokEnd) && nearFullWidth(line)
        r = row + 1
        while atRowEnd, r <= min(rows.count - 1, row + maxSteps) {
            let nextLine = rows[r]
            let afterSpaces = nextLine.drop(while: { $0 == " " })
            guard let firstChar = afterSpaces.first, !isDelimiter(firstChar) else { break }
            let nextTok = afterSpaces.prefix(while: { !isDelimiter($0) })
            guard !nextTok.isEmpty else { break }
            if startsNewRoot(nextTok), !joined.hasSuffix("/") { break }
            joined += nextTok
            endRow = r
            let rest = afterSpaces.dropFirst(nextTok.count)
            atRowEnd = rest.allSatisfy { $0 == " " } && nearFullWidth(nextLine)
            r += 1
        }

        guard startRow != endRow else { return nil }
        guard case .file(let full, let hidden) = target(joined, matched: true), !hidden, full != fragment else {
            return nil
        }
        return full
    }

    /// Terminal display columns of a string (2 for East Asian wide scalars).
    private static func displayWidth(_ text: String) -> Int {
        text.unicodeScalars.reduce(0) { n, s in
            let v = s.value
            let wide = (0x1100...0x115F).contains(v)
                || (0x2E80...0xA4CF).contains(v)
                || (0xAC00...0xD7A3).contains(v)
                || (0xF900...0xFAFF).contains(v)
                || (0xFE10...0xFE6F).contains(v)
                || (0xFF00...0xFF60).contains(v)
                || (0xFFE0...0xFFE6).contains(v)
                || (0x20000...0x3FFFD).contains(v)
            return n + (wide ? 2 : 1)
        }
    }
}
