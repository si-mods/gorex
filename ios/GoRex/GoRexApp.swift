import SwiftUI

@main
struct GoRexApp: App {
    @State private var store: HostStore
    @State private var spaces: SpaceBook
    @Environment(\.scenePhase) private var phase
    @AppStorage("scheme") private var scheme = "dark"
    /// When the app went to the background.
    @State private var away: Date?

    /// Ghostty starts with the app, as in rootshell, not in a terminal's
    /// push; the notifications too, for a tap that launches it. A
    /// downloaded font is known before. So does the built-in Tailscale,
    /// when on: the hosts on it wait for it to run. The engine comes
    /// before the hosts, which tell it those paired over tailcat. The files
    /// a run opened from its hosts go. The API key and the settings that
    /// sync through iCloud come in after the font, which one names, and
    /// before Ghostty, which reads some.
    init() {
        OpenedFiles.clean()
        Maple.shared.register()
        Polish.syncKey()
        CloudSettings.shared.start()
        _ = Ghostty.app
        Notifier.shared.start()
        let engine = TailnetEngine.shared
        _store = State(initialValue: HostStore())
        SpaceBook.dropOldData()
        _spaces = State(initialValue: SpaceBook())
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
                .environment(spaces)
                .preferredColorScheme(Self.colorScheme(scheme))
                .onChange(of: phase) { _, p in
                    if p == .background {
                        away = .now
                        // A setting changed just now goes before the app may end.
                        CloudSettings.shared.flush()
                        HotspotShare.shared.handleBackground()
                    }
                    guard p == .active else { return }
                    let suspended = HostStore.suspended(since: away)
                    away = nil
                    HotspotShare.shared.handleForeground()
                    Task { await store.foreground(suspended: suspended) { await TailnetEngine.shared.resume() } }
                }
                .onChange(of: store.hosts.map(\.config)) { _, configs in
                    HotspotShare.shared.updateHosts(configs)
                }
                // Hosts the gateway refused try again as the engine runs anew.
                .onChange(of: TailnetEngine.shared.status.state) { _, s in
                    if s == "Running" { store.hosts.forEach { $0.reconnectIfNeeded() } }
                }
        }
    }
}
