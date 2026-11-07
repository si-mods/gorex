import Compression
import CoreText
import Foundation
import zlib

/// Maple Mono NF CN (https://github.com/subframe7536/maple-font, OFL-1.1),
/// a terminal font the app does not ship: Settings downloads its four
/// faces, and only them, by ranges of the release's zip.
@Observable final class Maple {
    static let shared = Maple()
    /// The family Ghostty and iOS find it by.
    static let family = "Maple Mono NF CN"
    /// The pinned release and its zip's length: GitHub serves ranges, not
    /// a range of the last bytes.
    private static let release = URL(string: "https://github.com/subframe7536/maple-font/releases/download/v7.9/MapleMono-NF-CN.zip")!
    private static let length = 159_498_447
    /// The faces Ghostty draws with, regular, bold, italic and bold italic,
    /// and what each weighs in the zip.
    private static let faces = [("Regular", 9_684_600), ("Bold", 9_788_184), ("Italic", 10_439_155), ("BoldItalic", 10_577_003)]
        .map { (name: "MapleMono-NF-CN-\($0).ttf", size: $1) }
    private static let dir = URL.applicationSupportDirectory.appending(path: "Fonts")
    private static var files: [URL] { faces.map { dir.appending(path: $0.name) } }

    /// The faces on disk: all of them, or what a download left.
    private(set) var saved = Set(Maple.faces.map(\.name).filter { FileManager.default.fileExists(atPath: Maple.dir.appending(path: $0).path) })
    var installed: Bool { saved.count == Self.faces.count }
    /// What the faces not on disk weigh.
    var remaining: Int64 { Int64(Self.faces.filter { !saved.contains($0.name) }.reduce(0) { $0 + $1.size }) }
    /// How much of a download is done; nil when none runs.
    private(set) var progress: Double?
    private(set) var error: String?
    private var task: Task<Void, Never>?
    /// The zip's URL; after the first range, the signed one it redirects to.
    private var url = Maple.release

    /// Makes the faces known to iOS and Ghostty, at launch and after a
    /// download.
    func register() {
        if installed { CTFontManagerRegisterFontURLs(Self.files as CFArray, .process, true, nil) }
    }

    func download() {
        error = nil
        progress = 0
        task = Task {
            do {
                try await fetch()
                register()
            } catch {
                if !Task.isCancelled { self.error = error.localizedDescription }
            }
            progress = nil
        }
    }

    func cancel() { task?.cancel() }

    /// Removes the faces, or what a download left; a terminal that had
    /// them gets Ghostty's own.
    func delete() {
        task?.cancel()
        CTFontManagerUnregisterFontURLs(Self.files as CFArray, .process, nil)
        try? FileManager.default.removeItem(at: Self.dir)
        saved = []
        if UserDefaults.standard.string(forKey: "fontFamily") == Self.family { UserDefaults.standard.removeObject(forKey: "fontFamily") }
    }

    /// Reads the zip's central directory, then each face not yet here; a
    /// face is written once it inflates to its CRC.
    private func fetch() async throws {
        var dir = Self.dir
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        var v = URLResourceValues()
        v.isExcludedFromBackup = true
        try dir.setResourceValues(v)
        url = Self.release
        let tail = Self.length - 65536
        let entries = try Zip.entries(try await get(tail..<Self.length), at: tail)
        let todo = try Self.faces.map(\.name).filter { !saved.contains($0) }.map {
            guard let e = entries[$0] else { throw URLError(.cannotDecodeContentData) }
            return (e, $0)
        }
        let total = todo.reduce(0) { $0 + $1.0.compressed }
        var done = 0
        for (e, name) in todo {
            let start = e.offset + (try Zip.dataOffset(try await get(e.offset..<e.offset + 30)))
            var data = Data(capacity: e.compressed)
            while data.count < e.compressed {
                let n = min(1 << 21, e.compressed - data.count)
                data += try await get(start + data.count..<start + data.count + n)
                done += n
                progress = Double(done) / Double(total)
            }
            try await Zip.inflate(data, e).write(to: Self.dir.appending(path: name), options: .atomic)
            saved.insert(name)
        }
    }

    private func get(_ r: Range<Int>) async throws -> Data {
        var req = URLRequest(url: url)
        req.setValue("bytes=\(r.lowerBound)-\(r.upperBound - 1)", forHTTPHeaderField: "Range")
        let (data, res) = try await URLSession.shared.data(for: req)
        guard (res as? HTTPURLResponse)?.statusCode == 206, data.count == r.count else {
            // The signed URL expires: sign it again, once.
            guard url != Self.release else { throw URLError(.badServerResponse) }
            url = Self.release
            return try await get(r)
        }
        url = res.url ?? url
        return data
    }
}

/// Just enough of zip for a few entries of a big one: the central
/// directory, a local header's length, and raw deflate.
nonisolated enum Zip {
    struct Entry {
        let offset: Int
        let compressed: Int
        let size: Int
        let crc: UInt32
        let method: Int
    }

    /// The central directory's entries, by name, from tail: the zip's last
    /// bytes, which start at base.
    static func entries(_ tail: Data, at base: Int) throws -> [String: Entry] {
        let d = [UInt8](tail)
        guard d.count >= 22, let end = stride(from: d.count - 22, through: 0, by: -1).first(where: { u32(d, $0) == 0x0605_4b50 }) else {
            throw URLError(.cannotDecodeContentData)
        }
        var p = u32(d, end + 16) - base
        var out: [String: Entry] = [:]
        for _ in 0..<u16(d, end + 10) {
            guard p >= 0, p + 46 <= d.count, u32(d, p) == 0x0201_4b50, p + 46 + u16(d, p + 28) <= d.count else {
                throw URLError(.cannotDecodeContentData)
            }
            let name = String(decoding: d[p + 46..<p + 46 + u16(d, p + 28)], as: UTF8.self)
            out[name] = Entry(offset: u32(d, p + 42), compressed: u32(d, p + 20), size: u32(d, p + 24),
                              crc: UInt32(u32(d, p + 16)), method: u16(d, p + 10))
            p += 46 + u16(d, p + 28) + u16(d, p + 30) + u16(d, p + 32)
        }
        return out
    }

    /// How far past an entry's local header, its first 30 bytes, the data starts.
    static func dataOffset(_ head: Data) throws -> Int {
        let d = [UInt8](head)
        guard d.count >= 30, u32(d, 0) == 0x0403_4b50 else { throw URLError(.cannotDecodeContentData) }
        return 30 + u16(d, 26) + u16(d, 28)
    }

    /// An entry's data, deflated, inflated and checked against its CRC.
    @concurrent static func inflate(_ data: Data, _ e: Entry) async throws -> Data {
        var out = Data(count: e.size)
        let n = e.method != 8 || e.size == 0 || data.isEmpty ? 0 : out.withUnsafeMutableBytes { o in
            data.withUnsafeBytes { compression_decode_buffer(o.baseAddress!, e.size, $0.baseAddress!, data.count, nil, COMPRESSION_ZLIB) }
        }
        let crc = out.withUnsafeBytes { UInt32(crc32(0, $0.baseAddress, uInt($0.count))) }
        guard n == e.size, crc == e.crc else { throw URLError(.cannotDecodeContentData) }
        return out
    }

    private static func u16(_ d: [UInt8], _ i: Int) -> Int { Int(d[i]) | Int(d[i + 1]) << 8 }
    private static func u32(_ d: [UInt8], _ i: Int) -> Int { u16(d, i) | u16(d, i + 2) << 16 }
}
