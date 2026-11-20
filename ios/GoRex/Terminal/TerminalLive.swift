import Foundation
import Observation

/// The terminal activity's logic, apart from ActivityKit, which a Driver
/// stands for (docs/ios-live-activities.md, Terminal activity): one
/// activity at a time, for the terminal on screen; its updates, at most one
/// per interval; and the grace on leaving the app, during which the
/// streams stay open, before they are suspended as they were before.
final class TerminalLive {
    /// What ActivityKit does for it; a test's records the calls.
    protocol Driver: AnyObject {
        /// Whether activities may start: Settings › Live Activities on.
        var enabled: Bool { get }
        /// Whether the activity started last still runs: iOS ends one after
        /// 8 hours.
        var running: Bool { get }
        /// Starts an activity, in place of any other.
        func start(_ attributes: TerminalActivity, _ state: TerminalActivity.ContentState)
        func update(_ state: TerminalActivity.ContentState, stale: Date?)
        /// Ends it, dismissed at once.
        func end()
    }

    /// The terminal it shows. state reads the host and the stream, all but
    /// the line, as of the moment, given when the app left the foreground,
    /// nil while in front; a change of what it read updates the activity.
    /// readLine reads the screen's last line, maybe later, maybe never, as
    /// a terminal not drawn yet has no screen.
    struct Source {
        let attributes: TerminalActivity
        let state: (_ away: Date?) -> TerminalActivity.ContentState
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
    /// terminal again only updates, or starts anew once iOS ended it.
    func show(_ s: Source, owner o: UUID) {
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

    /// The screen closed: its activity ends, dismissed at once, and a grace
    /// still running ends with it. Another screen's close does nothing.
    func close(owner o: UUID) {
        guard owner == o else { return }
        _ = grace.cancel()
        driver.end()
        owner = nil
        source = nil
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
    /// then suspend calls the screen to suspend them, and the activity
    /// says it is paused. Each update meanwhile goes stale when the grace
    /// would end, should the app not get to say so.
    func background(suspend: @escaping () -> Void) {
        away = now()
        paused = false
        grace.start { [weak self] in
            suspend()
            self?.pause()
        }
        publish(force: true)
    }

    /// The app is in front again: a grace still running ends, with nothing
    /// to resume; true when the grace had run out and the screen resumes
    /// the streams. The activity is sent again without a stale date; one
    /// iOS ended starts anew.
    @discardableResult
    func active() -> Bool {
        _ = grace.cancel()
        let resume = paused
        away = nil
        paused = false
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

    private func start() {
        guard let source, driver.enabled else { return }
        let s = state(source)
        driver.start(source.attributes, s)
        sent = s
        lastUpdate = now()
    }

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
        if force || s != sent, driver.enabled, driver.running {
            driver.update(s, stale: away.map { $0.addingTimeInterval(grace.limit + 5) })
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
        var s = source.state(away)
        s.line = line
        return s
    }

    /// The grace ran out: the activity says the stream closed, stale now.
    private func pause() {
        paused = true
        due = nil
        guard var s = sent else { return }
        s.connected = false
        if driver.running { driver.update(s, stale: now()) }
        sent = s
    }

    /// The activity's state of a block's terminal: the window's title, the
    /// tile, the block's most urgent record, the line cut short. In the
    /// background, records that came since the app left count as not seen,
    /// though the screen, still open, marked them.
    static func state(title: String, program: String?, records: [Host.ProgramStatus], seen: Date?, away: Date?,
                      line: String = "", connected: Bool) -> TerminalActivity.ContentState {
        let seen = away.map { a in seen.map { min($0, a) } } ?? seen
        let r = Host.ProgramStatus.mostUrgent(records, seen: seen).flatMap { $0.state == "idle" ? nil : $0 }
        return .init(title: TerminalActivity.cut(title), program: WidgetPublisher.program(r?.app, block: program),
                     record: r.map { .init(state: $0.state, kind: $0.kind, progress: $0.progress, title: $0.title.map { TerminalActivity.cut($0) }, updated: $0.updated) },
                     line: TerminalActivity.cut(line, TerminalActivity.maxLine), connected: connected)
    }
}

/// The time iOS gives a backgrounded app to finish (a background task),
/// used to keep the terminal's streams open for a while: it ends after
/// limit seconds, or as iOS takes the time back, whichever comes first,
/// calling its work once, and the task ends exactly once, at the end or as
/// the app comes back.
final class BackgroundGrace {
    /// Asks for background time, calling expired as iOS takes it back; nil
    /// when iOS gives none.
    typealias Begin = (_ expired: @escaping @MainActor () -> Void) -> Int?

    let limit: TimeInterval
    private let begin: Begin
    private let end: (Int) -> Void
    private let now: () -> Date
    private let schedule: TerminalLive.Schedule

    private var task: Int?
    private var work: (() -> Void)?
    private var generation = 0
    /// When the app was last kept running: the grace's end.
    private(set) var keptUntil: Date?

    var running: Bool { work != nil }

    init(limit: TimeInterval = 25, begin: @escaping Begin, end: @escaping (Int) -> Void,
         now: @escaping () -> Date = Date.init, schedule: @escaping TerminalLive.Schedule = WidgetPublisher.after) {
        self.limit = limit
        self.begin = begin
        self.end = end
        self.now = now
        self.schedule = schedule
    }

    /// Starts the grace, which calls work as it ends. Without background
    /// time, work runs at once, as the app may stop any moment.
    func start(_ work: @escaping () -> Void) {
        guard !running else { return }
        generation += 1
        let g = generation
        self.work = work
        task = begin { [weak self] in self?.expire(g) }
        guard task != nil else { return expire(g) }
        schedule(limit) { [weak self] in self?.expire(g) }
    }

    /// The grace ends with its work, then its task.
    private func expire(_ g: Int) {
        guard g == generation, let w = work else { return }
        work = nil
        keptUntil = now()
        w()
        finish()
    }

    /// The app came back during the grace: it ends without its work. True
    /// when one was running.
    func cancel() -> Bool {
        guard running else { return false }
        generation += 1
        work = nil
        keptUntil = now()
        finish()
        return true
    }

    private func finish() {
        guard let t = task else { return }
        task = nil
        end(t)
    }

    /// Since when the app, in the background since away, was likely not
    /// running: nil while a grace runs, else the later of away and the
    /// grace's end (HostStore.suspended).
    func awake(since away: Date?) -> Date? {
        guard !running else { return nil }
        return [away, keptUntil].compactMap(\.self).max()
    }
}
