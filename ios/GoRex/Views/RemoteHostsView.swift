import SwiftUI

/// The hosts, as Rex's Remote Hosts page lists them: edit to remove or
/// reorder, ⓘ for one's details, Add Host to pair or set one up.
struct RemoteHostsView: View {
    @Environment(HostStore.self) private var store
    @State private var sheet: Sheet?

    private enum Sheet: Identifiable {
        case connect, manual, scan, details(HostConfig)
        var id: String {
            switch self {
            case .connect: "connect"
            case .manual: "manual"
            case .scan: "scan"
            case .details(let c): c.id.uuidString
            }
        }
    }

    var body: some View {
        List {
            Section("Hosts") {
                ForEach(store.hosts) { h in
                    HostRow(host: h) { sheet = .details(h.config) }
                }
                .onDelete { $0.map { store.hosts[$0].id }.forEach(store.remove) }
                .onMove(perform: store.move)
                .listRowBackground(Color.card)
            }
            Section {
                Button { sheet = .connect } label: {
                    Label {
                        Text("Add Host")
                    } icon: {
                        Image(systemName: "globe").overlay(alignment: .bottomTrailing) {
                            Image(systemName: "plus.circle.fill").font(.system(size: 9, weight: .bold))
                                .background(Color.card, in: Circle()).offset(x: 3, y: 3)
                        }
                    }
                }
                .foregroundStyle(.primary)
                .listRowBackground(Color.card)
            }
        }
        .scrollContentBackground(.hidden)
        .background(Color.page)
        .navigationTitle("Remote Hosts")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar { EditButton() }
        .sheet(item: $sheet) { s in
            switch s {
            case .connect:
                ConnectSheet(manual: { sheet = .manual }, scan: { sheet = .scan })
            case .manual:
                HostForm(config: nil)
            case .scan:
                QRScanner(onScan: pair)
            case .details(let c):
                HostForm(config: c)
            }
        }
    }

    /// Adds the host of a scanned code, or shows it when it's there already.
    private func pair(_ code: String) -> String? {
        guard var c = HostConfig(pairing: code) else {
            return String(localized: "Not a Rex pairing code. Scan the Rex QR code that GoRex or Rex shows.")
        }
        if let h = store.host(at: c.url) {
            sheet = .details(h.config)
        } else {
            c.transport = TailnetEngine.shared.transport(of: c.url)
            store.add(c)
            sheet = nil
        }
        return nil
    }
}

private struct HostRow: View {
    let host: Host
    let info: () -> Void

    var body: some View {
        HStack(spacing: 14) {
            Image(systemName: host.config.symbol)
                .font(.title3).frame(width: 30)
            VStack(alignment: .leading, spacing: 2) {
                Text(host.config.name).foregroundStyle(.primary).lineLimit(1)
                Text(host.config.url.absoluteString).font(.footnote).foregroundStyle(.gray)
                    .lineLimit(1)
            }
            .layoutPriority(1)
            .alignmentGuide(.listRowSeparatorLeading) { $0[.leading] }
            Spacer(minLength: 8)
            Text(status).font(.subheadline).foregroundStyle(.gray).lineLimit(1).fixedSize()
            Button(action: info) { Image(systemName: "info.circle").font(.title3).foregroundStyle(.gray) }
                .buttonStyle(.borderless)
                .accessibilityLabel("Edit Host")
        }
    }

    private var status: LocalizedStringKey {
        switch host.status {
        case .online: "Connected"
        case .connecting: "Connecting…"
        case .offline: "Offline"
        }
    }
}

private struct ConnectSheet: View {
    let manual: () -> Void
    let scan: () -> Void

    var body: some View {
        VStack(spacing: 12) {
            Text("Connect to Remote Host").font(.title2.bold())
            Text("Pair automatically via Tailscale, or enter your Rex server details manually.")
                .font(.body).foregroundStyle(.secondary).multilineTextAlignment(.center)
            Spacer(minLength: 12)
            Button(action: manual) { Text("Set Up Manually").frame(maxWidth: .infinity) }
                .buttonStyle(.glass)
            Button(action: scan) { Label("Pair via QR Code", systemImage: "qrcode").frame(maxWidth: .infinity) }
                .buttonStyle(.glassProminent)
                .foregroundStyle(Color.page)
        }
        .font(.headline)
        .controlSize(.extraLarge)
        .padding(24)
        .presentationDetents([.height(320)])
    }
}

/// A host's address, icon, name and startup command, as Rex's Edit Host
/// sheet has them: a new host's, or one's to change or delete.
private struct HostForm: View {
    @Environment(HostStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    let config: HostConfig?
    @State private var address: String
    @State private var symbol: String
    @State private var name: String
    @State private var command: String
    /// Whether the host goes through the built-in Tailscale, as switched;
    /// nil keeps the host's way, or, for a new one or a new address, takes
    /// it by the address.
    @State private var tailnet: Bool?
    @State private var deleting = false
    @AppStorage(TailnetSettings.onKey) private var tailnetOn = false

    init(config: HostConfig?) {
        self.config = config
        _address = State(initialValue: config?.url.absoluteString ?? "")
        _symbol = State(initialValue: config?.symbol ?? "laptopcomputer")
        _name = State(initialValue: config?.name ?? "")
        _command = State(initialValue: config?.command ?? "")
    }

    /// The host as entered, if its address reads as a pairing code does;
    /// a bare host gets http://.
    private var entered: HostConfig? {
        let s = address.trimmingCharacters(in: .whitespaces)
        guard var c = HostConfig(pairing: s.contains("://") ? s : "http://" + s) else { return nil }
        if let config { c.id = config.id }
        let n = name.trimmingCharacters(in: .whitespaces)
        if !n.isEmpty { c.name = n }
        c.icon = symbol
        c.desktop = symbol == "desktopcomputer"
        let cmd = command.trimmingCharacters(in: .whitespaces)
        c.command = cmd.isEmpty ? nil : cmd
        c.transport = HostConfig.transport(switched: tailnet, was: config, at: c.url, rule: TailnetEngine.shared.transport(of: c.url))
        return c
    }

    var body: some View {
        let c = entered
        NavigationStack {
            Form {
                Section {
                    TextField("http://192.168.1.2:7297", text: $address)
                        .keyboardType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .accessibilityIdentifier("address")
                } header: {
                    VStack(spacing: 20) {
                        Text(config == nil ? "Enter the details of your Rex server." : "Update the details for this remote host.")
                            .font(.body).textCase(nil)
                            .frame(maxWidth: .infinity).multilineTextAlignment(.center)
                        Text("Server address").frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
                .listRowBackground(Color.card)
                Section("Host details") {
                    HStack(spacing: 12) {
                        Menu {
                            Picker("Device", selection: $symbol) {
                                ForEach(HostConfig.icons, id: \.symbol) { Label($0.name, systemImage: $0.symbol).tag($0.symbol) }
                            }
                        } label: {
                            HStack(spacing: 4) {
                                Image(systemName: symbol)
                                Image(systemName: "chevron.up.chevron.down").font(.caption2.weight(.semibold))
                            }
                            .foregroundStyle(.primary)
                        }
                        .accessibilityLabel("Device")
                        TextField(c.map { HostConfig.name($0.url.host() ?? "") } ?? String(localized: "Name"), text: $name)
                            .autocorrectionDisabled()
                    }
                }
                .listRowBackground(Color.card)
                Section {
                    TextField("Default login shell", text: $command)
                        .font(.body.monospaced())
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                } header: {
                    Text("Startup command")
                } footer: {
                    Text("Executable and arguments for new terminals on this host. Use quotes to group arguments. Leave empty to use the host's default login shell.")
                }
                .listRowBackground(Color.card)
                if tailnetOn || c?.transport == .tailnet {
                    Section {
                        Toggle("Built-in Tailscale", isOn: Binding { c?.transport == .tailnet } set: { tailnet = $0 })
                    } footer: {
                        Text("Reach this host through Rex Go's own Tailscale, not the phone's network or the Tailscale app. With built-in Tailscale off, it goes the phone's way.")
                    }
                    .listRowBackground(Color.card)
                }
                if let config {
                    Section {
                        Button("Delete Host", role: .destructive) { deleting = true }
                            .frame(maxWidth: .infinity)
                            .confirmationDialog("Delete \(config.name)?", isPresented: $deleting, titleVisibility: .visible) {
                                Button("Delete Host", role: .destructive) {
                                    store.remove(config.id)
                                    dismiss()
                                }
                            }
                    }
                    .listRowBackground(Color.card)
                }
            }
            .scrollContentBackground(.hidden)
            .background(Color.page)
            .navigationTitle(config == nil ? "Add Host" : "Edit Host")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel", systemImage: "xmark", role: .cancel) { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button(config == nil ? "Add" : "Save", systemImage: "checkmark", role: .confirm) {
                        guard let c else { return }
                        if config == nil { store.add(c) } else { store.update(c) }
                        dismiss()
                    }
                    .disabled(c == nil || c == config)
                }
            }
        }
        .presentationDetents([.large])
    }
}
