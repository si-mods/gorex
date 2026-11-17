import Compression
import Foundation
import ImageIO
import UniformTypeIdentifiers
import XCTest

/// A pane's gallery (docs/ios-pane-gallery.md): the image paths and Kitty
/// images its output shows, found across reads, and kept on disk.
nonisolated final class GalleryTests: XCTestCase {
    private func scan(_ parts: String...) -> [GalleryScanner.Found] {
        var s = GalleryScanner()
        return parts.flatMap { s.feed(Data($0.utf8)) }
    }

    private func paths(_ found: [GalleryScanner.Found]) -> [String] {
        found.compactMap { if case .path(let p) = $0 { p } else { nil } }
    }

    // MARK: Paths

    func testPathSplitAcrossReads() {
        XCTAssertEqual(paths(scan("saved to /tmp/sh", "ots/a.png\r\n")), ["/tmp/shots/a.png"])
        // A sequence cut in two does not end the path.
        XCTAssertEqual(paths(scan("./out/b.png\u{1b}", "[0m done\n")), ["./out/b.png"])
    }

    func testColoredPath() {
        XCTAssertEqual(paths(scan("\u{1b}[1;32m/tmp/demo/chart.PNG\u{1b}[0m\n")), ["/tmp/demo/chart.PNG"])
        // A color inside the path keeps it whole, as the screen shows it.
        XCTAssertEqual(paths(scan("\u{1b}[34m/tmp/demo/\u{1b}[35mpie.jpg\u{1b}[0m\n")), ["/tmp/demo/pie.jpg"])
    }

    func testOnlyImagePaths() {
        XCTAssertEqual(paths(scan("/tmp/a.txt ./b.go out/c.pdf /tmp/png /tmp/d.webp\n")), ["/tmp/d.webp"])
        // A bare name is no link, a web address is no file.
        XCTAssertEqual(paths(scan("a.png example.com/a.png https://example.com/b.png\n")), [])
    }

    func testNetworkPaths() {
        // Kept, but fetched only at a tap.
        let found = paths(scan("//server/share/a.png \u{1b}]8;;file:////server/share/b.png\u{07}b\u{1b}]8;;\u{07}\n"))
        XCTAssertEqual(found, ["//server/share/b.png", "//server/share/a.png"])
        XCTAssertTrue(found.allSatisfy(GalleryScanner.isNetworkPath))
        XCTAssertTrue(GalleryScanner.isNetworkPath(#"\\server\share\c.png"#))
        XCTAssertFalse(GalleryScanner.isNetworkPath("/tmp/a.png"))
        XCTAssertFalse(GalleryScanner.isNetworkPath(#"C:\a.png"#))
    }

    /// As Claude Code prints them on a Windows host, quoted or not.
    func testWindowsPaths() {
        XCTAssertEqual(paths(scan(#"Saved 'C:\Users\demo\Pictures\IMG_0001.PNG'"# + "\r\n")), [#"C:\Users\demo\Pictures\IMG_0001.PNG"#])
        XCTAssertEqual(paths(scan(#"see C:/work/out/chart.png, and D:\shots\a.jpg."# + "\n")), ["C:/work/out/chart.png", #"D:\shots\a.jpg"#])
        XCTAssertEqual(paths(scan(#"share \\fileserver\team\plot.webp done"# + "\n")), [#"\\fileserver\team\plot.webp"#])
        XCTAssertEqual(paths(scan(#"--out=C:\tmp\o.png (E:\x\y.gif)"# + "\n")), [#"C:\tmp\o.png"#, #"E:\x\y.gif"#])
        // Quotes hold spaces, of either kind.
        XCTAssertEqual(paths(scan(#"wrote "C:\Program Files\Demo\shot 1.png" and '/tmp/my shots/b.png'"# + "\n")),
                       [#"C:\Program Files\Demo\shot 1.png"#, "/tmp/my shots/b.png"])
        // Split across reads, and colored.
        XCTAssertEqual(paths(scan(#"'C:\Users\de"#, "mo\\x.png'\u{1b}[0m\n")), [#"C:\Users\demo\x.png"#])
        XCTAssertEqual(paths(scan("\u{1b}[36m" + #"C:\a\b.bmp"# + "\u{1b}[39m\n")), [#"C:\a\b.bmp"#])
        // Not images, not paths, and a path with no quote to close it.
        XCTAssertEqual(paths(scan(#"C:\a\b.txt C:\dir\ C:x.png ab:\x.png \\\x.png 'C:\a b.png"# + "\n")), [])
        XCTAssertEqual(paths(scan("it's /tmp/a.png and don't\n")), ["/tmp/a.png"])
        XCTAssertEqual(paths(scan("'see /tmp/b.png'\n")), ["/tmp/b.png"])
    }

    func testPathInProseAndMarkdown() {
        XCTAssertEqual(paths(scan("截图：/tmp/截图/图.png。\n")), ["/tmp/截图/图.png"])
        XCTAssertEqual(paths(scan("![chart](./docs/chart.svg) (~/shots/x.heic).\n")), ["./docs/chart.svg", "~/shots/x.heic"])
        XCTAssertEqual(paths(scan("--out=/tmp/o.png /tmp/p.png:12\n")), ["/tmp/o.png", "/tmp/p.png"])
    }

    func testLineBreakEndsPath() {
        // A path the program wrapped is two words; the part before the
        // break is no image, as the screen's link would not be.
        XCTAssertEqual(paths(scan("/tmp/long/na\r\nme.png\n")), [])
        // A cursor move ends a word, as a row of a snapshot does.
        XCTAssertEqual(paths(scan("/tmp/a.png\u{1b}[2;1H/tmp/b.png\n")), ["/tmp/a.png", "/tmp/b.png"])
    }

    func testOSC8FileLink() {
        let link = "\u{1b}]8;;file:///tmp/demo/shot%201.png\u{1b}\\shot\u{1b}]8;;\u{1b}\\\n"
        XCTAssertEqual(paths(scan(link)), ["/tmp/demo/shot 1.png"])
        XCTAssertEqual(paths(scan("\u{1b}]8;;https://example.com/a.png\u{07}a\u{1b}]8;;\u{07}\n")), [])
    }

    func testDuplicatePathOnce() throws {
        let store = GalleryStore(dir: try tempDir())
        XCTAssertTrue(store.add(.path("/tmp/a.png")))
        XCTAssertFalse(store.add(.path("/tmp/a.png")))
        XCTAssertTrue(store.add(.path("./a.png")))
        XCTAssertEqual(store.index.entries.map(\.path), ["/tmp/a.png", "./a.png"])
    }

    // MARK: Kitty

    /// A Kitty transmission of data, in chunks of at most chunk base64 bytes.
    private func kitty(_ data: Data, _ keys: String, chunk: Int = 4096) -> String {
        let b64 = Array(data.base64EncodedString())
        var parts: [String] = []
        var i = 0
        repeat {
            parts.append(String(b64[i..<min(i + chunk, b64.count)]))
            i += chunk
        } while i < b64.count
        if parts.count == 1 { return "\u{1b}_G\(keys);\(parts[0])\u{1b}\\" }
        return parts.enumerated().map { n, p in
            let head = n == 0 ? "\(keys),m=1" : n == parts.count - 1 ? "m=0" : "m=1"
            return "\u{1b}_G\(head);\(p)\u{1b}\\"
        }.joined()
    }

    /// A w×h PNG of one color.
    private func png(_ w: Int, _ h: Int, red: UInt8 = 200) -> Data {
        var pixels = Data()
        for _ in 0..<(w * h) { pixels += [red, 40, 90, 255] }
        let ctx = CGContext(data: nil, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4,
                            space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
        ctx.setFillColor(red: CGFloat(red) / 255, green: 0.2, blue: 0.4, alpha: 1)
        ctx.fill(CGRect(x: 0, y: 0, width: w, height: h))
        let out = NSMutableData()
        let dest = CGImageDestinationCreateWithData(out, UTType.png.identifier as CFString, 1, nil)!
        CGImageDestinationAddImage(dest, ctx.makeImage()!, nil)
        XCTAssertTrue(CGImageDestinationFinalize(dest))
        return out as Data
    }

    private func kittyImages(_ found: [GalleryScanner.Found]) -> [(Data, Int, Int)] {
        found.compactMap { if case .kitty(let d, let w, let h) = $0 { (d, w, h) } else { nil } }
    }

    func testKittyPNG() {
        let p = png(3, 2)
        let found = kittyImages(scan(kitty(p, "a=T,f=100,i=7")))
        XCTAssertEqual(found.count, 1)
        XCTAssertEqual(found.first?.0, p)
        XCTAssertEqual(found.first.map { [$0.1, $0.2] }, [3, 2])
    }

    func testKittyChunksAcrossReads() {
        let p = png(40, 30)
        let seq = Data(kitty(p, "a=T,f=100", chunk: 16).utf8)
        XCTAssertGreaterThan(seq.count, 3 * 97)
        var s = GalleryScanner()
        var found: [GalleryScanner.Found] = []
        // Cut at every 97 bytes: in the middle of keys, payloads and ESC \.
        var i = 0
        while i < seq.count {
            found += s.feed(seq[i..<min(i + 97, seq.count)])
            i += 97
        }
        XCTAssertEqual(kittyImages(found).first?.0, p)
        XCTAssertEqual(found.count, 1)
    }

    func testKittyRawPixels() throws {
        let rgb = Data([255, 0, 0, 0, 255, 0, 0, 0, 255, 9, 9, 9])
        let a = kittyImages(scan(kitty(rgb, "a=T,f=24,s=2,v=2")))
        XCTAssertEqual(a.count, 1)
        XCTAssertEqual(a.first.map { [$0.1, $0.2] }, [2, 2])
        XCTAssertTrue(a.first!.0.starts(with: [0x89, 0x50, 0x4e, 0x47]))

        let rgba = Data(repeating: 128, count: 4 * 5 * 3)
        let b = kittyImages(scan(kitty(rgba, "f=32,s=5,v=3,a=T")))
        XCTAssertEqual(b.first.map { [$0.1, $0.2] }, [5, 3])
        let src = try XCTUnwrap(CGImageSourceCreateWithData(b.first!.0 as CFData, nil))
        XCTAssertEqual(CGImageSourceGetCount(src), 1)

        // Fewer pixels than s×v is no image.
        XCTAssertTrue(kittyImages(scan(kitty(rgb, "a=T,f=24,s=3,v=3"))).isEmpty)
    }

    func testKittyZlib() {
        let rgba = Data((0..<(16 * 16 * 4)).map { UInt8($0 % 7) })
        let z = zlib(rgba)
        XCTAssertLessThan(z.count, rgba.count)
        let a = kittyImages(scan(kitty(z, "a=T,f=32,s=16,v=16,o=z", chunk: 32)))
        XCTAssertEqual(a.first.map { [$0.1, $0.2] }, [16, 16])

        let p = png(8, 8)
        XCTAssertEqual(kittyImages(scan(kitty(zlib(p), "a=T,f=100,o=z"))).first?.0, p)
    }

    func testKittySkipped() {
        // Larger than the server's 4 MiB, a file's (t=f), a query's, and
        // an unknown format's: none is kept, and the chunks after them
        // are not read as new images.
        var big = Data([0x89, 0x50, 0x4e, 0x47])
        big += Data(repeating: 1, count: GalleryScanner.maxImage)
        let after = png(2, 2)
        XCTAssertEqual(kittyImages(scan(kitty(big, "a=T,f=100"), kitty(after, "a=T,f=100"))).map(\.0), [after])
        let raw = Data(repeating: 7, count: 4 * 64 * 64)
        XCTAssertTrue(kittyImages(scan(kitty(raw, "a=q,f=32,s=64,v=64", chunk: 100))).isEmpty)
        XCTAssertTrue(kittyImages(scan(kitty(raw, "a=T,f=32,s=64,v=64,t=f", chunk: 100))).isEmpty)
        XCTAssertTrue(kittyImages(scan(kitty(after, "a=T,f=99", chunk: 16))).isEmpty)
        // A placement or a delete carries no image.
        XCTAssertTrue(scan("\u{1b}_Ga=p,i=7\u{1b}\\\u{1b}_Ga=d,d=I,i=7\u{1b}\\").isEmpty)
        // Too large inflated, though small sent.
        let zeros = zlib(Data(count: GalleryScanner.maxImage + 1))
        XCTAssertTrue(kittyImages(scan(kitty(zeros, "a=T,f=32,s=2048,v=513,o=z"))).isEmpty)
    }

    func testKittyReplayOnce() throws {
        let p = png(4, 4)
        let store = GalleryStore(dir: try tempDir())
        // The image, then the server's replay of it as the pane attaches again.
        for f in scan(kitty(p, "a=T,f=100,i=1"), "text\n", kitty(p, "a=T,f=100,i=1,q=2", chunk: 50)) {
            store.add(f)
        }
        XCTAssertEqual(store.index.entries.count, 1)
        let e = try XCTUnwrap(store.index.entries.first)
        XCTAssertEqual(e.source, .kitty)
        XCTAssertEqual([e.width, e.height], [4, 4])
        XCTAssertEqual(try Data(contentsOf: store.file(e)), p)
    }

    func testResetDropsUnfinished() {
        var s = GalleryScanner()
        let p = png(2, 2)
        let seq = Data(kitty(p, "a=T,f=100", chunk: 8).utf8)
        XCTAssertTrue(s.feed(seq.prefix(40)).isEmpty)
        s.reset()
        // The rest of the image, without its start, is no image.
        XCTAssertTrue(kittyImages(s.feed(seq.dropFirst(40))).isEmpty)
        XCTAssertEqual(kittyImages(s.feed(seq)).count, 1)
    }

    // MARK: Store

    func testLimitDropsOldest() throws {
        let store = GalleryStore(dir: try tempDir())
        let first = png(2, 2, red: 1)
        store.add(.kitty(first, width: 2, height: 2))
        let firstFile = store.file(try XCTUnwrap(store.index.entries.first))
        for i in 0..<GalleryStore.limit { store.add(.path("/tmp/\(i).png")) }
        XCTAssertEqual(store.index.entries.count, GalleryStore.limit)
        XCTAssertEqual(store.index.entries.first?.path, "/tmp/0.png")
        XCTAssertEqual(store.index.entries.last?.path, "/tmp/\(GalleryStore.limit - 1).png")
        XCTAssertFalse(FileManager.default.fileExists(atPath: firstFile.path))
    }

    func testIndexRoundTrip() throws {
        let dir = try tempDir()
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        let store = GalleryStore(dir: dir, now: now)
        store.add(.path("~/shots/a.png"), now: now)
        store.add(.kitty(png(3, 3), width: 3, height: 3), now: now.addingTimeInterval(5))
        store.use(now: now)
        try store.save()
        let again = GalleryStore(dir: dir)
        XCTAssertEqual(again.index, store.index)
        XCTAssertTrue(again.index.unseen)
        again.seen()
        again.remove([again.index.entries[0].id])
        try again.save()
        XCTAssertEqual(GalleryStore(dir: dir).index.entries.map(\.source), [.kitty])
        XCTAssertFalse(GalleryStore(dir: dir).index.unseen)
    }

    func testPruneOldGalleries() throws {
        let root = try tempDir()
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        let old = GalleryStore(dir: GalleryStore.dir(root: root, host: "h1", block: "b1"), now: now.addingTimeInterval(-31 * 24 * 3600))
        try old.save()
        let fresh = GalleryStore(dir: GalleryStore.dir(root: root, host: "h2", block: "b/2"), now: now.addingTimeInterval(-29 * 24 * 3600))
        try fresh.save()
        GalleryStore.prune(root: root, now: now)
        XCTAssertFalse(FileManager.default.fileExists(atPath: root.appending(path: "h1").path))
        XCTAssertTrue(fresh.exists)
    }

    // MARK: Helpers

    private func tempDir() throws -> URL {
        let dir = FileManager.default.temporaryDirectory.appending(path: "gallery-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: dir) }
        return dir
    }

    /// A zlib stream (RFC 1950) of data, as Kitty's o=z: a header, raw
    /// DEFLATE, and the Adler-32 of data.
    private func zlib(_ data: Data) -> Data {
        var raw = Data()
        let filter = try! OutputFilter(.compress, using: .zlib) { if let c = $0 { raw += c } }
        try! filter.write(data)
        try! filter.finalize()
        var a: UInt32 = 1, b: UInt32 = 0
        for byte in data {
            a = (a + UInt32(byte)) % 65521
            b = (b + a) % 65521
        }
        let adler = (b << 16) | a
        return Data([0x78, 0x9c]) + raw + Data([UInt8(adler >> 24), UInt8(adler >> 16 & 0xff), UInt8(adler >> 8 & 0xff), UInt8(adler & 0xff)])
    }
}
