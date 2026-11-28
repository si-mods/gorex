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
                    Text("You're in demo mode. Add a host to run your own tabs.")
                }
            }
            Section("Hosts") {
                if hosts.isEmpty {
                    EmptyState("No hosts yet", symbol: "desktopcomputer")
                        .listRowBackground(Color.card)
                }
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
        // A sheet of an iOS app on a Mac loses the objects the app
        // injected at the root; the store goes back in for its views.
        .sheet(item: $sheet) { s in
            Group {
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
            .environment(store)
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
            // A new code of the same computer brings its new token.
            if let t = c.lanToken, t != h.config.lanToken {
                var u = h.config
                u.lanToken = t
                store.update(u)
            }
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
                // A tailcat host's address is a secret, and its URL the
                // proxy's; an ssh:// host shows the address it was added by.
                Text(subtitle)
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
        // A password login offers to install the phone's key, once each
        // (docs/IOS-023-ssh-hosts.md).
        .onChange(of: host.status) { _, s in
            if s == .online, host.config.ssh != nil { TailnetEngine.shared.offerSSHInstallKey(host.config) }
        }
    }

    private var subtitle: String {
        if let s = host.config.ssh { return "ssh://\(s.user)@\(s.host)" }
        return host.config.tailcat == nil ? host.config.url.absoluteString : String(localized: "GoRex remote access")
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
    /// A new host as an address, or as SSH fields: the login, machine,
    /// port and endpoint each in their own field, not spelled inside a
    /// URL (docs/IOS-023-ssh-hosts.md).
    @State private var mode = Mode.address
    @State private var sshUser = ""
    @State private var sshHost = ""
    @State private var sshPort = ""
    @State private var sshEndpoint = ""
    @State private var sshKeyID: UUID?
    @State private var installingKey = false
    @State private var installKeyResult: String?
    /// Whether the host goes through the built-in Tailscale, as switched;
    /// nil keeps the host's way, or, for a new one or a new address, takes
    /// it by the address.
    @State private var tailnet: Bool?
    @State private var deleting = false
    @AppStorage(TailnetSettings.onKey) private var tailnetOn = false

    private enum Mode { case address, ssh }

    init(config: HostConfig?, pairByAddress: ((String, HostConfig) -> Void)? = nil) {
        self.config = config
        self.pairByAddress = pairByAddress
        _address = State(initialValue: config?.url.absoluteString ?? "")
        _symbol = State(initialValue: config?.symbol ?? "laptopcomputer")
        _name = State(initialValue: config?.name ?? "")
        _command = State(initialValue: config?.command ?? "")
        _sshKeyID = State(initialValue: config?.ssh?.keyID)
    }

    /// The SSH record the fields name, nil while they don't read as one,
    /// by the ssh:// address's own grammar (HostConfig.ssh).
    private var sshEntered: HostConfig.SSH? {
        var s = HostConfig.ssh(user: sshUser, host: sshHost, port: sshPort, endpoint: sshEndpoint)
        s?.keyID = sshKeyID
        return s
    }

    /// Each field's fault, checked alone beside valid others; an empty
    /// host has its own line, and an empty endpoint discovers.
    private var sshHostInvalid: Bool {
        !sshHost.trimmingCharacters(in: .whitespaces).isEmpty
            && HostConfig.ssh(user: "u", host: sshHost, port: "", endpoint: "") == nil
    }

    private var sshPortInvalid: Bool {
        HostConfig.ssh(user: "u", host: "h", port: sshPort, endpoint: "") == nil
    }

    private var sshEndpointInvalid: Bool {
        HostConfig.ssh(user: "u", host: "h", port: "", endpoint: sshEndpoint) == nil
    }

    /// A field's own error line under it, as rootshell's profile editor
    /// captions validation beneath each field.
    private func sshValidation(_ message: LocalizedStringKey?) -> some View {
        Group {
            if let message {
                Text(message)
                    .font(.caption)
                    .foregroundStyle(.red)
            }
        }
    }

    /// The host as entered, if its address reads as an ssh:// address or
    /// a pairing code does; a bare host gets http://. A tailcat or an
    /// ssh:// host keeps its own.
    private var entered: HostConfig? {
        var c: HostConfig
        if let config, config.tailcat != nil || config.ssh != nil {
            c = config
            if c.ssh != nil { c.ssh?.keyID = sshKeyID }
        } else if mode == .ssh {
            guard let s = sshEntered else { return nil }
            let id = UUID()
            c = HostConfig(id: id, name: "\(s.user)@\(s.host)", url: HostConfig.sshURL(id), ssh: s, lanToken: nil)
        } else {
            let s = address.trimmingCharacters(in: .whitespaces)
            if let ssh = HostConfig(ssh: s) {
                c = ssh
            } else {
                guard let p = HostConfig(pairing: s.contains("://") ? s : "http://" + s) else { return nil }
                c = p
            }
            if let config {
                c.id = config.id
                // The address shown has no token: the host keeps its own
                // till a code with one is pasted.
                if c.lanToken == nil, c.url == config.url { c.lanToken = config.lanToken }
            }
            // An ssh:// host's reach comes from its ssh member; the saved
            // transport stays nil (docs/IOS-023-ssh-hosts.md, Migration).
            if c.ssh == nil {
                c.transport = HostConfig.transport(switched: tailnet, was: config, at: c.url, rule: TailnetEngine.shared.transport(of: c.url))
            }
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
        guard config == nil, mode == .address, !s.contains("://"), let h = entered?.url.host()?.trimmingCharacters(in: ["[", "]"]),
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
                } else if let s = config?.ssh {
                    Section {
                        LabeledContent("SSH", value: "\(s.user)@\(s.host):\(s.port)")
                        if let socket = s.socket {
                            LabeledContent("Socket", value: socket)
                        }
                        if let tcp = s.tcp {
                            LabeledContent("TCP", value: tcp)
                        }
                        Picker("SSH key", selection: $sshKeyID) {
                            Text("Automatic").tag(UUID?.none)
                            ForEach(SSHKeyStore.shared.keys) { key in
                                Text(key.name).tag(UUID?.some(key.id))
                            }
                        }
                        if let hostKey = s.hostKey {
                            LabeledContent("Host key", value: hostKey)
                                .textSelection(.enabled)
                        }
                    } footer: {
                        Text("Forget the host key to be asked again on the next connect; change the address by deleting the host and adding it again.")
                    }
                    .listRowBackground(Color.card)
                    if let config {
                        Section {
                            Button {
                                installingKey = true
                                installKeyResult = nil
                                Task {
                                    do {
                                        try await TailnetEngine.shared.installSSHKeyDirectly(config)
                                        installKeyResult = String(localized: "SSH key installed on host.")
                                    } catch {
                                        installKeyResult = error.localizedDescription
                                    }
                                    installingKey = false
                                }
                            } label: {
                                HStack {
                                    Text("Install SSH Key")
                                    Spacer()
                                    if installingKey { ProgressView() }
                                }
                            }
                            .disabled(installingKey || SSHKeyStore.shared.keys.isEmpty)
                            if s.hostKey != nil {
                                Button("Forget Host Key", role: .destructive) {
                                    var updated = config
                                    updated.ssh?.hostKey = nil
                                    store.update(updated)
                                    dismiss()
                                }
                            }
                        } footer: {
                            if let installKeyResult { Text(installKeyResult) }
                        }
                        .listRowBackground(Color.card)
                    }
                } else if config == nil {
                    Section {
                        Picker("Type", selection: $mode) {
                            Text("Address").tag(Mode.address)
                            Text("SSH").tag(Mode.ssh)
                        }
                        if mode == .address {
                            TextField("http://192.168.1.2:7297", text: $address)
                                .keyboardType(.URL)
                                .textInputAutocapitalization(.never)
                                .autocorrectionDisabled()
                                .accessibilityIdentifier("address")
                        } else {
                            // As rootshell's profile editor: the fields
                            // in their own rows, each validation under
                            // its field, and the way in named.
                            TextField("Hostname", text: $sshHost)
                                .keyboardType(.URL)
                                .textInputAutocapitalization(.never)
                                .autocorrectionDisabled()
                                .accessibilityIdentifier("sshHost")
                            sshValidation(sshHost.trimmingCharacters(in: .whitespaces).isEmpty
                                          ? "Hostname is required."
                                          : sshHostInvalid ? "Hostname is a name or an IP address, with no port, user or path." : nil)
                            HStack {
                                Text("Port")
                                Spacer()
                                TextField("22", text: $sshPort)
                                    .keyboardType(.numberPad)
                                    .multilineTextAlignment(.trailing)
                                    .frame(width: 80)
                                    .accessibilityIdentifier("sshPort")
                            }
                            sshValidation(sshPortInvalid
                                          ? "Port must be between 1 and 65535, or blank for 22." : nil)
                            TextField("Username", text: $sshUser)
                                .textContentType(.username)
                                .textInputAutocapitalization(.never)
                                .autocorrectionDisabled()
                                .accessibilityIdentifier("sshUser")
                            sshValidation(sshUser.trimmingCharacters(in: .whitespaces).isEmpty
                                          ? "Username is required." : nil)
                            TextField("Endpoint (optional)", text: $sshEndpoint)
                                .textInputAutocapitalization(.never)
                                .autocorrectionDisabled()
                                .accessibilityIdentifier("sshEndpoint")
                            sshValidation(sshEndpointInvalid
                                          ? "Endpoint is a socket path like /run/rex/server.sock, or tcp://host:port." : nil)
                            LabeledContent("Authentication", value: String(localized: "SSH key, then password"))
                            Picker("SSH key", selection: $sshKeyID) {
                                Text("Automatic").tag(UUID?.none)
                                ForEach(SSHKeyStore.shared.keys) { key in
                                    Text(key.name).tag(UUID?.some(key.id))
                                }
                            }
                        }
                    } header: {
                        VStack(spacing: 20) {
                            Text("Enter the details of your Rex server.")
                                .font(.body).textCase(nil)
                                .frame(maxWidth: .infinity).multilineTextAlignment(.center)
                            Text(mode == .ssh ? "SSH login" : "Server address")
                                .frame(maxWidth: .infinity, alignment: .leading)
                        }
                    } footer: {
                        if mode == .address, let why = HostConfig.refusal(of: address) { Text(why) }
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
                            Text("Update the details for this remote host.")
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
                        TextField(c.map { $0.tailcat?.machine ?? $0.ssh.map { "\($0.user)@\($0.host)" } ?? HostConfig.name($0.url.host() ?? "") } ?? String(localized: "Name"), text: $name)
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
                if (tailnetOn || c?.transport == .tailnet) && config?.tailcat == nil && c?.ssh == nil {
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

/// Pairs with a GoRex machine by its Rex Go code (docs/IOS-002-tailcat.md,
/// Pairing a phone): asks it to allow this phone, shows the fingerprint to
/// compare with the one GoRex shows by the request, and adds the host,
/// which connects once the request is approved there.
struct TailcatPairSheet: View {
    let code: HostConfig
    @Environment(HostStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    @State private var fingerprint: String?
    /// True when the host answered that this phone's key is already on its
    /// allowlist: the host is added immediately without waiting for approval.
    @State private var alreadyAllowed = false
    /// When the host's pairing window for this request closes.
    @State private var deadline: Date?
    /// Why the sheet stopped: the pair failed, or the approval window ran out.
    @State private var end: PairEnd?
    /// The host or transport error for a failed pair, so the user knows
    /// which step failed.
    @State private var failureDetail: String?
    /// Whether the host stays: it came online or was already allowed. Any
    /// other way out removes the unapproved host.
    @State private var kept = false
    @State private var copied = false

    /// How long the sheet waits for the approval: GoRex's pairing window.
    private static let waitSeconds: TimeInterval = 5 * 60
    private static let wait = Duration.seconds(waitSeconds)

    private var online: Bool { store.host(code.id)?.status == .online }
    /// Added, and waiting for the approval.
    private var waiting: Bool { fingerprint != nil && end == nil && !online }

    /// A discrete key for animated transitions between pairing phases.
    private var phase: Int {
        if end != nil { return 4 }
        if online { return 3 }
        if alreadyAllowed { return 2 }
        if fingerprint != nil { return 1 }
        return 0
    }

    private func badge(systemName: String, color: Color) -> some View {
        Image(systemName: systemName)
            .font(.system(size: 24, weight: .semibold))
            .foregroundStyle(color)
            .frame(width: 52, height: 52)
            .background(color.opacity(0.14), in: RoundedRectangle(cornerRadius: 14, style: .continuous))
    }

    var body: some View {
        VStack(spacing: 16) {
            if let end {
                ContentUnavailableView {
                    Label(end == .expired ? "Pairing window expired." : "Couldn't pair.",
                          systemImage: end == .expired ? "clock" : "exclamationmark.triangle")
                } description: {
                    if end == .expired {
                        Text("The request wasn't approved in time. Open Rex Go QR… on \(code.name) and try again.")
                    } else if let failureDetail, !failureDetail.isEmpty {
                        Text(failureDetail)
                    } else {
                        Text("Check the connection and try again.")
                    }
                } actions: {
                    Button("Try again") { retry() }
                        .buttonStyle(.glassProminent)
                        .foregroundStyle(Color.page)
                    copyButton()
                }
            } else if online {
                Spacer(minLength: 8)
                badge(systemName: "checkmark.circle.fill", color: .green)
                    .symbolEffect(.bounce, value: online)
                VStack(spacing: 6) {
                    Text("Connected to \(code.name)").font(.title2.bold())
                    Text("Paired and ready to use.").font(.subheadline).foregroundStyle(.secondary)
                }
                Spacer(minLength: 8)
            } else if alreadyAllowed {
                Spacer(minLength: 8)
                badge(systemName: "checkmark.circle.fill", color: .green)
                VStack(spacing: 6) {
                    Text("Already Paired").font(.title2.bold())
                    Text("Connecting to \(code.name)…").font(.subheadline).foregroundStyle(.secondary)
                }
                ProgressView()
                Spacer(minLength: 8)
            } else if let fingerprint {
                badge(systemName: "checkmark.shield", color: .accentColor)
                VStack(spacing: 6) {
                    Text("Approve on \(code.name)").font(.title2.bold())
                    Text("Confirm this fingerprint matches on \(code.name).")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                }
                Text(fingerprint)
                    .font(.title2.monospaced().bold())
                    .textSelection(.enabled)
                    .padding(.vertical, 14)
                    .padding(.horizontal, 24)
                    .frame(maxWidth: .infinity)
                    .background(Color.card, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
                if let deadline {
                    TimelineView(.periodic(from: .now, by: 1)) { ctx in
                        HStack(spacing: 6) {
                            ProgressView().controlSize(.small)
                            Text("Waiting · \(HostConfig.remainingPairingText(until: deadline, now: ctx.date)) left")
                                .font(.footnote.monospacedDigit())
                                .foregroundStyle(.secondary)
                                .contentTransition(.numericText())
                        }
                    }
                } else {
                    ProgressView()
                }
            } else {
                Spacer(minLength: 8)
                badge(systemName: "antenna.radiowaves.left.and.right", color: .accentColor)
                    .symbolEffect(.pulse)
                VStack(spacing: 6) {
                    Text("Pair with \(code.name)").font(.title2.bold())
                    Text("Sending pairing request…").font(.subheadline).foregroundStyle(.secondary)
                }
                ProgressView()
                Spacer(minLength: 8)
            }
            Spacer(minLength: 8)
            Button { finish() } label: {
                Text(waiting || (fingerprint == nil && !alreadyAllowed && end == nil) ? "Cancel" : "Done")
                    .frame(maxWidth: .infinity)
            }
            .buttonStyle(.glass)
            .font(.headline)
            .controlSize(.extraLarge)
        }
        .multilineTextAlignment(.center)
        .padding(24)
        .animation(.snappy, value: phase)
        .presentationDetents([.medium])
        .interactiveDismissDisabled(waiting)
        .task { await pair() }
        .onChange(of: online) { _, on in
            guard on else { return }
            kept = true
            Task { try? await Task.sleep(for: .milliseconds(800)); dismiss() }
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
            if f.isEmpty {
                // Already on the host's allowlist: add immediately, no approval needed.
                alreadyAllowed = true
                kept = true
                store.add(code)
                return
            }
            fingerprint = f
            deadline = Date.now.addingTimeInterval(Self.waitSeconds)
            store.add(code)
            try await Task.sleep(for: Self.wait)
            guard !online else { return }
            // Window expired without approval: remove the unapproved host
            // instead of keeping one that can never connect.
            store.remove(code.id)
            end = .expired
        } catch is CancellationError {
        } catch {
            if HostConfig.isAlreadyAllowed(error) {
                alreadyAllowed = true
                kept = true
                store.add(code)
                return
            }
            ConnectionLog.note("pair \(code.name): \(error.diagnosis)")
            failureDetail = error.localizedDescription
            end = .failed
        }
    }

    private func retry() {
        end = nil
        failureDetail = nil
        fingerprint = nil
        alreadyAllowed = false
        deadline = nil
        kept = false
        Task { await pair() }
    }

    /// The button under a failed pair: the recent connection log, headed by
    /// the versions a report dates by, on the pasteboard.
    private func copyButton() -> some View {
        Button(copied ? "Copied" : "Copy diagnostics") {
            UIPasteboard.general.string = diagnosticsText()
            copied = true
            Task { try? await Task.sleep(for: .seconds(1.5)); copied = false }
        }
        .buttonStyle(.glass)
    }

    private func finish() { dismiss() }
}

/// The text a pair sheet's Copy puts on the pasteboard: the app's and
/// iOS's versions, then the recent connection events.
private func diagnosticsText() -> String {
    let build = Bundle.main.infoDictionary?["CFBundleVersion"] as? String ?? "0"
    let head = "Rex Go \(Bundle.main.version) (\(build)), iOS \(UIDevice.current.systemVersion), \(Date.now.ISO8601Format())"
    return ([head] + ConnectionLog.lines).joined(separator: "\n")
}

/// Why a pair sheet stopped: it failed and can retry, or the
/// codes did not match, which the user settles by leaving.
private enum PairEnd {
    case failed
    case mismatch
    /// The approval window ran out before the host approved the request.
    case expired
}

/// Pairs with a GoRex machine by its IP address, through its own relay
/// (docs/NET-002-host-links.md, Pairing by address): shows the code to compare
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
    @State private var end: PairEnd?
    @State private var copied = false
    /// The wait for the approval, cancelled when the sheet goes.
    @State private var confirming: Task<Void, Never>?

    var body: some View {
        VStack(spacing: 16) {
            Text("Pair with \(host.name)").font(.title2.bold())
            if let end {
                ContentUnavailableView {
                    Label(end == .mismatch ? "The pairing didn't match." : "Couldn't pair.",
                          systemImage: end == .mismatch ? "exclamationmark.shield" : "exclamationmark.triangle")
                } description: {
                    Text(end == .mismatch
                         ? "The machine may have let in an unknown device: remove its newest device in its Settings › Server › Devices."
                         : "Check the connection and try again.")
                } actions: {
                    if end == .failed {
                        Button("Try again") { retry() }
                            .buttonStyle(.glassProminent)
                            .foregroundStyle(Color.page)
                        Button(copied ? "Copied" : "Copy diagnostics") {
                            UIPasteboard.general.string = diagnosticsText()
                            copied = true
                            Task { try? await Task.sleep(for: .seconds(1.5)); copied = false }
                        }
                        .buttonStyle(.glass)
                    }
                }
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
            Button(end == nil ? "Cancel" : "Done") { dismiss() }
                .buttonStyle(.glass)
                .font(.headline)
                .controlSize(.extraLarge)
        }
        .multilineTextAlignment(.center)
        .padding(24)
        .presentationDetents([.medium, .large])
        .interactiveDismissDisabled(code != nil && end == nil)
        .task { await ask() }
        .onDisappear {
            confirming?.cancel()
            if let code { TailnetEngine.shared.cancelAddressPairing(code: code) }
        }
    }

    private func ask() async {
        do {
            let c = try await TailnetEngine.shared.pairByAddress(address)
            // Left while it asked: the request goes.
            if Task.isCancelled {
                TailnetEngine.shared.cancelAddressPairing(code: c)
                return
            }
            code = c
        } catch {
            ConnectionLog.note("pair \(host.name): \(error.diagnosis)")
            end = .failed
        }
    }

    private func retry() {
        if let code { TailnetEngine.shared.cancelAddressPairing(code: code) }
        code = nil
        matched = false
        end = nil
        Task { await ask() }
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
                if TailnetEngine.shared.isAddressPairingWarning(error) {
                    end = .mismatch
                } else {
                    ConnectionLog.note("pair \(host.name): \(error.diagnosis)")
                    end = .failed
                }
            }
        }
    }

    private func differ() {
        guard let code else { return }
        end = .mismatch
        TailnetEngine.shared.mismatchAddressPairing(code: code)
    }
}
