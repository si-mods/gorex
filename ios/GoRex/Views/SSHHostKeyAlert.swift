import SwiftUI

/// The engine's questions of an ssh:// host: trust its key on first
/// sight or for the run, say a changed one is the host's own, give the
/// secret its login asks, and take the offer to install the phone's key
/// (docs/IOS-023-ssh-hosts.md, Trust). Shown over whatever screen is
/// open, as the hotspot share's admission prompt is, since a host
/// connects anywhere the app is.
private struct SSHHostKeyAlert: ViewModifier {
    private let engine = TailnetEngine.shared
    @State private var secret = ""
    /// The question a button last answered: the dismiss that follows a
    /// button isn't a cancel, and the card then showing is its own.
    @State private var answered: Int64?

    func body(content: Content) -> some View {
        content
            .sshHostKeyCard(engine, answered: $answered)
            .sshAuthCard(engine, secret: $secret, answered: $answered)
            .sshInstallCard(engine)
    }
}

private extension View {
    /// The host-key card: Trust & Save keeps the fingerprint, Connect
    /// Once trusts it for the run only, and a changed key warns of a
    /// man-in-the-middle and replaces the saved one.
    func sshHostKeyCard(_ engine: TailnetEngine, answered: Binding<Int64?>) -> some View {
        let q = engine.sshHostKeyQuestion
        return alert(
            q?.changed == true ? "SSH host key changed" : "Trust this SSH host?",
            // The dismiss refuses the question this card showed (q), not
            // the one a button's answer made current.
            isPresented: Binding { q != nil } set: { show in
                if !show { engine.dismissSSHHostKey(shown: q, answered: answered.wrappedValue) }
            },
            presenting: q
        ) { q in
            let answer: (Bool?) -> Void = { save in
                answered.wrappedValue = q.qid
                engine.answerSSHHostKey(q, save: save)
            }
            if q.changed {
                Button("Replace & Connect", role: .destructive) { answer(true) }
                if !engine.savedSSHHostKey(q) { Button("Connect Once") { answer(false) } }
            } else {
                Button("Trust & Save") { answer(true) }
                Button("Connect Once") { answer(false) }
            }
            Button("Cancel", role: .cancel) { answer(nil) }
        } message: { q in
            if q.changed {
                Text("\(q.user)@\(q.host):\(String(q.port)) answered with a different key than the one trusted before. This could be a man-in-the-middle attack, or the host was reinstalled.\n\nTrusted: \(q.previous)\nNew (\(q.keyType)): \(q.fingerprint)")
            } else {
                Text("The \(q.keyType) key of \(q.user)@\(q.host):\(String(q.port)) is\n\n\(q.fingerprint)")
            }
        }
    }

    /// The secret card: the password or keyboard-interactive answer the
    /// login asks, with OTP AutoFill when the prompt wants a code.
    func sshAuthCard(_ engine: TailnetEngine, secret: Binding<String>, answered: Binding<Int64?>) -> some View {
        let q = engine.sshAuthQuestion
        return alert(
            q.map { "\($0.user)@\($0.host):\(String($0.port))" } ?? "",
            isPresented: Binding { q != nil } set: { show in
                if !show { engine.dismissSSHAuth(shown: q, answered: answered.wrappedValue) }
            },
            presenting: q
        ) { q in
            if q.otp {
                TextField("Code", text: secret)
                    .textContentType(.oneTimeCode)
                    .keyboardType(.numberPad)
            } else if q.secret {
                SecureField("Password", text: secret)
                    .textContentType(.password)
            } else {
                TextField(q.prompt.isEmpty ? "Response" : q.prompt, text: secret)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
            }
            Button("Continue") {
                answered.wrappedValue = q.qid
                engine.answerSSHAuth(q, secret: secret.wrappedValue)
                secret.wrappedValue = ""
            }
            Button("Cancel", role: .cancel) {
                answered.wrappedValue = q.qid
                engine.answerSSHAuth(q, secret: nil)
                secret.wrappedValue = ""
            }
        } message: { q in
            Text(q.retry ? "Incorrect, try again." : q.prompt)
        }
    }

    /// The install-key card: a password login offers the phone's key,
    /// which the next connect then needs no password for. Install Key's
    /// dismiss leaves the offer while the install runs, so that a
    /// failure shows in the card again.
    func sshInstallCard(_ engine: TailnetEngine) -> some View {
        let c = engine.sshInstallOffer
        return alert(
            "Install this phone's key?",
            isPresented: Binding { c != nil && !engine.sshInstalling } set: { show in
                if !show { engine.dismissSSHInstall() }
            },
            presenting: c
        ) { c in
            Button("Install Key") { engine.installSSHKey(c) }
            Button("Not Now", role: .cancel) { engine.skipSSHInstallKey() }
        } message: { c in
            Text("You logged in to \(c.name) with a password. Installing this phone's SSH key on it connects without one.\(engine.sshInstallError.map { "\n\n\($0)" } ?? "")")
        }
    }
}

extension View {
    /// Shows the engine's SSH questions, on any screen of the app.
    func sshHostKeyAlert() -> some View {
        modifier(SSHHostKeyAlert())
    }
}
