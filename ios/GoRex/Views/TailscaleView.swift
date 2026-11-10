import SwiftUI

/// Settings › Tailscale, as the desktop's Tailscale card: the built-in
/// Tailscale on or off, signing in and out, how it reaches the GoRex
/// machines, the device's name and the control server.
struct TailscaleView: View {
    @Environment(HostStore.self) private var store
    @AppStorage(TailnetSettings.onKey) private var on = false
    @AppStorage(TailnetSettings.controlKey) private var control = ""
    @State private var name = TailnetSettings.name
    @State private var controlText = TailnetSettings.controlURL
    /// Used once to sign in, never kept.
    @State private var authKey = ""
    private let engine = TailnetEngine.shared

    var body: some View {
        let s = engine.status
        List {
            Section {
                Toggle("Built-in Tailscale", isOn: $on)
            } footer: {
                Text("Rex Go joins your tailnet itself, with no Tailscale app or VPN. Hosts on the tailnet go through it.")
            }
            .listRowBackground(Color.card)
            if on {
                Section {
                    LabeledContent("Status", value: state(s.state))
                    if let n = s.name { LabeledContent("Device", value: String(n.prefix { $0 != "." })) }
                    if let ip = s.ips?.first { LabeledContent("Address") { Text(ip).monospaced() } }
                    if let e = engine.failure ?? s.error { Text(e).font(.footnote).foregroundStyle(.red) }
                }
                .listRowBackground(Color.card)
                let machines = (s.peers ?? []).filter { $0.name.prefix { $0 != "." }.contains("-gorex") }
                if !machines.isEmpty {
                    Section("GoRex Machines") {
                        ForEach(machines, id: \.name) { p in LabeledContent(HostConfig.name(p.name), value: path(p)) }
                    }
                    .listRowBackground(Color.card)
                }
                if s.state == "Running" {
                    Section {
                        Button("Sign Out", role: .destructive) { Task { await engine.signOut() } }
                    }
                    .listRowBackground(Color.card)
                } else {
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
                Section {
                    LabeledContent("Device Name") {
                        TextField(TailnetSettings.name, text: $name)
                            .multilineTextAlignment(.trailing)
                            .foregroundStyle(.secondary)
                            .submitLabel(.done)
                            .onSubmit {
                                guard TailnetEngine.validName(name), name != TailnetSettings.name else { return }
                                UserDefaults.standard.set(name, forKey: TailnetSettings.nameKey)
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
                                Task { await engine.apply() }
                            }
                    }
                } footer: {
                    VStack(alignment: .leading, spacing: 4) {
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
            store.hosts.filter { $0.config.transport == .tailnet }.forEach { $0.reconnect() }
        }
        // How each peer is reached changes unannounced.
        .task(id: on) {
            while on, !Task.isCancelled {
                await engine.refresh()
                try? await Task.sleep(for: .seconds(2))
            }
        }
    }

    private func state(_ s: String) -> String {
        switch s {
        case "Running": String(localized: "Signed In")
        case "NeedsLogin": String(localized: "Not Signed In")
        case "NeedsMachineAuth": String(localized: "Waiting for Approval")
        case "Stopped": String(localized: "Stopped")
        default: String(localized: "Starting…")
        }
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
