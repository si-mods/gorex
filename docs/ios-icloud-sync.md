# Rex Go: iCloud sync

Status: implemented.

## Problem

A user with an iPhone and an iPad sets Rex Go up twice: the theme, the font,
the dictation service and its API key. This design makes those follow the
user between their own devices, through iCloud, with no account or server of
GoRex.

## What syncs, and where

- **The API key** (dictation's Polish, Settings › Dictation) goes in the iCloud
  Keychain: a Keychain item with `kSecAttrSynchronizable`, which iCloud
  Keychain encrypts end to end. It never goes in the key-value store, which is
  not end-to-end encrypted. A synchronizable item cannot be `ThisDeviceOnly`,
  so it is `AfterFirstUnlock`. Keychain sync needs no entitlement. With iCloud
  Keychain off, the item stays on the device.
- **Preferences** go in the iCloud key-value store (`NSUbiquitousKeyValueStore`),
  mirrored with `UserDefaults`, which the app keeps reading and writing as
  before (`@AppStorage` included): `scheme` (Interface), `theme`, `fontSize`,
  `fontFamily`, `minimumContrast`, `polishBase` and `polishModel`. Values are
  strings and numbers only. `CloudSettings` (`ios/GoRex/Rex`) does the mirror.

## What does not sync

- `clientLabel`: the name of each device.
- Tailscale keys and state: a device's identity on the tailnet.
- `dictationModel`: the models are downloaded per device, and some need more
  memory than another device has.
- `hosts` (a LAN gateway's token is in the Keychain, per device), `backgrounds`,
  `expanded`, the input trace and anything else not listed above.

## Merge

- iCloud's last writer wins, per key.
- At launch the cloud wins: a key it holds is written into `UserDefaults`. A key
  it lacks is pushed from the device, so the first launch after the update
  seeds the cloud.
- The key-value store drops what is written before its first download, so
  the launch seed can be lost. After the first download (initial sync), an
  account change or a quota violation, every key is reconciled again: the
  cloud's value is taken, or, where the cloud has none, this device's is
  pushed. That also seeds a new iCloud account. These never delete a local value.
- A server change names its keys: each is applied, and a key another device
  removed is removed here.
- A change made here is pushed once it has settled, 1.5 s after the last one,
  as a pinch zoom writes the size at each step and a text field at each
  keystroke, and a partial value must not reach other devices. A value is
  written only where it differs, and only a change made on the device is
  pushed, so nothing loops and what came from the cloud does not go back.
- Removing a key on a device removes it in the cloud too. An empty API key
  means polish is off, so clearing it clears it on every device.
- The API key is saved when its field is submitted, the page closes or the
  app leaves the foreground, and only if it was typed in, not per keystroke,
  so that a partial key does not sync and a stale field does not overwrite a
  key that iCloud brought.
- The terminals are told as Settings tells them (`.terminalFont`,
  `.terminalTheme`, `.minimumContrast`), so open ones follow.

## Maple Mono

The font family `Maple Mono NF CN` is downloaded per device, yet it syncs as
a plain string. Where the font is not installed, it is read as Ghostty's own
font (`TerminalView.family`, also what Settings checks), and the stored value
stays, so the font shows once it is downloaded. Deleting the font does not
change the stored family.

## Key migration

An older build kept the key in a local item. Once, at launch,
`Polish.syncKey()` saves it as synchronizable and removes the local one. A
synced key already there wins, as another device may have rotated it, and the
local one only goes. `polishKeySynced` in `UserDefaults` records that it ran,
or that no local item exists.

## Downgrade

An older build reads the key without `kSecAttrSynchronizableAny`, so it does
not see the synchronizable item: after a downgrade dictation polish is off until
the key is typed again.

## Entitlement

`com.apple.developer.ubiquity-kvstore-identifier` =
`$(TeamIdentifierPrefix)$(CFBundleIdentifier)`, in `ios/GoRex/GoRex.entitlements`,
set by `CODE_SIGN_ENTITLEMENTS` in `ios/project.yml`. The App ID needs iCloud
key-value storage enabled in the developer portal (Automatic signing does it).

