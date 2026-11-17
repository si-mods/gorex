import Compression
import Foundation
import ImageIO
import UniformTypeIdentifiers

/// Finds the images a pane's output shows (docs/ios-pane-gallery.md): the
/// image files a program prints the paths of, by the rules a tap on a path
/// follows (FileLink), and the pictures it draws in band with Kitty's
/// graphics protocol. It keeps its state between reads, as a path or an
/// image may come in two.
nonisolated struct GalleryScanner {
    enum Found: Equatable {
        /// A path as printed: the host resolves a relative one.
        case path(String)
        /// A Kitty image as a PNG file, its size in pixels, and the
        /// number (i=) the program gave it, which it may draw again.
        case kitty(Data, width: Int, height: Int, number: Int? = nil)
    }

    /// The largest image kept, as the server's maxGraphicsImage: a larger
    /// one is skipped.
    static let maxImage = 4 << 20
    /// The largest side and the most pixels of an image kept or drawn as a
    /// thumbnail: a size from the output is not trusted, as its product
    /// overflows and its decoding fills the memory.
    static let maxSide = 16384
    static let maxPixels = 50_000_000

    /// Whether an image of this size is small enough to keep.
    static func fits(width: Int, height: Int) -> Bool {
        width > 0 && height > 0 && width <= maxSide && height <= maxSide && width * height <= maxPixels
    }

    /// The files ImageIO draws: an SVG it does not.
    static let imageExtensions: Set<String> = ["png", "jpg", "jpeg", "gif", "webp", "heic", "avif", "bmp", "tiff"]

    private enum Mode {
        case ground, esc, escIntermediate, csi, osc, apc, string
    }

    private var mode = Mode.ground
    /// The parameters of the CSI read now, for those of SGR.
    private var csi: [UInt8] = []
    /// Whether SGR 8 hides the text now: a path in it is not collected,
    /// as no one sees it and the gallery downloads its thumbnail.
    private var concealed = false
    /// Whether the last byte of an OSC, APC or other string was ESC, which
    /// ends it before a backslash.
    private var stringEsc = false
    /// The visible text of the line so far: a quoted path may hold spaces.
    private var line: [UInt8] = []
    /// The hidden text of the line so far, the visible as blanks.
    private var hiddenLine: [UInt8] = []
    /// The image paths printed in hidden text, oldest first: Ghostty reads
    /// hidden text out as any other, so the screen's text (§3.4) does not
    /// show which is hidden, and a path in it is skipped by this list.
    private(set) var concealedPaths: [String] = []
    /// The most hidden paths remembered.
    static let concealedLimit = 500
    private var osc: [UInt8] = []
    private var apc: [UInt8] = []
    /// Whether the APC is not a Kitty one, or too large to keep.
    private var apcSkip = false
    private var transfer: Transfer?
    private var found: [Found] = []
    /// The target of the OSC 8 link open now, and the text printed in it.
    private var linkPath: String?
    private var linkText: [UInt8] = []

    /// A Kitty image whose chunks (m=1) still come.
    private struct Transfer {
        var keys: [String: String]
        var payload: [UInt8]
        /// An image of a kind not kept, or too large: its chunks go unread.
        var skip: Bool
    }

    private static let maxLine = 4096
    private static let maxOSC = 8192
    /// The base64 of the largest image, and the header round it.
    private static let maxPayload = maxImage / 3 * 4 + 8

    init() {}

    /// The images the bytes complete.
    mutating func feed(_ data: Data) -> [Found] {
        data.withUnsafeBytes { raw in
            for b in raw { step(b) }
        }
        defer { found = [] }
        return found
    }

    private mutating func step(_ b: UInt8) {
        switch mode {
        case .ground:
            switch b {
            case 0x1b: mode = .esc
            case 0x09: append(0x20)
            case 0x7f, ..<0x20: endLine()
            default: append(b)
            }
        case .esc:
            switch b {
            case UInt8(ascii: "["): mode = .csi; csi = []
            case UInt8(ascii: "]"): mode = .osc; osc = []; stringEsc = false
            case UInt8(ascii: "_"): mode = .apc; apc = []; apcSkip = false; stringEsc = false
            case UInt8(ascii: "P"), UInt8(ascii: "X"), UInt8(ascii: "^"): mode = .string; stringEsc = false
            case 0x1b: break
            // An intermediate, as the ( of ESC ( B, which picks a character
            // set: the bytes up to the final one are no text.
            case 0x20...0x2f: mode = .escIntermediate
            default:
                // A two-byte escape, as a reset, moves the cursor or clears.
                endLine()
                mode = .ground
            }
        case .escIntermediate:
            if b == 0x1b {
                mode = .esc
            } else if b < 0x20 {
                mode = .ground
                step(b)
            } else if b >= 0x30 {
                mode = .ground
            }
        case .csi:
            // Colors do not end a line, as a path printed in color shows
            // whole; what moves the cursor or clears does.
            if b >= 0x40 && b <= 0x7e {
                if b == UInt8(ascii: "m") { sgr() } else { endLine() }
                mode = .ground
            } else if b == 0x1b {
                mode = .esc
            } else if b < 0x20 {
                // A control cuts the CSI short; it runs as in text.
                mode = .ground
                step(b)
            } else if csi.count < 64 {
                csi.append(b)
            }
        case .osc:
            if stringEsc {
                stringEsc = false
                endOSC()
                if b != UInt8(ascii: "\\") { mode = .esc; step(b) }
            } else if b == 0x1b {
                stringEsc = true
            } else if b == 0x07 {
                endOSC()
            } else if osc.count < Self.maxOSC {
                osc.append(b)
            }
        case .apc:
            if stringEsc {
                stringEsc = false
                endAPC()
                if b != UInt8(ascii: "\\") { mode = .esc; step(b) }
            } else if b == 0x1b {
                stringEsc = true
            } else if !apcSkip {
                if apc.isEmpty && b != UInt8(ascii: "G") || apc.count >= Self.maxPayload + 1024 {
                    apcSkip = true
                    apc = []
                } else {
                    apc.append(b)
                }
            }
        case .string:
            if stringEsc {
                stringEsc = false
                mode = .ground
                if b != UInt8(ascii: "\\") { mode = .esc; step(b) }
            } else if b == 0x1b {
                stringEsc = true
            }
        }
    }

    /// Notes whether an SGR hides the text (8) or shows it (28, or a
    /// reset); the numbers of a color, as the 8 of 38;5;8, are skipped.
    private mutating func sgr() {
        // A private one, as CSI > 4 m, sets no attribute.
        guard csi.first.map({ $0 >= 0x30 && $0 <= 0x3b }) ?? true else { return }
        // A color's numbers after colons (38:5:8) stay in its own.
        let params = String(decoding: csi, as: UTF8.self).split(separator: ";", omittingEmptySubsequences: false)
        var i = 0
        while i < params.count {
            switch params[i] {
            case "", "0", "28": concealed = false
            case "8": concealed = true
            case "38", "48", "58":
                if i + 1 < params.count { i += params[i + 1] == "5" ? 2 : params[i + 1] == "2" ? 4 : 0 }
            default: break
            }
            i += 1
        }
    }

    // MARK: Paths

    private mutating func append(_ b: UInt8) {
        // Hidden text shows as blanks, and is read as them; it is read
        // apart to know its paths.
        hiddenLine.append(concealed ? b : 0x20)
        let b = concealed ? 0x20 : b
        // A line past its bound is read in parts: a path across the cut
        // is lost, as one the program wrapped is.
        if line.count >= Self.maxLine { endLine() }
        line.append(b)
        if linkPath != nil && linkText.count < Self.maxLine { linkText.append(b) }
    }

    private mutating func endLine() {
        defer { line = []; hiddenLine = [] }
        guard !line.isEmpty else { return }
        for p in Self.paths(inLine: String(decoding: line, as: UTF8.self)) { found.append(.path(p)) }
        guard hiddenLine.contains(where: { $0 != 0x20 }) else { return }
        for p in Self.paths(inLine: String(decoding: hiddenLine, as: UTF8.self)) where !concealedPaths.contains(p) {
            concealedPaths.append(p)
        }
        if concealedPaths.count > Self.concealedLimit { concealedPaths.removeFirst(concealedPaths.count - Self.concealedLimit) }
    }

    // MARK: Screen

    /// The most rows a path is joined across, the most rows of a screen
    /// read, and of the scrollback above it.
    static let maxJoin = 32
    static let maxScreenRows = 300
    static let maxScrollback = 200

    /// The image paths the rows of the screen show (docs/ios-pane-gallery.md
    /// §3.4), as Ghostty reads them out: soft-wrapped rows come as one. A
    /// program as Claude Code wraps a long path itself, a row full to the
    /// last column and the rest on the next, after an indent: such rows are
    /// read as one where that makes an image path across them, else each
    /// as it is.
    static func paths(inRows rows: [String], columns: Int) -> [String] {
        screen(rows: rows, columns: columns).paths
    }

    /// The image paths of the rows, and the pieces of them the rows show
    /// alone: the output's bytes, read a line at a time, find a piece as
    /// a path of its own, as wrapped/b.png of /tmp/a/wrapped/b.png.
    static func screen(rows: [String], columns: Int) -> (paths: [String], pieces: [String]) {
        var out: [String] = [], pieces: [String] = []
        var i = 0
        while i < rows.count {
            var text = rows[i], end = i
            var joined = rows[i], last = paths(inLine: rows[i])
            var k = i
            while k + 1 < rows.count, k - i < maxJoin - 1, fills(rows[k], columns: columns) {
                let next = rows[k + 1].drop { $0 == " " }
                guard let c = next.first, isPathCharacter(c) else { break }
                // A path that ends at the last column, and one that starts
                // the next row, as in a list, are two.
                let tail = joined.split(whereSeparator: \.isWhitespace).last ?? "", head = next.prefix { !$0.isWhitespace }
                // A row that ends in a plain word, as "to", goes on in
                // another word: "to" and "/tmp/x.png" are not one.
                guard startsPath(tail) else { break }
                if !paths(in: String(tail)).isEmpty && !paths(in: String(head)).isEmpty { break }
                joined += next
                k += 1
                // A path the join makes, which neither side holds alone.
                let now = paths(inLine: joined), alone = Set(last + paths(inLine: rows[k]))
                if now.contains(where: { !alone.contains($0) }) { (text, end) = (joined, k) }
                last = now
            }
            let found = paths(inLine: text)
            out += found
            if end > i {
                pieces += rows[i...end].flatMap { paths(inLine: String($0.drop { $0 == " " })) }.filter { !found.contains($0) }
            }
            i = end + 1
        }
        return (out, pieces)
    }

    /// Whether a word looks like the start of a path: it holds a slash, or
    /// starts with "~", ".", or a drive letter and a colon.
    static func startsPath(_ word: Substring) -> Bool {
        if word.contains(where: { $0 == "/" || $0 == "\\" }) || word.first == "~" || word.first == "." { return true }
        let c = Array(word.prefix(2))
        return c.count == 2 && c[0].isASCII && c[0].isLetter && c[1] == ":"
    }

    /// Whether a row's text reaches its last column, a path's character in
    /// it: the program may go on with the word on the next row.
    static func fills(_ row: String, columns: Int) -> Bool {
        guard columns > 0, let c = row.last, isPathCharacter(c) else { return false }
        let w = width(row)
        return w >= columns && w % columns == 0
    }

    static func isPathCharacter(_ c: Character) -> Bool {
        !c.isWhitespace && !FileLink.proseDelimiters.contains(c)
    }

    /// The columns a text takes: an East Asian wide character, as most
    /// CJK ones and emoji, takes two.
    static func width(_ text: String) -> Int {
        text.reduce(0) { n, c in
            let wide = c.unicodeScalars.contains { s in
                s.value == 0xfe0f || wideRanges.contains { $0.contains(s.value) }
            }
            return n + (wide ? 2 : 1)
        }
    }

    private static let wideRanges: [ClosedRange<UInt32>] = [
        0x1100...0x115f, 0x2e80...0x303e, 0x3041...0x33ff, 0x3400...0x4dbf, 0x4e00...0x9fff,
        0xa000...0xa4cf, 0xac00...0xd7a3, 0xf900...0xfaff, 0xfe30...0xfe4f, 0xff00...0xff60,
        0xffe0...0xffe6, 0x1f300...0x1f64f, 0x1f900...0x1f9ff, 0x20000...0x3fffd,
    ]

    /// The image paths a line of text prints: those in quotes first, read
    /// to the closing quote as they may hold spaces, then those in the
    /// words between spaces.
    static func paths(inLine text: String) -> [String] {
        var out: [String] = []
        var rest = ""
        var i = text.startIndex
        while i < text.endIndex {
            let c = text[i]
            if c == "'" || c == "\"", let close = text[text.index(after: i)...].firstIndex(of: c),
               let p = quotedPath(String(text[text.index(after: i)..<close])) {
                out.append(p)
                rest += " "
                i = text.index(after: close)
                continue
            }
            rest.append(c)
            i = text.index(after: i)
        }
        for word in rest.split(whereSeparator: \.isWhitespace) where mayBeImage(word) { out += paths(in: String(word)) }
        return out
    }

    /// The image path a quoted run is, whole: a Windows one, or one with
    /// a slash before its first space, so that a quoted sentence, as
    /// 'see /tmp/a.png', is none.
    static func quotedPath(_ inner: String) -> String? {
        if let p = windowsPath(inner), p == inner { return p }
        guard inner.prefix(while: { !$0.isWhitespace }).contains("/"),
              let p = FileLink.path(inner), p == inner, isImage(p), !isNetworkPath(p) else { return nil }
        return p
    }

    /// The image paths a word printed between spaces holds. Prose may run
    /// into a path without a space, as 截图：/tmp/a.png, and Markdown puts
    /// one in brackets, as ![shot](/tmp/a.png); Ghostty's matcher finds
    /// the path in both.
    static func paths(in word: String) -> [String] {
        word.split(whereSeparator: { FileLink.proseDelimiters.contains($0) }).compactMap { part in
            guard mayBeImage(part) else { return nil }
            var s = Substring(part)
            if let r = s.range(of: "](", options: .backwards) { s = s[r.upperBound...] }
            s = s.drop(while: { "([{<*\"'`".contains($0) })
            return windowsPath(String(s)) ?? imagePath(String(s))
        }
    }

    /// Whether a word holds an image's extension after a dot, as most
    /// words do not: a cheap test before the rules of a tapped path.
    static func mayBeImage(_ word: Substring) -> Bool {
        var i = word.startIndex
        while let dot = word[i...].firstIndex(of: ".") {
            let ext = word[word.index(after: dot)...].prefix { $0.isASCII && $0.isLetter }
            if imageExtensions.contains(ext.lowercased()) { return true }
            i = word.index(after: dot)
        }
        return false
    }

    /// The path of an image file a word prints, by the rules of a tapped
    /// path, nil for another word. Ghostty matches only paths with a slash.
    static func imagePath(_ word: String) -> String? {
        guard case .file(let p) = FileLink.target(word, matched: true), p.contains("/"), isImage(p), !isNetworkPath(p) else { return nil }
        return p
    }

    /// The Windows image path in a word, as Claude Code prints one on a
    /// Windows host: a drive's (C:\x.png, C:/x.png), from where it starts
    /// in the word, as in --out=C:\x.png, without what ends the sentence
    /// after it. A share's path is none (isNetworkPath).
    static func windowsPath(_ word: String) -> String? {
        let c = Array(word)
        var start: Int?
        for i in c.indices where i + 2 < c.count {
            // A drive after a slash ends a device path (\\?\C:\x.png).
            let drive = c[i].isASCII && c[i].isLetter && c[i + 1] == ":" && (c[i + 2] == "\\" || c[i + 2] == "/")
                && (i == 0 || !(c[i - 1].isLetter || c[i - 1].isNumber || c[i - 1] == "\\" || c[i - 1] == "/"))
            if drive {
                start = i
                break
            }
        }
        guard let start else { return nil }
        let p = FileLink.trimmed(String(c[start...]))
        let body = p.dropFirst(3)
        guard !body.isEmpty, !body.contains(where: { ":*?\"<>|\0".contains($0) }), isImage(p) else { return nil }
        return p
    }

    /// Whether a path ends in an image file's extension.
    static func isImage(_ path: String) -> Bool {
        guard let dot = path.lastIndex(of: "."), !path[dot...].contains(where: { $0 == "/" || $0 == "\\" }) else { return false }
        return imageExtensions.contains(path[path.index(after: dot)...].lowercased())
    }

    /// Whether a path names another computer or a device, as
    /// //server/share/x.png, \\server\share\x.png and \\?\C:\x.png, with
    /// either slash in either place, and once its %XX are decoded: the
    /// server refuses them, and a Windows host that read one would contact
    /// that computer (docs/lessons.md), so the gallery never collects one.
    static func isNetworkPath(_ path: String) -> Bool {
        [path, path.removingPercentEncoding ?? path].contains { p in
            let two = p.prefix(2)
            return two.count == 2 && two.allSatisfy { $0 == "/" || $0 == "\\" }
        }
    }

    /// A program's OSC 8 link to an image file: 8;params;file://... It is
    /// kept when its text ends, and only if the text is its target path:
    /// a link that shows one name and opens another file (docs/lessons.md)
    /// is not followed. The text is read as any other text, so a path
    /// printed in it counts on its own.
    private mutating func endOSC() {
        mode = .ground
        defer { osc = [] }
        guard osc.starts(with: Array("8;".utf8)) else { return }
        endLink()
        let parts = String(decoding: osc, as: UTF8.self).split(separator: ";", maxSplits: 2, omittingEmptySubsequences: false)
        guard parts.count == 3, let p = FileLink.fileURLPath(String(parts[2])), Self.isImage(p), !Self.isNetworkPath(p) else { return }
        linkPath = p
    }

    private mutating func endLink() {
        defer { linkPath = nil; linkText = [] }
        guard let p = linkPath else { return }
        let text = String(decoding: linkText, as: UTF8.self).trimmingCharacters(in: .whitespaces)
        if FileLink.trimmed(text) == p { found.append(.path(p)) }
    }

    // MARK: Kitty

    private mutating func endAPC() {
        mode = .ground
        defer { apc = [] }
        if apcSkip {
            // A Kitty chunk too large to keep ends its image: what follows
            // it reads as new, and has no size to be one.
            transfer = nil
            return
        }
        guard apc.first == UInt8(ascii: "G") else { return }
        let body = apc.dropFirst()
        let semi = body.firstIndex(of: UInt8(ascii: ";")) ?? body.endIndex
        let keys = Self.keys(body[body.startIndex..<semi])
        let payload = semi < body.endIndex ? body[(semi + 1)...] : []
        let more = keys["m"] == "1"
        if var t = transfer {
            // A chunk after the first carries only m (and q): it goes on
            // with the image the first began. Taken out, its payload grows
            // in place rather than copied for each chunk.
            transfer = nil
            if !t.skip {
                if t.payload.count + payload.count > Self.maxPayload {
                    t.skip = true
                    t.payload = []
                } else {
                    t.payload += payload
                }
            }
            transfer = more ? t : nil
            if !more, !t.skip, let image = Self.image(keys: t.keys, base64: t.payload) { found.append(image) }
            return
        }
        let action = keys["a"] ?? "t"
        guard action == "t" || action == "T" || action == "q" else { return }
        // The server rewrites a file's image in band: only t=d comes, and
        // a query (a=q) is no image the program shows.
        let kept = action != "q" && (keys["t"] ?? "d") == "d" && ["24", "32", "100"].contains(keys["f"] ?? "32")
        let t = Transfer(keys: keys, payload: Array(payload), skip: !kept || payload.count > Self.maxPayload)
        if more {
            transfer = t
        } else if !t.skip, let image = Self.image(keys: t.keys, base64: t.payload) {
            found.append(image)
        }
    }

    private static func keys(_ header: ArraySlice<UInt8>) -> [String: String] {
        var keys: [String: String] = [:]
        for kv in header.split(separator: UInt8(ascii: ",")) {
            let s = String(decoding: kv, as: UTF8.self)
            guard let eq = s.firstIndex(of: "=") else { continue }
            keys[String(s[..<eq])] = String(s[s.index(after: eq)...])
        }
        return keys
    }

    /// The PNG of a whole Kitty image: its base64 decoded, inflated with
    /// o=z, and raw pixels (f=24, f=32) of size s×v encoded; nil for one
    /// that does not read, or is too large.
    static func image(keys: [String: String], base64: [UInt8]) -> Found? {
        guard var data = Data(base64Encoded: Data(base64), options: .ignoreUnknownCharacters) else { return nil }
        if keys["o"] == "z" {
            guard let d = inflate(data, limit: maxImage) else { return nil }
            data = d
        }
        guard data.count <= maxImage else { return nil }
        // A number of 0 is none.
        let number = Int(keys["i"] ?? "").flatMap { $0 > 0 ? $0 : nil }
        switch keys["f"] ?? "32" {
        case "100":
            // The size the PNG's header declares, read without decoding it.
            guard data.starts(with: [0x89, 0x50, 0x4e, 0x47]),
                  let src = CGImageSourceCreateWithData(data as CFData, nil),
                  let props = CGImageSourceCopyPropertiesAtIndex(src, 0, nil) as? [CFString: Any],
                  let w = props[kCGImagePropertyPixelWidth] as? Int, let h = props[kCGImagePropertyPixelHeight] as? Int,
                  fits(width: w, height: h) else { return nil }
            return .kitty(data, width: w, height: h, number: number)
        case let f:
            let bpp = f == "24" ? 3 : 4
            // The sides are bounded before they multiply.
            guard let w = Int(keys["s"] ?? ""), let h = Int(keys["v"] ?? ""), fits(width: w, height: h),
                  w * h * bpp <= data.count, let png = png(data, width: w, height: h, bytesPerPixel: bpp) else { return nil }
            return .kitty(png, width: w, height: h, number: number)
        }
    }

    /// Raw RGB or RGBA pixels, row after row, as a PNG.
    private static func png(_ pixels: Data, width: Int, height: Int, bytesPerPixel bpp: Int) -> Data? {
        let info: CGBitmapInfo = bpp == 4
            ? CGBitmapInfo(rawValue: CGImageAlphaInfo.last.rawValue)
            : CGBitmapInfo(rawValue: CGImageAlphaInfo.none.rawValue)
        guard let provider = CGDataProvider(data: pixels as CFData),
              let image = CGImage(width: width, height: height, bitsPerComponent: 8, bitsPerPixel: bpp * 8, bytesPerRow: width * bpp,
                                  space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: info, provider: provider,
                                  decode: nil, shouldInterpolate: false, intent: .defaultIntent) else { return nil }
        let out = NSMutableData()
        guard let dest = CGImageDestinationCreateWithData(out, UTType.png.identifier as CFString, 1, nil) else { return nil }
        CGImageDestinationAddImage(dest, image, nil)
        return CGImageDestinationFinalize(dest) ? out as Data : nil
    }

    /// Inflates a zlib stream (RFC 1950), as Kitty's o=z sends it, up to
    /// limit bytes: a small payload must not grow without end.
    static func inflate(_ data: Data, limit: Int) -> Data? {
        // Compression's zlib is raw DEFLATE: the stream's two-byte header
        // and its checksum go.
        guard data.count > 6, data[data.startIndex] & 0x0f == 8 else { return nil }
        let deflate = data.dropFirst(2).dropLast(4)
        final class Sink { var data = Data() }
        struct TooLarge: Error {}
        let sink = Sink()
        do {
            let filter = try OutputFilter(.decompress, using: .zlib) { chunk in
                guard let chunk else { return }
                sink.data.append(chunk)
                if sink.data.count > limit { throw TooLarge() }
            }
            try filter.write(deflate)
            try filter.finalize()
        } catch {
            return nil
        }
        return sink.data
    }

    /// Forgets what a read left unfinished, as a new stream's snapshot
    /// replaces the screen.
    /// The hidden paths stay: the snapshot shows them again.
    mutating func reset() {
        let hidden = concealedPaths
        self = GalleryScanner()
        concealedPaths = hidden
    }
}
