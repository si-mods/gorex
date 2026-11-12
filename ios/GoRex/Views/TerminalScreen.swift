import AVFAudio
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
    /// What is said while the mic is on.
    @State private var said: Dictation.Said?

    init(target: TerminalTarget, host: Host, go: @escaping (TerminalTarget?) -> Void) {
        self.target = target
        self.host = host
        self.go = go
    }

    var body: some View {
        // The terminal's background fills the page edge to edge, under the
        // bar and the key bar too; the bar and the text on it take its scheme.
        let bg = session?.background ?? .black
        let scheme: ColorScheme = bg.light ? .light : .dark
        // The terminal goes up under the clear bar and the status bar: its
        // grid starts below them, and its scrollback fills the band.
        Group { if let session { TerminalRepresentable(session: session, newTab: newTab, closeTab: closeTab) } else { Color.clear } }
            .ignoresSafeArea(.container, edges: .top)
            .ignoresSafeArea(.keyboard)
            .overlay(alignment: .top) { if let p = session?.progress { ProgressLine(progress: p).padding(.top, statusBar).ignoresSafeArea(.container, edges: .top) } }
            .background {
                // The status bar is the window's inset, read as the page's changes.
                Color(cgColor: bg.cgColor)
                    .ignoresSafeArea()
                    .onGeometryChange(for: CGFloat.self) { $0.safeAreaInsets.top } action: { _ in statusBar = Self.statusBar }
            }
            // What goes up under the status bar blurs and fades into the
            // background, for the clock over it.
            .overlay(alignment: .top) { TopEdge(color: Color(cgColor: bg.cgColor), height: statusBar + 10) }
            .overlay(alignment: .top) { if let said { Heard(said: said) } }
            .overlay { status }
            .navigationTitle(title.asText)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                // The title floats in a capsule of glass, with the tile of
                // the window's program, as its row in the list shows it; a
                // tap opens the session's sheet.
                ToolbarItem(placement: .principal) {
                    // As tall as the back button beside it.
                    Button { sheet = true } label: {
                        HStack(spacing: 10) {
                            let k = ProgramTiles.scale, small = 0.85
                            ProgramTile(program: .of(host.blocks[target.block]?.program))
                                .frame(width: 21 * k, height: 16 * k)
                                .scaleEffect(small)
                                .frame(width: 21 * k * small, height: 16 * k * small)
                            Text(title.asText).font(.body.weight(.semibold)).lineLimit(1)
                        }
                        .padding(.leading, 12)
                        .padding(.trailing, 18)
                        .frame(height: 44)
                        // Glass behind, not over: on the content it would fade the tile.
                        .background { Capsule().fill(.clear).glassEffect(.regular, in: .capsule) }
                    }
                    .buttonStyle(.plain)
                }
                .sharedBackgroundVisibility(.hidden)
                // The mic, and the window's menu, New Tab being in the
                // session's sheet and on ⌘T; the bar's glass draws them.
                ToolbarItemGroup(placement: .topBarTrailing) {
                    Mic(terminal: { terminal }, said: $said)
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
            .sheet(isPresented: $sheet) { SessionSheet(target: target, host: host, go: go, newTab: newTab).environment(\.colorScheme, scheme) }
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
                let s = session ?? TerminalSession(host: host, session: target.session, block: target.block)
                session = s
                s.start()
                Notifier.shared.onScreen = target
                host.see(target.block)
            }
            .onDisappear {
                host.see(target.block)
                session?.close()
                if Notifier.shared.onScreen == target { Notifier.shared.onScreen = nil }
            }
            .alert(failure?.title ?? "", isPresented: Binding { failure != nil } set: { if !$0 { failure = nil } }) {
            } message: {
                Text(failure?.why ?? "")
            }
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

    /// The terminal showing the session.
    private var terminal: TerminalView? { session?.output as? TerminalView }

    /// The session of the window open, as the host has it.
    private var hostSession: Host.Session? {
        host.sessions.first { $0.id == target.session }
    }

    /// The window open, as its session's view has it.
    private var window: SessionView.Window? {
        hostSession?.view?.windows?.first { $0.holds(target.block) }
    }

    /// The window's title as its row shows it, when the block open is the
    /// one in front, else as the row gave it, else its session's name, as
    /// for a new session.
    private var title: String {
        let live = window.flatMap { $0.block == target.block ? host.title($0) : nil } ?? ""
        return [live, target.title].first { !$0.isEmpty } ?? sessionLabel
    }

    /// The name of the window's session.
    private var sessionLabel: String {
        hostSession?.label ?? ""
    }

    /// Opens a new window in the session, and goes to it.
    private func newTab() {
        Task {
            do {
                if let b = try await host.newWindow(in: target.session) {
                    go(TerminalTarget(host: host.id, session: target.session, block: b, title: ""))
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
                try await host.closeWindow(w.window_id, in: target.session)
                go(nil)
            } catch {
                failure = ("Can't Close the Tab", error.localizedDescription)
            }
        }
    }

    @ViewBuilder private var status: some View {
        switch session?.state ?? .connecting {
        case .connecting:
            ProgressView()
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

/// The mic: a tap turns it on, and what is said shows as it is spoken,
/// recognised on the phone (Dictation); a tap turns it off and types it
/// into the terminal, unsent, once it spins as its last phrase is. Its
/// first tap asks to download the models, and while they download it shows
/// how far, and a tap asks to stop it. It goes off, typing nothing, as it
/// goes away with the screen or a call comes.
private struct Mic: View {
    let terminal: () -> TerminalView?
    @Binding var said: Dictation.Said?
    @State private var on = false
    @State private var busy = false
    @State private var asking = false
    @State private var stopping = false
    private let dictation = Dictation.shared

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
                on = false
                busy = true
                Task {
                    await terminal()?.typeDictation()
                    busy = false
                }
            } else if !dictation.installed {
                asking = true
            } else {
                on = terminal()?.dictate {
                    said = $0
                    if $0 == nil { on = false }
                } == true
            }
        }
        .onDisappear { terminal()?.stopDictating() }
        .onReceive(NotificationCenter.default.publisher(for: AVAudioSession.interruptionNotification)) { _ in terminal()?.stopDictating() }
        .accessibilityAddTraits(on ? [.isButton, .isSelected] : .isButton)
        .alert("Download the Speech Model?", isPresented: $asking) {
            Button("Download") { dictation.download() }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Speech is recognised on this device, by SenseVoice. Its model, \(Dictation.size.formatted(.byteCount(style: .file))), downloads once from Hugging Face, or from hf-mirror.com, a mirror run by others, where Hugging Face can't be reached. Delete it in Settings › Terminal.")
        }
        .stopsDownload($stopping)
        .alert("Can't Download the Speech Model", isPresented: Binding { dictation.error != nil } set: { if !$0 { dictation.error = nil } }) {
        } message: {
            Text(dictation.error ?? "")
        }
    }
}

/// What is said while the mic is on, under the bar, in glass as the
/// screen's notes are: the microphone's level, then what is for good, and
/// in grey the phrase still open; three lines at most, the newest.
private struct Heard: View {
    let said: Dictation.Said

    var body: some View {
        let settled = said.settled
        var open = AttributedString(said.text.dropFirst(settled.count))
        open.foregroundColor = .secondary
        let text = Text(AttributedString(settled) + open).font(.callout)
        return HStack(spacing: 10) {
            Level()
            ViewThatFits(in: .vertical) {
                text
                ScrollView { text }.defaultScrollAnchor(.bottom).scrollIndicators(.hidden)
            }
            .frame(maxWidth: .infinity, maxHeight: 64, alignment: .leading)
        }
        .padding(12)
        .glassEffect(.regular, in: .rect(cornerRadius: 14))
        .padding(.horizontal, 12)
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
/// color at the top, clear below.
private struct TopEdge: View {
    let color: Color
    /// How far down from the top of the screen it reaches.
    let height: CGFloat

    var body: some View {
        let fade = LinearGradient(stops: [.init(color: .black, location: 0), .init(color: .black, location: 0.35), .init(color: .clear, location: 1)],
                                  startPoint: .top, endPoint: .bottom)
        // From the top of the screen, height down.
        VStack(spacing: 0) {
            ZStack {
                Rectangle().fill(.ultraThinMaterial)
                color.opacity(0.75)
            }
            .mask(fade)
            .frame(height: height)
            Spacer(minLength: 0)
        }
        .ignoresSafeArea(.container, edges: .top)
        .allowsHitTesting(false)
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

private struct TerminalRepresentable: UIViewRepresentable {
    let session: TerminalSession
    let newTab: () -> Void
    let closeTab: () -> Void

    /// The view lands without the keyboard: a tap gives it focus, as one
    /// does in the terminal.
    func makeUIView(context: Context) -> TerminalHost {
        let host = TerminalHost(TerminalView(session: session))
        updateUIView(host, context: context)
        return host
    }

    func updateUIView(_ v: TerminalHost, context: Context) {
        v.terminal.newTab = newTab
        v.terminal.closeTab = closeTab
    }

    static func dismantleUIView(_ v: TerminalHost, coordinator: ()) { v.terminal.close() }
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
