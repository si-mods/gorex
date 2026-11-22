import Foundation

// The work of the activity's buttons, in the app's process
// (docs/IOS-006-live-activities.md, Actions). GoRexApp sets
// ActivityAnswerer.send to HostStore.answer.

extension HostStore {
    /// The store of the run — the app holds it — for the parts that act
    /// without the scene's environment: the activity's intents.
    static weak var current: HostStore?

    /// The answer of an activity's intent, on the host it names: one the
    /// list does not hold, or Demo Mode's, fails the intent and the
    /// activity stays as it was.
    func answer(_ host: UUID, session: String, block: String, _ action: TerminalAction) async throws {
        guard let h = self.host(host), !h.isDemo else {
            throw RexError(code: "unknown_host", message: String(localized: "The activity's host is not in the list."))
        }
        try await h.answerBlock(action, session: session, block: block)
    }
}

extension Host {
    /// Answers the block's blocked record: the control connection, as
    /// online() gives it — the bootstrap a terminal's open awaits, minus
    /// the views — the block's records read again, and the answer's
    /// bytes written to its stdin as typed input is written
    /// (TerminalSession.type). A record no longer one a key answers
    /// takes nothing: the second tap on a resolved record is a no-op.
    func answerBlock(_ action: TerminalAction, session: String, block: String) async throws {
        reconnectIfNeeded()
        let c = try await online()
        let r = try await c.callBlock("program_status", session: session, block: block)
        let answerable = (r["records"] as? [Any] ?? []).compactMap(ProgramStatus.init)
            .contains { TerminalActivity.Record.answerable(state: $0.state, kind: $0.kind) }
        guard answerable else { return }
        try await c.callBlock("write", session: session, block: block, ["data": action.bytes])
    }
}
