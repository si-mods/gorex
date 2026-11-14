import Foundation
import Observation

/// Centralized cross-host space manifest persisted on the iOS control plane
/// (docs/ios-centralized-spaces.md).
struct SpaceManifest: Codable, Equatable {
    var version: Int = 1
    var revision: Int = 0
    var spaces: [CentralSpace] = []
    var tombstones: [SpaceTombstone] = []
}

/// A cross-host space as a first-class entity, independent of any single
/// host's online state or session ID.
struct CentralSpace: Codable, Equatable, Identifiable, Hashable {
    var id: String
    var name: String
    var color: String?
    var order: Int
    var updated_at: TimeInterval
    var members: [SpaceMember]
}

/// A terminal block belonging to a space, anchored by its host's durable
/// `machine_id` and `block_id`.
struct SpaceMember: Codable, Equatable, Hashable {
    var machine_id: String
    var block_id: String
    var window_id: String?
    var order: Int
    var added_at: TimeInterval
}

/// Deletion marker preventing offline hosts or stale syncs from resurrecting
/// deleted spaces or manually unassigned members.
struct SpaceTombstone: Codable, Equatable, Hashable {
    enum Kind: String, Codable {
        case space
        case member
    }

    var kind: Kind
    var id: String
    var name: String?
    var machine_id: String?
    var block_id: String?
    var deleted_at: TimeInterval
}

/// Owns the centralized `SpaceManifest`, persists it locally, and merges
/// discovered sessions/windows from connected remote hosts.
@MainActor
@Observable
final class SpaceStore {
    /// Synthetic session ID used by `rexapi` for unplaced CLI/iOS terminals.
    static let desktopTerminalsSessionID = "gorex-desktop-terminals"
    /// Retention window for tombstones before pruning (7 days).
    static let tombstoneRetention: TimeInterval = 7 * 24 * 60 * 60

    struct HostSnapshot {
        struct Session {
            let id: String
            let label: String
            let view: SessionView?
            let remote: [Spaces.Member]
        }

        let machine: String
        let online: Bool
        let sessions: [Session]
    }

    private(set) var manifest: SpaceManifest
    @ObservationIgnored private let fileURL: URL

    init(fileURL: URL? = nil) {
        let url = fileURL ?? Self.defaultFileURL()
        self.fileURL = url
        if let data = try? Data(contentsOf: url),
           let decoded = try? JSONDecoder().decode(SpaceManifest.self, from: data) {
            self.manifest = decoded
        } else {
            self.manifest = SpaceManifest()
        }
    }

    static func defaultFileURL() -> URL {
        let dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first
            ?? FileManager.default.temporaryDirectory
        return dir.appendingPathComponent("spaces-manifest.json")
    }

    // MARK: - Queries

    var orderedSpaces: [CentralSpace] {
        manifest.spaces.sorted {
            if $0.order != $1.order { return $0.order < $1.order }
            return $0.id < $1.id
        }
    }

    func space(id: String) -> CentralSpace? {
        manifest.spaces.first { $0.id == id }
    }

    func space(for machine: String, block: String) -> CentralSpace? {
        manifest.spaces.first { s in
            s.members.contains { $0.machine_id == machine && $0.block_id == block }
        }
    }

    /// Whether any block of `window` on `machine` belongs to `spaceID`.
    func contains(spaceID: String, machine: String, window: SessionView.Window) -> Bool {
        guard let s = space(id: spaceID) else { return false }
        let blocks = Set(window.blocks)
        return s.members.contains { $0.machine_id == machine && blocks.contains($0.block_id) }
    }

    /// Whether none of `window`'s blocks on `machine` are assigned to any space.
    func isUnassigned(machine: String, window: SessionView.Window) -> Bool {
        let blocks = Set(window.blocks)
        guard !blocks.isEmpty else { return false }
        return !manifest.spaces.contains { s in
            s.members.contains { $0.machine_id == machine && blocks.contains($0.block_id) }
        }
    }

    // MARK: - Mutations

    @discardableResult
    func createSpace(name: String, color: String? = nil, now: Date = .now) -> CentralSpace {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        let label = trimmed.isEmpty ? "New Space" : trimmed
        let ts = now.timeIntervalSince1970
        let nextOrder = (manifest.spaces.map(\.order).max() ?? -1) + 1
        let space = CentralSpace(
            id: "sp_" + UUID().uuidString.lowercased(),
            name: label,
            color: color,
            order: nextOrder,
            updated_at: ts,
            members: []
        )
        manifest.tombstones.removeAll {
            $0.kind == .space && ($0.name?.caseInsensitiveCompare(label) == .orderedSame)
        }
        manifest.spaces.append(space)
        manifest.revision += 1
        save()
        return space
    }

    func renameSpace(_ id: String, to name: String, now: Date = .now) {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, let i = manifest.spaces.firstIndex(where: { $0.id == id }) else { return }
        manifest.spaces[i].name = trimmed
        manifest.spaces[i].updated_at = now.timeIntervalSince1970
        manifest.revision += 1
        save()
    }

    func deleteSpace(_ id: String, now: Date = .now) {
        guard let i = manifest.spaces.firstIndex(where: { $0.id == id }) else { return }
        let removed = manifest.spaces.remove(at: i)
        let ts = now.timeIntervalSince1970
        manifest.tombstones.removeAll { $0.kind == .space && $0.id == id }
        manifest.tombstones.append(SpaceTombstone(
            kind: .space,
            id: removed.id,
            name: removed.name,
            deleted_at: ts
        ))
        manifest.revision += 1
        save()
    }

    /// Assigns `(machine, block)` to `spaceID`, removing it from any previous space.
    func assign(machine: String, block: String, window: String? = nil, to spaceID: String, now: Date = .now) {
        guard !machine.isEmpty, !block.isEmpty,
              let targetIndex = manifest.spaces.firstIndex(where: { $0.id == spaceID }) else { return }
        let ts = now.timeIntervalSince1970

        for i in manifest.spaces.indices {
            manifest.spaces[i].members.removeAll { $0.machine_id == machine && $0.block_id == block }
        }
        manifest.tombstones.removeAll {
            $0.kind == .member && $0.machine_id == machine && $0.block_id == block
        }

        let nextOrder = (manifest.spaces[targetIndex].members.map(\.order).max() ?? -1) + 1
        manifest.spaces[targetIndex].members.append(SpaceMember(
            machine_id: machine,
            block_id: block,
            window_id: window,
            order: nextOrder,
            added_at: ts
        ))
        manifest.spaces[targetIndex].updated_at = ts
        manifest.revision += 1
        save()
    }

    /// Assigns every block of `window` on `machine` to `spaceID`.
    func assign(window: SessionView.Window, on machine: String, to spaceID: String, now: Date = .now) {
        for block in window.blocks {
            assign(machine: machine, block: block, window: window.window_id, to: spaceID, now: now)
        }
    }

    func unassign(machine: String, block: String, now: Date = .now) {
        var changed = false
        let ts = now.timeIntervalSince1970
        for i in manifest.spaces.indices {
            let before = manifest.spaces[i].members.count
            manifest.spaces[i].members.removeAll { $0.machine_id == machine && $0.block_id == block }
            if manifest.spaces[i].members.count != before {
                manifest.spaces[i].updated_at = ts
                changed = true
            }
        }
        manifest.tombstones.removeAll {
            $0.kind == .member && $0.machine_id == machine && $0.block_id == block
        }
        manifest.tombstones.append(SpaceTombstone(
            kind: .member,
            id: "\(machine)/\(block)",
            machine_id: machine,
            block_id: block,
            deleted_at: ts
        ))
        if changed {
            manifest.revision += 1
            save()
        }
    }

    // MARK: - Host Sync & Safe GC

    /// Incrementally merges spaces and blocks from connected hosts into the
    /// centralized manifest, and garbage-collects dead blocks only for hosts
    /// that are online and have attached all their sessions.
    func sync(from hosts: [HostSnapshot], now: Date = .now) {
        let ts = now.timeIntervalSince1970
        var changed = false

        pruneTombstones(before: ts - Self.tombstoneRetention, changed: &changed)

        // 1. Discover spaces & members from online hosts.
        for host in hosts where host.online {
            for session in host.sessions {
                if session.id == Self.desktopTerminalsSessionID { continue }
                let label = session.label.trimmingCharacters(in: .whitespacesAndNewlines)
                let spaceName = label.isEmpty ? session.id : label

                // Skip spaces explicitly deleted by the user.
                if isSpaceTombstoned(name: spaceName) { continue }

                // Find or create CentralSpace (auto-merge same-name spaces).
                let targetID: String
                if let existing = manifest.spaces.first(where: { $0.name.caseInsensitiveCompare(spaceName) == .orderedSame }) {
                    targetID = existing.id
                } else {
                    let nextOrder = (manifest.spaces.map(\.order).max() ?? -1) + 1
                    let created = CentralSpace(
                        id: "sp_" + UUID().uuidString.lowercased(),
                        name: spaceName,
                        color: nil,
                        order: nextOrder,
                        updated_at: ts,
                        members: []
                    )
                    manifest.spaces.append(created)
                    targetID = created.id
                    changed = true
                }

                // Collect local blocks of this session's windows.
                for w in session.view?.windows ?? [] {
                    for b in w.blocks {
                        addDiscoveredMember(machine: host.machine, block: b, window: w.window_id, to: targetID, ts: ts, changed: &changed)
                    }
                }
                // Collect remote blocks referenced by this space.
                for r in session.remote {
                    addDiscoveredMember(machine: r.machine, block: r.block_id, window: nil, to: targetID, ts: ts, changed: &changed)
                }
            }
        }

        // 2. Safe GC: only prune blocks for hosts that are online AND have
        //    attached views for all of their sessions.
        for host in hosts where host.online && host.sessions.allSatisfy({ $0.view != nil }) {
            var liveBlocks: Set<String> = []
            for session in host.sessions {
                for w in session.view?.windows ?? [] {
                    liveBlocks.formUnion(w.blocks)
                }
            }
            for i in manifest.spaces.indices {
                let before = manifest.spaces[i].members.count
                manifest.spaces[i].members.removeAll { m in
                    m.machine_id == host.machine && !liveBlocks.contains(m.block_id)
                }
                if manifest.spaces[i].members.count != before {
                    manifest.spaces[i].updated_at = ts
                    changed = true
                }
            }
        }

        if changed {
            manifest.revision += 1
            save()
        }
    }

    /// LWW merge with another `SpaceManifest` (e.g. from iCloud).
    func merge(_ incoming: SpaceManifest) {
        var result = Self.merged(local: manifest, remote: incoming)
        if result != manifest {
            result.revision = max(manifest.revision, incoming.revision) + 1
            manifest = result
            save()
        }
    }

    static func merged(local: SpaceManifest, remote: SpaceManifest) -> SpaceManifest {
        var tombstonesByKey: [String: SpaceTombstone] = [:]
        for t in local.tombstones + remote.tombstones {
            let key = "\(t.kind.rawValue):\(t.id)"
            if let existing = tombstonesByKey[key] {
                if t.deleted_at > existing.deleted_at { tombstonesByKey[key] = t }
            } else {
                tombstonesByKey[key] = t
            }
        }

        var spacesByID: [String: CentralSpace] = [:]
        for s in local.spaces + remote.spaces {
            if let tomb = tombstonesByKey["space:\(s.id)"], tomb.deleted_at >= s.updated_at {
                continue
            }
            if let existing = spacesByID[s.id] {
                spacesByID[s.id] = s.updated_at >= existing.updated_at ? s : existing
            } else {
                spacesByID[s.id] = s
            }
        }

        // Enforce single-space membership per (machine_id, block_id) by latest added_at.
        var ownerByBlock: [String: (spaceID: String, addedAt: TimeInterval)] = [:]
        for (sid, s) in spacesByID {
            for m in s.members {
                let key = "\(m.machine_id)/\(m.block_id)"
                if let tomb = tombstonesByKey["member:\(key)"], tomb.deleted_at >= m.added_at {
                    continue
                }
                if let cur = ownerByBlock[key] {
                    if m.added_at > cur.addedAt { ownerByBlock[key] = (sid, m.added_at) }
                } else {
                    ownerByBlock[key] = (sid, m.added_at)
                }
            }
        }

        var mergedSpaces = Array(spacesByID.values).map { s -> CentralSpace in
            var copy = s
            copy.members = s.members.filter { m in
                let key = "\(m.machine_id)/\(m.block_id)"
                return ownerByBlock[key]?.spaceID == s.id
            }
            return copy
        }
        mergedSpaces.sort {
            if $0.order != $1.order { return $0.order < $1.order }
            return $0.id < $1.id
        }

        return SpaceManifest(
            version: max(local.version, remote.version),
            revision: max(local.revision, remote.revision),
            spaces: mergedSpaces,
            tombstones: Array(tombstonesByKey.values)
        )
    }

    // MARK: - Private Helpers

    private func addDiscoveredMember(
        machine: String,
        block: String,
        window: String?,
        to spaceID: String,
        ts: TimeInterval,
        changed: inout Bool
    ) {
        guard !machine.isEmpty, !block.isEmpty else { return }
        if isMemberTombstoned(machine: machine, block: block) { return }
        // Keep existing assignment if the user already placed this block in a space.
        if space(for: machine, block: block) != nil { return }
        guard let i = manifest.spaces.firstIndex(where: { $0.id == spaceID }) else { return }
        let nextOrder = (manifest.spaces[i].members.map(\.order).max() ?? -1) + 1
        manifest.spaces[i].members.append(SpaceMember(
            machine_id: machine,
            block_id: block,
            window_id: window,
            order: nextOrder,
            added_at: ts
        ))
        manifest.spaces[i].updated_at = ts
        changed = true
    }

    private func isSpaceTombstoned(name: String) -> Bool {
        manifest.tombstones.contains {
            $0.kind == .space && $0.name?.caseInsensitiveCompare(name) == .orderedSame
        }
    }

    private func isMemberTombstoned(machine: String, block: String) -> Bool {
        manifest.tombstones.contains {
            $0.kind == .member && $0.machine_id == machine && $0.block_id == block
        }
    }

    private func pruneTombstones(before cutoff: TimeInterval, changed: inout Bool) {
        let before = manifest.tombstones.count
        manifest.tombstones.removeAll { $0.deleted_at < cutoff }
        if manifest.tombstones.count != before { changed = true }
    }

    private func save() {
        let dir = fileURL.deletingLastPathComponent()
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        guard let data = try? JSONEncoder().encode(manifest) else { return }
        try? data.write(to: fileURL, options: .atomic)
    }
}
