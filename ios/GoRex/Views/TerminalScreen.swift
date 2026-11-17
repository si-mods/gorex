import AVFAudio
import GameController
import SwiftUI
import UIKit

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
    /// The way the last switch went, 1 from the right, -1 from the left,
    /// for the title's push.
    @State private var slideDir: CGFloat = 1
    /// How many tab switches are sliding: while any is, the terminals'
    /// layout snaps, and the pages beside it stay until the last ends.
    @State private var switching = 0
    /// The terminals on the page: the current one, one that waits to slide
    /// in, and those still sliding out.
    @State private var pages: [Pane] = []
    /// The pane of a switch that waits for its terminal to draw before it
    /// slides in. A newer switch takes its place.
    @State private var incoming: UUID?
    /// The page's width, which a pane's place is a fraction of.
    @State private var pageWidth: CGFloat = 0
    /// Whether the terminal has been reconnecting, or holding what was
    /// typed, for a second: a short reconnect shows nothing.
    @State private var stalled = false
    /// Whether the floating pane map shows, briefly after a pane switch.
    @State private var paneMapShown = false
    /// The task that hides the floating map.
    @State private var paneMapTask: Task<Void, Never>?
    /// Whether the screen is gone: a slide finishing after it must not start
    /// the map again.
    @State private var gone = false

    init(target: TerminalTarget, host: Host, go: @escaping (TerminalTarget?) -> Void) {
        self.target = target
        self.host = host
        self.go = go
        _currentTarget = State(initialValue: Self.remembered(target, host: host))
    }

    var body: some View {
        // The terminal's background fills the page edge to edge, under the
        // bar and the key bar too; the bar and the text on it take its scheme.
        let bg = session?.background ?? .black
        let scheme: ColorScheme = bg.light ? .light : .dark
        // The terminal goes up under the clear bar and the status bar: its
        // grid starts below them, and its scrollback fills the band.
        // Each pane sits at its place across the page: off it, to the
        // left or right, or on it. Only the current one takes touches.
        ZStack {
            if pages.isEmpty { Color.clear }
            ForEach(pages) { pane in
                let current = pane.session === session
                TerminalRepresentable(session: pane.session, newTab: newTab, closeTab: closeTab, newSplit: newSplit, mic: mic, voice: voiceCapsule, dictating: said != nil && current, snaps: switching > 0,
                                      paneInset: paneCellList.count > 1 ? PaneBar.height + 20 : 0,
                                      onSwipe: { stepPane($0) },
                                      canSwipe: { switching == 0 && incoming == nil && (terminal?.paneSwipeAllowed ?? false) })
                    .offset(x: pane.x * pageWidth)
                    // Before the page's width is known, off the page is hidden.
                    .opacity(pageWidth == 0 && pane.x != 0 ? 0 : 1)
                    .allowsHitTesting(current)
                    .accessibilityHidden(!current)
            }
        }
            .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { pageWidth = $0 }
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
            // The reconnect's note and the window's panes, above the key
            // bar; they ride the keyboard too.
            .overlay(alignment: .bottom) {
                VStack(spacing: 10) {
                    if stalled, let session {
                        Reconnecting(waitingToSend: session.waitingToSend)
                            .transition(.move(edge: .bottom).combined(with: .opacity))
                    }
                    if paneCellList.count > 1 {
                        PaneBar(cells: paneCellList, current: currentTarget.block, program: { .of(host.blocks[$0]?.program) }, select: selectPane, newSplit: newSplit)
                            .transition(.opacity.combined(with: .move(edge: .bottom)))
                    }
                }
                .padding(.bottom, hasBar ? KeyBar.height + 10 : 10)
            }
            // The layout, floating under the toolbar's right side for a
            // moment after a switch, as Rex Go shows it.
            .overlay(alignment: .topTrailing) {
                if paneMapShown {
                    PaneMiniMap(cells: paneCellList, current: currentTarget.block, currentColor: paneAccent(currentTarget.block))
                        .frame(width: 84, height: 56)
                        .padding(.top, 8)
                        .padding(.trailing, 12)
                        .transition(.opacity.combined(with: .scale(scale: 0.96, anchor: .topTrailing)))
                }
            }
            .animation(.spring(duration: 0.28, bounce: 0.16), value: paneMapShown)
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
                    principalTitlePill
                }
                .sharedBackgroundVisibility(.hidden)
                // The mic where the terminal has no key bar, and the
                // window's menu, New Tab being in the session's sheet and on
                // ⌘T, the split on ⌘D as the desktop's; the bar's glass
                // draws them.
                ToolbarItemGroup(placement: .topBarTrailing) {
                    if !hasBar { mic }
                    Menu {
                        Button("New Tab", systemImage: "plus") { newTab() }
                        Button("Split Pane", systemImage: "rectangle.split.2x1") { newSplit() }
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
                gone = false
                let s = session ?? TerminalSession(host: host, session: currentTarget.session, block: currentTarget.block)
                session = s
                if pages.isEmpty { pages = [Pane(session: s, block: currentTarget.block, x: 0)] }
                if let key = viewedKey { ViewedBlockStore.shared.set(currentTarget.block, for: key) }
                s.start()
                Notifier.shared.onScreen = currentTarget
                host.see(session: currentTarget.session, block: currentTarget.block)
            }
            .onDisappear {
                gone = true
                paneMapTask?.cancel()
                terminal?.stopDictating()
                dropIncoming()
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
                    // A switch still waiting stops: only the shown stream suspends.
                    dropIncoming()
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
        if let w = window, let pane = host.paneTitle(currentTarget.block, in: w), !pane.isEmpty { return pane }
        let live = window.map { host.title($0) } ?? ""
        return [live, currentTarget.title].first { !$0.isEmpty } ?? sessionLabel
    }

    /// The name of the window's session.
    private var sessionLabel: String {
        hostSession?.label ?? ""
    }

    private var sessionWindows: [(block: String, window: SessionView.Window)] {
        (hostSession?.view?.windows ?? []).compactMap { w in w.block.map { (block: $0, window: w) } }
    }

    /// Where this device remembers the window's last viewed pane.
    private var viewedKey: ViewedBlockKey? {
        guard let w = window else { return nil }
        return Self.key(for: w, session: currentTarget.session, host: host)
    }

    /// The window's panes as the map and the bar show them, from the view's
    /// split tree and the host's names and records.
    private var paneCellList: [PaneCell] {
        guard let w = window else { return [] }
        return paneCells(
            layout: w.layout,
            panes: w.panes,
            name: { host.paneTitle($0, in: w) ?? String(localized: "Terminal") },
            record: { block in host.status([block]).map { PaneRecord(state: $0.state) } }
        )
    }

    private static func key(for w: SessionView.Window, session: String, host: Host) -> ViewedBlockKey {
        ViewedBlockKey(machine: host.spaceMachineKey, session: session, window: w.window_id)
    }

    /// The target as this device opens it: the pane it last viewed of the
    /// window takes the place of the window's focused one, when the target
    /// names that focused pane (a row's, not a notification's). An explicit
    /// other pane stays as given.
    private static func remembered(_ target: TerminalTarget, host: Host) -> TerminalTarget {
        guard !target.explicit else { return target }
        guard let s = host.sessions.first(where: { $0.id == target.session }),
              let w = s.view?.windows?.first(where: { $0.holds(target.block) }),
              target.block == w.block
        else { return target }
        let memory = key(for: w, session: target.session, host: host)
        guard let stored = ViewedBlockStore.shared.block(for: memory) else { return target }
        guard w.panes.contains(stored), stored != target.block else {
            if !w.panes.contains(stored) { ViewedBlockStore.shared.forget(memory) }
            return target
        }
        return TerminalTarget(host: target.host, session: target.session, block: stored, title: host.paneTitle(stored, in: w) ?? target.title)
    }

    /// Switches to a pane of the window open, remembering it on this device.
    /// The shared layout's focus is not touched.
    private func selectPane(_ block: String) {
        guard let w = window, w.panes.contains(block), block != currentTarget.block else { return }
        if let key = viewedKey { ViewedBlockStore.shared.set(block, for: key) }
        let from = w.panes.firstIndex(of: currentTarget.block) ?? 0
        let to = w.panes.firstIndex(of: block) ?? from
        switchTo(TerminalTarget(host: host.id, session: currentTarget.session, block: block, title: host.paneTitle(block, in: w) ?? host.title(w)), dir: to >= from ? 1 : -1)
    }

    /// Moves to the pane nearest in a direction, for a two-finger swipe.
    private func stepPane(_ direction: PaneDirection) {
        guard let next = paneNeighbor(paneCellList, from: currentTarget.block, direction: direction) else { return }
        selectPane(next)
    }

    /// Shows the layout under the toolbar's right side for a moment, then
    /// takes it away. Only a pane switch does this; changing tabs does not.
    private func flashPaneMap() {
        guard !gone else { return }
        paneMapTask?.cancel()
        withAnimation(.spring(duration: 0.28, bounce: 0.16)) { paneMapShown = true }
        paneMapTask = Task { @MainActor in
            try? await Task.sleep(for: .seconds(4))
            guard !Task.isCancelled else { return }
            withAnimation(.easeOut(duration: 0.2)) { paneMapShown = false }
        }
    }

    /// The color Rex Go gives the current pane in the map: its program's
    /// tile, preferring the foreground unless that is white.
    private func paneAccent(_ block: String) -> Color {
        let program = Program.of(host.blocks[block]?.program)
        return Color(rgb: program.tileFg == 0xffffff ? program.tileBg : program.tileFg)
    }

    /// A terminal on the page, at x widths from its place: -1 off to the
    /// left, 0 on the page, 1 off to the right. Its id is its own, not its
    /// block's: a block can come back while its last pane still slides out.
    private struct Pane: Identifiable {
        let id = UUID()
        let session: TerminalSession
        let block: String
        var x: CGFloat
    }

    /// Opens the window's block in place of this one: it opens off the
    /// page, on the side it comes from, and slides in once its terminal
    /// has drawn what the block shows, or after 300 ms.
    private func switchTo(_ target: TerminalTarget, dir: CGFloat? = nil) {
        guard target.host == currentTarget.host else {
            go(target)
            return
        }
        let next = Self.remembered(target, host: host)
        guard next.session == currentTarget.session else {
            go(next)
            return
        }
        guard next.block != currentTarget.block else {
            // Back to the one shown: the switch still waiting stops.
            dropIncoming()
            return
        }
        if let s = host.sessions.first(where: { $0.id == next.session }),
           let w = s.view?.windows?.first(where: { $0.holds(next.block) }) {
            ViewedBlockStore.shared.set(next.block, for: Self.key(for: w, session: next.session, host: host))
        }
        // The same block again, as a second swipe while it opens, waits on.
        if let id = incoming, pages.first(where: { $0.id == id })?.block == next.block { return }
        let resolvedDir: CGFloat
        if let dir {
            resolvedDir = dir
        } else {
            let all = sessionWindows
            let fromIdx = all.firstIndex { $0.window.holds(currentTarget.block) } ?? 0
            let toIdx = all.firstIndex { $0.window.holds(next.block) } ?? fromIdx
            resolvedDir = toIdx >= fromIdx ? 1 : -1
        }
        dropIncoming()
        // The terminal opens off the page, without animation, so that its
        // surface is made before the slide, not in its first frame.
        let nextSession = TerminalSession(host: host, session: next.session, block: next.block)
        nextSession.start()
        let pane = Pane(session: nextSession, block: next.block, x: resolvedDir)
        let id = pane.id
        var t = Transaction()
        t.disablesAnimations = true
        withTransaction(t) { pages.append(pane) }
        incoming = id
        Task { @MainActor in
            // Until the terminal has drawn the snapshot, or 300 ms; a
            // failure ends the wait, to show on the new pane.
            let deadline = ContinuousClock.now + .milliseconds(300)
            @MainActor func waits() -> Bool {
                switch nextSession.state {
                case .connecting: true
                case .live: (nextSession.output as? TerminalView)?.drawn != true
                default: false
                }
            }
            while waits(), ContinuousClock.now < deadline, incoming == id {
                try? await Task.sleep(for: .milliseconds(16))
            }
            guard incoming == id else { return }
            promote(id, to: next, dir: resolvedDir)
        }
    }

    @ViewBuilder private var principalTitlePill: some View {
        let k = ProgramTiles.scale
        let small: CGFloat = 0.85
        let edge: Edge = slideDir > 0 ? .trailing : .leading
        HStack(spacing: 10) {
            ProgramTile(program: .of(host.blocks[currentTarget.block]?.program))
                .frame(width: 21 * k, height: 16 * k)
                .scaleEffect(small)
                .frame(width: 21 * k * small, height: 16 * k * small)
            Text(title.asText)
                .font(.body.weight(.semibold))
                .lineLimit(1)
                .truncationMode(.tail)
                .id(currentTarget.block)
                .transition(.push(from: edge))
        }
        .padding(.leading, 12)
        .padding(.trailing, 18)
        .frame(minWidth: 180)
        .frame(height: 44)
        .clipped()
        .background { Capsule().fill(.clear).glassEffect(.regular, in: .capsule) }
        .contentShape(.capsule)
        .accessibilityAddTraits(.isButton)
        .offset(x: pillDrag)
        // A tap or a long press opens the session's sheet, whose rows carry
        // the tile of each window's programs; the menu a long press once
        // opened had no room for a tile.
        .onTapGesture { sheet = true }
        .onLongPressGesture(minimumDuration: 0.4) { sheet = true }
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
        .sensoryFeedback(.selection, trigger: currentTarget.block)
    }

    /// Slides the waiting pane in and the current one out, the way the
    /// switch goes, and closes the current one's stream.
    private func promote(_ id: UUID, to next: TerminalTarget, dir: CGFloat) {
        guard let i = pages.firstIndex(where: { $0.id == id }) else { return }
        incoming = nil
        let nextSession = pages[i].session
        terminal?.stopDictating()
        host.see(session: currentTarget.session, block: currentTarget.block)
        session?.close()
        Notifier.shared.onScreen = next
        host.see(session: next.session, block: next.block)
        switching += 1
        let paneSwitch = next.session == currentTarget.session && (window?.panes.contains(next.block) ?? false)
        withAnimation(.spring(duration: 0.32, bounce: 0.12), completionCriteria: .removed) {
            slideDir = dir
            if let j = pages.firstIndex(where: { $0.session === session }) { pages[j].x = -dir }
            pages[i].x = 0
            session = nextSession
            currentTarget = next
        } completion: {
            if paneSwitch { flashPaneMap() }
            switching -= 1
            // Once no slide is left, the pages beside it go, but one
            // that waits to slide in.
            guard switching == 0 else { return }
            pages.removeAll { $0.session !== session && $0.id != incoming }
        }
    }

    /// Drops the pane of a switch that still waits, and closes its stream.
    private func dropIncoming() {
        guard let id = incoming else { return }
        incoming = nil
        pages.first(where: { $0.id == id })?.session.close()
        var t = Transaction()
        t.disablesAnimations = true
        withTransaction(t) { pages.removeAll { $0.id == id } }
    }

    /// Steps from the window a switch still waits to show, if any, so a
    /// second quick swipe goes one window farther.
    private func stepTab(by delta: Int) {
        let all = sessionWindows
        let from = incoming.flatMap { id in pages.first(where: { $0.id == id })?.block } ?? currentTarget.block
        guard all.count > 1, let i = all.firstIndex(where: { $0.window.holds(from) }) else { return }
        let next = all[(i + delta + all.count) % all.count]
        guard !next.window.holds(from) else { return }
        switchTo(TerminalTarget(host: host.id, session: currentTarget.session, block: next.block, title: host.title(next.window)), dir: delta > 0 ? 1 : -1)
    }

    /// Splits the pane in place, a new shell beside it in the same window,
    /// and goes to it.
    private func newSplit() {
        Task {
            do {
                if let b = try await host.newSplit(beside: currentTarget.block, in: currentTarget.session) {
                    switchTo(TerminalTarget(host: host.id, session: currentTarget.session, block: b, title: ""), dir: 1)
                }
            } catch {
                failure = ("Can't Split the Pane", error.localizedDescription)
            }
        }
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
            // A reconnect shows its note above the key bar instead.
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

/// The note of a terminal that reconnects, above the key bar, or above the
/// keyboard where the terminal has none: it lets the terminal show, and
/// touches go through it.
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
        private let edgeView: EdgeFadeView

        init(color: UIColor) {
            self.edgeView = EdgeFadeView(edge: .top, color: color)
            super.init(nibName: nil, bundle: nil)
        }

        @available(*, unavailable)
        required init?(coder: NSCoder) { fatalError() }

        override func loadView() {
            view = edgeView
        }

        override func viewWillAppear(_ animated: Bool) {
            super.viewWillAppear(animated)
            if let tc = transitionCoordinator {
                edgeView.blurView.alpha = 0
                tc.animate(alongsideTransition: { _ in
                    self.edgeView.blurView.alpha = 1
                }, completion: { ctx in
                    if ctx.isCancelled { self.edgeView.blurView.alpha = 0 }
                })
            } else {
                edgeView.blurView.alpha = 1
            }
        }

        override func viewDidAppear(_ animated: Bool) {
            super.viewDidAppear(animated)
            edgeView.blurView.alpha = 1
        }

        override func viewWillDisappear(_ animated: Bool) {
            super.viewWillDisappear(animated)
            if let tc = transitionCoordinator {
                tc.animate(alongsideTransition: { _ in
                    self.edgeView.blurView.alpha = 0
                }, completion: { ctx in
                    if ctx.isCancelled { self.edgeView.blurView.alpha = 1 }
                })
            } else {
                edgeView.blurView.alpha = 0
            }
        }

        func setColor(_ c: UIColor) {
            edgeView.setColor(c)
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
            Button {
                Haptics.tapped()
                cancel()
            } label: {
                Image(systemName: "xmark")
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(.secondary)
                    .frame(width: 28, height: 28)
                    .contentShape(.circle)
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Cancel")
            Button {
                Haptics.tapped()
                finish()
            } label: {
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
    let newSplit: () -> Void
    let mic: Mic
    let voice: VoiceCapsule
    let dictating: Bool
    let snaps: Bool
    /// How far the pane bar reaches over the terminal, for the cursor to
    /// stay above it; 0 when the bar is away.
    let paneInset: CGFloat
    /// Moves to the pane next to the current one, the way a two-finger
    /// swipe goes.
    let onSwipe: (PaneDirection) -> Void
    /// Whether a swipe may switch now: no switch under way, no selection,
    /// no full-screen program with the mouse.
    let canSwipe: () -> Bool

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
        v.snaps = snaps
        v.terminal.newTab = newTab
        v.terminal.closeTab = closeTab
        v.terminal.newSplit = newSplit
        v.terminal.paneInset = paneInset
        v.aboveBar = paneInset
        v.onSwipe = onSwipe
        v.canSwipe = canSwipe
        v.setBottomEdgeColor(UIColor(cgColor: (session.background ?? .black).cgColor))
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
