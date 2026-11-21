import Foundation

/// A question a host's action asks, from `client_prompt_requested`, and
/// the answer `client.reply` carries (docs/MOD-005-client-prompts.md).
nonisolated struct Prompt: Identifiable, Equatable {
    let callID: String
    let action: String
    let spec: Spec
    /// When the host stops waiting for an answer.
    let deadline: Date

    var id: String { callID }

    /// The question: a text field, or a list of choices.
    struct Spec: Equatable {
        let kind: Kind
        let title: String
        let placeholder: String?
        let value: String?
        let confirm: String?
        let multiple: Bool
        let allowOther: Bool
        let items: [Item]

        enum Kind: String, Equatable {
            case input
            case pick
        }
    }

    /// One choice of a pick.
    struct Item: Identifiable, Equatable {
        let id: String
        let title: String
        let subtitle: String?
        let icon: String?
        let detail: String?
        let selected: Bool
    }

    /// The answer a person gave.
    enum Answer: Equatable {
        case text(String)
        case ids([String])
        case cancelled

        /// The `result` a `client.reply` carries.
        var result: [String: Any] {
            switch self {
            case .text(let text): return ["text": text]
            case .ids(let ids): return ["ids": ids]
            case .cancelled: return ["cancelled": true]
            }
        }
    }

    /// Reads a `client_prompt_requested` event's payload, or nil when it
    /// is another event or lacks a question.
    static func from(_ payload: [String: Any], now: Date = .now) -> Prompt? {
        guard let callID = payload["call_id"] as? String,
              let raw = payload["prompt"] as? [String: Any],
              let kind = (raw["kind"] as? String).flatMap(Spec.Kind.init(rawValue:)),
              let title = raw["title"] as? String
        else { return nil }
        let items = (raw["items"] as? [[String: Any]] ?? []).compactMap { Item($0) }
        let seconds = (payload["timeout_ms"] as? Int).map { TimeInterval($0) / 1000 } ?? 300
        return Prompt(
            callID: callID,
            action: payload["action"] as? String ?? "",
            spec: Spec(
                kind: kind,
                title: title,
                placeholder: raw["placeholder"] as? String,
                value: raw["value"] as? String,
                confirm: raw["confirm"] as? String,
                multiple: raw["multiple"] as? Bool ?? false,
                allowOther: raw["allow_other"] as? Bool ?? false,
                items: items
            ),
            deadline: now.addingTimeInterval(seconds)
        )
    }
}

nonisolated extension Prompt.Item {
    /// Reads one item of a pick, or nil when it has no ID or title.
    init?(_ raw: [String: Any]) {
        guard let id = raw["id"] as? String, let title = raw["title"] as? String else { return nil }
        self.init(
            id: id,
            title: title,
            subtitle: raw["subtitle"] as? String,
            icon: raw["icon"] as? String,
            detail: raw["detail"] as? String,
            selected: raw["selected"] as? Bool ?? false
        )
    }
}
