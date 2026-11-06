import Foundation
import Observation
import os

private let log = Logger(subsystem: "dev.gorex.ios", category: "terminal")

/// One block's terminal on the phone: its data stream fed into a
/// VTerminal, the block owned so that it takes the phone's size, and the
/// keys typed sent back as block events.
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
    /// Bumped whenever the screen changed, for the view to draw.
    private(set) var frame = 0
    /// The terminal's default background, for the page around it. Until
    /// the view draws the screen it is the last one seen on this host with
    /// this theme, else the theme's, so that the page opens in its color.
    private(set) var background: VTerminal.RGB?

    @ObservationIgnored private(set) var terminal: VTerminal?
    @ObservationIgnored private var stream = TerminalStream(nonce: 0)
    @ObservationIgnored private var data: DataConnection
    @ObservationIgnored private var size: (cols: Int, rows: Int, cw: Int, ch: Int)?
    @ObservationIgnored private var closed = false

    init(host: Host, session: String, block: String) {
        self.host = host
        self.session = session
        self.block = block
        data = DataConnection(base: host.config.url)
        background = (UserDefaults.standard.dictionary(forKey: "backgrounds")?[backgroundKey] as? Int).map(VTerminal.RGB.init)
            ?? TerminalTheme.chosen.map { VTerminal.RGB($0.colors[16]) }
    }

    private var backgroundKey: String { "\(host.id) \(UserDefaults.standard.string(forKey: "theme") ?? "")" }

    /// Notes the background the view drew, for the page and the next opening.
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
                terminal = nil
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
                terminal = t
                if let m = TerminalTheme.chosen?.message { t.setTheme(m) }
                state = .live
            case .output(let o):
                terminal?.write(o)
            case .message(let m):
                switch m["type"] as? String {
                case "resize":
                    terminal?.resize(cols: m["cols"] as? Int ?? 0, rows: m["rows"] as? Int ?? 0,
                                     cellWidth: m["cell_width_px"] as? Int ?? 0, cellHeight: m["cell_height_px"] as? Int ?? 0)
                case "theme":
                    terminal?.setTheme(m)
                case "child_exited":
                    state = .exited(m["exit_code"] as? Int ?? 0)
                default:
                    break
                }
            }
        }
        frame &+= 1
    }

    // MARK: Input

    /// The view's size in cells, and a cell's in pixels, for the block.
    func fit(cols: Int, rows: Int, cellWidth: Int, cellHeight: Int) {
        guard cols > 0, rows > 0, size.map({ ($0.cols, $0.rows) != (cols, rows) }) ?? true else { return }
        size = (cols, rows, cellWidth, cellHeight)
        sendSize()
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

    func send(_ keys: [KeyEvent]) {
        terminal?.scroll(nil)
        for k in keys { event("key", k.payload) }
    }

    func paste(_ text: String) {
        terminal?.scroll(nil)
        event("paste", ["text": text])
    }

    /// Turns the wheel at a cell, for a program that reads it.
    func wheel(up: Bool, col: Int, row: Int) {
        event("wheel", ["direction": up ? "up" : "down", "mods": "", "column": col, "row": row])
    }

    func scrollLocal(_ rows: Int) {
        terminal?.scroll(rows)
        frame &+= 1
    }

    private func event(_ type: String, _ payload: [String: Any]) {
        host.control?.post("block.event", [
            "session_id": session, "block_id": block, "event": ["type": type, "payload": payload],
        ])
    }
}
