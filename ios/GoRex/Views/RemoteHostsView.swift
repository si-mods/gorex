import SwiftUI

/// The hosts, as Rex's Remote Hosts page lists them: edit to remove or
/// reorder, ⓘ for one's details, Add Host to pair or set one up.
struct RemoteHostsView: View {
    @Environment(HostStore.self) private var store
    @State private var sheet: Sheet?

    private enum Sheet: Identifiable {
        case connect, manual, scan, details(HostConfig), pair(HostConfig)
        var id: String {
            switch self {
            case .connect: "connect"
            case .manual: "manual"
            case .scan: "scan"
            case .details(let c): c.id.uuidString
            case .pair(let c): "pair " + c.id.uuidString
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
            } footer: {
                // The key GoRex's Devices list shows for this phone, once paired.
                let key = TailnetEngine.shared.tailcatKey
                if !key.isEmpty {
                    Text("This phone's key for GoRex's remote access: \(key)").textSelection(.enabled)
                }
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
            case .pair(let c):
                TailcatPairSheet(code: c)
            }
        }
    }

    /// Adds the host of a scanned code, or shows it when it's there
    /// already; a Rex Go code pairs first.
    private func pair(_ code: String) -> String? {
        if let c = HostConfig(tailcat: code) {
            if let h = c.tailcat.flatMap({ store.host(tailcat: $0.address) }) {
                sheet = .details(h.config)
            } else {
                sheet = .pair(c)
            }
            return nil
        }
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
                // A tailcat host's address is a secret, and its URL the proxy's.
                Text(host.config.tailcat == nil ? host.config.url.absoluteString : String(localized: "GoRex remote access"))
                    .font(.footnote).foregroundStyle(.gray)
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
    /// a bare host gets http://. A tailcat host keeps its address.
    private var entered: HostConfig? {
        var c: HostConfig
        if let config, config.tailcat != nil {
            c = config
        } else {
            let s = address.trimmingCharacters(in: .whitespaces)
            guard let p = HostConfig(pairing: s.contains("://") ? s : "http://" + s) else { return nil }
            c = p
            if let config { c.id = config.id }
            c.transport = HostConfig.transport(switched: tailnet, was: config, at: c.url, rule: TailnetEngine.shared.transport(of: c.url))
        }
        let n = name.trimmingCharacters(in: .whitespaces)
        if !n.isEmpty { c.name = n } else if let m = c.tailcat?.machine, !m.isEmpty { c.name = m }
        c.icon = symbol
        c.desktop = symbol == "desktopcomputer"
        let cmd = command.trimmingCharacters(in: .whitespaces)
        c.command = cmd.isEmpty ? nil : cmd
        return c
    }

    var body: some View {
        let c = entered
        NavigationStack {
            Form {
                if let t = config?.tailcat {
                    Section {
                        LabeledContent("GoRex remote access", value: t.machine)
                    } footer: {
                        Text("Paired by GoRex's Rex Go code. Deleting the host here doesn't revoke this phone: remove it from Devices in GoRex's Settings › Remote.")
                    }
                    .listRowBackground(Color.card)
                } else {
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
                }
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
                        TextField(c.map { $0.tailcat?.machine ?? HostConfig.name($0.url.host() ?? "") } ?? String(localized: "Name"), text: $name)
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
                if (tailnetOn || c?.transport == .tailnet) && config?.tailcat == nil {
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

/// Pairs with a GoRex machine by its Rex Go code (docs/ios-tailcat.md,
/// Pairing a phone): asks it to allow this phone, shows the fingerprint to
/// compare with the one GoRex shows by the request, and adds the host,
/// which connects once the request is approved there.
struct TailcatPairSheet: View {
    let code: HostConfig
    @Environment(HostStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    @State private var fingerprint: String?
    @State private var problem: String?
    /// Whether the host stays: it came online, or waiting timed out while
    /// it may be approved. Any other way out takes it back.
    @State private var kept = false

    /// How long the sheet waits for the approval: GoRex's pairing window.
    private static let wait = Duration.seconds(5 * 60)

    private var online: Bool { store.host(code.id)?.status == .online }
    /// Added, and waiting for the approval.
    private var waiting: Bool { fingerprint != nil && problem == nil && !online }

    var body: some View {
        VStack(spacing: 16) {
            Text("Pair with \(code.name)").font(.title2.bold())
            if let problem {
                Text(problem).foregroundStyle(.secondary)
            } else if let fingerprint {
                if online {
                    Text("Paired. Rex Go is connected.").foregroundStyle(.secondary)
                } else {
                    Text("On \(code.name), approve \(TailnetSettings.name) in Settings › Remote › Devices only if its fingerprint is this one.")
                        .foregroundStyle(.secondary)
                }
                Text(fingerprint).font(.title3.monospaced().bold()).textSelection(.enabled)
                if !online { ProgressView() }
            } else {
                Text("Asking \(code.name) to pair…").foregroundStyle(.secondary)
                ProgressView()
            }
            Spacer(minLength: 12)
            Button(waiting || fingerprint == nil && problem == nil ? "Cancel" : "Done") { finish() }
                .buttonStyle(.glass)
                .font(.headline)
                .controlSize(.extraLarge)
        }
        .multilineTextAlignment(.center)
        .padding(24)
        .presentationDetents([.medium])
        .interactiveDismissDisabled(waiting)
        .task { await pair() }
        .onChange(of: online) { _, on in
            guard on else { return }
            kept = true
            Task { try? await Task.sleep(for: .seconds(1)); dismiss() }
        }
        // Cancel, a new code in its place or Settings closing: a host not
        // approved goes. It stays pending on the machine until its pairing
        // window closes.
        .onDisappear { if !kept && !online { store.remove(code.id) } }
    }

    private func pair() async {
        guard let address = code.tailcat?.address else { return }
        do {
            let f = try await TailnetEngine.shared.pair(address: address)
            try Task.checkCancellation()
            fingerprint = f
            store.add(code)
            try await Task.sleep(for: Self.wait)
            guard !online else { return }
            // Approved, it may still be connecting, or refused for sessions
            // not shared with it: it stays, and tries on.
            kept = true
            problem = String(localized: "Not connected yet. Rex Go keeps trying: approve this phone on the machine, and share its sessions with this device.")
        } catch is CancellationError {
        } catch {
            problem = String(localized: "Couldn't pair: \(error.localizedDescription)")
        }
    }

    private func finish() { dismiss() }
}
