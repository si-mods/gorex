import Foundation

/// The handshake messages of a data connection ("Handshake Messages").
enum DataHandshake: Equatable {
    case welcome(token: Data, keepMillis: UInt32, nonce: UInt64)
    case reject(reason: UInt8)

    /// HELLO: 0x01 and the 16 bytes of the ticket, from its base64url form.
    static func hello(ticket: String) -> Data? {
        var s = ticket.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        s += String(repeating: "=", count: (4 - s.count % 4) % 4)
        guard let t = Data(base64Encoded: s), t.count == 16 else { return nil }
        return Data([0x01]) + t
    }

    /// RESUME: 0x02, the latest reconnect token and the stream bytes used.
    static func resume(token: Data, received: UInt64) -> Data {
        Data([0x02]) + token + withUnsafeBytes(of: received.littleEndian) { Data($0) }
    }

    /// Reads the server's answer to a HELLO or a RESUME.
    init?(_ d: Data) {
        let b = [UInt8](d)
        if b.count == 32, b[0] == 0x81 {
            let keep = b[20..<24].reversed().reduce(UInt32(0)) { $0 << 8 | UInt32($1) }
            let nonce = b[24..<32].reversed().reduce(UInt64(0)) { $0 << 8 | UInt64($1) }
            self = .welcome(token: Data(b[1..<17]), keepMillis: keep, nonce: nonce)
        } else if b.count == 2, b[0] == 0x82 {
            self = .reject(reason: b[1])
        } else {
            return nil
        }
    }
}

/// Splits a terminal stream ("The Terminal Stream") into the Ghostty
/// snapshot it starts with, the program's output, and Rex's own messages,
/// `ESC _ rex1;<nonce>;<json> ESC \`, in order. Bytes may arrive split
/// anywhere.
struct TerminalStream {
    enum Item {
        case snapshot(Data)
        case output(Data)
        case message([String: Any])
    }

    let nonce: UInt64
    private var buf = Data()
    private var snapshotDone = false

    init(nonce: UInt64) { self.nonce = nonce }

    private static let marker = Data("\u{1b}_rex1;".utf8)
    private static let end = Data("\u{1b}\\".utf8)
    /// The longest Rex message held while waiting for its end.
    private static let maxMessage = 1 << 20

    mutating func feed(_ data: Data) -> [Item] {
        buf.append(data)
        var items: [Item] = []
        if !snapshotDone {
            guard let n = Self.snapshotLength(buf) else { return [] }
            items.append(.snapshot(buf.prefix(n)))
            buf = Data(buf.dropFirst(n))
            snapshotDone = true
        }
        while !buf.isEmpty {
            guard let start = buf.range(of: Self.marker) else {
                // Keep a tail that could begin the marker.
                let keep = Self.partialMarker(buf)
                if buf.count > keep { items.append(.output(buf.prefix(buf.count - keep))) }
                buf = Data(buf.suffix(keep))
                break
            }
            if start.lowerBound > buf.startIndex { items.append(.output(buf[buf.startIndex..<start.lowerBound])) }
            guard let stop = buf.range(of: Self.end, in: start.upperBound..<buf.endIndex) else {
                if buf.endIndex - start.lowerBound > Self.maxMessage {
                    items.append(.output(buf[start.lowerBound...]))
                    buf = Data()
                } else {
                    buf = Data(buf[start.lowerBound...])
                }
                break
            }
            if let m = message(buf[start.upperBound..<stop.lowerBound]) { items.append(.message(m)) }
            buf = Data(buf[stop.upperBound...])
        }
        return items
    }

    /// The JSON of a Rex message whose nonce is the channel's; nil for one
    /// that a program printed.
    private func message(_ body: Data) -> [String: Any]? {
        guard let semi = body.firstIndex(of: UInt8(ascii: ";")),
              UInt64(String(decoding: body[body.startIndex..<semi], as: UTF8.self)) == nonce
        else { return nil }
        return try? JSONSerialization.jsonObject(with: body[(semi + 1)...]) as? [String: Any]
    }

    /// How many bytes at the end of d are a beginning of the marker.
    private static func partialMarker(_ d: Data) -> Int {
        for n in stride(from: min(marker.count - 1, d.count), to: 0, by: -1)
        where d.suffix(n).elementsEqual(marker.prefix(n)) { return n }
        return 0
    }

    /// The length of the snapshot at the start of d, through its FINISH
    /// record, or nil until all of it has arrived. A snapshot is a 10-byte
    /// envelope ("GHOSTSNP", version) and records of tag (u16), payload
    /// length (u32), CRC (u32) and payload.
    static func snapshotLength(_ d: Data) -> Int? {
        let b = [UInt8](d.prefix(10))
        guard b.count == 10 else { return nil }
        var at = d.startIndex + 10
        while d.endIndex - at >= 10 {
            let tag = UInt16(d[at]) | UInt16(d[at + 1]) << 8
            let len = (0..<4).reduce(0) { $0 | Int(d[at + 2 + $1]) << (8 * $1) }
            let next = at + 10 + len
            if next > d.endIndex { return nil }
            if tag == 6 { return next - d.startIndex } // FINISH
            at = next
        }
        return nil
    }
}

/// One data connection carrying a terminal's stream, which it resumes
/// after a drop.
final class DataConnection {
    enum Failure: LocalizedError {
        case rejected(UInt8)
        case malformed
        var errorDescription: String? {
            switch self {
            case .rejected(let r): String(format: "data connection refused (0x%02x)", r)
            case .malformed: "bad data handshake"
            }
        }
    }

    /// Stream bytes, as they arrive.
    var onBytes: (Data) -> Void = { _ in }
    /// The connection dropped or ended, with its close code.
    var onDrop: (Int) -> Void = { _ in }

    private(set) var nonce: UInt64 = 0
    private var token = Data()
    /// The stream bytes passed to onBytes, across connections.
    private var received: UInt64 = 0
    private var task: URLSessionWebSocketTask?
    private let base: URL
    private let session: URLSession

    init(base: URL, session: URLSession = .shared) {
        self.base = base
        self.session = session
    }

    /// Opens the stream with a ticket.
    func open(ticket: String) async throws {
        guard let hello = DataHandshake.hello(ticket: ticket) else { throw Failure.malformed }
        received = 0
        try await connect(hello)
    }

    /// Continues the stream on a new connection.
    func resume() async throws {
        try await connect(DataHandshake.resume(token: token, received: received))
    }

    /// Drops the connection, leaving the stream for a resume.
    func suspend() {
        task?.cancel(with: .goingAway, reason: nil)
        task = nil
    }

    /// Ends the stream.
    func close() {
        task?.cancel(with: .normalClosure, reason: nil)
        task = nil
    }

    private func connect(_ first: Data) async throws {
        task?.cancel()
        let t = session.webSocketTask(with: URLRequest(url: base.rexURL("/api/data")).withSubprotocol("rex.data.terminal.v1"))
        t.maximumMessageSize = 1 << 20
        task = t
        t.resume()
        do {
            try await t.send(.data(first))
            guard case .data(let d) = try await t.receive(), let answer = DataHandshake(d) else { throw Failure.malformed }
            switch answer {
            case .reject(let r):
                throw Failure.rejected(r)
            case .welcome(let tok, _, let n):
                token = tok
                nonce = n
            }
        } catch {
            // A handshake that failed leaves no socket open.
            t.cancel()
            if task === t { task = nil }
            throw error
        }
        Task { await read(t) }
    }

    private func read(_ t: URLSessionWebSocketTask) async {
        do {
            while true {
                guard case .data(let d) = try await t.receive() else { continue }
                if t !== task { return }
                received += UInt64(d.count)
                onBytes(d)
            }
        } catch {
            if t === task {
                task = nil
                onDrop(t.closeCode.rawValue)
            }
        }
    }
}

extension Data {
    /// The length of the bytes without a UTF-8 character cut off at their
    /// end: a lead byte with fewer bytes after it than it needs.
    var wholeUTF8: Int {
        let b = [UInt8](suffix(3))
        for (i, c) in b.enumerated().reversed() where c & 0xC0 != 0x80 {
            guard (0xC2...0xF4).contains(c) else { return count }
            let need = c >= 0xF0 ? 4 : c >= 0xE0 ? 3 : 2
            return b.count - i >= need ? count : count - (b.count - i)
        }
        return count
    }

    /// The bytes as text, which is what a terminal write takes: UTF-8 as
    /// it is, and a byte that is no UTF-8, as an X10 mouse report's
    /// coordinate past 95, as the character of its value. The server
    /// writes that one as two bytes: what the UTF-8 mouse encoding sends.
    var text: String {
        if let s = String(validating: self, as: UTF8.self) { return s }
        var s = ""
        var i = startIndex
        while i < endIndex {
            if let n = (1...Swift.min(4, endIndex - i)).first(where: { String(validating: self[i..<i + $0], as: UTF8.self) != nil }) {
                s += String(decoding: self[i..<i + n], as: UTF8.self)
                i += n
            } else {
                s.unicodeScalars.append(Unicode.Scalar(self[i]))
                i += 1
            }
        }
        return s
    }

    /// What one write takes from the start of the bytes, and its text:
    /// at most max of them. Cut at max, it ends on a whole character. All
    /// of them end on one too, or it is nil, until flush: the rest may be
    /// on its way, or the last byte is no UTF-8, as an X10 mouse report's
    /// coordinate, and goes as it is.
    func nextWrite(max: Int, flush: Bool = false) -> (count: Int, text: String)? {
        let n = count > max ? prefix(max).wholeUTF8 : flush || wholeUTF8 == count ? count : 0
        return n > 0 ? (n, prefix(n).text) : nil
    }
}
