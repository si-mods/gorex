import Foundation

/// A model as GitHub Releases carries it: one .tar.bz2 of the compiled model
/// files. bzip2 is the system's; tar's headers are read here, as the archive
/// comes.
nonisolated enum TarBz2 {
    /// Why an archive could not be unpacked.
    struct Failure: LocalizedError {
        let message: String
        var errorDescription: String? { message }
    }

    /// The files wanted, each written into dir as .partial and taking its
    /// name once whole; strip drops as many of the archive's path
    /// components as it lists, its own top directory. progress is how much
    /// of the archive is read, 0 to 1. Reading stops at the last file
    /// wanted, and fails unless each is whole at its size.
    static func extract(_ files: [(path: String, size: Int)], from archive: URL, into dir: URL,
                        strip: Int = 1, progress: @escaping (Double) -> Void = { _ in }) throws {
        let wanted = Dictionary(files.map { ($0.path, $0.size) }, uniquingKeysWith: { first, _ in first })
        let reader = try Reader(archive) { progress($0) }
        var written: Set<String> = []
        var long: String?
        var sized: Int?  // a pax header's size, for the entry after it
        while written.count < wanted.count {
            let header = try reader.take(512)
            if header.isEmpty || header.allSatisfy({ $0 == 0 }) { break }  // the end, one or two blocks of zeroes
            try check(header)
            let kind = header[156]
            let size = number(header, 124, 12)
            switch kind {
            case UInt8(ascii: "L"), UInt8(ascii: "K"):  // the long name that follows
                let data = try reader.take(size)
                guard data.count == size else { throw Failure(message: "the archive ends in a name") }
                long = String(decoding: data.prefix { $0 != 0 }, as: UTF8.self)
                try reader.skip(padding(size))
            case UInt8(ascii: "x"):  // a pax header: a path or a size too long for its field
                let data = try reader.take(size)
                guard data.count == size else { throw Failure(message: "the archive ends in a header") }
                let fields = pax(data)
                long = fields["path"] ?? long
                sized = fields["size"].flatMap(Int.init)
                try reader.skip(padding(size))
            default:
                var path = long ?? text(header, 0, 100)
                let prefix = text(header, 345, 155)
                if !prefix.isEmpty { path = prefix + "/" + path }
                long = nil
                var bytes = size
                if let n = sized { bytes = n; sized = nil }
                switch kind {
                case 0, UInt8(ascii: "0"), UInt8(ascii: "7"):
                    let rel = path.split(separator: "/").dropFirst(strip).joined(separator: "/")
                    if let want = wanted[rel], !written.contains(rel) {
                        guard want == bytes else { throw Failure(message: "\(rel) is \(bytes) bytes in the archive, not \(want)") }
                        try write(reader, bytes, to: dir.appending(path: rel))
                        written.insert(rel)
                    } else {
                        try reader.skip(bytes)
                    }
                    try reader.skip(padding(bytes))
                default:
                    try reader.skip(bytes + padding(bytes))  // a directory, or something not wanted
                }
            }
        }
        let missing = Set(wanted.keys).subtracting(written).sorted()
        guard missing.isEmpty else { throw Failure(message: "the archive has no \(missing.joined(separator: ", "))") }
    }

    /// The bytes of a file wanted, into its .partial, which takes its name
    /// once whole.
    private static func write(_ reader: Reader, _ size: Int, to file: URL) throws {
        let fm = FileManager.default
        try fm.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
        let part = file.appendingPathExtension("partial")
        try? fm.removeItem(at: part)
        guard fm.createFile(atPath: part.path, contents: nil) else { throw Failure(message: "\(part.path) could not be made") }
        let out = try FileHandle(forWritingTo: part)
        defer { try? out.close() }
        var left = size
        while left > 0 {
            let data = try reader.take(min(left, 1 << 20))
            guard !data.isEmpty else { throw Failure(message: "the archive ends in \(file.lastPathComponent)") }
            try out.write(contentsOf: data)
            left -= data.count
        }
        try? fm.removeItem(at: file)
        try fm.moveItem(at: part, to: file)
    }

    /// A header's checksum, as tar writes it: its bytes, those of the
    /// field itself as spaces.
    private static func check(_ header: [UInt8]) throws {
        let told = number(header, 148, 8)
        let sum = header.enumerated().reduce(0) { $0 + ((148..<156).contains($1.offset) ? 32 : Int($1.element)) }
        guard sum == told else { throw Failure(message: "a tar header's checksum does not match") }
    }

    /// A field's text, up to its null.
    private static func text(_ header: [UInt8], _ at: Int, _ count: Int) -> String {
        String(decoding: header[at..<at + count].prefix { $0 != 0 }, as: UTF8.self)
    }

    /// A field's number: octal, or base 256 for a size too big for it.
    private static func number(_ header: [UInt8], _ at: Int, _ count: Int) -> Int {
        let field = header[at..<at + count]
        if field.first == 0x80 || field.first == 0xff {
            return field.dropFirst().reduce(0) { $0 << 8 | Int($1) }
        }
        let digits = String(decoding: field, as: UTF8.self).trimmingCharacters(in: CharacterSet(charactersIn: " \0"))
        return Int(digits, radix: 8) ?? 0
    }

    /// How many bytes follow a file's own to fill its last block.
    private static func padding(_ size: Int) -> Int {
        (512 - size % 512) % 512
    }

    /// A pax header's records: a length, a key, "=" and a value.
    private static func pax(_ data: [UInt8]) -> [String: String] {
        var fields: [String: String] = [:]
        var at = 0
        while at < data.count {
            var space = at
            while space < data.count, data[space] != UInt8(ascii: " ") { space += 1 }
            guard let length = Int(String(decoding: data[at..<space], as: UTF8.self)), length > 0, at + length <= data.count else { break }
            let record = data[(space + 1)..<(at + length)]
            if let equals = record.firstIndex(of: UInt8(ascii: "=")) {
                let key = String(decoding: record[record.startIndex..<equals], as: UTF8.self)
                fields[key] = String(decoding: record[record.index(after: equals)...].prefix { $0 != UInt8(ascii: "\n") }, as: UTF8.self)
            }
            at += length
        }
        return fields
    }
}

/// An archive's bytes as tar asks for them, bzip2 decoded as they come: a
/// block in, a block out, and no more than one file in memory.
private nonisolated final class Reader {
    private let file: FileHandle
    private var stream = bz_stream()
    private let input: UnsafeMutablePointer<CChar>
    private let output: UnsafeMutablePointer<CChar>
    private static let block = 256 << 10
    /// The bytes decoded and not yet taken, and how many of them were.
    private var spare: [UInt8] = []
    private var taken = 0
    /// The file's end read, and the stream's end reached.
    private var eof = false
    private var ended = false
    private var read = 0
    private let total: Int
    private let progress: (Double) -> Void

    init(_ archive: URL, _ progress: @escaping (Double) -> Void) throws {
        total = (try? archive.resourceValues(forKeys: [.fileSizeKey]).fileSize) ?? 0
        self.progress = progress
        file = try FileHandle(forReadingFrom: archive)
        input = .allocate(capacity: Self.block)
        output = .allocate(capacity: Self.block)
        guard BZ2_bzDecompressInit(&stream, 0, 0) == BZ_OK else {
            input.deallocate()
            output.deallocate()
            try? file.close()
            throw TarBz2.Failure(message: "\(archive.lastPathComponent) is not a bzip2 archive")
        }
    }

    deinit {
        BZ2_bzDecompressEnd(&stream)
        input.deallocate()
        output.deallocate()
        try? file.close()
    }

    /// Up to n bytes, fewer at the archive's end.
    func take(_ n: Int) throws -> [UInt8] {
        while spare.count - taken < n {
            if try !decode() { break }
        }
        let k = min(n, spare.count - taken)
        let out = Array(spare[taken..<taken + k])
        taken += k
        if taken == spare.count {
            spare = []
            taken = 0
        }
        return out
    }

    /// n bytes, whatever they are.
    func skip(_ n: Int) throws {
        var left = n
        while left > 0 {
            while spare.count == taken {
                if try !decode() { break }
            }
            guard spare.count > taken else { throw TarBz2.Failure(message: "the archive ends early") }
            let k = min(left, spare.count - taken)
            taken += k
            left -= k
            if taken == spare.count {
                spare = []
                taken = 0
            }
        }
    }

    /// More bytes, decoded: false when none more come.
    private func decode() throws -> Bool {
        while !ended {
            if stream.avail_in == 0, !eof {
                let data = try file.read(upToCount: Self.block) ?? Data()
                if data.isEmpty {
                    eof = true  // all of it was given; the stream's end says what is left
                } else {
                    data.copyBytes(to: UnsafeMutableRawPointer(input).assumingMemoryBound(to: UInt8.self), count: data.count)
                    stream.next_in = input
                    stream.avail_in = UInt32(data.count)
                    read += data.count
                    progress(Double(read) / Double(max(total, 1)))
                }
            }
            stream.next_out = output
            stream.avail_out = UInt32(Self.block)
            let status = BZ2_bzDecompress(&stream)
            let made = Self.block - Int(stream.avail_out)
            if made > 0 {
                output.withMemoryRebound(to: UInt8.self, capacity: made) { bytes in
                    spare.append(contentsOf: UnsafeBufferPointer(start: bytes, count: made))
                }
                return true
            }
            switch status {
            case BZ_STREAM_END:
                ended = true
            case BZ_OK:
                if eof, stream.avail_in == 0 { ended = true }  // nothing more will come
            default:
                throw TarBz2.Failure(message: "the archive could not be decompressed")
            }
        }
        return false
    }
}
