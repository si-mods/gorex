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

    private var backgroundKey: String { "\(host.id) \(UserDefaults.standard.string(forKey: "theme") ?? "")" }

    /// Notes the background the view shows, for the page and the next opening.
    func drew(background b: VTerminal.RGB) {
        guard b != background else { return }
        background = b
        var all = UserDefaults.standard.dictionary(forKey: "backgrounds") ?? [:]
        all[backgroundKey] = b.packed
        UserDefaults.standard.set(all, forKey: "backgrounds")
    }

    /// Opens the stream from the start: a ticket, a HELLO, a new snapshot.
    func start() {
        state = .connecting
        Task {
            do {
                guard let c = host.control else { throw RexError(code: "offline", message: "offline") }
                let t = try await c.call("data.connect", [
                    "session_id": session, "block_id": block,
                    "data_ref_id": "terminal", "subprotocol": "rex.data.terminal.v1",
                ]).payload["ticket"] as? String ?? ""
                data = DataConnection(base: host.config.url)
                wire(data)
                try await data.open(ticket: t)
                log.info("opened \(self.block, privacy: .public)")
                stream = TerminalStream(nonce: data.nonce)
                try await claim()
            } catch {
                state = .failed(error.localizedDescription)
            }
        }
    }

    /// Continues the stream after the app comes back, or starts over when
    /// the server no longer has it.
    func resume() {
        guard !closed else { return }
        host.reconnectIfNeeded()
        Task {
            do {
                try await data.resume()
                log.info("resumed \(self.block, privacy: .public)")
                // A client that came back too late to resume lost the block.
                try? await claim()
            } catch {
                log.info("resume failed, starting over: \(error.localizedDescription, privacy: .public)")
                start()
            }
        }
    }

    /// Owns the block, so that it takes this view's size.
    private func claim() async throws {
        try await host.control?.call("session.own_block", ["session_id": session, "block_ids": [block], "mode": "take"])
        sendSize()
    }

    /// Leaves the stream for a resume, as when the app goes to the background.
    func suspend() { data.suspend() }

    /// Ends the stream and gives the block back.
    func close() {
        closed = true
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
            case 1000, 1013: self.start()
            default: self.resume()
            }
        }
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
    /// it has held for a while: steps of a pinch, or a keyboard on its way.
    func fit(cols: Int, rows: Int, cellWidth: Int, cellHeight: Int, after delay: Duration = .milliseconds(100)) {
        guard cols > 0, rows > 0, size.map({ ($0.cols, $0.rows) != (cols, rows) }) ?? true else { return }
        size = (cols, rows, cellWidth, cellHeight)
        sizing?.cancel()
        sizing = Task {
            try? await Task.sleep(for: delay)
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
    /// and what comes meanwhile goes together next, whole characters only.
    /// The server drops what has the shape of Ghostty's own answers to the
    /// program's queries: its terminal answers them.
    func type(_ bytes: Data) {
        typed += bytes
        guard !typing else { return }
        let n = typed.wholeUTF8
        guard n > 0 else { return }
        let text = String(decoding: typed.prefix(n), as: UTF8.self)
        typed = Data(typed.dropFirst(n))
        typing = true
        let (session, block) = (session, block)
        Task {
            _ = try? await host.control?.callBlock("write", session: session, block: block, ["data": text])
            typing = false
            if !typed.isEmpty { type(Data()) }
        }
    }
}
