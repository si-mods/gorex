import SwiftUI
import UIKit

/// A question from a host's action: a text field or a list of choices, a
/// confirm, and a cancel (docs/MOD-005-client-prompts.md).
struct PromptSheet: View {
    let prompt: Prompt
    /// The answer given; the host tells the server, which ends the
    /// question on the other apps.
    let answer: (Prompt.Answer) -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var text: String
    @State private var picked: Set<String>
    /// The text of an allow_other pick, which wins over the choices.
    @State private var other = ""

    init(prompt: Prompt, answer: @escaping (Prompt.Answer) -> Void) {
        self.prompt = prompt
        self.answer = answer
        _text = State(initialValue: prompt.spec.value ?? "")
        _picked = State(initialValue: Set(prompt.spec.items.filter(\.selected).map(\.id)))
    }

    private var spec: Prompt.Spec { prompt.spec }
    private var trimmedOther: String { other.trimmingCharacters(in: .whitespacesAndNewlines) }
    private var confirms: Bool {
        switch spec.kind {
        case .input: return true
        case .pick: return !picked.isEmpty || (spec.allowOther && !trimmedOther.isEmpty)
        }
    }

    var body: some View {
        NavigationStack {
            Form {
                if !prompt.action.isEmpty {
                    Section {
                        Text(prompt.action)
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                    }
                }
                switch spec.kind {
                case .input:
                    Section {
                        TextField(spec.placeholder ?? "Answer", text: $text)
                            .submitLabel(.done)
                            .onSubmit(submit)
                    } header: {
                        Text(spec.title)
                    }
                case .pick:
                    Section {
                        ForEach(spec.items) { item in
                            itemRow(item)
                        }
                        if spec.allowOther {
                            TextField("Other", text: $other)
                                .submitLabel(.done)
                                .onSubmit(submit)
                        }
                    } header: {
                        Text(spec.title)
                    }
                }
            }
            .navigationTitle(spec.kind == .input ? "" : spec.title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") {
                        answer(.cancelled)
                        dismiss()
                    }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button(spec.confirm ?? "Send", action: submit)
                        .disabled(!confirms)
                }
            }
        }
    }

    /// One choice: a check when picked, and a tap that picks it alone or
    /// adds it, as multiple says.
    private func itemRow(_ item: Prompt.Item) -> some View {
        Button {
            if spec.multiple {
                if picked.contains(item.id) { picked.remove(item.id) } else { picked.insert(item.id) }
            } else {
                picked = [item.id]
            }
        } label: {
            HStack(alignment: .firstTextBaseline, spacing: 10) {
                if let icon = item.icon, UIImage(systemName: icon) != nil {
                    Image(systemName: icon)
                        .foregroundStyle(.secondary)
                        .frame(width: 22)
                }
                VStack(alignment: .leading, spacing: 2) {
                    Text(item.title)
                        .foregroundStyle(.primary)
                    if let subtitle = item.subtitle, !subtitle.isEmpty {
                        Text(subtitle)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    if let detail = item.detail, !detail.isEmpty {
                        Text(detail)
                            .font(.caption2)
                            .foregroundStyle(.tertiary)
                    }
                }
                Spacer(minLength: 8)
                if picked.contains(item.id) {
                    Image(systemName: "checkmark")
                        .foregroundStyle(.tint)
                }
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }

    private func submit() {
        switch spec.kind {
        case .input:
            finish(.text(text))
        case .pick:
            if spec.allowOther, !trimmedOther.isEmpty {
                finish(.text(trimmedOther))
            } else {
                let ids = spec.items.map(\.id).filter(picked.contains)
                guard !ids.isEmpty else { return }
                finish(.ids(ids))
            }
        }
    }

    private func finish(_ answer: Prompt.Answer) {
        self.answer(answer)
        dismiss()
    }
}

/// Shows each host's question over whatever screen is open, one at a
/// time, and closes it when the host ends it.
private struct PromptPresenter: ViewModifier {
    let store: HostStore
    @State private var target: Target?

    struct Target: Identifiable {
        let host: Host
        let prompt: Prompt
        var id: String { "\(host.id.uuidString)/\(prompt.callID)" }
    }

    func body(content: Content) -> some View {
        content
            .onChange(of: asking, initial: true) { _, _ in sync() }
            .sheet(item: $target, onDismiss: { sync() }) { t in
                PromptSheet(prompt: t.prompt) { t.host.answer(t.prompt, $0) }
            }
    }

    /// The questions the hosts ask, as text that changes when one comes
    /// or goes.
    private var asking: String {
        store.hosts.compactMap { h in h.prompt.map { "\(h.id.uuidString)/\($0.callID)" } }
            .joined(separator: ",")
    }

    /// Shows the first unanswered question, or closes the sheet of one
    /// that ended, as another device answered it or its time passed.
    private func sync() {
        if let t = target {
            if t.host.prompt?.callID != t.prompt.callID { target = nil }
            return
        }
        for host in store.hosts {
            if let prompt = host.prompt {
                target = Target(host: host, prompt: prompt)
                return
            }
        }
    }
}

extension View {
    /// Shows the hosts' questions, on any screen of the app.
    func promptSheet(_ store: HostStore) -> some View {
        modifier(PromptPresenter(store: store))
    }
}
