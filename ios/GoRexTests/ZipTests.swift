import Foundation
import XCTest

/// Reading entries out of a zip by its ranges, as Maple Mono's download
/// does, on a small zip of two deflated files.
nonisolated final class ZipTests: XCTestCase {
    private let zip = Data(base64Encoded: "UEsDBBQAAAAIAEK5Z12cBiMZHAAAANACAAAFAAAAYS50dGbzTSzISVXwzc/LV3iyd8HTpXsVfEdFRkWGrAgAUEsDBBQAAAAIAEK5Z11j7XdwBgAAAAQAAAAFAAAAYi50dGZLys9JAQBQSwECFAMUAAAACABCuWddnAYjGRwAAADQAgAABQAAAAAAAAAAAAAAgAEAAAAAYS50dGZQSwECFAMUAAAACABCuWddY+13cAYAAAAEAAAABQAAAAAAAAAAAAAAgAE/AAAAYi50dGZQSwUGAAAAAAIAAgBmAAAAaAAAAAAA")!

    /// An entry of the zip, read from its tail, its local header and its data.
    private func read(_ name: String, tail: Int) async throws -> Data {
        let e = try XCTUnwrap(try Zip.entries(zip.subdata(in: tail..<zip.count), at: tail)[name])
        let start = e.offset + (try Zip.dataOffset(zip.subdata(in: e.offset..<e.offset + 30)))
        return try await Zip.inflate(zip.subdata(in: start..<start + e.compressed), e)
    }

    func testEntriesFromTail() async throws {
        let a = try await read("a.ttf", tail: 100)
        XCTAssertEqual(String(decoding: a, as: UTF8.self), String(repeating: "Maple Mono 你好 ", count: 40))
        let b = try await read("b.ttf", tail: 0)
        XCTAssertEqual(String(decoding: b, as: UTF8.self), "bold")
    }

    /// A tail that misses the central directory, a byte off in an entry's
    /// data, or a local header that isn't one, is an error.
    func testDamaged() async throws {
        XCTAssertThrowsError(try Zip.entries(zip.subdata(in: 200..<zip.count), at: 200))
        XCTAssertThrowsError(try Zip.dataOffset(zip.subdata(in: 1..<31)))
        let e = try XCTUnwrap(try Zip.entries(zip, at: 0)["a.ttf"])
        let start = e.offset + (try Zip.dataOffset(zip.subdata(in: e.offset..<e.offset + 30)))
        var data = zip.subdata(in: start..<start + e.compressed)
        data[10] ^= 1
        do {
            _ = try await Zip.inflate(data, e)
            XCTFail("inflated damaged data")
        } catch {}
    }
}
