import SwiftUI

/// Settings, as Rex's iPhone app lays them out: cards of rows that open a
/// page each, the client's label, and a search over the rows' titles.
struct SettingsView: View {
    @Environment(HostStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    @AppStorage("scheme") private var scheme = "dark"
    @AppStorage("theme") private var theme = TerminalTheme.standard
    @AppStorage("clientLabel") private var label = ""
    @AppStorage(Grouping.key) private var grouping = Grouping.standard.rawValue
    @State private var query = ""

    private struct Item: Identifiable {
        let title: LocalizedStringResource
        let icon: AnyView
        var value: String?
        let page: AnyView
        var id: String { title.key }
    }

    private static let schemes: [(String, LocalizedStringResource)] = [("system", "System"), ("dark", "Dark"), ("light", "Light")]
    private static let groupings: [(String, LocalizedStringResource)] = [
        (Grouping.spaces.rawValue, "Desktop Spaces"), (Grouping.hosts.rawValue, "By Host"),
    ]

    private var cards: [[Item]] {
        let version = Bundle.main.version
        let build = Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? ""
        return [
            [Item(title: "Interface", icon: tile("circle.lefthalf.filled", .indigo),
                  value: String(localized: Self.schemes.first { $0.0 == scheme }?.1 ?? "Dark"),
                  page: AnyView(Choices(title: "Interface", options: Self.schemes.map { ($0.0, String(localized: $0.1)) }, selection: $scheme))),
             Item(title: "Themes", icon: tile("paintpalette.fill", .pink), value: theme.isEmpty ? String(localized: "Host Colors") : theme,
                  page: AnyView(Themes(selection: $theme)))],
            [Item(title: "Terminal", icon: tile("apple.terminal.fill", .gray), page: AnyView(TerminalSettings())),
             Item(title: "Remote Hosts", icon: tile("globe", .orange), page: AnyView(RemoteHostsView())),
             Item(title: "Grouping", icon: tile("rectangle.3.group.fill", .teal),
                  value: String(localized: Self.groupings.first { $0.0 == Grouping(stored: grouping).rawValue }?.1 ?? "Desktop Spaces"),
                  page: AnyView(Choices(title: "Grouping", options: Self.groupings.map { ($0.0, String(localized: $0.1)) }, selection: $grouping,
                                        footer: "Desktop Spaces lists a desktop's windows on your other hosts in its spaces, as the desktop groups them. Only hosts you added here and that are online show.")))],
            [Item(title: "Features", icon: tile("sparkles", .blue), page: AnyView(FeaturesView())),
             Item(title: "About", icon: tile("info.circle.fill", .gray),
                  value: "\(version) (\(build))", page: AnyView(AboutView(version: version, build: build)))],
        ]
    }

    var body: some View {
        NavigationStack {
            List {
                ForEach(Array(cards.map { $0.filter { matches($0.title) } }.filter { !$0.isEmpty }.enumerated()), id: \.offset) { _, items in
                    Section {
                        ForEach(items) { item in
                            NavigationLink { item.page } label: {
                                SettingsRow(icon: item.icon, title: item.title) {
                                    if let v = item.value { Text(v).foregroundStyle(.secondary).lineLimit(1) }
                                }
                            }
                            .listRowBackground(Color.card)
                        }
                    }
                }
                if matches("Label") {
                    Section("Client Info") {
                        SettingsRow(icon: nil, title: "Label") {
                            TextField(ControlConnection.label, text: $label)
                                .multilineTextAlignment(.trailing)
                                .foregroundStyle(.secondary)
                                .autocorrectionDisabled()
                                .submitLabel(.done)
                                .onSubmit(sendLabel)
                        }
                        .listRowBackground(Color.card)
                    }
                }
            }
            .scrollContentBackground(.hidden)
            .background(Color.page)
            .searchable(text: $query, prompt: "Search Settings")
            .textInputAutocapitalization(.never)
            .autocorrectionDisabled()
            .navigationTitle("Settings")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", systemImage: "xmark", role: .close) { dismiss() }
                }
                DefaultToolbarItem(kind: .search, placement: .bottomBar)
            }
        }
        // A sheet keeps its own scheme: change it here too.
        .preferredColorScheme(GoRexApp.colorScheme(scheme))
    }

    private func matches(_ title: LocalizedStringResource) -> Bool {
        query.isEmpty || String(localized: title).localizedCaseInsensitiveContains(query)
    }

    /// Tells the connected servers the new label, which later hellos carry.
    private func sendLabel() {
        let l = ControlConnection.label
        for h in store.hosts {
            Task { _ = try? await h.control?.call("client.set_label", ["label": l]) }
        }
    }

    /// A row's icon as the Settings app draws one: a white symbol on a
    /// tile of a system color.
    private func tile(_ symbol: String, _ color: Color) -> AnyView {
        AnyView(Image(systemName: symbol).font(.system(size: 17, weight: .medium)).foregroundStyle(.white)
            .frame(width: 30, height: 30)
            .background(color, in: RoundedRectangle(cornerRadius: 7, style: .continuous)))
    }
}

/// A settings row: an icon, a title, and what goes on the right.
private struct SettingsRow<Trailing: View>: View {
    let icon: AnyView?
    let title: LocalizedStringResource
    @ViewBuilder let trailing: Trailing

    var body: some View {
        HStack(spacing: 14) {
            if let icon { icon.accessibilityHidden(true) }
            Text(title).alignmentGuide(.listRowSeparatorLeading) { $0[.leading] }
            Spacer(minLength: 12)
            trailing
        }
    }
}

/// A list to pick one of options in.
private struct Choices: View {
    let title: LocalizedStringResource
    let options: [(id: String, name: String)]
    @Binding var selection: String
    var footer: LocalizedStringResource?

    var body: some View {
        List {
            Section {
                ForEach(options, id: \.id) { o in
                    Button { selection = o.id } label: {
                        HStack {
                            Text(o.name).foregroundStyle(.primary)
                            Spacer()
                            if o.id == selection { Image(systemName: "checkmark").fontWeight(.semibold) }
                        }
                    }
                    .listRowBackground(Color.card)
                }
            } footer: {
                if let footer { Text(footer) }
            }
        }
        .scrollContentBackground(.hidden)
        .background(Color.page)
        .navigationTitle(Text(title))
        .navigationBarTitleDisplayMode(.inline)
    }
}

/// The terminal's themes, after rootshell's ThemeSettingsView: a preview
/// of the one chosen, a filter by its background, a search, and a swatch of
/// each one's colors. "" keeps the host's colors.
private struct Themes: View {
    @Binding var selection: String
    @State private var query = ""
    /// Light themes, dark ones, or all of them.
    @State private var light: Bool?

    var body: some View {
        let shown = TerminalTheme.all.filter {
            (light == nil || $0.light == light) && (query.isEmpty || $0.name.localizedCaseInsensitiveContains(query))
        }
        List {
            if light == nil, query.isEmpty {
                row(nil)
            }
            ForEach(shown, id: \.name) { row($0) }
        }
        .scrollContentBackground(.hidden)
        .background(Color.page)
        .safeAreaInset(edge: .top) {
            VStack(spacing: 12) {
                if let t = TerminalTheme.all.first(where: { $0.name == selection }) { Preview(theme: t) }
                Picker("Filter", selection: $light) {
                    Text("All").tag(Bool?.none)
                    Text("Dark").tag(Bool?.some(false))
                    Text("Light").tag(Bool?.some(true))
                }
                .pickerStyle(.segmented)
            }
            .padding(.horizontal, 20)
            .padding(.bottom, 8)
            .background(Color.page)
        }
        .searchable(text: $query)
        .navigationTitle("Themes")
        .navigationBarTitleDisplayMode(.inline)
    }

    private func row(_ t: TerminalTheme?) -> some View {
        let id = t?.name ?? ""
        return Button { selection = id } label: {
            HStack(spacing: 12) {
                Swatch(theme: t)
                Text(t?.name ?? String(localized: "Host Colors")).foregroundStyle(.primary)
                Spacer()
                if id == selection { Image(systemName: "checkmark").fontWeight(.semibold) }
            }
        }
        .listRowBackground(Color.card)
    }
}

/// A theme's background with a few of its colors on it; the host's colors,
/// unknown here, as a computer.
private struct Swatch: View {
    let theme: TerminalTheme?

    var body: some View {
        HStack(spacing: 3) {
            if let theme {
                ForEach([1, 2, 3, 4, 5], id: \.self) { Circle().fill(Color(rgb: UInt32(theme.colors[$0]))).frame(width: 7, height: 7) }
            } else {
                Image(systemName: "desktopcomputer").font(.caption).foregroundStyle(.secondary)
            }
        }
        .frame(width: 56, height: 22)
        .background(theme.map { Color(rgb: UInt32($0.colors[16])) } ?? Color.page, in: RoundedRectangle(cornerRadius: 5, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 5, style: .continuous).strokeBorder(.secondary.opacity(0.3), lineWidth: 0.5))
    }
}

/// A few lines of a shell in a theme's colors and the terminal's font,
/// after rootshell's TerminalSnippetView.
private struct Preview: View {
    let theme: TerminalTheme
    @AppStorage("fontFamily") private var family = ""

    var body: some View {
        // Palette 4 is blue, 2 green, 1 red; 17 the foreground, 18 the cursor.
        VStack(alignment: .leading, spacing: 2) {
            line(("~/gorex ", 4), ("ls", 17))
            line(("docs  src  ", 4), ("build.sh  ", 2), ("README.md", 17))
            line(("~/gorex ", 4), ("git status -s", 17))
            line((" M ", 1), ("main.go", 17))
            line(("?? ", 1), ("notes.txt", 17))
            line(("~/gorex ", 4), ("█", 18))
        }
        .font(Fonts.face(family, size: 13))
        .lineLimit(1)
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(12)
        .background(color(16), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
    }

    private func color(_ i: Int) -> Color { Color(rgb: UInt32(theme.colors[i])) }

    /// A line of runs, each in a theme color; shell text, not localized.
    private func line(_ runs: (String, Int)...) -> Text {
        Text(runs.reduce(into: AttributedString()) { $0 += AttributedString($1.0, attributes: AttributeContainer().foregroundColor(color($1.1))) })
    }
}

/// The terminal's fonts: Ghostty's own, then the monospaced ones iOS has,
/// by the family names Ghostty finds them by; Maple Mono once downloaded.
private enum Fonts {
    static let all: [(family: String, name: String)] = [("", "JetBrains Mono"), ("Menlo", "Menlo"), ("Courier New", "Courier New")]

    /// A family's face, for a preview. Ghostty's own is not iOS's: the
    /// system's monospaced face stands in for it.
    static func face(_ family: String, size: CGFloat) -> Font {
        family.isEmpty ? .system(size: size, design: .monospaced) : .custom(family, size: size)
    }
}

/// The terminal's options: its font, font size and minimum contrast.
private struct TerminalSettings: View {
    @AppStorage("fontSize") private var fontSize = 12.0
    @AppStorage("fontFamily") private var family = ""
    @AppStorage("minimumContrast") private var contrast = 0.0

    /// The minimum contrasts offered; 0 is the default, 3.
    private static let contrasts: [(Double, LocalizedStringResource)] = [
        (1, "Off"), (2, "2"), (0, "3 (Default)"), (4.5, "4.5 (WCAG AA)"), (7, "7 (WCAG AAA)"),
    ]
    private let maple = Maple.shared

    var body: some View {
        List {
            Stepper(value: $fontSize, in: 8...24) {
                LabeledContent("Font Size", value: String(localized: "\(Int(fontSize)) pt"))
            }
            .listRowBackground(Color.card)
            Section("Font") {
                ForEach(Fonts.all, id: \.family) { row($0.family, $0.name) }
                if maple.installed {
                    row(Maple.family, Maple.family)
                        .swipeActions { Button("Delete", role: .destructive) { maple.delete() } }
                } else {
                    download.swipeActions { if !maple.saved.isEmpty { Button("Delete", role: .destructive) { maple.delete() } } }
                }
            }
            Section {
                ForEach(Self.contrasts, id: \.0) { value, name in
                    Button { contrast = value } label: {
                        HStack {
                            Text(name).foregroundStyle(.primary)
                            Spacer()
                            if value == contrast || (contrast == 3 && value == 0) { Image(systemName: "checkmark").fontWeight(.semibold) }
                        }
                    }
                    .listRowBackground(Color.card)
                }
            } header: {
                Text("Minimum Contrast")
            } footer: {
                Text("Lightens or darkens text too close to its background, colors programs set too. The hue stays, so errors and warnings keep apart; box drawing keeps its colors. Off shows programs' colors as they are.")
            }
        }
        .scrollContentBackground(.hidden)
        .background(Color.page)
        .navigationTitle("Terminal")
        .navigationBarTitleDisplayMode(.inline)
    }

    private func row(_ f: String, _ name: String) -> some View {
        Button { family = f } label: {
            HStack {
                Text(name).font(Fonts.face(f, size: 17)).foregroundStyle(.primary)
                Spacer()
                if f == family { Image(systemName: "checkmark").fontWeight(.semibold) }
            }
        }
        .listRowBackground(Color.card)
    }

    /// Maple Mono, not downloaded: what's left to fetch and a download, or
    /// the download's progress and a stop.
    private var download: some View {
        Button { maple.progress == nil ? maple.download() : maple.cancel() } label: {
            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    Text(verbatim: Maple.family).foregroundStyle(.primary)
                    Spacer()
                    if let p = maple.progress {
                        ProgressView(value: p).frame(width: 64)
                        Image(systemName: "stop.circle").accessibilityLabel("Cancel")
                    } else {
                        Text(maple.remaining.formatted(.byteCount(style: .file))).foregroundStyle(.secondary)
                        Image(systemName: "arrow.down.circle").accessibilityLabel("Download")
                    }
                }
                if let e = maple.error { Text(e).font(.footnote).foregroundStyle(.red) }
            }
        }
        .listRowBackground(Color.card)
    }
}

/// About, as Rex's iPhone app lays it out: the app and its version, the
/// hosts' Rex versions, what to copy into a problem report, and the open
/// source the app ships.
private struct AboutView: View {
    let version: String
    let build: String
    @Environment(HostStore.self) private var store
    /// Each host's Rex version, as server.version tells it.
    @State private var versions: [UUID: String] = [:]
    /// The row last copied, by its icon, for it to say so a moment.
    @State private var copied: String?

    private let name = Bundle.main.object(forInfoDictionaryKey: "CFBundleDisplayName") as? String ?? "Rex Go"
    /// The short git hash the upload builds from; empty for a local build.
    private let commit = Bundle.main.object(forInfoDictionaryKey: "GoRexCommit") as? String ?? ""
    private let system = UIDevice.current.systemName + " " + UIDevice.current.systemVersion

    /// The hardware model, as iPhone18,2; the simulator names the one it
    /// simulates.
    private let device: String = ProcessInfo.processInfo.environment["SIMULATOR_MODEL_IDENTIFIER"] ?? {
        var u = utsname()
        uname(&u)
        return withUnsafeBytes(of: u.machine) { String(decoding: $0.prefix { $0 != 0 }, as: UTF8.self) }
    }()

    /// The open source the app ships, by its section in the notices.
    private static let components = [("Ghostty", "MIT"), ("JetBrains Mono", "SIL OFL 1.1"), ("rootshell", "MIT"),
                                     ("iTerm2-Color-Schemes", "MIT"), ("Simple Icons", "CC0 1.0")]

    var body: some View {
        List {
            Section {
                VStack(spacing: 6) {
                    Image("AboutIcon").resizable().frame(width: 110, height: 110)
                        .clipShape(.rect(cornerRadius: 25, style: .continuous))
                        .padding(.bottom, 8)
                    Text(name).font(.largeTitle.bold())
                    Text("Version \(version) (\(build))").foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity)
                .listRowBackground(Color.clear)
            }
            Section("Version Info") {
                LabeledContent("Version", value: "\(version) (\(build))")
                if !commit.isEmpty {
                    LabeledContent("Commit") { Text(commit).monospaced() }
                }
                LabeledContent("Device", value: device)
                // "System" is the interface's System elsewhere.
                LabeledContent { Text(system) } label: { Text(LocalizedStringResource("About System", defaultValue: "System")) }
            }
            .listRowBackground(Color.card)
            if !store.hosts.isEmpty {
                Section("Hosts") {
                    ForEach(store.hosts) { h in
                        LabeledContent(h.config.name, value: rex(h) ?? String(localized: "not connected"))
                    }
                }
                .listRowBackground(Color.card)
            }
            Section {
                copy("Copy Version Info", icon: "info.circle", versionInfo)
                copy("Copy Debug Info", icon: "doc.on.doc", debugInfo)
            } footer: {
                Text("Include version info when you report a problem. Debug info also describes the network and the hosts.")
            }
            .listRowBackground(Color.card)
            Section {
                ForEach(Self.components, id: \.0) { name, license in
                    NavigationLink { Notices(section: name) } label: { LabeledContent(name, value: license) }
                }
                Link(destination: URL(string: "https://github.com/si-mods/gorex")!) {
                    LabeledContent("GitHub", value: "si-mods/gorex")
                }
            } header: {
                Text("Open Source")
            } footer: {
                Text("Code, fonts and themes that ship with Rex Go.")
            }
            .listRowBackground(Color.card)
        }
        .scrollContentBackground(.hidden)
        .background(Color.page)
        .navigationTitle("About")
        .navigationBarTitleDisplayMode(.inline)
        .task(id: store.hosts.map(\.status)) {
            for h in store.hosts where h.status == .online {
                if let v = try? await h.control?.call("server.version").payload["rex_version"] as? String { versions[h.id] = v }
            }
        }
        .task(id: copied) {
            try? await Task.sleep(for: .seconds(1.5))
            copied = nil
        }
        .sensoryFeedback(.success, trigger: copied) { _, new in new != nil }
    }

    /// A host's Rex version while it is online.
    private func rex(_ h: Host) -> String? {
        h.status == .online ? versions[h.id].map { "Rex \($0)" } : nil
    }

    private func copy(_ title: LocalizedStringKey, icon: String, _ text: String) -> some View {
        Button {
            UIPasteboard.general.string = text
            copied = icon
        } label: {
            LabeledContent {
                if copied == icon { Text("Copied") }
            } label: {
                Label(title, systemImage: icon)
            }
        }
    }

    // What the rows copy is for whoever reads the report: not localized.

    private var versionInfo: String {
        ["\(name) \(version) (\(build))", commit.isEmpty ? nil : "Commit \(commit)", "Device \(device)", "System \(system)"]
            .compactMap { $0 }.joined(separator: "\n")
            + store.hosts.map { "\n\($0.config.name): \(rex($0) ?? "not connected")" }.joined()
    }

    private var debugInfo: String {
        versionInfo + store.hosts.map { h in
            let status = switch h.status {
            case .online: "online"
            case .connecting: "connecting"
            case .offline(let why): "offline (\(why))"
            }
            return "\n\n\(h.config.name)\nURL \(h.config.url.absoluteString)\nStatus \(status)\nSessions \(h.sessions.count)"
        }.joined()
    }
}

/// The third-party notices, scrolled to a component's section.
private struct Notices: View {
    let section: String
    private let parts = (Bundle.main.url(forResource: "THIRD_PARTY_NOTICES", withExtension: "md")
        .flatMap { try? String(contentsOf: $0, encoding: .utf8) } ?? "").split(separator: /\n(?=## )/).map(String.init)

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(parts.indices, id: \.self) { Text(parts[$0]).id($0) }
                }
                .font(.caption.monospaced()).textSelection(.enabled).padding(20)
            }
            .onAppear {
                if let i = parts.firstIndex(where: { $0.hasPrefix("## " + section) }) { proxy.scrollTo(i, anchor: .top) }
            }
        }
        .background(Color.page)
        .navigationTitle("Third-Party Notices")
        .navigationBarTitleDisplayMode(.inline)
    }
}
