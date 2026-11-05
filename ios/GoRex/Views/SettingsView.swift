import SwiftUI

/// The hosts: add one by its address, swipe one away to remove it.
struct SettingsView: View {
    @Environment(HostStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    @State private var adding = false

    var body: some View {
        NavigationStack {
            List {
                Section("Hosts") {
                    ForEach(store.hosts) { h in
                        VStack(alignment: .leading) {
                            Text(h.config.name)
                            Text(h.config.url.absoluteString).font(.footnote).foregroundStyle(.secondary)
                        }
                    }
                    .onDelete { $0.map { store.hosts[$0].id }.forEach(store.remove) }
                    Button("Add Host", systemImage: "plus") { adding = true }
                }
            }
            .navigationTitle("Settings")
            .toolbar { Button("Done") { dismiss() } }
            .sheet(isPresented: $adding) { AddHostView() }
        }
    }
}

private struct AddHostView: View {
    @Environment(HostStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var address = ""
    @State private var desktop = false

    /// The address as an http(s) URL with a host; a bare host gets http://.
    private var url: URL? {
        let s = address.trimmingCharacters(in: .whitespaces)
        guard let u = URL(string: s.contains("://") ? s : "http://" + s),
              ["http", "https"].contains(u.scheme), u.host() != nil else { return nil }
        return u
    }

    var body: some View {
        NavigationStack {
            Form {
                TextField("Name", text: $name)
                    .autocorrectionDisabled()
                TextField("http://192.168.1.2:7297", text: $address)
                    .keyboardType(.URL)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .accessibilityIdentifier("address")
                Picker("Device", selection: $desktop) {
                    Text("Laptop").tag(false)
                    Text("Desktop").tag(true)
                }
            }
            .navigationTitle("Add Host")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Add") {
                        guard let url else { return }
                        store.add(HostConfig(name: name.isEmpty ? url.host() ?? "" : name, url: url, desktop: desktop))
                        dismiss()
                    }
                    .disabled(url == nil)
                }
            }
        }
    }
}
