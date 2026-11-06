import SwiftUI

/// Settings, as Rex's iPhone app lays them out: cards of rows that open a
/// page each, the client's label, and a search over the rows' titles.
struct SettingsView: View {
    @Environment(HostStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    @AppStorage("scheme") private var scheme = "dark"
    @AppStorage("theme") private var theme = ""
    @AppStorage("clientLabel") private var label = ""
    @State private var query = ""

    private struct Item: Identifiable {
        let title: LocalizedStringResource
        let icon: AnyView
        var value: String?
        let page: AnyView
        var id: String { title.key }
    }

    private static let schemes: [(String, LocalizedStringResource)] = [("system", "System"), ("dark", "Dark"), ("light", "Light")]

    private var cards: [[Item]] {
        let version = Bundle.main.version
        let build = Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? ""
        return [
            [Item(title: "Interface", icon: tile("circle.lefthalf.filled", .indigo),
                  value: String(localized: Self.schemes.first { $0.0 == scheme }?.1 ?? "Dark"),
                  page: AnyView(Choices(title: "Interface", options: Self.schemes.map { ($0.0, String(localized: $0.1)) }, selection: $scheme))),
             Item(title: "Themes", icon: tile("paintpalette.fill", .pink), value: theme.isEmpty ? String(localized: "Default") : theme,
                  page: AnyView(Choices(title: "Themes", options: [("", String(localized: "Default"))] + TerminalTheme.all.map { ($0.name, $0.name) },
                                        selection: $theme)))],
            [Item(title: "Terminal", icon: tile("apple.terminal.fill", .gray), page: AnyView(TerminalSettings())),
             Item(title: "Remote Hosts", icon: tile("globe", .orange), page: AnyView(RemoteHostsView()))],
            [Item(title: "About", icon: tile("info.circle.fill", .gray),
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

/// A list to pick one of options in, with a search when they are many.
private struct Choices: View {
    let title: LocalizedStringResource
    let options: [(id: String, name: String)]
    @Binding var selection: String
    @State private var query = ""

    var body: some View {
        let shown = options.filter { query.isEmpty || $0.name.localizedCaseInsensitiveContains(query) }
        let list = List(shown, id: \.id) { o in
            Button { selection = o.id } label: {
                HStack {
                    Text(o.name).foregroundStyle(.primary)
                    Spacer()
                    if o.id == selection { Image(systemName: "checkmark").fontWeight(.semibold) }
                }
            }
            .listRowBackground(Color.card)
        }
        .scrollContentBackground(.hidden)
        .background(Color.page)
        .navigationTitle(Text(title))
        .navigationBarTitleDisplayMode(.inline)
        if options.count > 10 {
            list.searchable(text: $query)
        } else {
            list
        }
    }
}

/// The terminal's options: its font size.
private struct TerminalSettings: View {
    @AppStorage("fontSize") private var fontSize = 12.0

    var body: some View {
        List {
            Stepper(value: $fontSize, in: 8...24) {
                LabeledContent("Font Size", value: String(localized: "\(Int(fontSize)) pt"))
            }
            .listRowBackground(Color.card)
        }
        .scrollContentBackground(.hidden)
        .background(Color.page)
        .navigationTitle("Terminal")
        .navigationBarTitleDisplayMode(.inline)
    }
}

private struct AboutView: View {
    let version: String
    let build: String

    var body: some View {
        List {
            Section {
                LabeledContent("Version", value: version)
                LabeledContent("Build", value: build)
                Link(destination: URL(string: "https://github.com/si-mods/gorex")!) {
                    LabeledContent("GitHub", value: "si-mods/gorex")
                }
                NavigationLink("Third-Party Notices") { Notices() }
            }
            .listRowBackground(Color.card)
        }
        .scrollContentBackground(.hidden)
        .background(Color.page)
        .navigationTitle("About")
        .navigationBarTitleDisplayMode(.inline)
    }
}

private struct Notices: View {
    private let text = Bundle.main.url(forResource: "THIRD_PARTY_NOTICES", withExtension: "md")
        .flatMap { try? String(contentsOf: $0, encoding: .utf8) } ?? ""

    var body: some View {
        ScrollView {
            Text(text).font(.caption.monospaced()).textSelection(.enabled).padding(20)
        }
        .background(Color.page)
        .navigationTitle("Third-Party Notices")
        .navigationBarTitleDisplayMode(.inline)
    }
}
