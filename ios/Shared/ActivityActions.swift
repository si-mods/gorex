import AppIntents
import Foundation

// The terminal activity's buttons (docs/IOS-006-live-activities.md,
// Actions): Approve and Deny answer a blocked record without opening the
// app. A widget button's intent runs as an AppIntent in the app's
// process — where a host's control connection lives — not in the widget
// extension, which draws the buttons but never performs them. This file
// compiles in both.

/// An answer the buttons give a blocked record, as bytes on the block's
/// stdin through the control connection's input path, as typed input is
/// sent — OSC 7501 carries no answer channel (MOD-001). Approve is
/// Return, which takes the default choice the prompt offers — a
/// permission prompt highlights the grant; deny is Escape.
nonisolated enum TerminalAction: String, Sendable {
    case approve
    case deny

    /// What the answer writes to the block.
    var bytes: String { self == .approve ? "\r" : "\u{1B}" }
}

extension TerminalActivity.Record {
    /// Whether the record is one the buttons answer: blocked, of a kind
    /// a key answers — permission or auth; a question takes text the
    /// activity cannot hold. The app checks the same before it writes
    /// (Host.answerBlock), through the shared rule.
    nonisolated var answerable: Bool { Self.answerable(state: state, kind: kind) }

    nonisolated static func answerable(state: String, kind: String?) -> Bool {
        state == "blocked" && (kind == "permission" || kind == "auth")
    }
}

/// Why an answer failed, as the intent's failure reports it.
nonisolated enum ActivityAnswerError: Error {
    /// The app gave nothing to send through.
    case unreachable
    /// The intent's parameters name no activity's target.
    case badTarget
    /// The seconds an intent gets ran out; the send may still land, a
    /// call in flight not taken back.
    case timeout
}

/// Performs an answer in the app's process: the app sets send (GoRexApp),
/// a test's stands in, and the widget extension keeps the default, which
/// fails — it never runs perform.
nonisolated enum ActivityAnswerer {
    /// The seconds an answer may take; iOS gives an intent a few. A
    /// connect that outruns it, as the built-in Tailscale's can, fails
    /// the intent and the activity stays as it was.
    nonisolated(unsafe) static var budget = Duration.seconds(6)
    /// The send: the host's id, the session, the block and the answer.
    nonisolated(unsafe) static var send: @Sendable (UUID, String, String, TerminalAction) async throws -> Void = { _, _, _, _ in
        throw ActivityAnswerError.unreachable
    }

    /// Validates the intent's parameters and sends within the budget.
    static func answer(host: String, session: String, block: String, _ action: TerminalAction) async throws {
        guard let id = UUID(uuidString: host), !session.isEmpty, !block.isEmpty else {
            throw ActivityAnswerError.badTarget
        }
        try await withThrowingTaskGroup(of: Void.self) { group in
            group.addTask { try await send(id, session, block, action) }
            group.addTask {
                try await Task.sleep(for: budget)
                throw ActivityAnswerError.timeout
            }
            defer { group.cancelAll() }
            _ = try await group.next()
        }
    }
}

/// The Approve button: the host, session and block of the activity it
/// was drawn on. openAppWhenRun off: the tap answers without the app
/// coming to the front, on the Lock Screen and the island alike.
struct ApproveIntent: AppIntent {
    nonisolated static var title: LocalizedStringResource { "Approve" }
    nonisolated static var openAppWhenRun: Bool { false }

    @Parameter(title: "Host") var host: String
    @Parameter(title: "Session") var session: String
    @Parameter(title: "Block") var block: String

    init() {
        host = ""
        session = ""
        block = ""
    }

    init(host: String, session: String, block: String) {
        self.host = host
        self.session = session
        self.block = block
    }

    init(host: UUID, session: String, block: String) {
        self.init(host: host.uuidString, session: session, block: block)
    }

    nonisolated func perform() async throws -> some IntentResult {
        try await ActivityAnswerer.answer(host: host, session: session, block: block, .approve)
        return .result()
    }
}

/// The Deny button, as ApproveIntent is.
struct DenyIntent: AppIntent {
    nonisolated static var title: LocalizedStringResource { "Deny" }
    nonisolated static var openAppWhenRun: Bool { false }

    @Parameter(title: "Host") var host: String
    @Parameter(title: "Session") var session: String
    @Parameter(title: "Block") var block: String

    init() {
        host = ""
        session = ""
        block = ""
    }

    init(host: String, session: String, block: String) {
        self.host = host
        self.session = session
        self.block = block
    }

    init(host: UUID, session: String, block: String) {
        self.init(host: host.uuidString, session: session, block: block)
    }

    nonisolated func perform() async throws -> some IntentResult {
        try await ActivityAnswerer.answer(host: host, session: session, block: block, .deny)
        return .result()
    }
}
