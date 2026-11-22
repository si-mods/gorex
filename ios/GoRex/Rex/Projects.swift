import Foundation

/// A project's folder, as the list and the folder sheet show it, and the
/// check made before a tab opens in it (docs/MOD-002-projects.md).
enum ProjectFolder {
    /// Whether an error is the server's note that a project's folder is
    /// gone (conflict, folder_missing).
    static func missing(_ error: Error) -> Bool {
        guard let e = error as? RexError else { return false }
        return e.code == "conflict" && e.message.contains("folder_missing")
    }

    /// Fails as the server's folder_missing (conflict) when dir is not a
    /// folder the host can read, as gorex.dir.list answers it: the server
    /// would start a shell given a cwd that is gone at home, which a
    /// project's tab must not.
    static func ensure(_ dir: String, list: (String) async throws -> Host.Folders) async throws {
        if let code = try await list(dir).code {
            throw RexError(code: "conflict", message: "folder_missing: \(dir) (\(code))")
        }
    }

    /// A folder as the list shows it: the home directory as ~.
    static func short(_ dir: String) -> String {
        for prefix in ["/Users/", "/home/"] where dir.hasPrefix(prefix) {
            let rest = dir.dropFirst(prefix.count)
            guard let slash = rest.firstIndex(of: "/") else { return "~" }
            return "~" + rest[slash...]
        }
        return dir
    }

    /// A project's name by default: its folder's name.
    static func name(of dir: String) -> String {
        let trimmed = dir.trimmingCharacters(in: CharacterSet(charactersIn: "/\\"))
        return trimmed.split(whereSeparator: { $0 == "/" || $0 == "\\" }).last.map(String.init) ?? dir
    }
}

/// A space's name as the list shows it: "" is Default.
enum SpaceName {
    static func shown(_ space: String) -> String {
        space.isEmpty ? String(localized: "Default") : space
    }
}

/// Projects in order of last use, the latest first, as Codex lists them;
/// those not used for 30 days go apart, under Older (docs/MOD-002-projects.md).
/// A project never used, as one a migration made, counts as recent, after
/// those used.
enum ProjectOrder {
    static let older: TimeInterval = 30 * 24 * 60 * 60

    static func split<T>(_ items: [T], used: (T) -> Date?, name: (T) -> String, now: Date = .now) -> (recent: [T], older: [T]) {
        let sorted = items.sorted { a, b in
            let (x, y) = (used(a), used(b))
            if x != y { return (x ?? .distantPast) > (y ?? .distantPast) }
            return name(a).localizedStandardCompare(name(b)) == .orderedAscending
        }
        let cutoff = now.addingTimeInterval(-older)
        return (sorted.filter { used($0).map { $0 >= cutoff } ?? true },
                sorted.filter { used($0).map { $0 < cutoff } ?? false })
    }
}

/// A host's projects and its tabs with no project, as the list showed
/// them: kept on this device, to show greyed while the host is away
/// (docs/MOD-002-projects.md, Offline hosts). Only shown, never changed; it is
/// replaced as the host connects.
struct ProjectListing: Codable, Equatable {
    struct Tab: Codable, Equatable, Identifiable {
        var id: String
        var title: String
        /// The programs of its panes, the front one first, as the tiles
        /// showed them; nil in a copy a build before them wrote.
        var programs: [String?]?
    }

    struct Project: Codable, Equatable, Identifiable {
        var id: String
        var name: String
        var dir: String
        var space: String
        var used: Date?
        var color: String?
        var tabs: [Tab]
    }

    var projects: [Project] = []
    var loose: [Tab] = []
    /// The spaces the host offers that no project of it carries
    /// (gorex.space.list), as the old spaces of a layout it migrated.
    var offered: [String]? = nil

    init(projects: [Project] = [], loose: [Tab] = [], offered: [String]? = nil) {
        self.projects = projects
        self.loose = loose
        self.offered = offered
    }

    /// What a host with projects shows now: its sessions with a folder,
    /// and the tabs of the others.
    init(host: Host) {
        func tabs(_ s: Host.Session) -> [Tab] {
            (s.view?.windows ?? []).map { w in
                let ids = [w.block] + w.back.map { $0 as String? }
                return Tab(id: w.window_id, title: host.title(w),
                           programs: ids.map { $0.flatMap { host.blocks[$0]?.program } })
            }
        }
        for s in host.sessions {
            if let p = s.project {
                projects.append(Project(id: s.id, name: s.label.isEmpty ? ProjectFolder.name(of: p.dir) : s.label,
                                        dir: p.dir, space: p.space, used: p.used, color: p.color, tabs: tabs(s)))
            } else {
                loose += tabs(s)
            }
        }
        offered = host.offeredSpaces.isEmpty ? nil : host.offeredSpaces
    }

    /// The spaces its projects carry.
    var spaces: Set<String> { Set(projects.map(\.space)) }

    /// The spaces the picker lists for the host: those its projects carry,
    /// and those it offers.
    var pickerSpaces: Set<String> { spaces.union(offered ?? []) }
}

/// The colors a project or a space takes, as the desktop's presets
/// (internal/layout, Palette): an ID and its color as hex.
enum ProjectColor {
    static let all: [(id: String, hex: UInt32)] = [
        ("rose", 0xE54666), ("orange", 0xF76B15), ("amber", 0xFFB224),
        ("green", 0x30A46C), ("teal", 0x12A594), ("blue", 0x0090FF),
        ("indigo", 0x3E63DD), ("violet", 0x8E4EC6), ("pink", 0xD6409F),
    ]

    /// The hex of a color by its ID, or of a "#rrggbb" color; nil for none.
    static func hex(_ color: String?) -> UInt32? {
        guard let color, !color.isEmpty else { return nil }
        if let c = all.first(where: { $0.id == color }) { return c.hex }
        guard color.hasPrefix("#"), color.count == 7 else { return nil }
        return UInt32(color.dropFirst(), radix: 16)
    }
}
