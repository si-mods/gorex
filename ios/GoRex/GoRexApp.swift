import SwiftUI

@main
struct GoRexApp: App {
    @State private var store: HostStore
    @Environment(\.scenePhase) private var phase
    @AppStorage("scheme") private var scheme = "dark"
    /// When the app went to the background.
    @State private var away: Date?

    /// Ghostty starts with the app, as in rootshell, not in a terminal's
    /// push; the notifications too, for a tap that launches it. A
    /// downloaded font is known before. So does the built-in Tailscale,
    /// when on: the hosts on it wait for it to run. The engine comes
    /// before the hosts, which tell it those paired over tailcat.
    init() {
        Maple.shared.register()
        _ = Ghostty.app
        Notifier.shared.start()
        let engine = TailnetEngine.shared
        _store = State(initialValue: HostStore())
        Task { await engine.apply() }
    }

    /// The color scheme of Settings › Interface; nil follows the system.
    static func colorScheme(_ s: String) -> ColorScheme? {
        s == "dark" ? .dark : s == "light" ? .light : nil
    }

    var body: some Scene {
        WindowGroup {
            HostListView()
                .environment(store)
                .preferredColorScheme(Self.colorScheme(scheme))
                .onChange(of: phase) { _, p in
                    if p == .background { away = .now }
                    guard p == .active else { return }
                    let suspended = HostStore.suspended(since: away)
                    away = nil
                    Task { await store.foreground(suspended: suspended) { await TailnetEngine.shared.resume() } }
                }
                // Hosts the gateway refused try again as the engine runs anew.
                .onChange(of: TailnetEngine.shared.status.state) { _, s in
                    if s == "Running" { store.hosts.forEach { $0.reconnectIfNeeded() } }
                }
        }
    }
}
