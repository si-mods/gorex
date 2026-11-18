import Foundation
import Observation

/// The spaces as this device keeps them (docs/projects.md, Spaces). A
/// space is only a name on the hosts' projects; this device keeps their
/// order and colors, the spaces made here that no project carries yet,
/// the renames still to reach hosts that were away, the space shown, and
/// each host's last projects, to show while it is away. Nothing of it is
/// synced: each client keeps its own.
@MainActor
@Observable
final class SpaceBook {
    /// A space's rename, as a host is to take it (gorex.space.rename).
    struct Rename: Codable, Equatable {
        var from: String
        var to: String
    }

    private struct Saved: Codable {
        var order: [String] = []
        var colors: [String: String] = [:]
        var made: [String] = []
        var renames: [String: [Rename]] = [:]
    }

    static let savedKey = "spaces.v2"
    static let chosenKey = "spaces.chosen"

    /// The spaces in the order the user gave them; one not here comes
    /// after them, by name.
    private(set) var order: [String]
    /// Each space's color, by name: a ProjectColor ID.
    private(set) var colors: [String: String]
    /// The spaces made on this device that no project carries yet: they
    /// stay in the picker, empty, until a project moves into one.
    private(set) var made: [String]
    /// The renames each host is still to take, by host ID, in order.
    private(set) var renames: [String: [Rename]]
    /// Each host's projects as last seen, by host ID.
    private(set) var listings: [String: ProjectListing]
    /// The space shown; "" is Default.
    var chosen: String {
        didSet { defaults.set(chosen, forKey: Self.chosenKey) }
    }

    @ObservationIgnored private let defaults: UserDefaults
    @ObservationIgnored private let listingsURL: URL

    init(defaults: UserDefaults = .standard, listingsURL: URL = SpaceBook.defaultListingsURL()) {
        self.defaults = defaults
        self.listingsURL = listingsURL
        let saved = defaults.data(forKey: Self.savedKey).flatMap { try? JSONDecoder().decode(Saved.self, from: $0) } ?? Saved()
        order = saved.order
        colors = saved.colors
        made = saved.made
        renames = saved.renames
        chosen = defaults.string(forKey: Self.chosenKey) ?? ""
        listings = (try? Data(contentsOf: listingsURL)).flatMap { try? JSONDecoder().decode([String: ProjectListing].self, from: $0) } ?? [:]
    }

    static func defaultListingsURL() -> URL {
        let dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first
            ?? FileManager.default.temporaryDirectory
        return dir.appendingPathComponent("projects-cache.json")
    }

    // MARK: The picker

    /// The spaces the picker lists: Default, the spaces the projects carry,
    /// and those made here, in the user's order; new ones after, by name.
    func spaces(carried: Set<String>) -> [String] {
        var all = carried.union(made)
        all.insert("")
        let ordered = order.filter { all.contains($0) }
        let rest = all.subtracting(ordered).sorted { a, b in
            if a.isEmpty != b.isEmpty { return a.isEmpty }
            return a.localizedStandardCompare(b) == .orderedAscending
        }
        return ordered + rest
    }

    /// Forgets as made the spaces a project carries now: once none does
    /// again, such a space drops from the picker.
    func settle(carried: Set<String>) {
        let left = made.filter { !carried.contains($0) }
        if left != made {
            made = left
            save()
        }
    }

    /// Adds a space, empty, to the picker, and shows it; nil for a name of
    /// only spaces. A space that is there already is shown.
    @discardableResult
    func add(_ name: String, carried: Set<String>) -> String? {
        let n = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !n.isEmpty, n != SpaceName.shown("") else { return nil }
        if !carried.contains(n), !made.contains(n) {
            made.append(n)
            save()
        }
        chosen = n
        return n
    }

    /// Removes a space made here that no project carries.
    func remove(_ name: String) {
        made.removeAll { $0 == name }
        order.removeAll { $0 == name }
        colors[name] = nil
        if chosen == name { chosen = "" }
        save()
    }

    /// Sets the order of the spaces, as Edit Spaces leaves them.
    func reorder(_ spaces: [String]) {
        order = spaces
        save()
    }

    func setColor(_ color: String?, for space: String) {
        colors[space] = color
        save()
    }

    // MARK: Renames

    /// Renames a space: here at once, and on each host given, which may be
    /// away; each takes it as it connects (sent). Default, and a name of
    /// only spaces, are not renamed.
    func rename(_ from: String, to name: String, on hosts: [String]) {
        let to = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !from.isEmpty, !to.isEmpty, to != from, to != SpaceName.shown("") else { return }
        order = order.contains(to) ? order.filter { $0 != from } : order.map { $0 == from ? to : $0 }
        if colors[to] == nil { colors[to] = colors[from] }
        colors[from] = nil
        made = made.contains(to) ? made.filter { $0 != from } : made.map { $0 == from ? to : $0 }
        for h in hosts { renames[h, default: []].append(Rename(from: from, to: to)) }
        if chosen == from { chosen = to }
        save()
    }

    /// Forgets a rename the host took.
    func sent(_ r: Rename, to host: String) {
        guard var rs = renames[host], let i = rs.firstIndex(of: r) else { return }
        rs.remove(at: i)
        renames[host] = rs.isEmpty ? nil : rs
        save()
    }

    /// The space a host's project shows in: its own, with the renames the
    /// host is still to take.
    func space(_ space: String, on host: String) -> String {
        (renames[host] ?? []).reduce(space) { $0 == $1.from ? $1.to : $0 }
    }

    // MARK: Offline copies

    /// Keeps what a host with projects shows now, for while it is away.
    func remember(_ listing: ProjectListing, for host: String) {
        guard listings[host] != listing else { return }
        listings[host] = listing
        saveListings()
    }

    /// Forgets a host, as it is removed.
    func forget(host: String) {
        if listings.removeValue(forKey: host) != nil { saveListings() }
        if renames.removeValue(forKey: host) != nil { save() }
    }

    /// A host's projects as last seen, in the spaces they show in here.
    func listing(for host: String) -> ProjectListing? {
        guard var l = listings[host] else { return nil }
        for i in l.projects.indices { l.projects[i].space = space(l.projects[i].space, on: host) }
        l.offered = l.offered.map { $0.map { space($0, on: host) } }
        return l
    }

    // MARK: Storage

    private func save() {
        let s = Saved(order: order, colors: colors, made: made, renames: renames)
        defaults.set(try? JSONEncoder().encode(s), forKey: Self.savedKey)
    }

    private func saveListings() {
        guard let data = try? JSONEncoder().encode(listings) else { return }
        try? FileManager.default.createDirectory(at: listingsURL.deletingLastPathComponent(), withIntermediateDirectories: true)
        try? data.write(to: listingsURL, options: .atomic)
    }

    /// Drops what the app kept of spaces before projects lived on their
    /// hosts: the manifest of docs/ios-centralized-spaces.md, what the
    /// hosts' sessions reported to it, its sync settings, each host's last
    /// machine, the Grouping setting and the space shown. Nothing of it is
    /// carried over: the hosts keep the projects now.
    static func dropOldData(defaults: UserDefaults = .standard, directory: URL? = nil) {
        for key in ["spaceSync.icloud", "spaceSync.desktop", "spaceSync.primaryHost", "hostMachines", "grouping", "space"] {
            defaults.removeObject(forKey: key)
        }
        guard let dir = directory ?? FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first else { return }
        for name in ["spaces-manifest.json", "spaces-manifest.reported.json"] {
            try? FileManager.default.removeItem(at: dir.appendingPathComponent(name))
        }
    }
}
