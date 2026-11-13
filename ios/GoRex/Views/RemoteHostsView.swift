import Network
import SwiftUI

/// The hosts, as Rex's Remote Hosts page lists them: edit to remove or
/// reorder, ⓘ for one's details, Add Host to pair or set one up.
struct RemoteHostsView: View {
    @Environment(HostStore.self) private var store
    @State private var sheet: Sheet?

    private enum Sheet: Identifiable {
        case connect, manual, scan, details(HostConfig), pair(HostConfig), pairAddress(String, HostConfig)
        var id: String {
            switch self {
            case .connect: "connect"
            case .manual: "manual"
            case .scan: "scan"
            case .details(let c): c.id.uuidString
            case .pair(let c): "pair " + c.id.uuidString
            case .pairAddress(_, let c): "pair address " + c.id.uuidString
            }
        }
    }

    var body: some View {
        // Demo Mode's host runs on the phone, so it is no host to edit here.
        let hosts = store.hosts.filter { !$0.isDemo }
        List {
            if store.demo != nil {
                Section {
                    Button("Leave Demo Mode", systemImage: "sparkles", role: .destructive) {
                        store.exitDemo()
                    }
                    .listRowBackground(Color.card)
                } footer: {
                    Text("You're in demo mode. Add a host to run your own windows.")
                }
            }
            Section("Hosts") {
                ForEach(hosts) { h in
                    HostRow(host: h) { sheet = .details(h.config) }
                }
                .onDelete { $0.map { hosts[$0].id }.forEach(store.remove) }
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
                HostForm(config: nil) { sheet = .pairAddress($0, $1) }
            case .scan:
                QRScanner(onScan: pair)
            case .details(let c):
                HostForm(config: c)
            case .pair(let c):
                TailcatPairSheet(code: c)
            case .pairAddress(let address, let c):
                AddressPairSheet(address: address, host: c) { sheet = .details($0) }
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
    /// Pairs by the IP address typed, with the host as entered.
    var pairByAddress: ((String, HostConfig) -> Void)?
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

    init(config: HostConfig?, pairByAddress: ((String, HostConfig) -> Void)? = nil) {
        self.config = config
        self.pairByAddress = pairByAddress
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

    /// The address typed, when a GoRex machine could pair by it: an IP
    /// address, or IP:PORT.
    private var pairAddress: String? {
        let s = address.trimmingCharacters(in: .whitespaces)
        guard config == nil, !s.contains("://"), let h = entered?.url.host()?.trimmingCharacters(in: ["[", "]"]),
              IPv4Address(h) != nil || IPv6Address(h) != nil else { return nil }
        return s
    }

    var body: some View {
        let c = entered
        NavigationStack {
            Form {
                if let t = config?.tailcat {
                    Section {
                        LabeledContent("GoRex remote access", value: t.machine)
                    } footer: {
                        Text("Paired by GoRex's Rex Go code. Deleting the host here doesn't revoke this phone: remove it from Devices in GoRex's Settings › Server.")
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
                    } footer: {
                        if let why = HostConfig.refusal(of: address) { Text(why) }
                    }
                    .listRowBackground(Color.card)
                }
                if let p = pairAddress, let c, let pairByAddress {
                    Section {
                        Button("Pair with GoRex") { pairByAddress(p, c) }
                    } footer: {
                        Text("For a GoRex machine with Reachable at on: first choose Pair New Device… in its Settings › Server › Devices.")
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
                    Text("On \(code.name), approve \(TailnetSettings.name) in Settings › Server › Devices only if its fingerprint is this one.")
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
            // Not approved yet, or still connecting: it stays, and tries on.
            kept = true
            problem = String(localized: "Not connected yet. Rex Go keeps trying: approve this phone on the machine.")
        } catch is CancellationError {
        } catch {
            problem = String(localized: "Couldn't pair: \(error.localizedDescription)")
        }
    }

    private func finish() { dismiss() }
}

/// Pairs with a GoRex machine by its IP address, through its own relay
/// (docs/host-links.md, Pairing by address): shows the code to compare
/// with the one the machine shows, and once the user says they match and
/// the machine approves, adds the host at the tailcat address it got, as
/// a Rex Go code's. "They differ", an error or leaving saves nothing.
private struct AddressPairSheet: View {
    let address: String
    /// The host as entered: its name, icon and command.
    let host: HostConfig
    /// Shows the host there is already at the address paired, as a scan does.
    let existing: (HostConfig) -> Void
    @Environment(HostStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    @State private var code: String?
    @State private var matched = false
    @State private var problem: String?
    /// The wait for the approval, cancelled when the sheet goes.
    @State private var confirming: Task<Void, Never>?

    /// The pairing did not match, the codes or the machine's answer.
    private var warning: String {
        String(localized: "The pairing didn't match. The machine may have let in an unknown device: remove its newest device in its Settings › Server › Devices.")
    }

    var body: some View {
        VStack(spacing: 16) {
            Text("Pair with \(host.name)").font(.title2.bold())
            if let problem {
                Text(problem).foregroundStyle(.secondary)
            } else if let code {
                if matched {
                    Text("On \(host.name), open Settings › Server › Devices and approve the request showing this code.")
                        .foregroundStyle(.secondary)
                } else {
                    Text("Compare this code with the one \(host.name) shows. Both must be the same.")
                        .foregroundStyle(.secondary)
                }
                Text(code).font(.largeTitle.monospaced().bold())
                if matched {
                    ProgressView()
                } else {
                    VStack(spacing: 12) {
                        Button { confirm() } label: { Text("The codes match").frame(maxWidth: .infinity) }
                            .buttonStyle(.glassProminent)
                            .foregroundStyle(Color.page)
                        Button { differ() } label: { Text("They differ").frame(maxWidth: .infinity) }
                            .buttonStyle(.glass)
                    }
                    .font(.headline)
                    .controlSize(.extraLarge)
                }
            } else {
                Text("Asking \(host.name) to pair…").foregroundStyle(.secondary)
                ProgressView()
            }
            Spacer(minLength: 12)
            Button(problem == nil ? "Cancel" : "Done") { dismiss() }
                .buttonStyle(.glass)
                .font(.headline)
                .controlSize(.extraLarge)
        }
        .multilineTextAlignment(.center)
        .padding(24)
        .presentationDetents([.medium, .large])
        .interactiveDismissDisabled(code != nil && problem == nil)
        .task {
            do {
                let c = try await TailnetEngine.shared.pairByAddress(address)
                // Left while it asked: the request goes.
                if Task.isCancelled {
                    TailnetEngine.shared.cancelAddressPairing(code: c)
                    return
                }
                code = c
            } catch {
                problem = String(localized: "Couldn't pair: \(error.localizedDescription)")
            }
        }
        .onDisappear {
            confirming?.cancel()
            if let code { TailnetEngine.shared.cancelAddressPairing(code: code) }
        }
    }

    private func confirm() {
        guard let code else { return }
        matched = true
        confirming = Task {
            do {
                let a = try await TailnetEngine.shared.confirmAddressPairing(code: code)
                // The sheet went meanwhile: nothing is saved.
                guard !Task.isCancelled else { return }
                if let h = store.host(tailcat: a) {
                    existing(h.config)
                    return
                }
                var h = host
                h.url = HostConfig.tailcatURL(h.id)
                h.transport = nil
                h.tailcat = HostConfig.Tailcat(address: a, machine: h.name)
                store.add(h)
                dismiss()
            } catch {
                problem = TailnetEngine.shared.isAddressPairingWarning(error)
                    ? warning : String(localized: "Couldn't pair: \(error.localizedDescription)")
            }
        }
    }

    private func differ() {
        guard let code else { return }
        problem = warning
        TailnetEngine.shared.mismatchAddressPairing(code: code)
    }
}
