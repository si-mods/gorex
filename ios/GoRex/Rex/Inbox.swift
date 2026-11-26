import Foundation

extension Host.ProgramStatus {
    /// Whether it needs the user more than another, or as much and later.
    func outranks(_ o: Self) -> Bool {
        rank > o.rank || rank == o.rank && (updated ?? .distantPast) > (o.updated ?? .distantPast)
    }

    /// The record that most needs the user, the latest of those alike, nil
    /// without one, as the desktop's MostUrgent picks it. Done and error
    /// records updated by seen were seen, and do not count.
    static func mostUrgent(_ rs: [Self], seen: Date?) -> Self? {
        var best: Self?
        for r in rs {
            if r.state == "done" || r.state == "error", let seen, r.updated ?? .distantPast <= seen { continue }
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
    /// Its window. The title and the program are not kept here: reading
    /// them in the list's body would make it depend on every block, so
    /// a row reads its own (Host.title, Block.program).
    let window: SessionView.Window
    let status: Host.ProgramStatus
    var id: String { "\(host)/\(session)/\(block)" }

    /// The first line: the record's title, else the window's.
    func heading(title: String) -> String { status.title ?? title }
    /// The second line: the record's message, else, once done or failed,
    /// what it did last, else the window's title; empty when that only
    /// repeats the first.
    func detail(title: String) -> String {
        let finished = status.state == "done" || status.state == "error"
        let d = status.msg ?? (finished ? status.lastMsg : nil) ?? title
        return d == heading(title: title) ? "" : d
    }

    /// Whether the item waits for the user, failed, or finished unseen.
    var needsAttention: Bool { Inbox.actionableStates.contains(status.state) }

    /// Whether a search matches it, by what its row shows.
    func matches(_ query: String, title: String) -> Bool {
        query.isEmpty || [heading(title: title), detail(title: title), title].contains { $0.localizedCaseInsensitiveContains(query) }
    }
}

enum Inbox {
    /// States that need the user's attention, in order of urgency.
    static let actionableStates = ["blocked", "error", "done"]
    /// The states the inbox groups, the most urgent first.
    static let states = actionableStates + ["working"]

    /// Counts driving the inbox toolbar badge and header summary.
    struct Summary: Equatable {
        let attentionCount: Int
        let hasError: Bool
        let workingCount: Int
    }

    /// Computes attention and working counts from inbox items.
    static func summary(_ items: [InboxItem]) -> Summary {
        var attention = 0
        var hasError = false
        var working = 0
        for item in items {
            switch item.status.state {
            case "error":
                attention += 1
                hasError = true
            case "blocked", "done":
                attention += 1
            case "working":
                working += 1
            default:
                break
            }
        }
        return Summary(attentionCount: attention, hasError: hasError, workingCount: working)
    }

    /// Formats a count for the toolbar badge; nil for zero, capped at 99+.
    static func badgeText(_ count: Int) -> String? {
        guard count > 0 else { return nil }
        return count > 99 ? "99+" : "\(count)"
    }

    /// Splits items into actionable groups (blocked, error, done) and
    /// background working items, newest first within each group.
    static func sections(_ items: [InboxItem]) -> (attention: [(state: String, items: [InboxItem])], working: [InboxItem]) {
        let attention = actionableStates.compactMap { state -> (state: String, items: [InboxItem])? in
            let some = items.filter { $0.status.state == state }
                .sorted { ($0.status.updated ?? .distantPast) > ($1.status.updated ?? .distantPast) }
            return some.isEmpty ? nil : (state, some)
        }
        let working = items.filter { $0.status.state == "working" }
            .sorted { ($0.status.updated ?? .distantPast) > ($1.status.updated ?? .distantPast) }
        return (attention, working)
    }

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
    var inbox: [InboxItem] { inbox(matching: "") }

    /// The same, those a search matches. Window titles are read only for a
    /// search, so without one the result does not depend on any title.
    func inbox(matching query: String) -> [InboxItem] {
        guard status == .online else { return [] }
        return sessions.flatMap { s in
            (s.view?.windows ?? []).compactMap { w -> InboxItem? in
                guard let b = w.block else { return nil }
                // The block whose program needs the user most, which the row opens.
                var pick: (block: String, status: ProgramStatus)?
                for x in [b] + w.panes.filter({ $0 != b }) {
                    if let r = blocks[x].flatMap({ ProgramStatus.mostUrgent($0.statuses ?? [], seen: $0.seen) }), pick.map({ r.outranks($0.status) }) ?? true {
                        pick = (x, r)
                    }
                }
                guard let pick, Inbox.states.contains(pick.status.state) else { return nil }
                let item = InboxItem(host: id, session: s.id, block: pick.block, window: w, status: pick.status)
                return query.isEmpty || item.matches(query, title: title(w)) ? item : nil
            }
        }
    }
}
