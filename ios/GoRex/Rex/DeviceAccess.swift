import Foundation
import Security

/// A short-lived invitation. Its fragment is not sent in HTTP requests or
/// saved as the host's address. Existing address-only Rex QR codes still work.
struct DevicePairing {
    let base: URL
    let code: String

    nonisolated init?(_ text: String) {
        guard var c = URLComponents(string: text.trimmingCharacters(in: .whitespacesAndNewlines)),
              c.scheme?.lowercased() == "https", let host = c.host, !host.isEmpty,
              c.user == nil, c.password == nil, c.query == nil,
              c.path == "" || c.path == "/",
              let fragment = c.fragment, fragment.hasPrefix("gorex-pair=")
        else { return nil }
        let code = String(fragment.dropFirst("gorex-pair=".count))
        guard code.utf8.count == 43, code.utf8.allSatisfy({
            (65...90).contains($0) || (97...122).contains($0) || (48...57).contains($0) || $0 == 45 || $0 == 95
        }) else { return nil }
        c.scheme = "https"
        c.host = host.lowercased()
        if c.port == 443 { c.port = nil }
        c.path = ""
        c.fragment = nil
        guard let base = c.url else { return nil }
        self.base = base
        self.code = code
    }
}

/// Device credentials are scoped to an HTTPS origin and never enter the host
/// settings in UserDefaults, QR codes, URLs, or reconnect-token messages.
nonisolated enum DeviceAccess {
    private static let service = "dev.gorex.ios.device-access"
    /// An API credential stays on the endpoint the user chose. Redirects are
    /// not part of Rex's API and must not forward credentials to another host.
    static let session = makeSession(configuration: .ephemeral)

    /// Preserve the chosen route, including the embedded tailnet's SOCKS5
    /// proxy, while applying the same credential policy to either transport.
    static func makeSession(configuration config: URLSessionConfiguration) -> URLSession {
        config.httpShouldSetCookies = false
        return URLSession(configuration: config, delegate: NoDeviceRedirects(), delegateQueue: nil)
    }

    private static func account(_ base: URL) -> String? {
        guard var c = URLComponents(url: base, resolvingAgainstBaseURL: false),
              c.scheme == "https", c.host != nil, c.user == nil, c.password == nil else { return nil }
        c.host = c.host?.lowercased()
        if c.port == 443 { c.port = nil }
        c.path = ""; c.query = nil; c.fragment = nil
        return c.url?.absoluteString
    }

    private static func query(_ account: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: service, kSecAttrAccount as String: account]
    }

    static func sameOrigin(_ first: URL, _ second: URL) -> Bool {
        guard let first = account(first), let second = account(second) else { return false }
        return first == second
    }

    static func credential(for base: URL) -> String? {
        guard let account = account(base) else { return nil }
        var q = query(account)
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var item: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &item) == errSecSuccess, let data = item as? Data else { return nil }
        return String(data: data, encoding: .utf8)
    }

    static func save(_ token: String, for base: URL) throws {
        guard let account = account(base) else { throw failure("Device credentials require an HTTPS address.") }
        let data = Data(token.utf8)
        let q = query(account)
        var status = SecItemUpdate(q as CFDictionary, [kSecValueData as String: data] as CFDictionary)
        if status == errSecItemNotFound {
            var item = q
            item[kSecValueData as String] = data
            item[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlockedThisDeviceOnly
            status = SecItemAdd(item as CFDictionary, nil)
        }
        guard status == errSecSuccess else { throw failure("The device credential could not be saved to Keychain.") }
    }

    static func remove(for base: URL) {
        guard let account = account(base) else { return }
        SecItemDelete(query(account) as CFDictionary)
    }

    static func pair(base: URL, code: String, name: String) async throws {
        guard account(base) != nil else { throw failure("Pairing requires an HTTPS address.") }
        var c = URLComponents(url: base, resolvingAgainstBaseURL: false)!
        c.path = "/api/access/pair"; c.query = nil; c.fragment = nil
        var r = URLRequest(url: c.url!)
        r.httpMethod = "POST"
        r.timeoutInterval = 15
        r.setValue("application/json", forHTTPHeaderField: "Content-Type")
        r.httpBody = try JSONSerialization.data(withJSONObject: ["code": code, "name": name])
        // A pairing code is sent only to the address the user approved, even
        // if that endpoint redirects. Native auth uses Keychain, not cookies.
        let (data, response) = try await session.data(for: r)
        let answer = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
        guard (response as? HTTPURLResponse)?.statusCode == 200,
              let token = answer?["token"] as? String, !token.isEmpty else {
            throw failure(answer?["error"] as? String ?? "Pairing failed. Check the address and pairing code.")
        }
        try save(token, for: base)
    }

    private static func failure(_ text: String) -> NSError {
        NSError(domain: "GoRex.DeviceAccess", code: 1, userInfo: [NSLocalizedDescriptionKey: text])
    }
}

private nonisolated final class NoDeviceRedirects: NSObject, URLSessionTaskDelegate {
    func urlSession(_ session: URLSession, task: URLSessionTask,
                    willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest,
                    completionHandler: @escaping (URLRequest?) -> Void) {
        completionHandler(nil)
    }
}

extension URLRequest {
    nonisolated func withDeviceCredential(for base: URL) -> URLRequest {
        var request = self
        if let token = DeviceAccess.credential(for: base) {
            request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        return request
    }
}
