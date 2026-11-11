import SwiftUI

/// The connection to a host on the tailnet, for a report to the
/// developer: why a terminal failed, how the app reaches the host, how
/// the device reaches it, disco pings of three sizes, and the recent
/// connection log. Copy takes it all as text; pull down to run it again.
struct DiagnosticsView: View {
    /// The host's tailnet name or address.
    let address: String
    /// The host of a terminal; nil from Settings › Tailscale.
    var host: HostConfig?
    /// Why the terminal failed (TerminalSession.diagnosis).
    var failure: String?

    @State private var before: String?
    @State private var after: String?
    @State private var pings: [Int: String] = [:]
    @State private var copied = false

    /// A small ping, and two around the largest packet the tailnet sends
    /// (1280 bytes inside): a large one lost where the small one returns,
    /// or one that comes back by DERP, points at a path that drops large
    /// packets.
    private static let sizes = [64, 1200, 1300]

    /// Whether the host is reached through the built-in Tailscale, the
    /// only one the path and the pings read; Settings › Tailscale has none.
    private var onTailnet: Bool { host.map { $0.reach(tailnetOn: TailnetSettings.isOn) == .tailnet } ?? true }

    // What the sheet shows is for whoever reads the report: not localized,
    // but for its headings.
    private var sections: [(title: LocalizedStringResource, text: String)] {
        let build = Bundle.main.infoDictionary?["CFBundleVersion"] as? String ?? "0"
        var app = ["Rex Go \(Bundle.main.version) (\(build)), iOS \(UIDevice.current.systemVersion)", Date.now.ISO8601Format()]
        app.append("Address \(address)")
        if let host {
            app.append("URL \(host.url.absoluteString)")
            app.append("Transport \(host.reach(tailnetOn: TailnetSettings.isOn).rawValue)")
        }
        let all: [(title: LocalizedStringResource, text: String)?] = [
            failure.map { ("Failure", $0) },
            ("Host", app.joined(separator: "\n")),
            onTailnet ? ("Tailnet Path", "Before pings: \(before ?? "…")\nAfter pings: \(after ?? "…")") : nil,
            onTailnet ? ("Ping", Self.sizes.map { pings[$0] ?? "\($0) B: …" }.joined(separator: "\n")) : nil,
            ("Log", ConnectionLog.lines.joined(separator: "\n")),
        ]
        return all.compactMap { $0 }
    }

    var body: some View {
        List {
            ForEach(sections, id: \.title.key) { s in
                Section {
                    Text(s.text).font(.caption.monospaced()).textSelection(.enabled)
                } header: {
                    Text(s.title)
                }
                .listRowBackground(Color.card)
            }
        }
        .scrollContentBackground(.hidden)
        .background(Color.page)
        .navigationTitle("Diagnostics")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button(copied ? "Copied" : "Copy", systemImage: copied ? "checkmark" : "doc.on.doc") {
                    UIPasteboard.general.string = sections.map { "\($0.title.key)\n\($0.text)" }.joined(separator: "\n\n")
                    copied = true
                }
            }
        }
        .task { await run() }
        .refreshable { await run() }
        .task(id: copied) {
            try? await Task.sleep(for: .seconds(1.5))
            copied = false
        }
        .sensoryFeedback(.success, trigger: copied) { _, new in new }
    }

    /// Reads the path, pings one size after another, and reads it again.
    private func run() async {
        before = nil
        after = nil
        pings = [:]
        guard onTailnet else { return }
        before = Self.path(await TailnetEngine.path(to: address))
        for size in Self.sizes {
            pings[size] = Self.ping(await TailnetEngine.ping(address, size: size))
        }
        after = Self.path(await TailnetEngine.path(to: address))
    }

    /// A PeerPath answer as a line.
    private static func path(_ json: String) -> String {
        guard let j = try? JSONSerialization.jsonObject(with: Data(json.utf8)) as? [String: Any] else { return json }
        if let e = j["error"] as? String { return e }
        let endpoint = j["endpoint"] as? String ?? "", relay = j["relay"] as? String ?? ""
        let way = switch j["path"] as? String {
        case "direct": "direct \(endpoint)"
        case "derp": "DERP \(relay)"
        case "peer-relay": "peer relay \(endpoint)"
        default: "idle"
        }
        return [
            "\(j["name"] as? String ?? "") \(j["ip"] as? String ?? "")",
            j["online"] as? Bool == true ? "online" : "offline",
            way,
            relay.isEmpty ? nil : "home DERP \(relay)",
            (j["handshake"] as? Int).map { "handshake \($0) s ago" } ?? "no handshake",
            "rx \(j["rx"] as? Int ?? 0) B, tx \(j["tx"] as? Int ?? 0) B",
        ].compactMap { $0 }.joined(separator: ", ")
    }

    /// A Ping answer as a line.
    private static func ping(_ json: String) -> String {
        guard let j = try? JSONSerialization.jsonObject(with: Data(json.utf8)) as? [String: Any],
              let size = j["size"] as? Int else { return json }
        if let e = j["error"] as? String { return "\(size) B: \(e)" }
        let way = j["path"] as? String == "derp" ? "DERP" : j["path"] as? String ?? ""
        return "\(size) B: \(j["ms"] as? Double ?? 0) ms, \(way) \(j["endpoint"] as? String ?? "")"
    }
}
