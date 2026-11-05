import SwiftUI

@main
struct GoRexApp: App {
    @State private var store = HostStore()
    @Environment(\.scenePhase) private var phase

    var body: some Scene {
        WindowGroup {
            HostListView()
                .environment(store)
                .onChange(of: phase) { _, p in
                    if p == .active { store.hosts.forEach { $0.reconnectIfNeeded() } }
                }
        }
    }
}
