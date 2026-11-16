import Foundation

/// The pane a device last viewed of a window: a local cache for the next
/// opening, never a source of truth. It is not the session's focus and is
/// never sent to the server.
struct ViewedBlockKey: Hashable {
    /// Host.spaceMachineKey: the machine_id, or the host's UUID in Demo Mode.
    let machine: String
    let session: String
    let window: String
}

/// The last viewed pane of each window, in UserDefaults, by App install.
/// A block the window no longer holds is ignored and forgotten by the
/// caller, as session.view stays the only source of what exists.
final class ViewedBlockStore {
    static let shared = ViewedBlockStore()

    private let defaults: UserDefaults
    private static let storageKey = "viewedBlocks.v1"

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
    }

    func block(for key: ViewedBlockKey) -> String? {
        entries[key.storageKey]
    }

    func set(_ block: String, for key: ViewedBlockKey) {
        var all = entries
        guard !block.isEmpty else {
            guard all.removeValue(forKey: key.storageKey) != nil else { return }
            defaults.set(all, forKey: Self.storageKey)
            return
        }
        guard all[key.storageKey] != block else { return }
        all[key.storageKey] = block
        defaults.set(all, forKey: Self.storageKey)
    }

    func forget(_ key: ViewedBlockKey) {
        var all = entries
        guard all.removeValue(forKey: key.storageKey) != nil else { return }
        defaults.set(all, forKey: Self.storageKey)
    }

    private var entries: [String: String] {
        defaults.dictionary(forKey: Self.storageKey) as? [String: String] ?? [:]
    }
}

private extension ViewedBlockKey {
    /// The dictionary key: the parts joined by a unit separator, which an
    /// ID cannot hold.
    var storageKey: String {
        [machine, session, window].joined(separator: "\u{1F}")
    }
}
