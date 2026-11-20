# Rex Go: iCloud sync

Status: built (#798).

## Problem

A user with an iPhone and an iPad sets Rex Go up twice. The user sets the theme, the font, the dictation service and its API key on each device. This design makes those items follow the user between their own devices. It uses iCloud. It needs no account or server of GoRex.

## Design

### What syncs, and where

- **The API key** (the Polish key of dictation, in Settings › Dictation) goes in the iCloud Keychain. It is a Keychain item with `kSecAttrSynchronizable`. iCloud Keychain encrypts this item end to end.
  - The key never goes in the key-value store, because that store is not end-to-end encrypted.
  - A synchronizable item cannot be `ThisDeviceOnly`, so the item is `AfterFirstUnlock`.
  - Keychain sync needs no entitlement.
  - With iCloud Keychain off, the item stays on the device.
- **Preferences** go in the iCloud key-value store (`NSUbiquitousKeyValueStore`). The app mirrors the store with `UserDefaults`. The app keeps reading and writing `UserDefaults` as before, `@AppStorage` included.
  - The keys are `scheme` (Interface), `theme`, `fontSize`, `fontFamily`, `minimumContrast`, `polishBase` and `polishModel`.
  - Values are strings and numbers only.
  - `CloudSettings` (`ios/GoRex/Rex`) does the mirror.

### What does not sync

- `clientLabel`: the name of each device.
- Tailscale keys and state: they are the identity of a device on the tailnet.
- `dictationModel`: each device downloads its own models, and some models need more memory than another device has.
- `hosts`: the Keychain holds the token of a LAN gateway for each device.
- `backgrounds`, `expanded`, the input trace and anything else that this document does not list.

### Merge

- iCloud keeps the last writer, for each key.
- At launch the cloud wins. The app writes each key that the cloud holds into `UserDefaults`. The app pushes each key that the cloud lacks from the device. Because of this, the first launch after the update seeds the cloud.
- The key-value store drops what the app writes before its first download. The launch seed can be lost for this reason.
  - After the first download (initial sync), an account change or a quota violation, the app reconciles every key again.
  - The app takes the value of the cloud. Where the cloud has no value, the app pushes the value of this device.
  - This also seeds a new iCloud account.
  - These steps never remove a local value.
- A server change names its keys. The app applies each key. If another device removed a key, the app removes it here.
- The app pushes a change made here 1.5 s after the last change, when the change has settled. A pinch zoom writes the size at each step and a text field writes at each keystroke. A partial value must not reach other devices.
  - The app writes a value only where it differs.
  - The app pushes only a change made on the device. Nothing loops, and a value that came from the cloud does not go back.
- When the user removes a key on a device, the app removes it in the cloud too. An empty API key means that polish is off. When the user clears the key, it clears on every device.
- The app saves the API key when the user submits the field, when the page closes or when the app leaves the foreground. It saves the key only if the user typed it in, and not at each keystroke. A partial key does not sync. A stale field does not overwrite a key that iCloud brought.
- The app sends the terminals the same notifications as Settings does (`.terminalFont`, `.terminalTheme`, `.minimumContrast`). Open terminals follow the change.

### Maple Mono

Each device downloads the font family `Maple Mono NF CN`. The family syncs as a plain string. Where the font is not installed, the app reads the family as the own font of Ghostty (`TerminalView.family`, which Settings also checks). The stored value stays. The font shows when the device downloads it. If the user removes the font, the stored family does not change.

### Entitlement

The entitlement is `com.apple.developer.ubiquity-kvstore-identifier` = `$(TeamIdentifierPrefix)$(CFBundleIdentifier)`. It is in `ios/GoRex/GoRex.entitlements`. `CODE_SIGN_ENTITLEMENTS` in `ios/project.yml` sets it. The App ID needs iCloud key-value storage enabled in the developer portal. Automatic signing does this.

## Migration

### Key migration

An older build kept the key in a local item. At launch, `Polish.syncKey()` runs once. It saves the key as synchronizable and removes the local item. If a synced key is already there, that key wins, because another device may have rotated it. In that case the function only removes the local key. `polishKeySynced` in `UserDefaults` records that the function ran, or that no local item exists.

### Downgrade

An older build reads the key without `kSecAttrSynchronizableAny`. It does not see the synchronizable item. After a downgrade, dictation polish is off until the user types the key again.
