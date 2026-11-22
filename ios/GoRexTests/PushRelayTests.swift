import CryptoKit
import Foundation
import XCTest

/// The relay's bind and unbind requests (docs/NET-003-push-relay.md,
/// Relay API): JSON to /v1/, signed by the device key over the raw body,
/// best effort — a failure only logs.
nonisolated final class PushRelayTests: XCTestCase {
    /// Records the request and answers as the test says.
    @MainActor final class Sender {
        var requests: [URLRequest] = []
        var answer: (Data, URLResponse) = (
            Data(#"{"sent":1}"#.utf8),
            HTTPURLResponse(url: URL(string: "https://relay.gorex.uk")!, statusCode: 200,
                            httpVersion: nil, headerFields: nil)!
        )
        var error: Error?

        func send(_ r: URLRequest) async throws -> (Data, URLResponse) {
            requests.append(r)
            if let error { throw error }
            return answer
        }
    }

    @MainActor private func relay(_ s: Sender, _ keys: PushKeys = MemoryPushKeys()) -> HTTPPushRelay {
        HTTPPushRelay(env: "sandbox", keys: keys, send: s.send)
    }

    private func target() -> PushTarget {
        PushTarget(key: "aG9zdC1rZXk", relay: URL(string: "https://relay.gorex.uk")!)
    }

    /// The signature the request carries, verified against the keys'
    /// public part over the request's raw body; nil when missing or bad.
    @MainActor private func signature(_ r: URLRequest, _ keys: PushKeys) throws -> Bool {
        let body = try XCTUnwrap(r.httpBody)
        let s = try XCTUnwrap(r.value(forHTTPHeaderField: "X-Gorex-Sig"))
        let sig = try P256.Signing.ECDSASignature(derRepresentation: XCTUnwrap(Data(base64Encoded: s)))
        let pub = try P256.Signing.PublicKey(derRepresentation: XCTUnwrap(Data(base64Encoded: keys.publicKeyB64)))
        return pub.isValidSignature(sig, for: body)
    }

    /// /v1/bind carries the token, the device key, the host's key and the
    /// environment, signed by the device key.
    @MainActor func testBindPostsSignedJSON() async throws {
        let keys = MemoryPushKeys(), s = Sender()
        await relay(s, keys).bind(token: "abcd", target: target())
        let r = try XCTUnwrap(s.requests.only)
        XCTAssertEqual(r.httpMethod, "POST")
        XCTAssertEqual(r.url?.absoluteString, "https://relay.gorex.uk/v1/bind")
        XCTAssertEqual(r.value(forHTTPHeaderField: "Content-Type"), "application/json")
        let json = try XCTUnwrap(JSONSerialization.jsonObject(with: XCTUnwrap(r.httpBody)) as? [String: Any])
        XCTAssertEqual(json["token"] as? String, "abcd")
        XCTAssertEqual(json["device"] as? String, keys.publicKeyB64)
        XCTAssertEqual(json["host"] as? String, "aG9zdC1rZXk")
        XCTAssertEqual(json["env"] as? String, "sandbox")
        XCTAssertTrue(try signature(r, keys), "X-Gorex-Sig verifies over the raw body")
    }

    /// /v1/unbind carries the token alone, signed the same way.
    @MainActor func testUnbindPostsSignedJSON() async throws {
        let keys = MemoryPushKeys(), s = Sender()
        await relay(s, keys).unbind(token: "abcd", target: target())
        let r = try XCTUnwrap(s.requests.only)
        XCTAssertEqual(r.url?.absoluteString, "https://relay.gorex.uk/v1/unbind")
        let json = try XCTUnwrap(JSONSerialization.jsonObject(with: XCTUnwrap(r.httpBody)) as? [String: Any])
        XCTAssertEqual(json as? [String: String], ["token": "abcd"])
        XCTAssertTrue(try signature(r, keys))
    }

    /// A start bind carries kind "start" and the attributes type on top
    /// of the bind's fields; a start unbind narrows the app's token to
    /// this pairing by the host's key.
    @MainActor func testStartBindAndUnbind() async throws {
        let keys = MemoryPushKeys(), s = Sender()
        let r = relay(s, keys)
        await r.bindStart(token: "st", attributesType: "TerminalActivity", target: target())
        await r.unbindStart(token: "st", attributesType: "TerminalActivity", target: target())
        XCTAssertEqual(s.requests.count, 2)

        let bind = s.requests[0]
        XCTAssertEqual(bind.url?.absoluteString, "https://relay.gorex.uk/v1/bind")
        var json = try XCTUnwrap(JSONSerialization.jsonObject(with: XCTUnwrap(bind.httpBody)) as? [String: Any])
        XCTAssertEqual(json["kind"] as? String, "start")
        XCTAssertEqual(json["attributes_type"] as? String, "TerminalActivity")
        XCTAssertEqual(json["token"] as? String, "st")
        XCTAssertEqual(json["device"] as? String, keys.publicKeyB64)
        XCTAssertEqual(json["host"] as? String, "aG9zdC1rZXk")
        XCTAssertTrue(try signature(bind, keys))

        let unbind = s.requests[1]
        XCTAssertEqual(unbind.url?.absoluteString, "https://relay.gorex.uk/v1/unbind")
        json = try XCTUnwrap(JSONSerialization.jsonObject(with: XCTUnwrap(unbind.httpBody)) as? [String: Any])
        XCTAssertEqual(json["kind"] as? String, "start")
        XCTAssertEqual(json["attributes_type"] as? String, "TerminalActivity")
        XCTAssertEqual(json["host"] as? String, "aG9zdC1rZXk", "the host narrows it to this pairing")
        XCTAssertTrue(try signature(unbind, keys))
    }

    /// A send that throws, an error status, and an {"error": ...} answer
    /// all pass quietly — binding is best effort.
    @MainActor func testFailuresOnlyLog() async {
        let keys = MemoryPushKeys(), s = Sender()
        let r = relay(s, keys)
        s.error = URLError(.cannotConnectToHost)
        await r.bind(token: "a", target: target())
        s.error = nil
        s.answer = (Data(#"{"error":"bad"}"#.utf8), HTTPURLResponse(url: URL(string: "https://relay.gorex.uk")!,
                                                                  statusCode: 429, httpVersion: nil, headerFields: nil)!)
        await r.bind(token: "a", target: target())
        s.answer = (Data(#"{"error":"bad"}"#.utf8), HTTPURLResponse(url: URL(string: "https://relay.gorex.uk")!,
                                                                  statusCode: 200, httpVersion: nil, headerFields: nil)!)
        await r.unbind(token: "a", target: target())
        XCTAssertEqual(s.requests.count, 3, "all three went, none threw")
    }
}

private extension Array {
    /// The one element, nil for zero or more than one.
    var only: Element? { count == 1 ? first : nil }
}
