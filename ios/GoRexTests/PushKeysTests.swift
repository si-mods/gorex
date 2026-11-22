import CryptoKit
import Foundation
import XCTest

/// A device key in memory, for a coordinator or relay under test.
final class MemoryPushKeys: PushKeys {
    let key = P256.Signing.PrivateKey()
    var publicKeyB64: String { key.publicKey.derRepresentation.base64EncodedString() }
    func sign(_ body: Data) throws -> String {
        try key.signature(for: body).derRepresentation.base64EncodedString()
    }
}

/// The phone's push device key (docs/NET-003-push-relay.md, Parties and
/// keys): a P-256 pair whose public part travels as base64 of the X.509
/// SubjectPublicKeyInfo, and whose signatures the relay checks over the
/// raw request body.
nonisolated final class PushKeysTests: XCTestCase {
    /// The public key is the SPKI DER of a P-256 key, base64: 91 bytes,
    /// which CryptoKit reads back.
    @MainActor func testPublicKeyIsSPKIDER() throws {
        let k = MemoryPushKeys()
        let der = try XCTUnwrap(Data(base64Encoded: k.publicKeyB64))
        XCTAssertEqual(der.count, 91, "X.509 SubjectPublicKeyInfo of a P-256 key")
        let pub = try P256.Signing.PublicKey(derRepresentation: der)
        XCTAssertEqual(pub.derRepresentation, der)
    }

    /// A signature is the ECDSA signature's DER form, base64, and the
    /// exported public key verifies it over the body.
    @MainActor func testSignatureVerifies() throws {
        let k = MemoryPushKeys()
        let body = Data(#"{"device":"d","host":"h","token":"t"}"#.utf8)
        let sig = try XCTUnwrap(Data(base64Encoded: k.sign(body)))
        let s = try P256.Signing.ECDSASignature(derRepresentation: sig)
        let pub = try P256.Signing.PublicKey(derRepresentation: XCTUnwrap(Data(base64Encoded: k.publicKeyB64)))
        XCTAssertTrue(pub.isValidSignature(s, for: body))
        XCTAssertFalse(pub.isValidSignature(s, for: body + " "))
    }

    /// One round trip through the real Keychain, where the test runs
    /// with one: a stored pair loads, and a missing store is told.
    @MainActor func testKeychainRoundTrip() throws {
        let k = KeychainPushKeys(), made = P256.Signing.PrivateKey()
        defer { k.forget() }
        let status = k.store(made)
        if status == errSecMissingEntitlement { throw XCTSkip("no Keychain in this test process (\(status))") }
        XCTAssertEqual(status, errSecSuccess)
        XCTAssertEqual(k.load()?.publicKey.derRepresentation, made.publicKey.derRepresentation)
        XCTAssertFalse(k.sign(Data("body".utf8)).isEmpty)
        k.forget()
        XCTAssertNil(k.load())
    }
}
