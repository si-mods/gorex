# Rex Go: cross-host Space sync with iOS (and iCloud) as the central control app

Status: replaced by [projects.md](projects.md).

This doc is history. Rex Go no longer stores `SpaceManifest`, and it no longer syncs through iCloud. Each host stores its projects, and Rex Go reads them from the host (projects.md, phase 3). The text below is the old design. Phase 1 is in #580. Phases 2 and 3 are in #581.

## 1. Problem and diagnosis

The iOS app (Rex Go) is a pure control app. It runs no local shell or PTY. All terminal windows run on the remote hosts (MacBook, Mac mini, Linux servers and others).

The cross-host Space code today uses a **decentralized, temporary patchwork in memory**. The code is in [internal/rexapi/spaces.go](../internal/rexapi/spaces.go), [ios/GoRex/Rex/Spaces.swift](../ios/GoRex/Rex/Spaces.swift) and [ios/GoRex/Views/HostListView.swift](../ios/GoRex/Views/HostListView.swift). This code has these structural defects:

1. **No central persistent source of truth. When the desktop goes offline, the groups collapse.**
   - Each host reports through `GET /api/spaces` which `block_id` values of other `machine_id` values one of its local Spaces refers to.
   - The `CrossHost` of the iOS app joins only the hosts that are **online now**, and only in memory. A desktop (for example a MacBook) can record a cross-host group. When that desktop sleeps or goes offline, `Host.crossHost` becomes `nil`. The iOS app loses all cross-host groups at once. The windows of the other online remote hosts (for example Linux) fall back to isolated `Desktop terminals` groups.
2. **A Space has the key `(host, session)`. Several hosts cannot unify Spaces with the same name.**
   - `HostListView` puts the `sessions` of each online host flat in the top strip. Suppose two hosts both have `Work`. The top strip then splits into two separate capsules, `Work · MacBook` and `Work · Mac Studio`. The user cannot manage them as one cross-host workspace.
3. **The cross-host `machine_id` depends on the `hosts.json` cache of the desktop.**
   - In [internal/rexapi/model.go](../internal/rexapi/model.go), `mp.machines[n.Host]` depends on `HostMachines` of the desktop `hosts.json`. The desktop may lack the recorded `machine` of a remote host. The code then skips the `remote_block` of that host without a message.
4. **A new or orphan terminal on a remote host makes many fragment Spaces.**
   - A user creates a terminal on the remote host with the CLI or directly on iOS. The terminal goes into `gorex-desktop-terminals`. While no desktop Space refers to it, `CrossHost.isGone` stays `false`. The top strip then keeps adding a `Desktop terminals · <Host>` capsule for each host.
5. **The iOS app cannot define or change a cross-host Space by itself.**
   - iOS has no `rex` server and no `layout.json` of its own. The user creates a Space on the phone and sorts windows across hosts. The current architecture has no single place that stores this.

---

## 2. Goals and non-goals

### Goals

- **A central control app.** The `SpaceManifest` that the iOS app stores is the one central source of truth for cross-host Space groups. iCloud syncs it among the Apple control devices of the user (iPhone and iPad). It needs **no always-on server or Hub host**.
- **Passive sync to the desktop.** When a desktop (GoRex on Mac, Linux or Windows) is online, the control app passively syncs the related Space membership to it. The design also accepts new changes that it merges from the `layout.json` of the desktop. When the desktop is offline, iOS still holds the full Space groups. This does not affect how the other online remote hosts show and operate.
- **One unified cross-host Space model.** The global `space.id` is the first-class key. This removes the split of same-name Spaces and the `Desktop terminals` fragment capsules of each host.

### Non-goals

- Do not run a local shell or PTY on iOS.
- Do not add a self-built cloud relay or a central account server. Reuse iCloud and the existing `tailcat`, Tailscale and `rexapi`.
- Do not break backward compatibility with old `rexapi` servers. A remote host that is not upgraded can still join as a normal host, and the central manifest can still group it.

---

## 3. Core data model: `SpaceManifest`

The design takes the "Space group directory" out of the `layout.json` of one machine. It defines an independent cross-host index structure:

```json
{
  "version": 1,
  "revision": 42,
  "spaces": [
    {
      "id": "sp_01J...",
      "name": "Work",
      "color": "blue",
      "order": 0,
      "updated_at": 1763150000,
      "members": [
        {
          "machine_id": "9c63...",
          "block_id": "block:a1",
          "window_id": "win:1",
          "order": 0,
          "added_at": 1763149000
        },
        {
          "machine_id": "f4b7...",
          "block_id": "block:b2",
          "window_id": "win:9",
          "order": 1,
          "added_at": 1763149500
        }
      ]
    }
  ],
  "tombstones": [
    {
      "kind": "space",
      "id": "sp_old",
      "deleted_at": 1763140000
    },
    {
      "kind": "member",
      "machine_id": "9c63...",
      "block_id": "block:x9",
      "deleted_at": 1763145000
    }
  ]
}
```

### 3.1 The home of a Project: `home_machine` and `dir`

In the user interface, a Space is called a Project (`docs/projects.md`). A Project can bind to a folder: a host and a directory on it. For this, a Space in the manifest gets two optional fields. The names on the wire and in the code stay `CentralSpace` and `gorex.spaces`:

```json
{ "id": "sp_01J...", "name": "windflow", "home_machine": "9c63...", "dir": "/home/alice/code/windflow", "members": [] }
```

- `home_machine`: the `rex.MachineID` of the host that holds the home directory. It is the same on all devices. The host ID of `hosts.json` differs on each device, so it cannot serve here.
- `dir`: the absolute path on that host. The default (nil) means "unknown". This is a manifest that an app wrote before the project feature. The empty string means "no folder".
- Decoding is lenient. When a field is missing or has the wrong type, the app reads it as nil. The manifest still loads.
- **iCloud merge.** Suppose the newer copy has no home (an old app version dropped the fields). The app then keeps the local home. A home that is newer, or already cleared, is taken as usual.
- **Sync to the desktop** (`gorex.spaces_apply`, feature `gorex.projects`). When the home is known, iOS sends `home_machine` and `dir`. When it is unknown, iOS does not send them, and the desktop keeps its own home. A `dir` of `""` clears the home. Suppose the desktop does not know the `machine_id`. It then keeps the folder and shows the home host as unknown. `homes` in the result is the number of homes that changed.
- **Desktop to iOS.** The app reads the `project {home_machine, dir}` field of a session (in bootstrap, `session_created` and every view). The home of the desktop fills a Project whose home iOS does not know yet. The app takes a home that the desktop changes later (for example Change Folder). A home that the phone changed, and that the desktop has not yet followed, stays unchanged.
- **How iOS tells the two cases apart.** The two cases are "the desktop changed it" and "the desktop has not yet followed". For each session, iOS records the Space, the name and the home that the session reported last. It keys the record by the `machine_id` of the host and the session ID. It stores the record on this device in `spaces-manifest.reported.json`. This file is in the same directory as the manifest and does not go to iCloud. So the app takes a change that the desktop made while the app was closed at the next start.
  - Only a change in the report of the same session counts as a change by the desktop. Another session has its own first report. An example is a Space that `gorex.spaces_apply` creates for a new name and that has no home yet. Its first report says "no folder". This does not clear the folder of the Project.
  - Suppose this connection has not yet read the `machine_id` of the host (the host just connected, and `/api/spaces` has not answered). The app then syncs that host by host ID. It does not record or take the report of that host. The member cleanup does not touch the members of any machine.
- **Remembered `machine_id`.** iOS remembers the last `machine_id` of each host (UserDefaults `hostMachines`). It uses this only to show the home host of a Project and to find it. When the host is offline, the app shows "offline" and not "not added". It does not use this record for sync. When the address of a host changes (it may be another computer), the app clears the record.
- **Rename.** A user renames a Project on iOS. The desktop session may still report the old name. The session then stays in the original Project, and the app does not create a new Project for the old name. Before iOS pushes the manifest, it changes the session to the new name with `session.set_label`. `gorex.spaces_apply` then finds the session by the new name. It does not create a new Space and leave an old empty Space. Suppose a user renames the session on the desktop and the manifest name is unchanged. The Project then uses the new name. Its ID and folder stay.
- **New window.** A Project that has a folder creates a new window on the gateway of the home host. The `cwd` is that folder. When the host is not online, the app says why. It does not open the window elsewhere. Some creations carry a `cwd`: the first window of New Project, and New Window. Before each of them, the app checks the folder with `gorex.dir.list`. When the folder no longer exists, the app shows "Folder Not Found" (with Change Folder…). It opens no window. A host that does not list folders (its welcome has no `gorex.dir_list`) skips the check. Change Folder and Remove Folder update the manifest. They also call `gorex.project.set_home` on each host that is online and holds a session of that Project.

### Key design points

1. **Stable anchor `(machine_id, block_id)`.**
   - `machine_id` is the persistent identity of the machine (the hash of the GoRex client key, as in [docs/rex-api.md](rex-api.md)). It does not change with the IP, the Tailscale domain name or the host alias.
   - `window_id` is a helper hint. Several panes in one window share one window membership. The app lists them once for each window.
2. **A Space is a first-class object.**
   - The primary key is an independent `space.id` (UUID or ULID). It is no longer coupled to `(host.id, session.id)`.
   - All hosts of a Space may be offline for a while. The definition of the Space (name, color, order and member references) stays complete.
3. **Tombstones.**
   - A tombstone records a Space that the user removed recently, or a member that the user removed by hand. It stops an offline desktop that reconnects from bringing back a removed item. It also covers the delay of an iCloud sync. The app cleans tombstones regularly after a retention window (for example 7 days).

---

## 4. Central architecture and sync

```text
                ┌───────────────────────────────────┐
                │       iCloud (KV / Drive)         │
                │   iPhone ◄─── sync ───► iPad      │
                └─────────────────▲─────────────────┘
                                  │
                                  ▼
┌───────────────────────────────────────────────────────────────────┐
│                iOS control app (SpaceStore)                       │
│     Local spaces-manifest.json (central grouping basis)           │
└───────┬─────────────────────────┬─────────────────────────┬───────┘
        │ passive sync (Read/Push)│ Read / Attach           │ Read / Attach
        ▼                         ▼                         ▼
┌───────────────┐         ┌───────────────┐         ┌───────────────┐
│  Desktop Host │         │  Linux Host A │         │  Linux Host B │
│  (Mac / Win)  │         │  (Headless)   │         │  (Headless)   │
└───────────────┘         └───────────────┘         └───────────────┘
```

### 4.1 Local persistence of the control app, and iCloud sync (between iOS devices)

1. **Local persistence.**
   - The iOS app writes `SpaceManifest` atomically to `Application Support/spaces-manifest.json`. The app loads it at start. It does not wait for any host to come online.
2. **iCloud sync carrier.**
   - The recommended first choice is **`NSUbiquitousKeyValueStore`** (or an iCloud Drive coordinated file with `NSFileCoordinator`):
     - `NSUbiquitousKeyValueStore` has low latency and needs no complex database migration. Its limit of 1 MB is enough for an index of only IDs and metadata. It holds thousands of terminal members.
     - The app must enable the iCloud Key-Value Store entitlement for `dev.gorex.ios` in [ios/project.yml](../ios/project.yml).
3. **Conflict merge among several control apps (LWW).**
   - The merge is entity-level Last-Write-Wins. It uses `space.updated_at` and `tombstones`:
     - One device removes a Space (it has a newer `deleted_at`), and the other device did not change that Space. The removal takes effect.
     - Membership follows the newer `added_at` or `deleted_at`. This guarantees that one `(machine_id, block_id)` belongs to at most one Space.

### 4.2 Sync between the control app and the hosts (passive sync to the desktop)

#### A. Host to iOS: discovery and incremental merge

Each time a host connects and finishes `session.attach` and `GET /api/spaces`, the app runs an incremental merge (not an overwrite):

1. **Import of the existing desktop Spaces.**
   - Suppose the desktop reports a Space that is not yet in `SpaceManifest` and not in `tombstones`. The app imports it as a new Space. It writes the local windows of the desktop and its `remote_blocks` into `members`.
   - **Fix for the missing `machine_id`.** The iOS app connects directly to all the remote hosts that the user added, and it knows the `machine_id` of each. The desktop may miss a `remote_block` because `hosts.json` has no cache of it. The control app can then complete the membership from the existing `SpaceManifest`.
2. **One treatment for unassigned terminals.**
   - A user creates a terminal on one host with the CLI or SSH (for example in `gorex-desktop-terminals`). Suppose that terminal is in the `members` of no Space. The app **no longer makes a `Desktop terminals · <Host>` capsule for each host**. It gathers these terminals in one virtual **`Unassigned`** view (or offers to add them to the current Space in one tap).
   - The user taps "new window" or "new terminal" inside the **currently selected Space** on iOS. After the creation succeeds, the control app writes the `(machine_id, block_id)` of the new terminal into the `members` of the current Space.

#### B. iOS to desktop: passive sync to the desktop

- **Who it applies to.** Hosts that run the full desktop UI (they have `layout.json`): Mac, Linux and Windows Desktop. A pure headless server (headless `gorex -server`) keeps no cross-host desktop layout. It does not need to receive write-back. Only the iOS control app holds the groups.
- **When it syncs.** The desktop is online and supports the new capability `gorex.spaces_sync`. After iOS connects, it compares `revision` or the differences. It sends the Space changes that the control app started to the desktop. These are rename, new Space, and moving a window into or out of a Space.
- **Protocol extension (`internal/rexapi`).**
  - Add `gorex.spaces_sync` to `welcome.features`.
  - Add the RPC method `gorex.spaces_apply` (or `PUT /api/spaces/manifest`). The desktop applies the Space changes that relate to it safely to `layout.Store`. It reuses the existing `tab.to_space` and workspace rename logic. This avoids a direct overwrite of the pane tree that the desktop is editing.

### 4.3 Offline tolerance and cleanup of zombie members (GC)

1. **An offline host does not remove a group.**
   - When a host is in the `.offline` state, `SpaceManifest` keeps all `members` of its `machine_id`.
   - The Space still shows in the top strip as normal. The windows of the Space that belong to other **online** hosts show and work as normal. The offline host shows its offline state in its own block, or the block folds, as the setting says.
2. **Safe garbage collection (Safe GC).**
   - **Trigger.** A `machine_id` is **online now**, and the app pulled the views of all sessions of that host with `session.attach`. Only then, if a `block_id` in `SpaceManifest` is no longer in any window of that host, the app removes it from `members`.
   - This fully avoids a wrong deletion of group relations. The host may be offline for a while, the network may jitter, or the host may just have reconnected without a full view.

---

## 5. iOS UI and interaction changes (`HostListView.swift`)

1. **Top Space strip.**
   - The data source changes from `store.hosts.flatMap(...)` to `SpaceStore.spaces` (sorted by `order`).
   - The `Unassigned` capsule shows at the end (or as a fixed entry) only when an online window belongs to no Space.
   - Add a **+** button or a long-press menu. It supports new Space, rename, change color and remove Space.
2. **Window list inside a Space.**
   - The list groups by host and shows the windows of all **online** hosts that the current Space relates to.
   - The long-press menu of a window gets **"Move to Space…"**. The user can add or move any window on any remote host to any Space, even a window from a pure Linux server.
3. **Keep the `By Host` mode.**
   - `Grouping.hosts` in Settings stays unchanged. It is the alternative view for browsing by physical host.

---

## 6. Phases

1. **Phase 1: local persistence in the iOS control app, and the merge engine (the core fix).**
   - Add `ios/GoRex/Rex/SpaceStore.swift`. It implements the `SpaceManifest` model, local JSON persistence, the incremental merge with the `/api/spaces` of each host, Safe GC and the `Unassigned` grouping.
   - Change `HostListView.swift` to read the Space list from `SpaceStore`. It supports new, rename and move window to Space on the phone.
   - **Benefit.** It solves at once the three core pain points: "the desktop goes offline and the groups are lost", "same-name Spaces split" and "`Desktop terminals` fragments". It does not depend on a server release.
2. **Phase 2: iCloud sync among several control apps.**
   - Configure the iCloud entitlements in `ios/project.yml`. Connect `NSUbiquitousKeyValueStore` to sync `SpaceManifest`, with the LWW merge.
   - **Benefit.** An iPhone and an iPad share the same cross-host Space groups in real time.
3. **Phase 3: passive sync to the desktop (`gorex.spaces_sync`).**
   - Add the `gorex.spaces_apply` capability in `internal/rexapi/` and in the desktop `layout` apply layer. The Space changes that the user makes on iOS then sync back to the online desktop.

---

## 7. Decisions

1. **Choice of the iCloud sync carrier.** Use **`NSUbiquitousKeyValueStore`**. It needs no configuration and syncs in milliseconds. Its limit of 1 MB is enough for an index of only metadata. The app controls the size by cleaning `tombstones` regularly.
2. **First merge of Spaces with the same name.** **By default, merge them by name automatically** into one cross-host Space. At the first connection of several desktops, Spaces with the same name merge into one global `space.id`. Later, the user can also rename or split them by hand on iOS.
3. **Priority when the desktop and iOS conflict.** Use **last timestamp wins (LWW)**. The merge is automatic. It is based on the entity-level `updated_at`, `added_at` and `deleted_at`.
