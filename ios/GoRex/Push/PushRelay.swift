import Foundation

/// A host the Live Activities' tokens bind to, as gorex.push.register
/// answers it (docs/NET-003-push-relay.md): the host's push key, base64
/// of its X.509 SubjectPublicKeyInfo, and the relay it points at.
struct PushTarget: Equatable, Sendable {
    /// The host's push key, base64 of the X.509 SubjectPublicKeyInfo DER.
    let key: String
    /// The relay's base URL, as the register's answer gave it.
    let relay: URL
}

/// The push relay's client (docs/NET-003-push-relay.md, Relay API):
/// binding a Live Activity's token to the device key and a host's push
/// key, and dropping one. Best effort — a failure only leaves the
/// activity on phase-1 behavior — so the calls log and never throw.
protocol PushRelay {
    /// POST /v1/bind: the token, the device key, the host's key, the
    /// environment; a bind replaces the binding of the token.
    func bind(token: String, target: PushTarget) async
    /// POST /v1/bind with kind "start": the push-to-start token of the
    /// attributes type — the app's, not an activity's — which the
    /// device binds once for each host it pairs with.
    func bindStart(token: String, attributesType: String, target: PushTarget) async
    /// POST /v1/unbind: the token's binding goes.
    func unbind(token: String, target: PushTarget) async
    /// POST /v1/unbind of a start binding: the host's key narrows it to
    /// this pairing's record, as the token is the app's, shared by the
    /// hosts.
    func unbindStart(token: String, attributesType: String, target: PushTarget) async
}

/// The relay over HTTPS: JSON requests whose X-Gorex-Sig is the device
/// key's signature of the raw body, base64 of the signature's DER form.
final class HTTPPushRelay: PushRelay {
    /// Sends a request and answers its bytes and response; URLSession's,
    /// a test's own records the request.
    typealias Send = (URLRequest) async throws -> (Data, URLResponse)

    private let keys: PushKeys
    /// "sandbox" or "production", as the build's APNs environment is.
    private let env: String
    private let send: Send

    init(env: String, keys: PushKeys,
         send: @escaping Send = { try await URLSession.shared.data(for: $0) }) {
        self.env = env
        self.keys = keys
        self.send = send
    }

    func bind(token: String, target: PushTarget) async {
        await post(target.relay, path: "v1/bind",
                   json: ["token": token, "device": keys.publicKeyB64, "host": target.key, "env": env])
    }

    func bindStart(token: String, attributesType: String, target: PushTarget) async {
        await post(target.relay, path: "v1/bind",
                   json: ["token": token, "device": keys.publicKeyB64, "host": target.key, "env": env,
                          "kind": "start", "attributes_type": attributesType])
    }

    func unbind(token: String, target: PushTarget) async {
        await post(target.relay, path: "v1/unbind", json: ["token": token])
    }

    func unbindStart(token: String, attributesType: String, target: PushTarget) async {
        await post(target.relay, path: "v1/unbind",
                   json: ["token": token, "kind": "start", "attributes_type": attributesType, "host": target.key])
    }

    /// Posts the JSON body, signed; a failure or an error answer only
    /// logs, as the relay's is best effort.
    private func post(_ base: URL, path: String, json: [String: Any]) async {
        do {
            // Sorted, so what is signed is what is sent, and a reader of
            // the request sees the same bytes.
            let body = try JSONSerialization.data(withJSONObject: json, options: [.sortedKeys])
            var r = URLRequest(url: base.appending(path: path))
            r.httpMethod = "POST"
            r.setValue("application/json", forHTTPHeaderField: "Content-Type")
            r.setValue(try keys.sign(body), forHTTPHeaderField: "X-Gorex-Sig")
            r.httpBody = body
            let (data, response) = try await send(r)
            let code = (response as? HTTPURLResponse)?.statusCode ?? 0
            let answer = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
            guard (200..<300).contains(code), answer?["error"] == nil else {
                ConnectionLog.note("push relay /\(path): status \(code): \(String(decoding: data, as: UTF8.self))")
                return
            }
        } catch {
            ConnectionLog.note("push relay /\(path): \(error.diagnosis)")
        }
    }
}
