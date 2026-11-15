import AVFAudio
import GameController
import SwiftUI

/// What a window's row opens: the window's block on a host.
struct TerminalTarget: Hashable {
    let host: UUID
    let session: String
    let block: String
    let title: String
}

/// A window's terminal, full screen, under a clear navigation bar.
struct TerminalScreen: View {
    let target: TerminalTarget
    let host: Host
    /// Opens another terminal in place of this one, or goes back to the
    /// list, for ⌘T and ⌘W.
    let go: (TerminalTarget?) -> Void
    /// Made once, as the screen appears: SwiftUI makes the screen again
    /// on each update of the list, and keeps the first State.
    @State private var session: TerminalSession?
    @Environment(\.scenePhase) private var phase
    /// Whether the stream was left in the background, for a resume.
    @State private var suspended = false
    /// What failed last, and why, as the server says it.
    @State private var failure: (title: LocalizedStringKey, why: String)?
    /// Whether the session's sheet is open, as the title opens it.
    @State private var sheet = false
    /// Whether the diagnostics of a failed terminal show.
    @State private var diagnostics = false
    /// The height of the status bar.
    @State private var statusBar: CGFloat = 0
    /// What is said while the mic is on, and whether it is finishing.
    @State private var said: Dictation.Said?
    @State private var busy = false
    /// Whether the mic asks to download the speech model, or to stop it.
    @State private var asking = false
    @State private var stopping = false
    /// Whether the terminal has the key bar, which holds the mic.
    @State private var hasBar = TerminalView.hasBar
    @State private var currentTarget: TerminalTarget
    @State private var pillDrag: CGFloat = 0
    @State private var slideDir: CGFloat = 1
    /// Whether the terminal has been reconnecting, or holding what was
    /// typed, for a second: a short reconnect shows nothing.
    @State private var stalled = false

    init(target: TerminalTarget, host: Host, go: @escaping (TerminalTarget?) -> Void) {
        self.target = target
        self.host = host
        self.go = go
        _currentTarget = State(initialValue: target)
    }

    var body: some View {
        // The terminal's background fills the page edge to edge, under the
        // bar and the key bar too; the bar and the text on it take its scheme.
        let bg = session?.background ?? .black
        let scheme: ColorScheme = bg.light ? .light : .dark
        // The terminal goes up under the clear bar and the status bar: its
        // grid starts below them, and its scrollback fills the band.
        Group {
            if let session {
                TerminalRepresentable(session: session, newTab: newTab, closeTab: closeTab, mic: mic, voice: voiceCapsule, dictating: said != nil)
                    .id(currentTarget.block)
                    .transition(.asymmetric(
                        insertion: .move(edge: slideDir > 0 ? .trailing : .leading),
                        removal: .move(edge: slideDir > 0 ? .leading : .trailing)
                    ))
            } else {
                Color.clear
            }
        }
            .clipped()
            .ignoresSafeArea(.container, edges: .top)
            .ignoresSafeArea(.keyboard)
            .overlay(alignment: .top) { if let p = session?.progress { ProgressLine(progress: p).padding(.top, statusBar).ignoresSafeArea(.container, edges: .top) } }
            .background {
                // The status bar is the window's inset, read as the page's changes.
                Color(cgColor: bg.cgColor)
                    .ignoresSafeArea()
                    .onGeometryChange(for: CGSize.self) { $0.size } action: { _ in statusBar = Self.statusBar }
            }
            // What goes up under the status bar blurs and fades into the
            // background, for the clock over it.
            .overlay(alignment: .top) {
                TopEdge(color: UIColor(cgColor: bg.cgColor))
                    .frame(height: statusBar + 10)
                    .ignoresSafeArea(.container, edges: .top)
                    .allowsHitTesting(false)
            }
            // On iPad or with a hardware keyboard (no KeyBar), show the floating preview.
            .overlay(alignment: .bottom) { if !hasBar, let said { Heard(said: said) { terminal?.stopDictating() } } }
            .overlay { status }
            // Inside the safe area: under the navigation bar, whatever its height.
            .overlay(alignment: .top) {
                if stalled, let session {
                    Reconnecting(waitingToSend: session.waitingToSend)
                        .padding(.top, 8)
                        .transition(.move(edge: .top).combined(with: .opacity))
                }
            }
            .task(id: troubled) {
                guard troubled else {
                    withAnimation(.easeOut(duration: 0.2)) { stalled = false }
                    return
                }
                try? await Task.sleep(for: .seconds(1))
                guard !Task.isCancelled else { return }
                withAnimation(.easeOut(duration: 0.2)) { stalled = true }
            }
            .navigationTitle(title.asText)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                // The title floats in a capsule of glass, with the tile of
                // the window's program, as its row in the list shows it; a
                // tap opens the session's sheet.
                ToolbarItem(placement: .principal) {
                    // As tall as the back button beside it.
                    HStack(spacing: 10) {
                        let k = ProgramTiles.scale, small = 0.85
                        ProgramTile(program: .of(host.blocks[currentTarget.block]?.program))
                            .frame(width: 21 * k, height: 16 * k)
                            .scaleEffect(small)
                            .frame(width: 21 * k * small, height: 16 * k * small)
                        Text(title.asText).font(.body.weight(.semibold)).lineLimit(1)
                    }
                    .id(currentTarget.block)
                    .transition(.asymmetric(
                        insertion: .move(edge: slideDir > 0 ? .trailing : .leading).combined(with: .opacity),
                        removal: .move(edge: slideDir > 0 ? .leading : .trailing).combined(with: .opacity)
                    ))
                    .padding(.leading, 12)
                    .padding(.trailing, 18)
                    .frame(height: 44)
                    .clipped()
                    // Glass behind, not over: on the content it would fade the tile.
                    .background { Capsule().fill(.clear).glassEffect(.regular, in: .capsule) }
                    .contentShape(.capsule)
                    .accessibilityAddTraits(.isButton)
                    .offset(x: pillDrag)
                    .onTapGesture { sheet = true }
                    .highPriorityGesture(
                        DragGesture(minimumDistance: 12)
                            .onChanged { v in
                                guard sessionWindows.count > 1, abs(v.translation.width) > abs(v.translation.height) else { return }
                                pillDrag = max(-28, min(28, v.translation.width * 0.35))
                            }
                            .onEnded { v in
                                withAnimation(.spring(duration: 0.28, bounce: 0.2)) {
                                    pillDrag = 0
                                }
                                guard abs(v.translation.width) > 24, abs(v.translation.width) > abs(v.translation.height) else { return }
                                stepTab(by: v.translation.width < 0 ? 1 : -1)
                            }
                    )
                    .contextMenu {
                        ForEach(sessionWindows, id: \.window.window_id) { w in
                            let open = w.window.holds(currentTarget.block)
                            let label = host.title(w.window)
                            Button {
                                if !open {
                                    switchTo(TerminalTarget(host: host.id, session: currentTarget.session, block: w.block, title: label))
                                }
                            } label: {
                                if open {
                                    Label(label.isEmpty ? sessionLabel : label, systemImage: "checkmark")
                                } else {
                                    Text(label.isEmpty ? sessionLabel : label)
                                }
                            }
                        }
                        Divider()
                        Button("New Tab", systemImage: "plus") { newTab() }
                    }
                    .sensoryFeedback(.selection, trigger: currentTarget.block)
                }
                .sharedBackgroundVisibility(.hidden)
                // The mic where the terminal has no key bar, and the
                // window's menu, New Tab being in the session's sheet and on
                // ⌘T; the bar's glass draws them.
                ToolbarItemGroup(placement: .topBarTrailing) {
                    if !hasBar { mic }
                    Menu {
                        Button("New Tab", systemImage: "plus") { newTab() }
                    } label: {
                        Image(systemName: "ellipsis")
                    }
                    .accessibilityLabel("More")
                }
            }
            // The sheet takes the terminal's scheme as the bar does; the
            // one below does not reach it under a dark system.
            .sheet(isPresented: $sheet) { SessionSheet(target: currentTarget, host: host, go: { next in
                if let next { switchTo(next) } else { go(nil) }
            }, newTab: newTab).environment(\.colorScheme, scheme) }
            .sheet(isPresented: $diagnostics) {
                NavigationStack {
                    DiagnosticsView(address: host.config.url.host() ?? "", host: host.config, failure: session?.diagnosis)
                        .toolbar {
                            ToolbarItem(placement: .cancellationAction) {
                                Button("Close", systemImage: "xmark", role: .close) { diagnostics = false }
                            }
                        }
                }
                .environment(\.colorScheme, scheme)
            }
            .toolbarBackgroundVisibility(.hidden, for: .navigationBar)
            .toolbarColorScheme(scheme, for: .navigationBar)
            .environment(\.colorScheme, scheme)
            .onAppear {
                let s = session ?? TerminalSession(host: host, session: currentTarget.session, block: currentTarget.block)
                session = s
                s.start()
                Notifier.shared.onScreen = currentTarget
                host.see(session: currentTarget.session, block: currentTarget.block)
            }
            .onDisappear {
                terminal?.stopDictating()
                host.see(session: currentTarget.session, block: currentTarget.block)
                session?.close()
                if Notifier.shared.onScreen == currentTarget { Notifier.shared.onScreen = nil }
            }
            .alert(failure?.title ?? "", isPresented: Binding { failure != nil } set: { if !$0 { failure = nil } }) {
            } message: {
                Text(failure?.why ?? "")
            }
            // The mic's, here, as the one in the key bar can't show them.
            .alert("Download the Speech Model?", isPresented: $asking) {
                Button("Download") { Dictation.shared.download(Dictation.shared.model) }
                Button("Cancel", role: .cancel) {}
            } message: {
                Text("Speech is recognised on this device, by \(Dictation.shared.model.name). Its model, \(Dictation.shared.model.size.formatted(.byteCount(style: .file))), downloads once from Hugging Face, or from hf-mirror.com, a mirror run by others, where Hugging Face can't be reached. Delete it in Settings › Terminal.")
            }
            .stopsDownload($stopping)
            .alert("Can't Download the Speech Model", isPresented: Binding { Dictation.shared.error != nil } set: { if !$0 { Dictation.shared.error = nil } }) {
            } message: {
                Text(Dictation.shared.error ?? "")
            }
            .onReceive(NotificationCenter.default.publisher(for: AVAudioSession.interruptionNotification)) { _ in terminal?.stopDictating() }
            .onReceive(NotificationCenter.default.publisher(for: .GCKeyboardDidConnect).merge(with: NotificationCenter.default.publisher(for: .GCKeyboardDidDisconnect))) { _ in hasBar = TerminalView.hasBar }
            .onChange(of: phase) { _, p in
                // Inactive, as under Control Center or an alert, keeps it.
                if p == .background {
                    terminal?.stopDictating()
                    session?.suspend()
                    suspended = true
                }
                if p == .active, suspended {
                    session?.resume()
                    suspended = false
                }
            }
    }

    /// The height of the status bar, the window's top inset.
    private static var statusBar: CGFloat {
        UIApplication.shared.connectedScenes.lazy.compactMap { ($0 as? UIWindowScene)?.keyWindow }.first?.safeAreaInsets.top ?? 0
    }

    private var mic: Mic { Mic(terminal: { terminal }, said: $said, busy: $busy, asking: $asking, stopping: $stopping) }

    private var voiceCapsule: VoiceCapsule {
        VoiceCapsule(said: said, busy: $busy, cancel: { terminal?.stopDictating() }, finish: {
            guard !busy else { return }
            busy = true
            Task {
                await terminal?.typeDictation()
                busy = false
            }
        })
    }

    /// The terminal showing the session.
    private var terminal: TerminalView? { session?.output as? TerminalView }

    /// The session of the window open, as the host has it.
    private var hostSession: Host.Session? {
        host.sessions.first { $0.id == currentTarget.session }
    }

    /// The window open, as its session's view has it.
    private var window: SessionView.Window? {
        hostSession?.view?.windows?.first { $0.holds(currentTarget.block) }
    }

    /// The window's title as its row shows it, when the block open is the
    /// one in front, else as the row gave it, else its session's name, as
    /// for a new session.
    private var title: String {
        let live = window.flatMap { $0.block == currentTarget.block ? host.title($0) : nil } ?? ""
        return [live, currentTarget.title].first { !$0.isEmpty } ?? sessionLabel
    }

    /// The name of the window's session.
    private var sessionLabel: String {
        hostSession?.label ?? ""
    }

    private var sessionWindows: [(block: String, window: SessionView.Window)] {
        (hostSession?.view?.windows ?? []).compactMap { w in w.block.map { (block: $0, window: w) } }
    }

    private func switchTo(_ next: TerminalTarget, dir: CGFloat? = nil) {
        guard next.host == currentTarget.host && next.session == currentTarget.session else {
            go(next)
            return
        }
        guard next.block != currentTarget.block else { return }
        let resolvedDir: CGFloat
        if let dir {
            resolvedDir = dir
        } else {
            let all = sessionWindows
            let fromIdx = all.firstIndex { $0.window.holds(currentTarget.block) } ?? 0
            let toIdx = all.firstIndex { $0.window.holds(next.block) } ?? fromIdx
            resolvedDir = toIdx >= fromIdx ? 1 : -1
        }
        terminal?.stopDictating()
        host.see(session: currentTarget.session, block: currentTarget.block)
        session?.close()
        let nextSession = TerminalSession(host: host, session: next.session, block: next.block)
        nextSession.start()
        Notifier.shared.onScreen = next
        host.see(session: next.session, block: next.block)
        withAnimation(.spring(duration: 0.32, bounce: 0.12)) {
            slideDir = resolvedDir
            session = nextSession
            currentTarget = next
        }
    }

    private func stepTab(by delta: Int) {
        let all = sessionWindows
        guard all.count > 1, let i = all.firstIndex(where: { $0.window.holds(currentTarget.block) }) else { return }
        let next = all[(i + delta + all.count) % all.count]
        guard !next.window.holds(currentTarget.block) else { return }
        switchTo(TerminalTarget(host: host.id, session: currentTarget.session, block: next.block, title: host.title(next.window)), dir: delta > 0 ? 1 : -1)
    }

    /// Opens a new window in the session, and goes to it.
    private func newTab() {
        Task {
            do {
                if let b = try await host.newWindow(in: currentTarget.session) {
                    switchTo(TerminalTarget(host: host.id, session: currentTarget.session, block: b, title: ""), dir: 1)
                }
            } catch {
                failure = ("Can't Open a New Window", error.localizedDescription)
            }
        }
    }

    /// Closes the window, without asking, as its row's Close Tab does, and
    /// goes back to the list.
    private func closeTab() {
        guard let w = window else { return }
        Task {
            do {
                try await host.closeWindow(w.window_id, in: currentTarget.session)
                go(nil)
            } catch {
                failure = ("Can't Close the Tab", error.localizedDescription)
            }
        }
    }

    /// Whether the terminal reconnects, or holds what was typed, while
    /// its stream is open or opening.
    private var troubled: Bool {
        guard let session, session.state == .connecting || session.state == .live else { return false }
        return session.reconnecting || session.waitingToSend
    }

    @ViewBuilder private var status: some View {
        switch session?.state ?? .connecting {
        case .connecting:
            // A reconnect shows its note at the top instead.
            if session?.reconnecting != true { ProgressView() }
        case .live:
            EmptyView()
        case .exited(let code):
            label(String(localized: "The program exited with code \(code)."))
        case .failed(let why):
            VStack(spacing: 12) {
                label(why)
                HStack {
                    Button("Diagnostics") { diagnostics = true }.buttonStyle(.glass)
                    Button("Retry") { session?.start() }.buttonStyle(.glassProminent).foregroundStyle(Color.page)
                }
            }
        }
    }

    private func label(_ s: String) -> some View {
        Text(s).font(.callout).padding(12).glassEffect(.regular, in: .rect(cornerRadius: 14))
    }
}

/// The note of a terminal that reconnects, under the navigation bar: it
/// lets the terminal show, and touches go through it.
private struct Reconnecting: View {
    let waitingToSend: Bool

    var body: some View {
        VStack(spacing: 4) {
            HStack(spacing: 8) {
                ProgressView()
                Text("Reconnecting…")
            }
            if waitingToSend {
                Text("What you type is sent when the connection is back.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .font(.callout)
        .multilineTextAlignment(.center)
        .padding(.horizontal, 14)
        .padding(.vertical, 10)
        .glassEffect(.regular, in: .rect(cornerRadius: 14))
        .padding(.horizontal, 16)
        .accessibilityElement(children: .combine)
        .allowsHitTesting(false)
    }
}

/// The mic: a tap turns it on, and what is said shows as it is spoken,
/// recognised on the phone (Dictation); a tap turns it off and types it
/// into the terminal, unsent, once it spins as its last phrase is. Its
/// first tap asks to download the models, and while they download it shows
/// how far, and a tap asks to stop it. It goes off, typing nothing, as the
/// screen goes or a call comes. It is on while something is said.
private struct Mic: View {
    let terminal: () -> TerminalView?
    @Binding var said: Dictation.Said?
    @Binding var busy: Bool
    @Binding var asking: Bool
    @Binding var stopping: Bool
    @GestureState private var pressed = false
    private let dictation = Dictation.shared
    private var on: Bool { said != nil && !busy }

    var body: some View {
        Group {
            if let p = dictation.progress {
                Circle().stroke(.secondary.opacity(0.3), lineWidth: 2.5)
                    .overlay { Circle().trim(from: 0, to: p).stroke(.primary, style: StrokeStyle(lineWidth: 2.5, lineCap: .round)).rotationEffect(.degrees(-90)) }
                    .frame(width: 18, height: 18)
                    .accessibilityLabel("Downloading the Speech Model")
                    .accessibilityValue(p.formatted(.percent.precision(.fractionLength(0))))
                    .accessibilityHint("Tap to stop.")
            } else if busy {
                ProgressView().accessibilityLabel("Recognising")
            } else {
                // On, it is the red dot of a recording.
                Image(systemName: on ? "circle.fill" : "mic")
                    .foregroundStyle(on ? AnyShapeStyle(.red) : AnyShapeStyle(.primary))
                    .accessibilityLabel("Dictate")
                    .accessibilityHint("Tap to speak, and again to type what you said.")
            }
        }
        .frame(width: 36, height: 36)
        .contentShape(.circle)
        .onTapGesture {
            guard !busy else { return }
            if dictation.progress != nil {
                stopping = true
            } else if on {
                busy = true
                Task {
                    await terminal()?.typeDictation()
                    busy = false
                }
            } else if !dictation.installed.contains(dictation.model) {
                asking = true
            } else {
                _ = terminal()?.dictate { said = $0 }
            }
        }
        // A light tap as the finger goes down, as the key bar's keys give.
        .simultaneousGesture(DragGesture(minimumDistance: 0).updating($pressed) { _, p, _ in p = true })
        .sensoryFeedback(.impact(weight: .light), trigger: pressed) { _, p in p }
        .accessibilityAddTraits(on ? [.isButton, .isSelected] : .isButton)
    }
}

/// What is said while the mic is on, at the bottom, over the key bar and
/// the keyboard, in glass as the screen's notes are: the microphone's
/// level, then what is for good, and in grey the phrase still open; three
/// lines at most, the newest. Being polished, all of it is grey, after a
/// sparkle.
private struct Heard: View {
    let said: Dictation.Said
    let cancel: () -> Void

    var body: some View {
        let settled = said.polishing ? "" : said.settled
        var open = AttributedString(said.text.dropFirst(settled.count))
        open.foregroundColor = .secondary
        let text = Text(AttributedString(settled) + open).font(.callout)
        return HStack(spacing: 10) {
            if said.polishing {
                Image(systemName: "sparkles").foregroundStyle(.secondary).symbolEffect(.pulse).frame(height: 18).accessibilityLabel("Polishing")
            } else {
                Level()
            }
            ViewThatFits(in: .vertical) {
                text
                ScrollView { text }.defaultScrollAnchor(.bottom).scrollIndicators(.hidden)
            }
            .frame(maxWidth: .infinity, maxHeight: 64, alignment: .leading)
            Button(action: cancel) {
                Image(systemName: "xmark")
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(.secondary)
                    .frame(width: 28, height: 28)
                    .contentShape(.circle)
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Cancel")
        }
        .padding(12)
        .glassEffect(.regular, in: .rect(cornerRadius: 14))
        .padding([.horizontal, .bottom], 12)
    }
}

/// The microphone's level, as a call's: its last levels in bars, the
/// newest at the right; under Reduce Motion, a waveform that stays still.
private struct Level: View {
    @Environment(\.accessibilityReduceMotion) private var still

    var body: some View {
        let levels = Dictation.shared.levels
        Group {
            if still {
                Image(systemName: "waveform")
            } else {
                HStack(spacing: 2) {
                    ForEach(levels.indices, id: \.self) { i in
                        Capsule().frame(width: 3, height: 4 + 14 * CGFloat(levels[i]))
                    }
                }
                .animation(.easeOut(duration: 0.1), value: levels)
            }
        }
        .frame(height: 18)
        .accessibilityHidden(true)
    }
}

/// A soft edge at the top of the page: blurred and in the background's
/// color at the top, clear below. Drives the blur alpha alongside UIKit's
/// navigation `transitionCoordinator` so interactive swipe-back and pop
/// transitions fade the blur smoothly instead of snapping off.
private struct TopEdge: UIViewControllerRepresentable {
    let color: UIColor

    func makeUIViewController(context: Context) -> Controller {
        Controller(color: color)
    }

    func updateUIViewController(_ vc: Controller, context: Context) {
        vc.setColor(color)
    }

    final class Controller: UIViewController {
        private let blurView = UIVisualEffectView(effect: UIBlurEffect(style: .systemUltraThinMaterial))
        private let maskLayer = CAGradientLayer()
        private let colorLayer = CAGradientLayer()
        private var color: UIColor

        init(color: UIColor) {
            self.color = color
            super.init(nibName: nil, bundle: nil)
        }

        @available(*, unavailable)
        required init?(coder: NSCoder) { fatalError() }

        override func loadView() {
            let v = UIView()
            v.isUserInteractionEnabled = false
            v.backgroundColor = .clear

            maskLayer.colors = [UIColor.black.cgColor, UIColor.black.cgColor, UIColor.clear.cgColor]
            maskLayer.locations = [0, 0.35, 1]
            maskLayer.startPoint = CGPoint(x: 0.5, y: 0)
            maskLayer.endPoint = CGPoint(x: 0.5, y: 1)

            blurView.isUserInteractionEnabled = false
            blurView.layer.mask = maskLayer
            v.addSubview(blurView)

            colorLayer.locations = [0, 0.35, 1]
            colorLayer.startPoint = CGPoint(x: 0.5, y: 0)
            colorLayer.endPoint = CGPoint(x: 0.5, y: 1)
            v.layer.addSublayer(colorLayer)

            view = v
            applyColor()
        }

        override func viewDidLayoutSubviews() {
            super.viewDidLayoutSubviews()
            blurView.frame = view.bounds
            CATransaction.begin()
            CATransaction.setDisableActions(true)
            maskLayer.frame = blurView.bounds
            colorLayer.frame = view.bounds
            CATransaction.commit()
        }

        override func viewWillAppear(_ animated: Bool) {
            super.viewWillAppear(animated)
            if let tc = transitionCoordinator {
                blurView.alpha = 0
                tc.animate(alongsideTransition: { _ in
                    self.blurView.alpha = 1
                }, completion: { ctx in
                    if ctx.isCancelled { self.blurView.alpha = 0 }
                })
            } else {
                blurView.alpha = 1
            }
        }

        override func viewDidAppear(_ animated: Bool) {
            super.viewDidAppear(animated)
            blurView.alpha = 1
        }

        override func viewWillDisappear(_ animated: Bool) {
            super.viewWillDisappear(animated)
            if let tc = transitionCoordinator {
                tc.animate(alongsideTransition: { _ in
                    self.blurView.alpha = 0
                }, completion: { ctx in
                    if ctx.isCancelled { self.blurView.alpha = 1 }
                })
            } else {
                blurView.alpha = 0
            }
        }

        func setColor(_ c: UIColor) {
            guard c != color else { return }
            color = c
            applyColor()
        }

        private func applyColor() {
            colorLayer.colors = [
                color.cgColor,
                color.withAlphaComponent(0.75).cgColor,
                color.withAlphaComponent(0).cgColor,
            ]
        }
    }
}

/// The progress a program reports, as a thin line atop the terminal: its
/// share, or all of it faint while it can't tell, red on an error.
private struct ProgressLine: View {
    let progress: TerminalSession.Progress

    var body: some View {
        GeometryReader { g in
            Rectangle()
                .fill(progress.failed ? Color.red : Color.accentColor)
                .opacity(progress.fraction == nil ? 0.4 : 1)
                .frame(width: g.size.width * (progress.fraction ?? 1))
                .animation(.default, value: progress)
        }
        .frame(height: 2)
        .allowsHitTesting(false)
    }
}

/// What is said while the mic is on inside the key bar's horizontal voice
/// capsule: waveform/sparkles, single-line trailing-scrolled text, cancel, and
/// the red recording dot to finish and type into the terminal.
private struct VoiceCapsule: View {
    let said: Dictation.Said?
    @Binding var busy: Bool
    let cancel: () -> Void
    let finish: () -> Void

    var body: some View {
        let settled = (said?.polishing == true ? "" : said?.settled) ?? ""
        let full = said?.text ?? ""
        var open = AttributedString(full.dropFirst(min(settled.count, full.count)))
        open.foregroundColor = .secondary
        let text = Text(AttributedString(settled) + open).font(.callout)
        return HStack(spacing: 8) {
            if said?.polishing == true {
                Image(systemName: "sparkles").foregroundStyle(.secondary).symbolEffect(.pulse).frame(height: 18).accessibilityLabel("Polishing")
            } else {
                Level()
            }
            ScrollViewReader { proxy in
                ScrollView(.horizontal) {
                    HStack(spacing: 0) {
                        text.lineLimit(1)
                        Color.clear.frame(width: 1, height: 1).id("end")
                    }
                }
                .scrollIndicators(.hidden)
                .defaultScrollAnchor(.trailing)
                .onChange(of: full) { _, _ in
                    proxy.scrollTo("end", anchor: .trailing)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .mask {
                LinearGradient(stops: [
                    .init(color: .clear, location: 0),
                    .init(color: .black, location: 0.06),
                    .init(color: .black, location: 1),
                ], startPoint: .leading, endPoint: .trailing)
            }
            Button(action: cancel) {
                Image(systemName: "xmark")
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(.secondary)
                    .frame(width: 28, height: 28)
                    .contentShape(.circle)
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Cancel")
            Button(action: finish) {
                Group {
                    if busy {
                        ProgressView().accessibilityLabel("Recognising")
                    } else {
                        Image(systemName: "circle.fill")
                            .foregroundStyle(.red)
                            .accessibilityLabel("Finish Dictation")
                    }
                }
                .frame(width: 34, height: 34)
                .contentShape(.circle)
            }
            .buttonStyle(.plain)
            .disabled(busy)
        }
        .padding(.leading, 12)
        .padding(.trailing, 6)
    }
}

private struct TerminalRepresentable: UIViewRepresentable {
    let session: TerminalSession
    let newTab: () -> Void
    let closeTab: () -> Void
    let mic: Mic
    let voice: VoiceCapsule
    let dictating: Bool

    struct Coordinator {
        let mic: UIHostingController<Mic>
        let voice: UIHostingController<VoiceCapsule>
    }

    /// The mic and the voice capsule in the key bar.
    func makeCoordinator() -> Coordinator {
        let m = UIHostingController(rootView: mic)
        m.safeAreaRegions = []
        m.view.backgroundColor = .clear
        let v = UIHostingController(rootView: voice)
        v.safeAreaRegions = []
        v.view.backgroundColor = .clear
        return Coordinator(mic: m, voice: v)
    }

    /// The view lands without the keyboard, and on a phone with its key
    /// bar: a tap gives it the keyboard, as one does in the terminal.
    func makeUIView(context: Context) -> TerminalHost {
        let host = TerminalHost(TerminalView(session: session))
        for (sub, container) in [(context.coordinator.mic.view!, host.terminal.bar.mic),
                                 (context.coordinator.voice.view!, host.terminal.bar.voice)] {
            sub.frame = container.bounds
            sub.autoresizingMask = [.flexibleWidth, .flexibleHeight]
            container.addSubview(sub)
        }
        updateUIView(host, context: context)
        return host
    }

    func updateUIView(_ v: TerminalHost, context: Context) {
        v.terminal.newTab = newTab
        v.terminal.closeTab = closeTab
        context.coordinator.mic.rootView = mic
        context.coordinator.voice.rootView = voice
        v.terminal.bar.setVoice(active: dictating)
    }

    static func dismantleUIView(_ v: TerminalHost, coordinator: Coordinator) { v.terminal.close() }
}

extension View {
    /// Asks before a tap stops the speech model's download.
    func stopsDownload(_ asking: Binding<Bool>) -> some View {
        alert("Stop Downloading the Speech Model?", isPresented: asking) {
            Button("Stop Downloading", role: .destructive) { Dictation.shared.cancel() }
            Button("Keep Downloading", role: .cancel) {}
        }
    }
}
