import SwiftUI

@main
struct GoRexApp: App {
    @State private var store = HostStore()
    @Environment(\.scenePhase) private var phase
    @AppStorage("scheme") private var scheme = "dark"

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
                    if p == .active { store.hosts.forEach { $0.reconnectIfNeeded() } }
                }
        }
    }
}
