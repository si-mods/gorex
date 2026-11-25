---
name: IOS-026
description: "Rex Go manages local SSH keys in Settings with generate, import, copy, default ordering and per-host selection. Use when you change SSH key storage, SSH Keys settings UI, or engine multi-key auth."
metadata:
  created: "2026-11-26"
  updated: "2026-11-26"
---

# IOS-026: Manage local SSH keys in Rex Go

Status: built.

This doc extends [`IOS-023`](IOS-023-ssh-hosts.md) and replaces its single-key model with a lightweight multi-key store and Settings UI modeled after rootshell's SSH key management.

## Problem

[`IOS-023`](IOS-023-ssh-hosts.md) generates one Ed25519 device key lazily when the user adds the first `ssh://` host. The app shows its public key only as a footer line in `RemoteHostsView`, hidden until a host exists. Users cannot view a key's fingerprint, copy a public key before adding a host, import an existing private key, rotate a key, or keep separate keys for different hosts.

## Goal

- Users manage local SSH keys from `Settings > SSH Keys` at any time, including before adding any SSH host.
- Users generate Ed25519 keys, import existing OpenSSH or PEM private keys (by paste or file, with optional passphrase decrypted at import), rename keys, toggle default status, copy fingerprints and public keys, and delete keys.
- Users pick either `Automatic` (try default keys in order) or a specific key per SSH host in Add Host and Edit Host, and can trigger `Install SSH Key` manually from Edit Host.
- Existing single device keys in the Keychain migrate automatically without breaking already-paired SSH hosts.

## Non-goals

- Hardware keys (YubiKey, FIDO2, Passkey), Secure Enclave signing, OpenPubkey, SSH user certificates, GPG bridges, and iCloud Keychain key sync. Those belong to full SSH clients such as rootshell and add complexity Rex Go does not need.
- Desktop SSH key management. Desktop hosts use the system SSH agent or CLI keys.
- Storing key passphrases. Encrypted keys decrypt once at import and store as unencrypted OpenSSH PEM protected by the iOS Keychain.

## Design

### Behavior

- `Settings` adds an `SSH Keys` row in the network card beneath `Remote Hosts`, showing the count of saved keys when non-zero.
- `SSH Keys` lists all saved keys with name, algorithm badge (`ED25519`, `RSA`, `ECDSA`), short SHA256 fingerprint, and a `Default` badge when enabled. Swipe-to-delete asks for confirmation.
- The toolbar `+` menu offers `Generate New Key` and `Import Key`.
- `Generate New Key` asks for a name (defaulting to a readable label such as `This Device` when empty) and creates an Ed25519 key marked as default.
- `Import Key` offers `Paste` and `File` modes, a name field, and an optional passphrase field. The engine parses and decrypts the key once and returns normalized OpenSSH PEM, public key, algorithm, and SHA256 fingerprint.
- Tapping a key opens its detail view: name with rename action, algorithm, creation date, `Use by Default` toggle, copyable `SHA256` fingerprint, copyable OpenSSH public key (`authorized_keys` format), and a destructive `Delete Key` button.
- `RemoteHostsView` removes the raw SSH public key footer line.
- `HostForm` (Add Host in SSH mode and Edit Host for an SSH host) shows an `SSH Key` picker with `Automatic` (`nil`) and every saved key. Edit Host also provides an `Install SSH Key` action to append the host's key (or primary default key) to `~/.ssh/authorized_keys`.

### Model

- `SSHKeyRecord` holds non-secret metadata: `id: UUID`, `name: String`, `type: String`, `publicKey: String`, `fingerprint: String`, `createdAt: Date`, `isDefault: Bool`.
- Metadata persists in `UserDefaults` under `"sshKeys"`.
- Private key material persists in the iOS Keychain under service `dev.gorex.ios.ssh.keys`, account `<uuid-lowercase>`, with `kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly`.
- `HostConfig.SSH` adds an optional `keyID: UUID?`. `nil` means `Automatic`.
- When dialing an SSH host, the engine tries the host's selected key if `keyId` matches a loaded key. Otherwise it tries all loaded keys with `isDefault == true` in list order, falling back to all loaded keys if none are marked default. Password and keyboard-interactive auth remain as fallback even when no keys exist.
- `SSHInstallKey` installs the host's selected key if set, otherwise the first default key.

### Code

- `ios/tailnet/ssh.go`: add `ParseSSHKey(pemKey, passphrase string) (string, error)` and `SetSSHKeys(keysJSON string) error`; extend `sshHost` with `KeyID string`; keep `SetSSHKey` and `SSHPublicKey` as compatibility wrappers; update `dialSSH`, `sshAuthMethods`, and `SSHInstallKey` to use the key list and return an error when the remote install command fails.
- `ios/GoRex/Rex/SSHKeyStore.swift`: `SSHKeyRecord`, `SSHKeychain` protocol (`KeychainSSHKeys` and in-memory test implementation), and `@Observable` `SSHKeyStore` with legacy migration, generate, import, rename, setDefault, delete, and engine sync.
- `ios/GoRex/Views/SSHKeysView.swift`: `SSHKeysView`, `SSHKeyDetailView`, `SSHKeyGenerateSheet`, and `SSHKeyImportSheet`.
- `ios/GoRex/Rex/Host.swift`: add optional `keyID: UUID?` to `HostConfig.SSH`.
- `ios/GoRex/Tailnet/TailnetEngine.swift`: replace single-key `useSSHKey` with `SSHKeyStore.shared` sync (`syncSSHKeys`) and include `keyId` in `setSSHHosts`.
- `ios/GoRex/Views/SettingsView.swift`: add `SSH Keys` row.
- `ios/GoRex/Views/RemoteHostsView.swift`: remove footer public key text, add `SSH Key` picker in `HostForm`, and add manual `Install SSH Key` in Edit Host.

## Alternatives

- **Single-key management page**: simpler engine surface, but cannot hold both an auto-generated device key and an imported work/personal key without overwriting one. Rejected because multi-key storage avoids a later schema migration.
- **Full rootshell key vault (YubiKey, FIDO2, Secure Enclave, certificates, iCloud sync)**: requires SwiftNIO SSH or a Swift signing bridge into Go and adds large UI and security surfaces outside Rex Go's scope. Rejected in favor of pure `x/crypto/ssh` software keys in the local Keychain.

## Security

- Private keys live only in the iOS Keychain with `ThisDeviceOnly` accessibility and never enter `UserDefaults`, logs, or iCloud.
- Passphrases entered during import stay in memory only for the `ParseSSHKey` call and are never written to disk or Keychain.
- Deleting a key removes its Keychain item and drops active pooled SSH clients in the engine so the removed key is no longer used for new connections.

## Migration

- On first load when `"sshKeys"` is absent in `UserDefaults`, `SSHKeyStore` reads the legacy Keychain item (`service: "dev.gorex.ios.ssh"`, `account: "client"`). If present, it parses the key and saves it as `"This Device"` (`isDefault: true`) in the new Keychain service while leaving the legacy item untouched so an older build can still read it on downgrade.
- `HostConfig.SSH.keyID` is optional. Older saved host lists decode with `keyID == nil` (`Automatic`), and older builds ignore the unknown `keyID` field.

## Tests

- Go (`ios/tailnet/ssh_test.go`): `ParseSSHKey` (Ed25519, RSA, encrypted with valid/wrong/missing passphrase, invalid input), `SetSSHKeys` (multiple keys, default ordering, per-host `keyId` selection, password fallback with zero keys, client drop on key change), and `SSHInstallKey` (selected key vs default key, non-zero exit error).
- Swift (`ios/GoRexTests/SSHTests.swift`): `SSHKeyStore` generate/import/rename/toggle-default/delete, legacy Keychain migration, and `HostConfig.SSH.keyID` Codable round-trip and backwards compatibility.
- Simulator UI on Mac mini: `Settings > SSH Keys` list, generate, detail view, and `HostForm` key picker.

## Phases

1. Multi-key engine support, Keychain store with legacy migration, `Settings > SSH Keys` UI, per-host key picker, and manual `Install SSH Key`.
