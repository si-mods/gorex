import Foundation

extension Host.ProgramStatus {
    /// The record that most needs the user, the latest of those alike, nil
    /// without one, as the desktop's MostUrgent picks it. Done and error
    /// records updated by seen were seen, and do not count; with no seen,
    /// nothing was.
    static func mostUrgent(_ rs: [Self], seen: Date?) -> Self? {
        var best: Self?
        for r in rs {
            if let seen, r.state == "done" || r.state == "error", (r.updated ?? .distantPast) <= seen { continue }
            guard let b = best else { best = r; continue }
            if r.rank > b.rank || r.rank == b.rank && (r.updated ?? .distantPast) > (b.updated ?? .distantPast) { best = r }
        }
        return best
    }
}

/// A window whose program needs the user, or works, as the inbox lists it.
struct InboxItem: Identifiable {
    let host: UUID
    let session: String
    /// The block its row opens.
    let block: String
    /// The window's title, as its row in the list has it.
    let title: String
    /// The foreground program of its block.
    let program: String?
    let status: Host.ProgramStatus
    var id: String { "\(host)/\(session)/\(block)" }

    /// The first line: the record's title, else the window's.
    var heading: String { status.title ?? title }
    /// The second line: the record's message, else the window's title;
    /// empty when that only repeats the first.
    var detail: String {
        let d = status.msg ?? title
        return d == heading ? "" : d
    }

    /// Whether a search matches it.
    func matches(_ query: String) -> Bool {
        query.isEmpty || [status.title, status.msg, title].contains { $0?.localizedCaseInsensitiveContains(query) == true }
    }
}

enum Inbox {
    /// The states the inbox groups, the most urgent first.
    static let states = ["blocked", "error", "done", "working"]

    /// Items by state, the most urgent first, the newest first within one;
    /// groups with none are left out.
    static func groups(_ items: [InboxItem]) -> [(state: String, items: [InboxItem])] {
        states.compactMap { state in
            let some = items.filter { $0.status.state == state }
                .sorted { ($0.status.updated ?? .distantPast) > ($1.status.updated ?? .distantPast) }
            return some.isEmpty ? nil : (state, some)
        }
    }
}

extension Host {
    /// The windows that need the user or work, each by the record of its
    /// blocks that most needs the user; none while not online.
    var inbox: [InboxItem] {
        guard status == .online else { return [] }
        return sessions.flatMap { s in
            (s.view?.windows ?? []).compactMap { w -> InboxItem? in
                guard let b = w.block else { return nil }
                let picks = ([b] + w.tiled.filter { $0 != b }).compactMap { ProgramStatus.mostUrgent(statuses[$0] ?? [], seen: seen[$0]) }
                guard let r = ProgramStatus.mostUrgent(picks, seen: nil), Inbox.states.contains(r.state) else { return nil }
                // As the list names a window.
                let title = w.name ?? titles[b].flatMap { $0.isEmpty ? nil : $0 } ?? w.label ?? ""
                return InboxItem(host: id, session: s.id, block: b, title: title, program: programs[b], status: r)
            }
        }
    }

    /// Marks what a window shows as seen, as the desktop's pane does: the
    /// done and error records of its blocks, until they change.
    func see(_ block: String) {
        let w = sessions.lazy.compactMap(\.view).flatMap { $0.windows ?? [] }.first { $0.block == block || $0.tiled.contains(block) }
        for b in Set([block] + (w?.tiled ?? [])) {
            if let t = statuses[b]?.compactMap(\.updated).max(), t > seen[b] ?? .distantPast { seen[b] = t }
        }
    }
}
