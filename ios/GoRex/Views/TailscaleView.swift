import SwiftUI

/// Settings › Tailscale, after rootshell's Tailscale settings: a status
/// card, the built-in Tailscale on or off, signing in, how it reaches the
/// GoRex machines, the device's name and control server, signing out.
struct TailscaleView: View {
    @Environment(HostStore.self) private var store
    @AppStorage(TailnetSettings.onKey) private var on = false
    @AppStorage(TailnetSettings.controlKey) private var control = ""
    @AppStorage(TailnetSettings.nameKey) private var savedName = ""
    @State private var name = TailnetSettings.name
    @State private var controlText = TailnetSettings.controlURL
    /// Used once to sign in, never kept.
    @State private var authKey = ""
    @State private var confirmSignOut = false
    private let engine = TailnetEngine.shared

    var body: some View {
        let s = engine.status
        let error = on ? engine.failure ?? s.error : nil
        List {
            Section {
                header(s, error: error)
                Toggle("Built-in Tailscale", isOn: $on)
                // Waiting for approval too: the admin approves it by this name.
                if on, let n = s.name {
                    LabeledContent(String(n.prefix { $0 != "." })) {
                        if let ip = s.ips?.first { Text(ip).monospaced() }
                    }
                }
                if let error { Text(error).font(.footnote).foregroundStyle(.red) }
            } header: {
                Text("Status")
            } footer: {
                Text("Rex Go joins your tailnet itself, with no Tailscale app or VPN. Hosts on the tailnet go through it.")
            }
            .listRowBackground(Color.card)
            if on {
                if !s.isRunning {
                    Section {
                        Button { Task { await engine.signIn() } } label: {
                            HStack {
                                Text("Sign In")
                                Spacer()
                                if engine.signingIn { ProgressView() }
                            }
                        }
                        .disabled(engine.signingIn)
                        SecureField("Auth Key", text: $authKey)
                            .submitLabel(.go)
                            .onSubmit {
                                let key = authKey
                                authKey = ""
                                Task { await engine.apply(authKey: key) }
                            }
                    } footer: {
                        Text("Sign in as the Tailscale user who signed in your GoRex machine. An auth key is used once and not kept.")
                    }
                    .listRowBackground(Color.card)
                }
                let machines = (s.peers ?? []).filter { $0.name.prefix { $0 != "." }.contains("-gorex") }
                if !machines.isEmpty {
                    Section {
                        ForEach(machines, id: \.name) { p in
                            NavigationLink { DiagnosticsView(address: p.name) } label: {
                                HStack {
                                    Image(systemName: "circle.fill").font(.caption2).foregroundStyle(p.online ? .green : .secondary)
                                        .accessibilityLabel(p.online ? Text("Online") : Text("Offline"))
                                    Text(HostConfig.name(p.name))
                                    Spacer(minLength: 8)
                                    Text(path(p)).foregroundStyle(.secondary)
                                }
                            }
                        }
                    } header: {
                        Text("GoRex Machines")
                    } footer: {
                        Text("Tap a machine to test the connection to it.")
                    }
                    .listRowBackground(Color.card)
                }
                Section {
                    LabeledContent("Device Name") {
                        TextField(savedName, text: $name)
                            .multilineTextAlignment(.trailing)
                            .foregroundStyle(.secondary)
                            .submitLabel(.done)
                            .onSubmit {
                                guard TailnetEngine.validName(name), name != savedName else { return }
                                savedName = name
                                Task { await engine.setHostname(name) }
                            }
                    }
                    LabeledContent("Control Server") {
                        TextField("Tailscale", text: $controlText)
                            .multilineTextAlignment(.trailing)
                            .foregroundStyle(.secondary)
                            .keyboardType(.URL)
                            .submitLabel(.done)
                            .onSubmit {
                                let c = controlText.trimmingCharacters(in: .whitespaces)
                                guard TailnetEngine.validControlURL(c), c != control else { return }
                                control = c
                                // A device belongs to one control server: a new one starts anew.
                                Task { await engine.signOut() }
                                reconnectTailnetHosts()
                            }
                    }
                } header: {
                    Text("Device")
                } footer: {
                    VStack(alignment: .leading, spacing: 4) {
                        if let e = engine.renameFailure { Text(e).foregroundStyle(.red) }
                        if !TailnetEngine.validName(name) {
                            Text("A device name has letters, digits and hyphens, not at either end.").foregroundStyle(.red)
                        }
                        if !TailnetEngine.validControlURL(controlText.trimmingCharacters(in: .whitespaces)) {
                            Text("A control server's URL starts with https://, or http:// for a local address.").foregroundStyle(.red)
                        }
                        Text("Leave the control server empty for Tailscale's own.")
                    }
                }
                .listRowBackground(Color.card)
                if s.hasDevice {
                    Section {
                        Button("Sign Out", role: .destructive) { confirmSignOut = true }
                            .confirmationDialog("Sign out of Tailscale?", isPresented: $confirmSignOut, titleVisibility: .visible) {
                                Button("Sign Out", role: .destructive) { Task { await engine.signOut() } }
                            }
                    }
                    .listRowBackground(Color.card)
                }
            }
        }
        .scrollContentBackground(.hidden)
        .background(Color.page)
        .textInputAutocapitalization(.never)
        .autocorrectionDisabled()
        .navigationTitle("Tailscale")
        .navigationBarTitleDisplayMode(.inline)
        // Hosts on the tailnet switch ways as it turns on or off.
        .onChange(of: on) {
            Task { await engine.apply() }
            reconnectTailnetHosts()
        }
        // The default name, made as the engine applies.
        .onChange(of: savedName) { if name.isEmpty { name = savedName } }
        // How each peer is reached changes unannounced.
        .task(id: on) {
            while on, !Task.isCancelled {
                await engine.refresh()
                try? await Task.sleep(for: .seconds(2))
            }
        }
    }

    private func reconnectTailnetHosts() {
        store.hosts.filter { $0.config.transport == .tailnet }.forEach { $0.reconnect() }
    }

    /// The status card, after rootshell's VPNStatusRow: Tailscale's tile
    /// with a dot of the state, what it is doing, and where it runs.
    private func header(_ s: TailnetEngine.Status, error: String?) -> some View {
        // A device that runs stays green; an error shows below the card.
        let (title, dot, busy): (LocalizedStringResource, Color, Bool) =
            if !on { ("Tailscale off", .gray, false) }
            else if s.isRunning { ("Tailscale connected", .green, false) }
            else if error != nil { ("Tailscale couldn't start", .red, false) }
            else if s.needsLogin { ("Tailscale needs sign-in", .orange, false) }
            else if s.awaitingApproval { ("Tailscale waiting for approval", .orange, false) }
            else { ("Tailscale connecting…", .orange, true) }
        return HStack(spacing: 12) {
            TailscaleGlyph.tile(size: 44)
                .overlay(alignment: .bottomTrailing) {
                    Circle().fill(dot).frame(width: 12, height: 12)
                        .overlay(Circle().stroke(Color.card, lineWidth: 2))
                        .offset(x: 3, y: 3)
                }
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.headline)
                Text("Tailscale inside Rex Go, no VPN").font(.subheadline).foregroundStyle(.secondary)
            }
            Spacer()
            if busy {
                Image(systemName: "arrow.trianglehead.2.clockwise")
                    .symbolEffect(.rotate, isActive: true)
                    .foregroundStyle(.orange)
            }
        }
        .padding(.vertical, 4)
    }

    /// How the device reaches a machine, as tailscale status tells.
    private func path(_ p: TailnetEngine.Peer) -> String {
        switch p.path {
        case "direct": String(localized: "Direct")
        case "derp": String(localized: "DERP Relay")
        case "peer-relay": String(localized: "Peer Relay")
        default: p.online ? String(localized: "Idle") : String(localized: "Offline")
        }
    }
}

/// Tailscale's mark, drawn as rootshell's TailscaleLogo: nine dots, the
/// middle row and the bottom one in the middle solid, the rest faint.
struct TailscaleGlyph: View {
    var body: some View {
        Canvas { c, size in
            let d = size.width / 4
            for row in 0..<3 {
                for col in 0..<3 {
                    var c = c
                    if !(row == 1 || (row == 2 && col == 1)) { c.opacity = 0.2 }
                    let r = CGRect(x: (0.125 + 0.375 * CGFloat(col)) * size.width - d / 2,
                                   y: (0.125 + 0.375 * CGFloat(row)) * size.height - d / 2, width: d, height: d)
                    c.fill(Path(ellipseIn: r), with: .foreground)
                }
            }
        }
        .aspectRatio(1, contentMode: .fit)
        .accessibilityHidden(true)
    }
}

extension TailscaleGlyph {
    /// The mark on a Settings tile of Tailscale's dark.
    static func tile(size: CGFloat = 30) -> some View {
        SettingsTile(color: Color(white: 0.12), size: size) {
            TailscaleGlyph().frame(width: size * 0.55, height: size * 0.55)
        }
    }
}
