import SwiftUI

/// The scheme the window takes while a terminal is on screen: the
/// terminal's background, not Settings › Interface, so that the clock and
/// the status bar keep their contrast over a light terminal.
@Observable final class ScreenStyle {
    static let shared = ScreenStyle()
    var terminal: ColorScheme?
    /// The system's scheme, from the window scene, which the window's
    /// preferredColorScheme does not change; nil until a window shows.
    var system: ColorScheme?
    /// The screen that set it. On an iPad the old screen fades out after
    /// the new one set its scheme, and must not clear that one.
    @ObservationIgnored private var owner: UUID?

    func set(_ scheme: ColorScheme, by screen: UUID) {
        owner = screen
        terminal = scheme
    }

    func clear(by screen: UUID) {
        guard owner == screen else { return }
        owner = nil
        terminal = nil
    }

    /// Clears it, whichever screen set it: the list is back.
    func release() {
        owner = nil
        terminal = nil
    }
}

/// Keeps ScreenStyle's system scheme current, from the scene of the
/// window it shows in.
private struct SystemScheme: UIViewRepresentable {
    func makeUIView(context: Context) -> SystemSchemeView { SystemSchemeView() }
    func updateUIView(_ v: SystemSchemeView, context: Context) {}
}

private final class SystemSchemeView: UIView {
    private weak var scene: UIWindowScene?

    override init(frame: CGRect) {
        super.init(frame: frame)
        isUserInteractionEnabled = false
    }

    required init?(coder: NSCoder) { fatalError() }

    override func didMoveToWindow() {
        super.didMoveToWindow()
        guard let s = window?.windowScene, s !== scene else { return }
        scene = s
        ScreenStyle.shared.system = ColorScheme(s.traitCollection.userInterfaceStyle)
        s.registerForTraitChanges([UITraitUserInterfaceStyle.self]) { (s: UIWindowScene, _: UITraitCollection) in
            ScreenStyle.shared.system = ColorScheme(s.traitCollection.userInterfaceStyle)
        }
    }
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
    /// frame. The widgets' snapshot follows the hosts from the start, and
    /// NetworkPath the network.
    init() {
        OpenedFiles.clean()
        Polish.syncKey()
        CloudSettings.shared.start()
        Notifier.shared.start()
        let engine = TailnetEngine.shared
        let store = HostStore()
        _store = State(initialValue: store)
        WidgetPublisher.shared.start(store)
        NetworkPath.shared.start()
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
                .promptSheet(store)
                .preferredColorScheme(ScreenStyle.shared.terminal ?? Self.colorScheme(scheme))
                .background { SystemScheme().accessibilityHidden(true) }
                .onAppear(perform: Self.startGhostty)
                .onChange(of: phase) { _, p in
                    if p == .background { Notifier.shared.inFront = false }
                    if p == .active { Notifier.shared.inFront = true }
                    if p == .background {
                        away = .now
                        // Any host's connection stays open a while, not only an open terminal's.
                        let grace = HostStore.needsGrace(store.hosts, terminal: TerminalLive.shared.host)
                        ConnectionLog.note("app: background\(grace ? ", grace" : "")")
                        if grace {
                            BackgroundGrace.shared.start()
                        } else {
                            // No grace: the terminal's work runs at once.
                            BackgroundGrace.shared.skip()
                        }
                        // A setting changed just now goes before the app may end.
                        CloudSettings.shared.flush()
                        HotspotShare.shared.handleBackground()
                        WidgetPublisher.shared.flush()
                    }
                    if p == .inactive {
                        // Inactive counts as the foreground: a network
                        // change while Control Center is down still acts.
                        ConnectionLog.note("app: inactive")
                    }
                    guard p == .active else { return }
                    // The grace kept the app running past away; back now, it ends.
                    let woke = BackgroundGrace.shared.awake(since: away)
                    let suspended = HostStore.suspended(since: woke)
                    let since = woke.map { " (\(Int(-$0.timeIntervalSinceNow)) s away)" } ?? ""
                    ConnectionLog.note("app: active\(suspended ? since : "")")
                    BackgroundGrace.shared.cancel()
                    away = nil
                    HotspotShare.shared.handleForeground()
                    Task { await store.foreground(suspended: suspended) { await TailnetEngine.shared.resume() } }
                }
                .onChange(of: store.hosts.map(\.config)) { _, configs in
                    HotspotShare.shared.updateHosts(configs)
                }
                // A new network: the hosts reconnect at once, not after a ping
                // finds the old path dead (docs/IOS-012-background-and-network-changes.md).
                .onChange(of: NetworkPath.shared.changes) {
                    store.networkChanged(inBackground: phase == .background)
                }
                // Hosts the gateway refused try again as the engine runs anew.
                .onChange(of: TailnetEngine.shared.status.state) { _, s in
                    if s == "Running" { store.hosts.forEach { $0.reconnectIfNeeded() } }
                    // The way a host marked tailnet is reached changed with it.
                    HotspotShare.shared.updateHosts(store.hosts.map(\.config))
                }
        }
    }
}
