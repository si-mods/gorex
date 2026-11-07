import Foundation

extension Host.ProgramStatus {
    /// What the user saw of a done or error record: the record as it was
    /// then. Its time alone would not do: updated_at has whole seconds, and
    /// a record may have none.
    var seenKey: String { "\(id)|\(state)|\(updated?.timeIntervalSince1970 ?? -1)" }

    /// Whether it needs the user more than another, or as much and later.
    func outranks(_ o: Self) -> Bool {
        rank > o.rank || rank == o.rank && (updated ?? .distantPast) > (o.updated ?? .distantPast)
    }

    /// The record that most needs the user, the latest of those alike, nil
    /// without one, as the desktop's MostUrgent picks it. Done and error
    /// records the user saw do not count.
    static func mostUrgent(_ rs: [Self], seen: Set<String>) -> Self? {
        var best: Self?
        for r in rs {
            if r.state == "done" || r.state == "error", seen.contains(r.seenKey) { continue }
            if best.map(r.outranks) ?? true { best = r }
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

    /// How long ago, as Rex tells it: now under a minute, else in the
    /// largest whole unit of minutes, hours and days, narrow, in the
    /// locale's words.
    static func ago(_ t: Date, now: Date, locale: Locale = .autoupdatingCurrent) -> String {
        let s = Int(now.timeIntervalSince(t))
        if s < 60 { return String(localized: "now") }
        let (unit, n): (Duration.UnitsFormatStyle.Unit, Int) = s < 3600 ? (.minutes, s / 60 * 60) : s < 86400 ? (.hours, s / 3600 * 3600) : (.days, s / 86400 * 86400)
        return Duration.seconds(n).formatted(.units(allowed: [unit], width: .narrow).locale(locale))
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
                // The block whose program needs the user most, which the row opens.
                var pick: (block: String, status: ProgramStatus)?
                for x in [b] + w.tiled.filter({ $0 != b }) {
                    if let r = ProgramStatus.mostUrgent(statuses[x] ?? [], seen: seen[x] ?? []), pick.map({ r.outranks($0.status) }) ?? true {
                        pick = (x, r)
                    }
                }
                guard let pick, Inbox.states.contains(pick.status.state) else { return nil }
                return InboxItem(host: id, session: s.id, block: pick.block, title: title(w), program: programs[pick.block], status: pick.status)
            }
        }
    }
}
