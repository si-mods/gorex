import Foundation

/// The hosts Rex Go shares over Personal Hotspot, as the JSON
/// TailnetShareSetHosts takes (docs/IOS-003-web-share.md).
enum ShareHosts {
    struct Payload: Encodable {
        let id: String
        let name: String
        let icon: String
        let url: String
        let transport: String
        /// The LAN gateway's token: the share proxy sends it to the host
        /// as Authorization: Bearer and never gives it to the browser.
        let token: String?
    }

    /// The payload of the configs, each with the transport the app reaches
    /// it by now (HostConfig.reach): its tailcat client, the built-in
    /// Tailscale while that is on, else the system's way. A host's stored
    /// transport says none of that: a tailcat host's is nil, and a host
    /// marked tailnet goes by the system while the engine is off. The demo
    /// host is left out: it answers nothing away from the app.
    static func json(_ configs: [HostConfig], tailnetOn: Bool) -> String? {
        let payload = configs.filter { $0.demo != true }.map { c in
            Payload(
                id: c.id.uuidString,
                name: c.name,
                icon: c.icon ?? "laptop",
                url: c.url.absoluteString,
                transport: c.reach(tailnetOn: tailnetOn).rawValue,
                token: c.auth
            )
        }
        guard let data = try? JSONEncoder().encode(payload) else { return nil }
        return String(data: data, encoding: .utf8)
    }
}
