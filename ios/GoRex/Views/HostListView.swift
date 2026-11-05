import SwiftUI

extension Color {
    static let page = Color(red: 0x22 / 255, green: 0x25 / 255, blue: 0x2F / 255)
    static let card = Color(red: 0x36 / 255, green: 0x3A / 255, blue: 0x47 / 255)
}

/// Every host, its sessions, and their windows, as Rex's iPhone app lists
/// them. The eye hides idle windows: those whose program is only the shell.
struct HostListView: View {
    @Environment(HostStore.self) private var store
    @State private var path: [TerminalTarget] = []
    @State private var collapsed: Set<String> = []
    @State private var hideIdle = false
    @State private var searching = false
    @State private var query = ""
    @State private var settings = false
    @FocusState private var searchFocused: Bool

    var body: some View {
        NavigationStack(path: $path) {
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 30) {
                    if store.hosts.isEmpty {
                        Text("No hosts yet. Add one in Settings.").foregroundStyle(.secondary).padding(.top, 40)
                    }
                    ForEach(store.hosts) { host in
                        HostSection(host: host, collapsed: $collapsed, filter: filter, open: { path.append($0) })
                    }
                }
                .padding(.horizontal, 20)
                .padding(.top, 12)
                .padding(.bottom, 100)
            }
            .background(Color.page)
            .overlay(alignment: .bottom) { controls }
            .toolbar(.hidden, for: .navigationBar)
            .navigationDestination(for: TerminalTarget.self) { t in
                if let host = store.host(t.host) { TerminalScreen(target: t, host: host) }
            }
            .sheet(isPresented: $settings) { SettingsView() }
        }
        .preferredColorScheme(.dark)
    }

    /// Whether a window's row shows, by the search and the eye.
    private func filter(_ title: String, _ program: String?) -> Bool {
        if hideIdle, program.map(AppIcon.isShell) ?? true { return false }
        return query.isEmpty || title.localizedCaseInsensitiveContains(query)
    }

    private var controls: some View {
        HStack {
            if searching {
                HStack {
                    Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
                    TextField("Search windows", text: $query)
                        .focused($searchFocused)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    Button { query = ""; searching = false } label: { Image(systemName: "xmark.circle.fill") }
                        .foregroundStyle(.secondary)
                        .accessibilityLabel("Close Search")
                }
                .padding(.horizontal, 16)
                .frame(height: 50)
                .glass(in: Capsule())
            } else {
                HStack(spacing: 4) {
                    Button { settings = true } label: { Image(systemName: "gearshape").frame(width: 44, height: 44) }
                        .accessibilityLabel("Settings")
                    Button { hideIdle.toggle() } label: {
                        Image(systemName: hideIdle ? "eye.slash" : "eye").frame(width: 44, height: 44)
                    }
                    .accessibilityLabel(hideIdle ? "Show Idle Windows" : "Hide Idle Windows")
                }
                .padding(.horizontal, 6)
                .glass(in: Capsule())
                Spacer()
                Button { searching = true; searchFocused = true } label: {
                    Image(systemName: "magnifyingglass").frame(width: 50, height: 50)
                }
                .accessibilityLabel("Search")
                .glass(in: Circle())
            }
        }
        .font(.title3)
        .foregroundStyle(.white)
        .padding(.horizontal, 20)
        .padding(.bottom, 8)
    }
}

private struct HostSection: View {
    let host: Host
    @Binding var collapsed: Set<String>
    let filter: (String, String?) -> Bool
    let open: (TerminalTarget) -> Void
    @Environment(HostStore.self) private var store

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            HStack(spacing: 10) {
                Image(systemName: host.config.desktop ? "desktopcomputer" : "laptopcomputer")
                    .font(.title2)
                Text(host.config.name).font(.system(size: 30, weight: .bold)).lineLimit(1)
                Spacer()
                Menu {
                    Button("Reconnect", systemImage: "arrow.clockwise") { host.reconnectIfNeeded() }
                    Button("Remove", systemImage: "trash", role: .destructive) { store.remove(host.id) }
                } label: {
                    Image(systemName: "ellipsis").font(.title3).frame(width: 36, height: 36)
                }
                .accessibilityLabel("Host Menu")
            }
            .foregroundStyle(.white)
            switch host.status {
            case .connecting where host.sessions.isEmpty:
                Text("Connecting…").foregroundStyle(.secondary)
            case .offline(let why):
                Label(why.isEmpty ? String(localized: "Offline") : String(localized: "Offline: \(why)"), systemImage: "wifi.slash")
                    .foregroundStyle(.secondary)
            default:
                if host.sessions.isEmpty { Text("No sessions").foregroundStyle(.secondary) }
                ForEach(host.sessions) { s in session(s) }
            }
        }
    }

    @ViewBuilder private func session(_ s: Host.Session) -> some View {
        let windows = (s.view?.windows ?? []).compactMap { w -> (String, String, String?)? in
            guard let b = w.block else { return nil }
            let title = host.titles[b].flatMap { $0.isEmpty ? nil : $0 } ?? w.label ?? ""
            return filter(title, host.programs[b]) ? (b, title, host.programs[b]) : nil
        }
        let isCollapsed = collapsed.contains(s.id)
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text(s.label).font(.title3.weight(.semibold)).foregroundStyle(.gray)
                Spacer()
                Button {
                    Task {
                        if let b = try? await host.newWindow(in: s.id) {
                            open(TerminalTarget(host: host.id, session: s.id, block: b, title: s.label))
                        }
                    }
                } label: { Image(systemName: "plus.circle").frame(width: 32, height: 32) }
                .accessibilityLabel("New Window")
                Button {
                    withAnimation(.snappy) { if isCollapsed { collapsed.remove(s.id) } else { collapsed.insert(s.id) } }
                } label: {
                    Image(systemName: "chevron.down").rotationEffect(.degrees(isCollapsed ? -90 : 0)).frame(width: 32, height: 32)
                }
                .accessibilityLabel(isCollapsed ? "Expand" : "Collapse")
            }
            .font(.title3)
            .foregroundStyle(.gray)
            if !isCollapsed && !windows.isEmpty {
                VStack(spacing: 0) {
                    ForEach(Array(windows.enumerated()), id: \.element.0) { i, w in
                        Button {
                            open(TerminalTarget(host: host.id, session: s.id, block: w.0, title: w.1))
                        } label: {
                            WindowRow(title: w.1, program: w.2, divider: i < windows.count - 1)
                        }
                        .buttonStyle(.plain)
                    }
                }
                .background(Color.card, in: RoundedRectangle(cornerRadius: 28, style: .continuous))
            }
        }
    }
}

private struct WindowRow: View {
    let title: String
    let program: String?
    let divider: Bool

    var body: some View {
        HStack(spacing: 14) {
            AppIcon(program: program)
            Text(title.isEmpty ? " " : title)
                .font(.body.weight(.medium))
                .foregroundStyle(.white)
                .lineLimit(1)
                .truncationMode(.tail)
            Spacer(minLength: 8)
            Image(systemName: "chevron.right").font(.footnote.weight(.semibold)).foregroundStyle(.gray)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
        .contentShape(Rectangle())
        .overlay(alignment: .bottom) {
            if divider {
                Rectangle().fill(.white.opacity(0.12)).frame(height: 1 / 3).padding(.leading, 16 + 44 + 14)
            }
        }
    }
}

/// The icon of a window's foreground program.
struct AppIcon: View {
    let program: String?

    static func isShell(_ p: String) -> Bool {
        ["zsh", "bash", "fish", "sh", "dash", "ksh", "tcsh", "nu", "pwsh", "login"].contains(p.trimmingCharacters(in: ["-"]))
    }

    var body: some View {
        let p = program?.lowercased() ?? ""
        let shape = RoundedRectangle(cornerRadius: 11, style: .continuous)
        Group {
            if p == "pi" {
                Text("π").font(.system(size: 26, weight: .bold, design: .serif)).foregroundStyle(.white)
                    .frame(width: 44, height: 44)
                    .background(LinearGradient(colors: [Color(red: 0.55, green: 0.36, blue: 0.96), Color(red: 0.93, green: 0.36, blue: 0.62)],
                                               startPoint: .topLeading, endPoint: .bottomTrailing), in: shape)
            } else if p == "claude" {
                Image(systemName: "asterisk").font(.system(size: 22, weight: .bold)).foregroundStyle(.white)
                    .frame(width: 44, height: 44)
                    .background(Color(red: 0.85, green: 0.47, blue: 0.34), in: shape)
            } else if p.isEmpty || Self.isShell(p) {
                Text(">_").font(.system(size: 17, weight: .bold, design: .monospaced)).foregroundStyle(.green)
                    .frame(width: 44, height: 44)
                    .background(Color(white: 0.13), in: shape)
            } else {
                Text(p.prefix(1).uppercased()).font(.system(size: 20, weight: .semibold, design: .rounded)).foregroundStyle(.white)
                    .frame(width: 44, height: 44)
                    .background(Color(white: 0.22), in: shape)
            }
        }
        .accessibilityHidden(true)
    }
}

extension View {
    /// Liquid Glass where the system has it, else a material.
    @ViewBuilder func glass(in shape: some Shape) -> some View {
        if #available(iOS 26, *) {
            glassEffect(.regular.interactive(), in: shape)
        } else {
            background(.ultraThinMaterial, in: shape)
        }
    }
}
