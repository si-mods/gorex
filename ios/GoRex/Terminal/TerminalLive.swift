import Foundation
import Observation

/// The terminal activity's logic, apart from ActivityKit, which a Driver
/// stands for (docs/IOS-006-live-activities.md, Terminal activity): one
/// activity at a time, for the terminal on screen; its updates, at most one
/// per interval; and its work at the end of the app's grace on leaving
/// (BackgroundGrace), until which the streams stay open, before they are
/// suspended as they were before.
final class TerminalLive {
    /// What ActivityKit does for it; a test's records the calls.
    protocol Driver: AnyObject {
        /// Whether activities may start: Settings › Live Activities on.
        var enabled: Bool { get }
        /// Whether the activity started last still runs: iOS ends one after
        /// 8 hours.
        var running: Bool { get }
        /// Whether the user dismissed the activity started last.
        var dismissed: Bool { get }
        /// Starts an activity, in place of any other.
        func start(_ attributes: TerminalActivity, _ state: TerminalActivity.ContentState)
        /// Updates it. Without done, the update asks for background time of
        /// its own to finish in; with done, it asks for none, and calls done
        /// once ActivityKit has it, as a background task ends.
        func update(_ state: TerminalActivity.ContentState, stale: Date?, done: (() -> Void)?)
        /// Ends it, dismissed at once.
        func end()
    }

    /// The terminal it shows. state reads the host and the stream as of
    /// the moment, with the line only when a record's message is it; a
    /// change of what it read updates the activity. readLine reads the
    /// screen's line (TerminalActivity.screenLine), maybe later, maybe
    /// never, as a terminal not drawn yet has no screen.
    struct Source {
        let attributes: TerminalActivity
        let state: () -> TerminalActivity.ContentState
        let readLine: (_ done: @escaping @MainActor @Sendable (String) -> Void) -> Void
    }

    /// Runs work after a wait, in seconds; a test runs it when it chooses.
    typealias Schedule = (TimeInterval, @escaping @MainActor () -> Void) -> Void

    let grace: BackgroundGrace
    private let driver: Driver
    /// The least time between two updates, in seconds.
    private let interval: TimeInterval
    private let now: () -> Date
    private let schedule: Schedule

    /// The screen whose terminal it shows, and the terminal.
    private(set) var owner: UUID?
    private var source: Source?
    /// The host of the terminal shown.
    var host: UUID? { source?.attributes.host }
    /// Its work's key with the grace.
    private var key: ObjectIdentifier { ObjectIdentifier(self) }
    /// The screen whose activity the user dismissed: it starts none again.
    private var declined: UUID?
    /// The line read last, and whether output came since it was read.
    private var line = ""
    private var lineDirty = false
    /// The state sent last, and when.
    private(set) var sent: TerminalActivity.ContentState?
    private var lastUpdate: Date?
    /// The update waiting, by its number; nil for none.
    private var due: Int?
    private var count = 0
    /// When the app left the foreground; nil while in front.
    private var away: Date?
    /// Whether the grace ran out: the streams are suspended, and the
    /// activity says so until the app is in front again.
    private(set) var paused = false

    init(driver: Driver, interval: TimeInterval = 2, now: @escaping () -> Date = Date.init,
         schedule: @escaping Schedule = WidgetPublisher.after, grace: BackgroundGrace) {
        self.driver = driver
        self.interval = interval
        self.now = now
        self.schedule = schedule
        self.grace = grace
    }

    /// A terminal screen shows a terminal, in the foreground: its activity
    /// starts, in place of another screen's or another terminal's. The same
    /// terminal again only updates, or starts anew once iOS ended it; one the
    /// user dismissed stays so for the screen.
    func show(_ s: Source, owner o: UUID) {
        noteDismissed()
        let same = owner == o && source?.attributes == s.attributes
        if !same {
            if owner != nil { driver.end() }
            owner = o
            sent = nil
            line = ""
            due = nil
        }
        source = s
        lineDirty = true
        if !same || !driver.running { start() }
        publish()
    }

    /// The screen closed: its activity ends, dismissed at once, and its
    /// work at the grace's end is not to run. Another screen's close does
    /// nothing.
    func close(owner o: UUID) {
        guard owner == o else { return }
        grace.unregister(key)
        driver.end()
        owner = nil
        source = nil
        declined = nil
        sent = nil
        due = nil
        paused = false
    }

    /// The terminal printed: its last line may have changed.
    func outputChanged() {
        lineDirty = true
        changed()
    }

    /// The app left the foreground: the streams stay open for the grace,
    /// whose end calls suspend for the screen to suspend them, and the
    /// activity says it is paused. Each update meanwhile goes stale when
    /// the grace would end, should the app not get to say so.
    func background(suspend: @escaping () -> Void) {
        away = now()
        paused = false
        grace.register(key) { [weak self] finished in
            suspend()
            if let self { self.pause(finished) } else { finished() }
        }
        publish(force: true)
    }

    /// The app is in front again: its work at the grace's end is not to
    /// run, with nothing to resume; true when it ran and the screen
    /// resumes the streams. The activity is sent again without a stale
    /// date; one iOS ended starts anew.
    @discardableResult
    func active() -> Bool {
        grace.unregister(key)
        let resume = paused
        away = nil
        paused = false
        noteDismissed()
        if owner != nil, !driver.running { start() }
        publish(force: true)
        return resume
    }

    /// Something the activity shows changed: an update within the interval
    /// of the last one waits for its end, and those that come meanwhile
    /// join it.
    func changed() {
        guard due == nil, source != nil, !paused else { return }
        count += 1
        let n = count
        due = n
        let wait = lastUpdate.map { interval - now().timeIntervalSince($0) } ?? 0
        schedule(max(0, wait)) { [weak self] in
            guard let self, self.due == n else { return }
            self.publish()
        }
    }

    /// The user dismissed the screen's activity: the screen starts none again.
    private func noteDismissed() {
        if let owner, driver.dismissed { declined = owner }
    }

    private func start() {
        guard let source, driver.enabled, declined != owner else { return }
        let s = state(source)
        driver.start(source.attributes, s)
        sent = s
        lastUpdate = now()
    }

    /// Whether an activity shows, for updates to go to.
    private var showing: Bool { driver.enabled && driver.running }

    /// Builds the state, watching what it reads, and sends it unless it is
    /// the one sent last; forced, it sends it anyway, with its stale date.
    private func publish(force: Bool = false) {
        due = nil
        guard let source, !paused else { return }
        let s = withObservationTracking {
            state(source)
        } onChange: { [weak self] in
            Task { @MainActor in self?.changed() }
        }
        guard showing else { return }
        if force || s != sent {
            driver.update(s, stale: away.map { $0.addingTimeInterval(grace.limit + 5) }, done: nil)
            sent = s
            lastUpdate = now()
        }
        // A new line comes as a change, in the next update.
        if lineDirty {
            lineDirty = false
            let o = owner
            source.readLine { [weak self] l in
                guard let self, self.owner == o, l != self.line else { return }
                self.line = l
                self.changed()
            }
        }
    }

    private func state(_ source: Source) -> TerminalActivity.ContentState {
        // A line the state has, a record's message, wins.
        var s = source.state()
        if s.line.isEmpty { s.line = line }
        return s
    }

    /// The grace ran out: the activity says the stream closed, stale now,
    /// within the grace's background time, which ends once it is sent.
    private func pause(_ finished: @escaping () -> Void) {
        paused = true
        due = nil
        guard var s = sent, showing else { return finished() }
        s.connected = false
        sent = s
        driver.update(s, stale: now(), done: finished)
    }

    /// The activity's state of a block's terminal: the window's title, the
    /// tile, the block's most urgent record, the line cut short: the
    /// record's message, in any state, which says what the program does or
    /// waits for; else, done or failed, the last message it had; else,
    /// for a program that reports none, the screen's.
    static func state(title: String, program: String?, records: [Host.ProgramStatus], seen: Date?,
                      line screen: String = "", connected: Bool) -> TerminalActivity.ContentState {
        let u = Host.ProgramStatus.mostUrgent(records, seen: seen)
        let r = u.flatMap { $0.state == "idle" ? nil : $0 }
        func text(_ s: String?) -> String? {
            let t = s?.split(whereSeparator: \.isNewline).joined(separator: " ").trimmingCharacters(in: .whitespaces)
            return t?.isEmpty == false ? t : nil
        }
        let finished = u?.state == "done" || u?.state == "error"
        let line = text(u?.msg) ?? (finished ? text(u?.lastMsg) : nil) ?? screen
        return .init(title: TerminalActivity.cut(title), program: WidgetPublisher.program(r?.app, block: program),
                     record: r.map { .init(state: $0.state, kind: $0.kind, progress: $0.progress, title: $0.title.map { TerminalActivity.cut($0) }, updated: $0.updated) },
                     line: TerminalActivity.cut(line, TerminalActivity.maxLine, bytes: TerminalActivity.maxLineBytes), connected: connected)
    }
}
