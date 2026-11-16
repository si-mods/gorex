import SwiftUI

/// Settings › Space Sync: iCloud sync toggle, desktop passive sync controls,
/// and live observable sync status across connected hosts.
struct SpaceSyncView: View {
    @Environment(HostStore.self) private var store
    @Environment(SpaceStore.self) private var spaceStore
    @State private var syncing = false

    private var assignedCount: Int {
        spaceStore.orderedSpaces.reduce(0) { $0 + $1.members.count }
    }

    var body: some View {
        @Bindable var spaceStore = spaceStore
        List {
            Section {
                Toggle("iCloud Sync", isOn: $spaceStore.iCloudSyncEnabled)
                    .listRowBackground(Color.card)
            } footer: {
                Text("Syncs your cross-host spaces and window assignments across your Apple devices via iCloud.")
            }

            Section {
                Toggle("Sync to Desktop", isOn: $spaceStore.desktopSyncEnabled)
                    .listRowBackground(Color.card)
                if spaceStore.desktopSyncEnabled {
                    Picker("Target Desktop", selection: $spaceStore.primaryHostID) {
                        Text("All Supporting Hosts").tag("")
                        ForEach(store.hosts) { host in
                            Text(host.config.name).tag(host.id.uuidString)
                        }
                    }
                    .listRowBackground(Color.card)
                }
            } footer: {
                Text("Automatically applies iOS space assignments back to online desktop hosts that support gorex.spaces_sync.")
            }

            Section("Status") {
                row("Spaces", value: "\(spaceStore.orderedSpaces.count)")
                row("Assigned Panes", value: "\(assignedCount)")
                row("Revision", value: "r\(spaceStore.manifest.revision)")
                row("Last iCloud Sync", value: format(spaceStore.lastCloudSyncAt))
                row("Last Desktop Sync", value: format(spaceStore.lastDesktopSyncAt))
            }

            if !store.hosts.isEmpty {
                Section("Desktop Hosts") {
                    ForEach(store.hosts) { host in
                        HStack {
                            Image(systemName: host.config.symbol)
                                .foregroundStyle(.secondary)
                            Text(host.config.name)
                            Spacer()
                            hostStatus(host)
                        }
                        .listRowBackground(Color.card)
                    }
                }
            }

            Section {
                Button {
                    syncNow()
                } label: {
                    HStack {
                        Text("Sync Now")
                        Spacer()
                        if syncing { ProgressView() }
                    }
                }
                .disabled(syncing)
                .listRowBackground(Color.card)
            }
        }
        .listStyle(.insetGrouped)
        .scrollContentBackground(.hidden)
        .background(Color.page.ignoresSafeArea())
        .navigationTitle("Space Sync")
        .navigationBarTitleDisplayMode(.inline)
    }

    private func row(_ title: LocalizedStringKey, value: String) -> some View {
        HStack {
            Text(title)
            Spacer()
            Text(value).foregroundStyle(.secondary)
        }
        .listRowBackground(Color.card)
    }

    @ViewBuilder private func hostStatus(_ host: Host) -> some View {
        if host.status != .online {
            Text("Offline").foregroundStyle(.secondary)
        } else if host.supportsSpacesSync {
            Text("Ready").foregroundStyle(.green)
        } else {
            Text("Not Supported").foregroundStyle(.secondary)
        }
    }

    private func format(_ date: Date?) -> String {
        guard let date else { return String(localized: "Never") }
        return date.formatted(date: .omitted, time: .standard)
    }

    private func syncNow() {
        syncing = true
        Task {
            defer { syncing = false }
            spaceStore.pullFromKVS()
            spaceStore.sync(from: store.hosts.map(\.spaceSnapshot))
            if spaceStore.desktopSyncEnabled {
                let all = spaceStore.orderedSpaces
                let primary = spaceStore.primaryHostID
                let targets = store.hosts.filter { h in
                    h.supportsSpacesSync && (primary.isEmpty || h.id.uuidString == primary)
                }
                for h in targets { await h.applySpaces(all) }
                if !targets.isEmpty { spaceStore.markDesktopSynced() }
            }
        }
    }
}
