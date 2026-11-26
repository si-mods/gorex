import SwiftUI
import UniformTypeIdentifiers

/// Local SSH key management in Settings (docs/IOS-026-ssh-keys.md), modeled
/// after rootshell's SSHKeyManagementView with generate, import, copy,
/// default toggles, and delete.
struct SSHKeysView: View {
    @State private var store = SSHKeyStore.shared
    @State private var sheet: Sheet?
    @State private var keyToDelete: SSHKeyRecord?
    @State private var errorMessage: String?

    private enum Sheet: Identifiable {
        case generate, importKey
        var id: String {
            switch self {
            case .generate: "generate"
            case .importKey: "import"
            }
        }
    }

    private var deleteAlertPresented: Binding<Bool> {
        Binding(get: { keyToDelete != nil }, set: { if !$0 { keyToDelete = nil } })
    }

    private var errorAlertPresented: Binding<Bool> {
        Binding(get: { errorMessage != nil }, set: { if !$0 { errorMessage = nil } })
    }

    var body: some View {
        keysList
            .scrollContentBackground(.hidden)
            .background(Color.page)
            .navigationTitle("SSH Keys")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .primaryAction) { addMenu }
            }
            .sheet(item: $sheet) { s in
                switch s {
                case .generate:
                    SSHKeyGenerateSheet()
                case .importKey:
                    SSHKeyImportSheet()
                }
            }
            .alert("Delete SSH Key?", isPresented: deleteAlertPresented, presenting: keyToDelete) { key in
                Button("Cancel", role: .cancel) { keyToDelete = nil }
                Button("Delete", role: .destructive) {
                    do {
                        try store.delete(id: key.id)
                    } catch {
                        errorMessage = error.localizedDescription
                    }
                    keyToDelete = nil
                }
            } message: { key in
                Text("Remove \"\(key.name)\" from this device's Keychain? This cannot be undone.")
            }
            .alert("Error", isPresented: errorAlertPresented) {
                Button("OK", role: .cancel) { errorMessage = nil }
            } message: {
                if let errorMessage { Text(errorMessage) }
            }
    }

    private var keysList: some View {
        List {
            if store.keys.isEmpty {
                Section {
                    EmptyState("No SSH keys", symbol: "key.fill")
                        .listRowBackground(Color.card)
                } footer: {
                    Text("Generate an Ed25519 key or import an existing private key to connect to SSH hosts without a password.")
                }
            } else {
                Section {
                    ForEach(store.keys) { key in
                        NavigationLink {
                            SSHKeyDetailView(keyID: key.id)
                        } label: {
                            SSHKeyRow(key: key)
                        }
                        .listRowBackground(Color.card)
                    }
                    .onDelete { offsets in
                        if let first = offsets.first {
                            keyToDelete = store.keys[first]
                        }
                    }
                } header: {
                    Text("Keys")
                } footer: {
                    Text("Private keys stay in this device's Keychain and are never synced to iCloud. Default keys are offered in order when an SSH host is set to Automatic.")
                }
            }
        }
    }

    private var addMenu: some View {
        Menu {
            Button {
                sheet = .generate
            } label: {
                Label("Generate New Key", systemImage: "wand.and.stars")
            }
            Button {
                sheet = .importKey
            } label: {
                Label("Import Key", systemImage: "square.and.arrow.down")
            }
        } label: {
            Image(systemName: "plus")
        }
        .accessibilityIdentifier("addSSHKey")
    }
}

private struct SSHKeyRow: View {
    let key: SSHKeyRecord

    var body: some View {
        HStack(spacing: 12) {
            Text(key.badge)
                .font(.caption2.weight(.semibold).monospaced())
                .foregroundStyle(.secondary)
                .frame(width: 58, alignment: .center)
                .padding(.vertical, 4)
                .background(.quaternary, in: RoundedRectangle(cornerRadius: 6, style: .continuous))

            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(key.name)
                        .lineLimit(1)
                    if key.isDefault {
                        Text("Default")
                            .font(.caption2.weight(.medium))
                            .foregroundStyle(.tint)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 2)
                            .background(.tint.opacity(0.14), in: Capsule())
                    }
                }
                Text(key.shortFingerprint)
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
        .padding(.vertical, 2)
    }
}

/// Detail screen for a single local SSH key: rename, default toggle,
/// copyable SHA256 fingerprint and OpenSSH public key, and delete.
struct SSHKeyDetailView: View {
    @Environment(\.dismiss) private var dismiss
    @State private var store = SSHKeyStore.shared
    let keyID: UUID

    @State private var showingRename = false
    @State private var renameText = ""
    @State private var showingDelete = false
    @State private var copiedField: String?
    @State private var errorMessage: String?

    private var key: SSHKeyRecord? { store.key(id: keyID) }

    var body: some View {
        if let key {
            List {
                Section("Key Information") {
                    Button {
                        renameText = key.name
                        showingRename = true
                    } label: {
                        HStack {
                            Text("Name")
                            Spacer()
                            Text(key.name).foregroundStyle(.secondary)
                            Image(systemName: "pencil")
                                .font(.caption)
                                .foregroundStyle(.tint)
                        }
                    }
                    .foregroundStyle(.primary)
                    .listRowBackground(Color.card)

                    LabeledContent("Type", value: key.type)
                        .listRowBackground(Color.card)

                    LabeledContent("Created", value: key.createdAt.formatted(date: .abbreviated, time: .shortened))
                        .listRowBackground(Color.card)

                    Toggle("Use by Default", isOn: Binding(
                        get: { key.isDefault },
                        set: { store.setDefault($0, for: key.id) }
                    ))
                    .listRowBackground(Color.card)
                }

                Section("Fingerprint") {
                    copyBlock(
                        id: "fp",
                        title: "SHA256",
                        value: key.fingerprint,
                        copyValue: key.fingerprint
                    )
                    .listRowBackground(Color.card)
                }

                Section {
                    copyBlock(
                        id: "pub",
                        title: "OpenSSH Format",
                        value: key.authorizedKeyLine,
                        copyValue: key.authorizedKeyLine
                    )
                    .listRowBackground(Color.card)
                } header: {
                    Text("Public Key")
                } footer: {
                    Text("Add this line to ~/.ssh/authorized_keys on any SSH host.")
                }

                Section {
                    Button("Delete Key", role: .destructive) {
                        showingDelete = true
                    }
                    .listRowBackground(Color.card)
                }
            }
            .scrollContentBackground(.hidden)
            .background(Color.page)
            .navigationTitle(key.name)
            .navigationBarTitleDisplayMode(.inline)
            .alert("Rename SSH Key", isPresented: $showingRename) {
                TextField("Key Name", text: $renameText)
                    .autocorrectionDisabled()
                Button("Cancel", role: .cancel) {}
                Button("Save") { store.rename(id: key.id, to: renameText) }
            }
            .alert("Delete SSH Key?", isPresented: $showingDelete) {
                Button("Cancel", role: .cancel) {}
                Button("Delete", role: .destructive) {
                    do {
                        try store.delete(id: key.id)
                        dismiss()
                    } catch {
                        errorMessage = error.localizedDescription
                    }
                }
            } message: {
                Text("Remove \"\(key.name)\" from this device's Keychain? This cannot be undone.")
            }
            .alert("Error", isPresented: Binding(
                get: { errorMessage != nil },
                set: { if !$0 { errorMessage = nil } }
            )) {
                Button("OK", role: .cancel) { errorMessage = nil }
            } message: {
                if let errorMessage { Text(errorMessage) }
            }
        } else {
            EmptyState("Key removed", symbol: "key.slash")
                .background(Color.page)
        }
    }

    private func copyBlock(id: String, title: LocalizedStringKey, value: String, copyValue: String) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text(title)
                    .font(.caption.weight(.medium))
                    .foregroundStyle(.secondary)
                Spacer()
                Button {
                    UIPasteboard.general.string = copyValue
                    copiedField = id
                    Task {
                        try? await Task.sleep(for: .seconds(1.5))
                        if copiedField == id { copiedField = nil }
                    }
                } label: {
                    Label(
                        copiedField == id ? "Copied" : "Copy",
                        systemImage: copiedField == id ? "checkmark" : "doc.on.doc"
                    )
                    .font(.caption.weight(.medium))
                }
                .buttonStyle(.borderless)
            }
            Text(value)
                .font(.caption.monospaced())
                .textSelection(.enabled)
        }
        .padding(.vertical, 2)
    }
}

/// Sheet to generate a new Ed25519 SSH key.
private struct SSHKeyGenerateSheet: View {
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var isDefault = true
    @State private var errorMessage: String?

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("This Device", text: $name)
                        .autocorrectionDisabled()
                        .accessibilityIdentifier("sshKeyName")
                } header: {
                    Text("Name")
                } footer: {
                    Text("A label for this key, such as Work Server or Personal.")
                }
                .listRowBackground(Color.card)

                Section {
                    LabeledContent("Algorithm", value: "Ed25519")
                    Toggle("Use by Default", isOn: $isDefault)
                } footer: {
                    Text("Ed25519 keys are fast, compact, and supported by all modern OpenSSH servers.")
                }
                .listRowBackground(Color.card)
            }
            .scrollContentBackground(.hidden)
            .background(Color.page)
            .navigationTitle("Generate Key")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel", role: .cancel) { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Generate") {
                        do {
                            try SSHKeyStore.shared.generateKey(name: name, isDefault: isDefault)
                            dismiss()
                        } catch {
                            errorMessage = error.localizedDescription
                        }
                    }
                    .accessibilityIdentifier("generateSSHKey")
                }
            }
            .alert("Error", isPresented: Binding(
                get: { errorMessage != nil },
                set: { if !$0 { errorMessage = nil } }
            )) {
                Button("OK", role: .cancel) { errorMessage = nil }
            } message: {
                if let errorMessage { Text(errorMessage) }
            }
        }
    }
}

/// Sheet to import an existing OpenSSH or PEM private key by paste or file.
private struct SSHKeyImportSheet: View {
    @Environment(\.dismiss) private var dismiss
    @State private var method: Method = .paste
    @State private var name = ""
    @State private var pemText = ""
    @State private var fileName = ""
    @State private var passphrase = ""
    @State private var isDefault = true
    @State private var showingImporter = false
    @State private var errorMessage: String?

    private enum Method: String, CaseIterable {
        case paste, file
    }

    private var looksEncrypted: Bool {
        pemText.contains("ENCRYPTED") || !passphrase.isEmpty
    }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Picker("Source", selection: $method) {
                        Text("Paste").tag(Method.paste)
                        Text("File").tag(Method.file)
                    }
                    .pickerStyle(.segmented)
                }
                .listRowBackground(Color.card)

                Section("Name") {
                    TextField("Imported Key", text: $name)
                        .autocorrectionDisabled()
                }
                .listRowBackground(Color.card)

                if method == .paste {
                    Section {
                        TextEditor(text: $pemText)
                            .font(.caption.monospaced())
                            .frame(minHeight: 160)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                            .accessibilityIdentifier("sshKeyPEM")
                    } header: {
                        Text("Private Key")
                    } footer: {
                        Text("Paste an OpenSSH or PEM private key (Ed25519, RSA, or ECDSA).")
                    }
                    .listRowBackground(Color.card)
                } else {
                    Section {
                        Button {
                            showingImporter = true
                        } label: {
                            HStack {
                                Label("Select Key File", systemImage: "doc")
                                Spacer()
                                if !fileName.isEmpty {
                                    Text(fileName)
                                        .foregroundStyle(.secondary)
                                        .lineLimit(1)
                                }
                            }
                        }
                    } header: {
                        Text("Private Key File")
                    } footer: {
                        Text("Choose a private key file such as id_ed25519 or a .pem file.")
                    }
                    .listRowBackground(Color.card)
                }

                Section {
                    SecureField(looksEncrypted ? "Passphrase (required)" : "Passphrase (optional)", text: $passphrase)
                        .textContentType(.password)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                } footer: {
                    Text("If the key is encrypted, the passphrase decrypts it once during import and is never saved.")
                }
                .listRowBackground(Color.card)

                Section {
                    Toggle("Use by Default", isOn: $isDefault)
                }
                .listRowBackground(Color.card)
            }
            .scrollContentBackground(.hidden)
            .background(Color.page)
            .navigationTitle("Import Key")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel", role: .cancel) { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Import") {
                        do {
                            try SSHKeyStore.shared.importKey(
                                name: name,
                                pem: pemText,
                                passphrase: passphrase,
                                isDefault: isDefault
                            )
                            dismiss()
                        } catch {
                            errorMessage = error.localizedDescription
                        }
                    }
                    .disabled(pemText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    .accessibilityIdentifier("importSSHKey")
                }
            }
            .fileImporter(
                isPresented: $showingImporter,
                allowedContentTypes: [.data, .plainText, .item],
                allowsMultipleSelection: false
            ) { result in
                switch result {
                case .success(let urls):
                    guard let url = urls.first else { return }
                    let scoped = url.startAccessingSecurityScopedResource()
                    defer { if scoped { url.stopAccessingSecurityScopedResource() } }
                    do {
                        let data = try Data(contentsOf: url)
                        pemText = String(decoding: data, as: UTF8.self)
                        fileName = url.lastPathComponent
                        if name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                            name = url.deletingPathExtension().lastPathComponent
                        }
                    } catch {
                        errorMessage = error.localizedDescription
                    }
                case .failure(let error):
                    errorMessage = error.localizedDescription
                }
            }
            .alert("Error", isPresented: Binding(
                get: { errorMessage != nil },
                set: { if !$0 { errorMessage = nil } }
            )) {
                Button("OK", role: .cancel) { errorMessage = nil }
            } message: {
                if let errorMessage { Text(errorMessage) }
            }
        }
    }
}
