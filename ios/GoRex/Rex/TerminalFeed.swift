import Foundation

/// A terminal stream's bytes, framed (TerminalStream) and their snapshot
/// restored (VTerminal) on a queue of their own, then handed to the main
/// actor (#844). The queue takes the bytes in the order they arrived and
/// the main queue takes the results in the order the queue made them, both
/// first in, first out: the terminal gets the bytes in order, and the
/// screen of a snapshot before the output that follows it. A drop goes the
/// same way, after the bytes that came before it.
nonisolated final class TerminalFeed: @unchecked Sendable {
    /// JSON values and bytes, which nothing mutates.
    enum Event: @unchecked Sendable {
        /// The stream's snapshot, restored; nil for one that could not be read.
        case snapshot(Restored?)
        case output(Data)
        /// A reflowed full-screen replay produced when the server PTY grid
        /// differs from the view's size (e.g. desktop holds a wider grid while
        /// the phone watches streaming output).
        case reflow(replay: Data, screen: Data)
        case message([String: Any])
        /// The connection dropped, with its close code, while the
        /// session's generation was the one given.
        case drop(Int, generation: Int)
    }

    /// A snapshot restored at the view's size.
    struct Restored {
        /// The escape sequences that draw its screen from a reset terminal.
        let replay: Data
        /// The reset and the replay: what the terminal gets.
        let screen: Data
        /// Its default colours, as a theme message gives them.
        let theme: [String: Any]
    }

    /// The view's size in cells, and a cell's in pixels.
    struct Size: Sendable, Equatable {
        var cols, rows, cellWidth, cellHeight: Int
    }

    private let queue = DispatchQueue(label: "dev.gorex.ios.terminal-feed", qos: .userInitiated)
    private let deliver: @MainActor @Sendable ([Event]) -> Void
    private let reflowInterval: TimeInterval
    /// Used on queue only.
    private var stream = TerminalStream(nonce: 0)
    /// Shadow terminal mirroring the server PTY at its own grid size, used to
    /// emulate cursor-up/erase streaming redraws faithfully before reflowing
    /// them to the phone's narrower viewport.
    private var shadow: VTerminal?
    private var serverSize: (cols: Int, rows: Int)?
    private var viewSize: Size?
    private var wasMismatched = false
    private var dirtyReflow = false
    private var pendingReflow: DispatchWorkItem?

    /// A feed that hands its events to deliver, on the main actor.
    init(reflowInterval: TimeInterval = 0.08, deliver: @escaping @MainActor @Sendable ([Event]) -> Void) {
        self.reflowInterval = reflowInterval
        self.deliver = deliver
    }

    /// Starts a stream of the nonce, as a HELLO does: the bytes that follow
    /// begin with a snapshot.
    func start(nonce: UInt64) {
        queue.async {
            self.stream = TerminalStream(nonce: nonce)
            self.shadow = nil
            self.serverSize = nil
            self.wasMismatched = false
            self.dirtyReflow = false
            self.pendingReflow?.cancel()
            self.pendingReflow = nil
        }
    }

    /// Cancels any pending reflow timer when the session closes or reconnects.
    func stop() {
        queue.async {
            self.dirtyReflow = false
            self.pendingReflow?.cancel()
            self.pendingReflow = nil
        }
    }

    /// Updates the view's grid size so shadow reflow tracks layout/zoom changes.
    func resize(_ size: Size) {
        queue.async {
            guard self.viewSize != size else { return }
            self.viewSize = size
            guard self.shadow != nil, self.serverSize != nil else { return }
            if self.isMismatched {
                self.wasMismatched = true
                self.dirtyReflow = true
                self.scheduleReflow()
            } else if self.wasMismatched {
                self.wasMismatched = false
                if let event = self.makeReflowEvent() { self.send([event]) }
            }
        }
    }

    /// Takes bytes of the stream; a snapshot among them is drawn at size.
    func feed(_ bytes: Data, size: Size?) {
        queue.async {
            if let size { self.viewSize = size }
            var events: [Event] = []
            for item in self.stream.feed(bytes) {
                switch item {
                case .snapshot(let s):
                    self.shadow = VTerminal(snapshot: s, retainContinuation: true)
                    self.serverSize = nil
                    self.wasMismatched = false
                    self.dirtyReflow = false
                    self.pendingReflow?.cancel()
                    self.pendingReflow = nil
                    events.append(.snapshot(Self.restore(s, size: self.viewSize)))
                case .output(let o):
                    self.shadow?.write(o)
                    if self.isMismatched {
                        self.wasMismatched = true
                        self.dirtyReflow = true
                        self.scheduleReflow()
                    } else {
                        events.append(.output(o))
                    }
                case .message(let m):
                    if m["type"] as? String == "resize",
                       let cols = (m["cols"] as? Int) ?? (m["columns"] as? Int),
                       let rows = m["rows"] as? Int, cols > 0, rows > 0 {
                        self.serverSize = (cols, rows)
                        self.shadow?.resize(cols: cols, rows: rows,
                                            cellWidth: self.viewSize?.cellWidth ?? 0,
                                            cellHeight: self.viewSize?.cellHeight ?? 0)
                        if self.isMismatched {
                            self.wasMismatched = true
                        } else if self.wasMismatched {
                            self.wasMismatched = false
                            if let event = self.makeReflowEvent() { events.append(event) }
                        }
                    }
                    events.append(.message(m))
                }
            }
            if !events.isEmpty { self.send(events) }
        }
    }

    /// Tells of a drop, after the bytes taken before it, with the
    /// generation of the session at the drop.
    func drop(_ code: Int, generation: Int = 0) {
        queue.async {
            var events: [Event] = []
            if self.dirtyReflow, let event = self.makeReflowEvent() {
                events.append(event)
            }
            events.append(.drop(code, generation: generation))
            self.send(events)
        }
    }

    /// Whether the server PTY's grid size differs from the phone view's size.
    private var isMismatched: Bool {
        guard shadow != nil, let srv = serverSize, let view = viewSize else { return false }
        return srv.cols != view.cols || srv.rows != view.rows
    }

    /// Coalesces reflow renders while streaming output arrives on a mismatched grid.
    private func scheduleReflow() {
        guard pendingReflow == nil else { return }
        let work = DispatchWorkItem { [weak self] in
            guard let self else { return }
            self.pendingReflow = nil
            guard self.dirtyReflow, let event = self.makeReflowEvent() else { return }
            self.send([event])
        }
        pendingReflow = work
        queue.asyncAfter(deadline: .now() + reflowInterval, execute: work)
    }

    /// Produces a flicker-free (mode 2026) reflowed replay of shadow at viewSize.
    private func makeReflowEvent() -> Event? {
        dirtyReflow = false
        pendingReflow?.cancel()
        pendingReflow = nil
        guard let shadow, let view = viewSize else { return nil }
        let replay: Data
        if shadow.size.cols == view.cols && shadow.size.rows == view.rows {
            replay = shadow.replay()
        } else if let r = shadow.reflowedReplay(cols: view.cols, rows: view.rows, cellWidth: view.cellWidth, cellHeight: view.cellHeight) {
            replay = r
        } else {
            return nil
        }
        let screen = Data("\u{1b}c\u{1b}[3J\u{1b}[?2026h".utf8) + replay + Data("\u{1b}[?2026l".utf8)
        return .reflow(replay: replay, screen: screen)
    }

    /// Decodes a snapshot, draws it at size when given, and turns it into
    /// the escape sequences that draw it; nil for a bad one.
    static func restore(_ snapshot: Data, size: Size?) -> Restored? {
        guard let t = VTerminal(snapshot: snapshot) else { return nil }
        // Drawn at the view's size, which the block takes next.
        if let size { t.resize(cols: size.cols, rows: size.rows, cellWidth: size.cellWidth, cellHeight: size.cellHeight) }
        let replay = t.replay()
        // A reset first: the snapshot of a new stream replaces the screen.
        return Restored(replay: replay, screen: Data("\u{1b}c".utf8) + replay, theme: t.theme)
    }

    private func send(_ events: [Event]) {
        let deliver = deliver
        DispatchQueue.main.async { MainActor.assumeIsolated { deliver(events) } }
    }
}
