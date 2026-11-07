import Foundation
import Observation
import os

private let log = Logger(subsystem: "dev.gorex.ios", category: "terminal")

/// What shows a session's screen: Ghostty's surface, in the view.
protocol TerminalOutput: AnyObject {
    /// Draws what the program printed.
    func write(_ bytes: Data)
    /// Sets the default colors, as a stream's theme message gives them.
    func setTheme(_ theme: [String: Any])
}

/// One block's terminal on the phone: its data stream fed to the view's
/// Ghostty surface, the block owned so that it takes the phone's size, and
/// the bytes Ghostty encodes for the keys typed written back to the block.
@Observable
final class TerminalSession {
    enum State: Equatable {
        case connecting
        case live
        case exited(Int)
        case failed(String)
    }

    let host: Host
    let session: String
    let block: String
    private(set) var state = State.connecting
    /// The terminal's default background, for the page around it. Until
    /// the stream gives one it is the last one seen on this host with this
    /// theme, else the theme's, so that the page opens in its color.
    private(set) var background: VTerminal.RGB?
    /// The progress a program reports (OSC 9;4), nil without one.
    var progress: Progress?

    struct Progress: Equatable {
        /// Done of the whole, nil while the program can't tell.
        var fraction: Double?
        var failed = false
    }

    /// The view, once it shows the session; what comes before waits for it.
    @ObservationIgnored weak var output: TerminalOutput? {
        didSet {
            if let theme { output?.setTheme(theme) }
            output?.write(pending)
            pending = Data()
        }
    }
    @ObservationIgnored private var pending = Data()
    /// The default colors: the theme chosen in Settings, else the
    /// snapshot's; a theme message from the server replaces them.
    @ObservationIgnored private var theme: [String: Any]?
    @ObservationIgnored private var stream = TerminalStream(nonce: 0)
    @ObservationIgnored private var data: DataConnection
    @ObservationIgnored private var size: (cols: Int, rows: Int, cw: Int, ch: Int)?
    @ObservationIgnored private var sizing: Task<Void, Never>?
    @ObservationIgnored private var closed = false
    /// Counts the starts and resumes: a task that awaited past a newer
    /// one, or past close, gives up.
    @ObservationIgnored private var generation = 0
    /// Drops in a row of connections that lasted less than the longest
    /// wait, and when the last one opened.
    @ObservationIgnored private var drops = 0
    @ObservationIgnored private var opened = ContinuousClock.now
    /// Bytes to write, and whether a write is on its way.
    @ObservationIgnored private var typed = Data()
    @ObservationIgnored private var typing = false

    init(host: Host, session: String, block: String) {
        self.host = host
        self.session = session
        self.block = block
        data = DataConnection(base: host.config.url)
        background = (UserDefaults.standard.dictionary(forKey: "backgrounds")?[backgroundKey] as? Int).map(VTerminal.RGB.init)
            ?? TerminalTheme.chosen.map { VTerminal.RGB($0.colors[16]) }
    }

    private var backgroundKey: String { "\(host.id) \(TerminalTheme.name)" }

    /// Notes the background the view shows, for the page and the next opening.
    func drew(background b: VTerminal.RGB) {
        guard b != background else { return }
        background = b
        var all = UserDefaults.standard.dictionary(forKey: "backgrounds") ?? [:]
        all[backgroundKey] = b.packed
        UserDefaults.standard.set(all, forKey: "backgrounds")
    }

    /// Opens the stream from the start: a ticket, a HELLO, a new snapshot.
    /// The stream before it ends. A closed session opens again.
    func start() {
        closed = false
        drops = 0
        open()
        write()
    }

    private func open() {
        state = .connecting
        generation += 1
        let g = generation
        data.close()
        Task {
            do {
                let t = try await host.live().call("data.connect", [
                    "session_id": session, "block_id": block,
                    "data_ref_id": "terminal", "subprotocol": "rex.data.terminal.v1",
                ]).payload["ticket"] as? String ?? ""
                guard g == generation else { return }
                // What ends this generation closes the connection it opens.
                let d = DataConnection(base: host.config.url)
                data = d
                wire(d)
                try await d.open(ticket: t)
                guard g == generation else { return }
                log.info("opened \(self.block, privacy: .public)")
                opened = .now
                stream = TerminalStream(nonce: d.nonce)
                await claim()
            } catch {
                guard g == generation else { return }
                // Opened by hand, it tells at once; after a drop, it tries again.
                if drops == 0 { state = .failed(error.localizedDescription) } else { retry(fresh: true) }
            }
        }
    }

    /// Continues the stream after the app comes back, or starts over when
    /// the server no longer has it. A stream that ended, as its window
    /// closed, stays so: Retry starts it.
    func resume() {
        guard !closed, going else { return }
        drops = 0
        rejoin()
    }

    private func rejoin() {
        host.reconnectIfNeeded()
        generation += 1
        let g = generation
        Task {
            do {
                try await data.resume()
                guard g == generation else { return }
                log.info("resumed \(self.block, privacy: .public)")
                opened = .now
                // A client that came back too late to resume lost the block.
                await claim()
            } catch {
                guard g == generation else { return }
                log.info("resume failed, starting over: \(error.localizedDescription, privacy: .public)")
                retry(fresh: true)
            }
        }
    }

    /// Owns the block, so that it takes this view's size. Not owned, the
    /// block keeps its size, and its stream goes on.
    private func claim() async {
        do {
            try await host.live().call("session.own_block", ["session_id": session, "block_ids": [block], "mode": "take"])
            sendSize()
        } catch {
            log.error("could not own \(self.block, privacy: .public): \(error.localizedDescription, privacy: .public)")
        }
    }

    /// Leaves the stream for a resume, as when the app goes to the background.
    func suspend() { data.suspend() }

    /// Ends the stream and gives the block back.
    func close() {
        closed = true
        generation += 1
        data.close()
        let (session, block) = (session, block)
        Task { _ = try? await host.control?.call("session.disown_block", ["session_id": session, "block_ids": [block]]) }
    }

    private func wire(_ d: DataConnection) {
        d.onBytes = { [weak self, weak d] bytes in
            guard let self, d === self.data else { return }
            self.receive(bytes)
        }
        d.onDrop = { [weak self, weak d] code in
            guard let self, d === self.data, !self.closed else { return }
            switch code {
            case 4000: self.state = .failed(String(localized: "The window closed."))
            case 1002, 1008: self.state = .failed(String(localized: "The server refused the terminal."))
            default: self.retry(fresh: code == 1000 || code == 1013)
            }
        }
    }

    /// Whether the stream is open or opening, not ended for good.
    private var going: Bool { state == .connecting || state == .live }

    /// Connects again after a drop, from the start or with a resume,
    /// waiting longer each time in a row, until it is time to give up.
    private func retry(fresh: Bool) {
        guard going else { return }
        if opened.duration(to: .now) > .seconds(10) { drops = 0 }
        drops += 1
        guard let wait = Self.backoff(after: drops) else {
            state = .failed(String(localized: "The connection was lost."))
            return
        }
        let g = generation
        Task {
            try? await Task.sleep(for: wait)
            guard g == generation, !closed else { return }
            if fresh { open() } else { rejoin() }
        }
    }

    /// The wait before connecting again after drops in a row: half a
    /// second, doubling up to ten; nil after the sixth.
    static func backoff(after drops: Int) -> Duration? {
        drops > 6 ? nil : min(.milliseconds(500) * (1 << (drops - 1)), .seconds(10))
    }

    private func receive(_ bytes: Data) {
        for item in stream.feed(bytes) {
            switch item {
            case .snapshot(let s):
                guard let t = VTerminal(snapshot: s) else {
                    state = .failed(String(localized: "The terminal's snapshot could not be read."))
                    return
                }
                // Drawn at the view's size, which the block takes next.
                if let size { t.resize(cols: size.cols, rows: size.rows, cellWidth: size.cw, cellHeight: size.ch) }
                apply(theme: TerminalTheme.chosen?.message ?? t.theme)
                // A reset first: the snapshot of a new stream replaces the screen.
                write(Data("\u{1b}c".utf8) + t.replay())
                state = .live
            case .output(let o):
                write(o)
            case .message(let m):
                switch m["type"] as? String {
                case "theme":
                    apply(theme: m)
                case "child_exited":
                    state = .exited(m["exit_code"] as? Int ?? 0)
                default:
                    // A resize follows this view's size, which it set.
                    break
                }
            }
        }
    }

    private func write(_ bytes: Data) {
        if let output { output.write(bytes) } else { pending += bytes }
    }

    private func apply(theme t: [String: Any]) {
        theme = t
        output?.setTheme(t)
    }

    // MARK: Input

    /// The view's size in cells, and a cell's in pixels, for the block, once
    /// it has held for a while, as for the steps of a pinch.
    func fit(cols: Int, rows: Int, cellWidth: Int, cellHeight: Int) {
        guard cols > 0, rows > 0, size.map({ ($0.cols, $0.rows) != (cols, rows) }) ?? true else { return }
        size = (cols, rows, cellWidth, cellHeight)
        sizing?.cancel()
        sizing = Task {
            try? await Task.sleep(for: .milliseconds(100))
            if !Task.isCancelled { sendSize() }
        }
    }

    private func sendSize() {
        guard let s = size else { return }
        let (session, block) = (session, block)
        Task {
            _ = try? await host.control?.callBlock("resize", session: session, block: block, [
                "columns": s.cols, "rows": s.rows, "cell_width_px": s.cw, "cell_height_px": s.ch,
            ])
        }
    }

    /// Writes what Ghostty encoded for a key, a paste or the wheel to the
    /// program. The server runs writes in any order, so one goes at a time
    /// and what comes meanwhile goes together next, whole characters only,
    /// in parts well under the server's message limit. What was not sent,
    /// offline, goes again; what was sent but not answered is dropped, as
    /// the server may have run it, and a command must not run twice. What
    /// is typed past a bound meanwhile is lost.
    /// The server drops what has the shape of Ghostty's own answers to the
    /// program's queries: its terminal answers them.
    func type(_ bytes: Data) {
        guard typed.count + bytes.count <= 4 << 20 else {
            return log.error("dropped \(bytes.count) typed bytes: \(self.typed.count) wait to be written")
        }
        typed += bytes
        write()
    }

    private func write(flush: Bool = false) {
        guard !typing, !closed, !typed.isEmpty else { return }
        guard let (n, text) = typed.nextWrite(max: 64 << 10, flush: flush) else {
            // A character cut off at the end goes as it is if its rest
            // doesn't come in a moment.
            let held = typed.count
            Task {
                try? await Task.sleep(for: .milliseconds(50))
                if typed.count == held { write(flush: true) }
            }
            return
        }
        typing = true
        let (session, block) = (session, block)
        Task {
            do {
                try await host.live().callBlock("write", session: session, block: block, ["data": text])
                typed = Data(typed.dropFirst(n))
            } catch let e as RexError where e.code == "offline" {
                try? await Task.sleep(for: .seconds(1))
            } catch {
                log.error("dropped a write of \(n) bytes: \(error.localizedDescription, privacy: .public)")
                typed = Data(typed.dropFirst(n))
            }
            typing = false
            write()
        }
    }
}
