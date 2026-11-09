import SwiftUI

/// The hosts, as Rex's Remote Hosts page lists them: edit to remove or
/// reorder, ⓘ for one's details, Add Host to pair or set one up.
struct RemoteHostsView: View {
    @Environment(HostStore.self) private var store
    @State private var sheet: Sheet?

    private enum Sheet: Identifiable {
        case connect, manual, scan, details(HostConfig), pair(DevicePairing)
        var id: String {
            switch self {
            case .connect: "connect"
            case .manual: "manual"
            case .scan: "scan"
            case .details(let c): c.id.uuidString
            case .pair: "pair"
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
            case .pair(let invitation):
                HostForm(config: store.host(at: invitation.base)?.config, invitation: invitation)
            }
        }
    }

    /// Adds the host of a scanned code, or shows it when it's there already.
    private func pair(_ code: String) -> String? {
        if let invitation = DevicePairing(code) { sheet = .pair(invitation); return nil }
        guard let c = HostConfig(pairing: code) else {
            return String(localized: "Not a Rex pairing code. Scan the Rex QR code that GoRex or Rex shows.")
        }
        if let h = store.host(at: c.url) {
            sheet = .details(h.config)
        } else {
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
            Image(systemName: host.config.desktop ? "desktopcomputer" : "laptopcomputer")
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
    @State private var desktop: Bool
    @State private var name: String
    @State private var command: String
    @State private var deleting = false
    @State private var pairingCode: String
    @State private var saving = false
    @State private var problem: String?

    init(config: HostConfig?, invitation: DevicePairing? = nil) {
        self.config = config
        _address = State(initialValue: invitation?.base.absoluteString ?? config?.url.absoluteString ?? "")
        _pairingCode = State(initialValue: invitation?.code ?? "")
        _desktop = State(initialValue: config?.desktop ?? false)
        _name = State(initialValue: config?.name ?? "")
        _command = State(initialValue: config?.command ?? "")
    }

    /// The host as entered, if its address reads as a pairing code does;
    /// a bare host gets http://.
    private var entered: HostConfig? {
        let s = DevicePairing(address)?.base.absoluteString ?? address.trimmingCharacters(in: .whitespaces)
        guard var c = HostConfig(pairing: s.contains("://") ? s : "http://" + s) else { return nil }
        if let config { c.id = config.id; c.transport = config.transport }
        let n = name.trimmingCharacters(in: .whitespaces)
        if !n.isEmpty { c.name = n }
        c.desktop = desktop
        let cmd = command.trimmingCharacters(in: .whitespaces)
        c.command = cmd.isEmpty ? nil : cmd
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
                if c?.url.scheme == "https" {
                    Section {
                        SecureField("Pairing code (optional)", text: $pairingCode)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                    } footer: {
                        Text("For public access without VPN, enter the code from GoRex on your computer. Leave empty for an existing connection or Tailscale.")
                    }
                    .listRowBackground(Color.card)
                }
                Section("Host details") {
                    HStack(spacing: 12) {
                        Menu {
                            Picker("Device", selection: $desktop) {
                                Label("Laptop", systemImage: "laptopcomputer").tag(false)
                                Label("Desktop", systemImage: "desktopcomputer").tag(true)
                            }
                        } label: {
                            HStack(spacing: 4) {
                                Image(systemName: desktop ? "desktopcomputer" : "laptopcomputer")
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
                        .disabled(saving)
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button(config == nil ? "Add" : "Save", systemImage: "checkmark", role: .confirm) {
                        guard let c else { return }
                        saving = true
                        Task {
                            do {
                                let code = DevicePairing(address)?.code ?? pairingCode.trimmingCharacters(in: .whitespacesAndNewlines)
                                if !code.isEmpty { try await DeviceAccess.pair(base: c.url, code: code, name: ControlConnection.label) }
                                if config == nil { store.add(c) } else {
                                    store.update(c)
                                    if !code.isEmpty { store.host(c.id)?.reconnect() }
                                }
                                dismiss()
                            } catch { problem = error.localizedDescription }
                            saving = false
                        }
                    }
                    .disabled(saving || c == nil || (c == config && pairingCode.isEmpty))
                }
            }
        }
        .interactiveDismissDisabled(saving)
        .alert("Connection failed", isPresented: Binding(get: { problem != nil }, set: { if !$0 { problem = nil } })) {
            Button("OK") { problem = nil }
        } message: { Text(problem ?? "") }
        .presentationDetents([.large])
    }
}
