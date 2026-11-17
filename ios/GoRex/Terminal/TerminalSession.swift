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
    /// Reads the text the screen shows, and the scrollback's above it where
    /// it can, for the gallery (docs/ios-pane-gallery.md §3.4); done gets
    /// it and the screen's columns off the main thread.
    func readScreen(_ done: @escaping @Sendable (_ text: String, _ columns: Int) -> Void)
}

extension TerminalOutput {
    func readScreen(_ done: @escaping @Sendable (_ text: String, _ columns: Int) -> Void) {}
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
    /// What the last open waited for when it failed, how long it took and
    /// the error, for the diagnostics sheet.
    private(set) var diagnosis: String?
    /// The terminal's default background, for the page around it. Until
    /// the stream gives one it is the last one seen on this host with this
    /// theme, else the theme's, so that the page opens in its color.
    private(set) var background: VTerminal.RGB?
    /// The progress a program reports (OSC 9;4), nil without one.
    var progress: Progress?
    /// Whether the stream connects again after a drop, or after the app
    /// came back, while its screen shows what it last had.
    private(set) var reconnecting = false
    /// Whether what was typed waits for the host, offline.
    private(set) var waitingToSend = false

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
    /// snapshot's; a theme message from the server replaces them. Until
    /// the stream gives them, the chosen theme, else the background, so
    /// that Ghostty does not draw its own.
    @ObservationIgnored private var theme: [String: Any]?
    @ObservationIgnored private var hostTheme: [String: Any]?
    @ObservationIgnored nonisolated(unsafe) private var themeObserver: NSObjectProtocol?
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
    /// The terminal of Demo Mode, which runs on the phone; nil for a
    /// host's, whose stream comes over a data connection.
    @ObservationIgnored private var demo: DemoTerminal?
    /// Whether the session owns the block, as the pane on the page does: a
    /// pane kept open off the page streams without it, at the block's size.
    @ObservationIgnored private var owns: Bool
    /// The images the pane's output shows; Demo Mode has none.
    @ObservationIgnored let gallery: PaneGallery?

    init(host: Host, session: String, block: String, owns: Bool = true) {
        self.host = host
        self.session = session
        self.block = block
        self.owns = owns
        gallery = host.isDemo ? nil : PaneGallery.of(host: host.id.uuidString, block: block)
        data = DataConnection(base: host.config.url, auth: host.config.auth)
        // The demo has no host colors: it takes the chosen theme, else the
        // one the app starts with.
        let theme = host.isDemo
            ? (TerminalTheme.chosen ?? TerminalTheme.all.first { $0.name == TerminalTheme.standard })
            : TerminalTheme.chosen
        background = (UserDefaults.standard.dictionary(forKey: "backgrounds")?[backgroundKey] as? Int).map(VTerminal.RGB.init)
            ?? theme.map { VTerminal.RGB($0.colors[16]) }
        self.theme = theme?.message ?? background.map { ["background": String(format: "%06x", $0.packed)] }
        if host.isDemo {
            let d = DemoTerminal(Demo.scene(block))
            d.onCommand = { [weak self] text in self?.host.demoCommand(text, block: block) }
            demo = d
        }
        themeObserver = NotificationCenter.default.addObserver(forName: .terminalTheme, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.themeChanged() }
        }
    }

    deinit {
        if let themeObserver { NotificationCenter.default.removeObserver(themeObserver) }
    }

    private var backgroundKey: String { "\(host.id) \(TerminalTheme.name)" }

    /// Notes the background the view shows, for the page and the next opening.
    func drew(background b: VTerminal.RGB) {
        guard b != background else { return }
        background = b
        // A demo host is the same each time: nothing to remember of it.
        guard !host.isDemo else { return }
        var all = UserDefaults.standard.dictionary(forKey: "backgrounds") ?? [:]
        all[backgroundKey] = b.packed
        UserDefaults.standard.set(all, forKey: "backgrounds")
    }

    /// Opens the stream from the start: a ticket, a HELLO, a new snapshot.
    /// The stream before it ends. A closed session opens again.
    func start() {
        begin()
    }

    /// Starts, as the user does or on its own after a lost connection.
    private func begin() {
        closed = false
        drops = 0
        setReconnecting(false)
        if let demo {
            state = .live
            demo.onOutput = { [weak self] bytes in self?.write(bytes) }
            demo.start()
            return
        }
        open()
        write()
    }

    private func open() {
        state = .connecting
        generation += 1
        let g = generation
        data.close()
        Task {
            let began = ContinuousClock.now
            var d: DataConnection?
            do {
                let t = try await host.online().call("data.connect", [
                    "session_id": session, "block_id": block,
                    "data_ref_id": "terminal", "subprotocol": RexProtocol.terminalProtocol,
                ]).payload["ticket"] as? String ?? ""
                guard g == generation else { return }
                ConnectionLog.note("terminal \(block): ticket, \(began.duration(to: .now).ms)")
                // What ends this generation closes the connection it opens.
                let c = DataConnection(base: host.config.url, auth: host.config.auth, session: try host.session())
                d = c
                data = c
                wire(c)
                try await c.open(ticket: t)
                guard g == generation else { return }
                log.info("opened \(self.block, privacy: .public)")
                opened = .now
                stream = TerminalStream(nonce: c.nonce)
                // One kept off the page gives back what the server restored.
                if owns { await claim() } else { await giveBack() }
            } catch {
                guard g == generation else { return }
                diagnosis = "Waiting for \(d?.stage ?? "the ticket (data.connect)"), \(began.duration(to: .now).ms): \(error.diagnosis)"
                if d == nil { ConnectionLog.note("terminal \(block): no ticket: \(error.diagnosis)") }
                // A terminal ends only as its session does, or as the server
                // refuses the stream, which tells at once: a socket lost, a
                // host away, or a handshake the server answered with an
                // unusable one tries again, as a window's pane does.
                if d != nil && !(error is DataConnection.Failure) {
                    retry(fresh: true)
                } else {
                    end(.failed(error.localizedDescription))
                }
            }
        }
    }

    /// Continues the stream after the app comes back, or starts over when
    /// the server no longer has it. A stream that ended, as its window
    /// closed, stays so: Retry starts it.
    func resume() {
        guard demo == nil, !closed, going else { return }
        drops = 0
        rejoin()
    }

    private func rejoin() {
        setReconnecting(true)
        host.reconnectIfNeeded()
        generation += 1
        let g = generation
        Task {
            do {
                try await data.resume()
                guard g == generation else { return }
                setReconnecting(false)
                log.info("resumed \(self.block, privacy: .public)")
                opened = .now
                // A client that came back too late to resume lost the block.
                // One kept off the page gives back what the server restored.
                if owns { await claim() } else { await giveBack() }
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
        guard owns, !closed else { return }
        do {
            try await host.online().call("session.own_block", ["session_id": session, "block_ids": [block], "mode": "take"])
            // Gone off the page or closed meanwhile: the block goes back.
            if owns, !closed { sendSize() } else { await giveBack() }
        } catch {
            log.error("could not own \(self.block, privacy: .public): \(error.localizedDescription, privacy: .public)")
        }
    }

    /// Gives the block back, the stream going on.
    private func giveBack() async {
        _ = try? await host.control?.call("session.disown_block", ["session_id": session, "block_ids": [block]])
        // Back on the page meanwhile: it owns the block again.
        if owns, !closed { await claim() }
    }

    /// Owns the block as the pane comes on the page, or gives it back as it
    /// goes off it, kept open. A stream still opening owns it once open.
    func own(_ yes: Bool) {
        guard owns != yes else { return }
        owns = yes
        if !yes { sizing?.cancel() }
        guard demo == nil, !closed, going else { return }
        Task { if yes { await claim() } else { await giveBack() } }
    }

    /// Leaves the stream for a resume, as when the app goes to the background.
    func suspend() {
        guard demo == nil else { return }
        data.suspend()
    }

    /// Ends the stream and gives the block back.
    func close() {
        closed = true
        generation += 1
        setReconnecting(false)
        guard demo == nil else { return }
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
            case 4000: self.end(.failed(String(localized: "The window closed.")))
            case 1002, 1008: self.end(.failed(String(localized: "The server refused the terminal.")))
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
        setReconnecting(true)
        let g = generation
        let wait = Self.backoff(after: drops)
        Task {
            try? await Task.sleep(for: wait)
            guard g == generation, !closed else { return }
            if fresh { open() } else { rejoin() }
        }
    }

    /// Ends the terminal: its program exited, its window closed, the server
    /// refused the stream, or its snapshot could not be read. A stream lost
    /// for want of a connection is not an end: it attaches again (retry).
    private func end(_ s: State) {
        state = s
        setReconnecting(false)
    }

    /// Sets the flags only as they change, for no needless updates.
    private func setReconnecting(_ r: Bool) {
        if reconnecting != r { reconnecting = r }
    }

    private func setWaitingToSend(_ w: Bool) {
        if waitingToSend != w { waitingToSend = w }
    }

    /// The wait before connecting again after drops in a row: a quarter of
    /// a second, doubling up to two, as a window's stream waits. There is no
    /// sixth: a stream lost attaches again while its session exists.
    static func backoff(after drops: Int) -> Duration {
        let n = min(max(drops, 1), 4)
        return min(.milliseconds(250) * (1 << (n - 1)), .seconds(2))
    }

    private func receive(_ bytes: Data) {
        for item in stream.feed(bytes) {
            switch item {
            case .snapshot(let s):
                guard let t = VTerminal(snapshot: s) else {
                    end(.failed(String(localized: "The terminal's snapshot could not be read.")))
                    return
                }
                // Drawn at the view's size, which the block takes next.
                if let size { t.resize(cols: size.cols, rows: size.rows, cellWidth: size.cw, cellHeight: size.ch) }
                hostTheme = t.theme
                apply(theme: TerminalTheme.chosen?.message ?? t.theme)
                // A reset first: the snapshot of a new stream replaces the screen.
                let replay = t.replay()
                write(Data("\u{1b}c".utf8) + replay)
                gallery?.feed(replay, fresh: true, screen: output)
                state = .live
                setReconnecting(false)
            case .output(let o):
                write(o)
                gallery?.feed(o, screen: output)
            case .message(let m):
                switch m["type"] as? String {
                case "theme":
                    // A chosen theme stays over the host's colors.
                    hostTheme = m
                    apply(theme: TerminalTheme.chosen?.message ?? m)
                case "child_exited":
                    end(.exited(m["exit_code"] as? Int ?? 0))
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

    private func themeChanged() {
        let fallback = host.isDemo ? TerminalTheme.all.first { $0.name == TerminalTheme.standard }?.message : nil
        if let next = TerminalTheme.chosen?.message ?? hostTheme ?? fallback {
            apply(theme: next)
        }
    }

    // MARK: Input

    /// The view's size in cells, and a cell's in pixels, for the block, once
    /// it has held for a while, as for the steps of a pinch.
    func fit(cols: Int, rows: Int, cellWidth: Int, cellHeight: Int) {
        guard cols > 0, rows > 0, size.map({ ($0.cols, $0.rows) != (cols, rows) }) ?? true else { return }
        size = (cols, rows, cellWidth, cellHeight)
        // Not owned, the block keeps its size: the size goes as it is owned.
        guard demo == nil, owns else { return }
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
        if let demo {
            demo.type(bytes)
            return
        }
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
                // Offline, the write waits for the host.
                if host.status != .online { setWaitingToSend(true) }
                try await host.online().callBlock("write", session: session, block: block, ["data": text])
                typed = Data(typed.dropFirst(n))
                setWaitingToSend(false)
            } catch let e as RexError where e.code == "offline" {
                setWaitingToSend(true)
                try? await Task.sleep(for: .seconds(1))
            } catch {
                log.error("dropped a write of \(n) bytes: \(error.localizedDescription, privacy: .public)")
                typed = Data(typed.dropFirst(n))
                setWaitingToSend(false)
            }
            typing = false
            write()
        }
    }
}
