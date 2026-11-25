import SwiftUI

/// The engine's question of an ssh:// host's key: trust it on first
/// sight, or say a changed one is the host's own
/// (docs/IOS-023-ssh-hosts.md, Trust). Shown over whatever screen is
/// open, as the hotspot share's admission prompt is, since a host
/// connects anywhere the app is.
private struct SSHHostKeyAlert: ViewModifier {
    private let engine = TailnetEngine.shared

    func body(content: Content) -> some View {
        let q = engine.sshHostKeyQuestion
        return content.alert(
            q?.changed == true ? "SSH host key changed" : "Trust this SSH host?",
            isPresented: Binding { q != nil } set: { show in
                if !show, let q = engine.sshHostKeyQuestion { engine.answerSSHHostKey(q, trust: false) }
            },
            presenting: q
        ) { q in
            if q.changed {
                Button("Trust New Key") { engine.answerSSHHostKey(q, trust: true) }
            } else {
                Button("Trust") { engine.answerSSHHostKey(q, trust: true) }
            }
            Button("Don't Trust", role: .cancel) { engine.answerSSHHostKey(q, trust: false) }
        } message: { q in
            if q.changed {
                Text("\(q.user)@\(q.host):\(String(q.port)) answered with a different key than the one trusted before.\n\nTrusted: \(q.previous)\nNew: \(q.fingerprint)")
            } else {
                Text("The key of \(q.user)@\(q.host):\(String(q.port)) is\n\n\(q.fingerprint)")
            }
        }
    }
}

extension View {
    /// Shows the engine's SSH host-key question, on any screen of the app.
    func sshHostKeyAlert() -> some View {
        modifier(SSHHostKeyAlert())
    }
}
