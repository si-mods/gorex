import SwiftUI

/// The scheme the window takes while a terminal is on screen: the
/// terminal's background, not Settings › Interface, so that the clock and
/// the status bar keep their contrast over a light terminal.
@Observable final class ScreenStyle {
    static let shared = ScreenStyle()
    var terminal: ColorScheme?
}

@main
struct GoRexApp: App {
    @State private var store: HostStore
    @State private var spaces: SpaceBook
    @Environment(\.scenePhase) private var phase
    @AppStorage("scheme") private var scheme = "dark"
    /// When the app went to the background.
    @State private var away: Date?

    /// The notifications start with the app, for a tap that launches it.
    /// So does the built-in Tailscale, when on: the hosts on it wait for
    /// it to run. The engine comes before the hosts, which tell it those
    /// paired over tailcat. The files a run opened from its hosts go. The
    /// API key and the settings that sync through iCloud come in before
    /// Ghostty, which reads some. Ghostty, with the downloaded font, starts
    /// once the list is on screen (startGhostty), not before the first
    /// frame.
    init() {
        OpenedFiles.clean()
        Polish.syncKey()
        CloudSettings.shared.start()
        Notifier.shared.start()
        let engine = TailnetEngine.shared
        _store = State(initialValue: HostStore())
        SpaceBook.dropOldData()
        _spaces = State(initialValue: SpaceBook())
        Task { await engine.apply() }
    }

    /// Starts Ghostty, as in rootshell, not in a terminal's push, but on
    /// the main queue's next turn after the list appears, so that its
    /// start and the font's registration (some 40 MB) do not hold the
    /// first frame. A terminal open before then, as a notification's tap
    /// opens one at launch, finds it all the same: Ghostty.app starts on
    /// its first use, and registers the font first.
    static func startGhostty() {
        DispatchQueue.main.async { _ = Ghostty.app }
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
                .preferredColorScheme(ScreenStyle.shared.terminal ?? Self.colorScheme(scheme))
                .onAppear(perform: Self.startGhostty)
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
