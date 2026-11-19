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
        case message([String: Any])
        /// The connection dropped, with its close code.
        case drop(Int)
    }

    /// A snapshot restored at the view's size.
    struct Restored {
        /// The escape sequences that draw its screen from a reset terminal.
        let replay: Data
        /// The reset and the replay: what the terminal gets.
        let screen: Data
        /// Its default colors, as a theme message gives them.
        let theme: [String: Any]
    }

    /// The view's size in cells, and a cell's in pixels.
    struct Size: Sendable, Equatable {
        var cols, rows, cellWidth, cellHeight: Int
    }

    private let queue = DispatchQueue(label: "dev.gorex.ios.terminal-feed", qos: .userInitiated)
    private let deliver: @MainActor @Sendable ([Event]) -> Void
    /// Used on queue only.
    private var stream = TerminalStream(nonce: 0)

    /// A feed that hands its events to deliver, on the main actor.
    init(deliver: @escaping @MainActor @Sendable ([Event]) -> Void) {
        self.deliver = deliver
    }

    /// Starts a stream of the nonce, as a HELLO does: the bytes that follow
    /// begin with a snapshot.
    func start(nonce: UInt64) {
        queue.async { self.stream = TerminalStream(nonce: nonce) }
    }

    /// Takes bytes of the stream; a snapshot among them is drawn at size.
    func feed(_ bytes: Data, size: Size?) {
        queue.async {
            let events = self.stream.feed(bytes).map { item -> Event in
                switch item {
                case .snapshot(let s): .snapshot(Self.restore(s, size: size))
                case .output(let o): .output(o)
                case .message(let m): .message(m)
                }
            }
            if !events.isEmpty { self.send(events) }
        }
    }

    /// Tells of a drop, after the bytes taken before it.
    func drop(_ code: Int) {
        queue.async { self.send([.drop(code)]) }
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
