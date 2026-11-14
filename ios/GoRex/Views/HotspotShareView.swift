import SwiftUI
import UIKit

/// Settings › Share on Hotspot: turns the hotspot web server on/off,
/// shows the URL to open on a laptop, manages the cached web client,
/// and admits or removes devices on the hotspot.
struct HotspotShareView: View {
    @Environment(HostStore.self) private var store
    @State private var share = HotspotShare.shared
    @State private var webStore = WebClientStore.shared
    @State private var dimmed = false
    @State private var savedBrightness: CGFloat?
    @State private var dimTask: Task<Void, Never>?

    var body: some View {
        ZStack {
            List {
                Section {
                    Toggle("Share on Hotspot", isOn: Binding(
                        get: { share.isSharing },
                        set: { on in
                            if on {
                                Task { await share.start(configs: store.configs) }
                            } else {
                                share.stop()
                                restoreBrightness()
                            }
                        }
                    ))
                    .listRowBackground(Color.card)
                } footer: {
                    Text("Keep Rex Go open while sharing. A laptop on your Personal Hotspot can open the web client without installing anything.")
                }

                if share.isSharing {
                    Section("Address") {
                        if let url = share.shareURL {
                            VStack(alignment: .leading, spacing: 6) {
                                Text(url)
                                    .font(.title3.monospaced().weight(.semibold))
                                    .textSelection(.enabled)
                                Text("Open this address in a browser on a laptop joined to your Personal Hotspot.")
                                    .font(.footnote)
                                    .foregroundStyle(.secondary)
                            }
                            .padding(.vertical, 4)
                            .listRowBackground(Color.card)
                        } else {
                            HStack(spacing: 10) {
                                Image(systemName: "personalhotspot")
                                    .foregroundStyle(.orange)
                                Text("Turn on Personal Hotspot in iOS Settings")
                                    .foregroundStyle(.secondary)
                            }
                            .listRowBackground(Color.card)
                        }
                        if let err = share.error {
                            Text(err)
                                .font(.footnote)
                                .foregroundStyle(.red)
                                .listRowBackground(Color.card)
                        }
                    }

                    Section("Admitted Devices") {
                        if share.admittedDevices.isEmpty {
                            Text("No devices admitted yet")
                                .foregroundStyle(.secondary)
                                .listRowBackground(Color.card)
                        } else {
                            ForEach(share.admittedDevices) { d in
                                HStack {
                                    VStack(alignment: .leading, spacing: 2) {
                                        Text(d.ip)
                                            .font(.body.monospaced())
                                        Text(d.userAgent)
                                            .font(.footnote)
                                            .foregroundStyle(.secondary)
                                    }
                                    Spacer()
                                    Button("Remove", role: .destructive) {
                                        share.revoke(d.ip)
                                    }
                                    .buttonStyle(.borderless)
                                }
                                .listRowBackground(Color.card)
                            }
                        }
                    }
                }

                Section("Web Client") {
                    TextField("R2 URL (https://...)", text: $webStore.baseURLString)
                        .keyboardType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .listRowBackground(Color.card)

                    HStack {
                        Text("Cached Copy")
                        Spacer()
                        switch webStore.state {
                        case .empty:
                            Text("Not Downloaded")
                                .foregroundStyle(.secondary)
                        case .downloading(let done, let total):
                            HStack(spacing: 6) {
                                ProgressView()
                                Text("\(done)/\(total)")
                                    .font(.footnote.monospacedDigit())
                                    .foregroundStyle(.secondary)
                            }
                        case .ready:
                            Text("Ready")
                                .foregroundStyle(.green)
                        case .failed(let msg):
                            Text(msg)
                                .font(.footnote)
                                .foregroundStyle(.red)
                                .lineLimit(2)
                        }
                    }
                    .listRowBackground(Color.card)

                    Button {
                        Task { await webStore.update() }
                    } label: {
                        Text(webStore.isCached ? "Update Web Client" : "Download Web Client")
                    }
                    .disabled({
                        if webStore.baseURL == nil { return true }
                        if case .downloading = webStore.state { return true }
                        return false
                    }())
                    .listRowBackground(Color.card)
                }
            }
            .scrollContentBackground(.hidden)
            .background(Color.page)

            if dimmed {
                Color.black.opacity(0.65)
                    .ignoresSafeArea()
                    .contentShape(Rectangle())
                    .onTapGesture {
                        restoreBrightness()
                        scheduleDim()
                    }
            }
        }
        .navigationTitle("Share on Hotspot")
        .navigationBarTitleDisplayMode(.inline)
        .onAppear {
            share.refreshHotspotIP()
            updateIdleTimer()
            scheduleDim()
        }
        .onDisappear {
            dimTask?.cancel()
            dimTask = nil
            UIApplication.shared.isIdleTimerDisabled = false
            restoreBrightness()
        }
        .onChange(of: share.isSharing) { _, _ in
            updateIdleTimer()
            scheduleDim()
        }
        .onChange(of: store.configs) { _, configs in
            share.updateHosts(configs)
        }
        .simultaneousGesture(
            TapGesture().onEnded {
                if dimmed { restoreBrightness() }
                scheduleDim()
            }
        )
        .alert(
            item: Binding(
                get: { share.pendingPrompt },
                set: { share.pendingPrompt = $0 }
            )
        ) { prompt in
            Alert(
                title: Text("Allow \(prompt.ip)?"),
                message: Text(prompt.userAgent),
                primaryButton: .default(Text("Allow")) {
                    share.admit(prompt.ip)
                },
                secondaryButton: .destructive(Text("Deny")) {
                    share.deny(prompt.ip)
                }
            )
        }
    }

    private func updateIdleTimer() {
        UIApplication.shared.isIdleTimerDisabled = share.isSharing
    }

    private func scheduleDim() {
        dimTask?.cancel()
        guard share.isSharing else { return }
        dimTask = Task {
            try? await Task.sleep(for: .seconds(30))
            guard !Task.isCancelled, share.isSharing else { return }
            dimScreen()
        }
    }

    private func dimScreen() {
        guard !dimmed else { return }
        if let screen = activeScreen() {
            if savedBrightness == nil { savedBrightness = screen.brightness }
            screen.brightness = 0.05
        }
        dimmed = true
    }

    private func restoreBrightness() {
        dimmed = false
        if let prev = savedBrightness, let screen = activeScreen() {
            screen.brightness = prev
        }
        savedBrightness = nil
    }

    private func activeScreen() -> UIScreen? {
        UIApplication.shared.connectedScenes
            .compactMap { $0 as? UIWindowScene }
            .first?.screen
    }
}
