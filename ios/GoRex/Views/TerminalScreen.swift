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
    /// The panes asked to close, until the server answers.
    @State private var closing: Set<String> = []
    /// Whether the session's sheet is open, as the title opens it.
    @State private var sheet = false
    /// Whether the diagnostics of a failed terminal show.
    @State private var diagnostics = false
    /// The height of the status bar.
    @State private var statusBar: CGFloat = 0
    /// Whether the status bar is in a column at the side, as on a folded phone.
    /// The header buttons then sit in a row at the top, and the terminal
    /// reaches the edge of the window.
    @State private var sideBar = false
    /// Regular width has the split view's sidebar toggle in the bar, which
    /// side mode would hide.
    @Environment(\.horizontalSizeClass) private var width
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
    /// in, those still sliding out, and the window's other panes, kept open
    /// off the page.
    @State private var pages: [Pane] = []
    /// The pane of a switch that waits for its terminal to draw before it
    /// slides in. A newer switch takes its place.
    @State private var incoming: UUID?
    /// The page's size, which a pane's place is a fraction of.
    @State private var pageSize: CGSize = .zero
    /// The two-finger drag between panes under way, if any.
    @State private var paneDrag: PaneDrag?
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
        // From the first frame, so the bar does not show and then go.
        _sideBar = State(initialValue: Self.sideStatusBar(Self.windowInsets))
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
                // Under the fingers too, layout snaps.
                TerminalRepresentable(session: pane.session, newTab: newTab, closeTab: closeTab, newSplit: newSplit, mic: mic, voice: voiceCapsule, dictating: said != nil && current, snaps: switching > 0 || paneDrag != nil,
                                      paneInset: paneCount > 1 ? PaneBar.height + 20 : 0,
                                      onPage: !pane.kept, current: current,
                                      onPaneDrag: { dragPane($0, $1, $2) },
                                      canSwipe: { switching == 0 && incoming == paneDrag?.pane && (terminal?.paneSwipeAllowed ?? false) })
                    .offset(x: pane.x * pageSize.width, y: pane.y * pageSize.height)
                    // A pane kept off the page is hidden, and so is off the
                    // page before the page's size is known.
                    .opacity(pane.kept || (pageSize.width == 0 && (pane.x != 0 || pane.y != 0)) ? 0 : 1)
                    .allowsHitTesting(current)
                    .accessibilityHidden(!current)
            }
        }
            .onGeometryChange(for: CGSize.self) { $0.size } action: { pageSize = $0 }
            .clipped()
            .ignoresSafeArea(.container, edges: Self.edges(side: sideBar, row: sideMode, insets: Self.windowInsets))
            .ignoresSafeArea(.keyboard)
            .overlay(alignment: .top) { if let p = session?.progress { ProgressLine(progress: p).padding(.top, statusBar).ignoresSafeArea(.container, edges: .top) } }
            .background {
                // The status bar is the window's inset, read as the page's changes.
                Color(cgColor: bg.cgColor)
                    .ignoresSafeArea()
                    .onGeometryChange(for: CGSize.self) { $0.size } action: { _ in
                        statusBar = Self.statusBar
                        sideBar = Self.sideStatusBar(Self.windowInsets)
                    }
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
                    if paneCount > 1 {
                        // The panes' names and records are read in the bar's
                        // own body: a title that changes redraws the bar alone.
                        OwnBody {
                            PaneBar(cells: paneCellList, current: currentTarget.block, program: { .of(host.blocks[$0]?.program) }, select: selectPane, newSplit: newSplit, close: closePane)
                        }
                        .transition(.opacity.combined(with: .move(edge: .bottom)))
                    }
                }
                .padding(.bottom, hasBar ? KeyBar.height + 10 : 10)
            }
            // The layout, floating under the toolbar's right side for a
            // moment after a switch, as Rex Go shows it.
            .overlay(alignment: .topTrailing) {
                if paneMapShown {
                    OwnBody { PaneMiniMap(cells: paneCellList, current: currentTarget.block, currentColor: paneAccent(currentTarget.block)) }
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
            // The title is read in a body of its own, as the pill's is: a
            // terminal's title that changes does not draw the screen again.
            .modifier(ScreenTitle(host: host, target: currentTarget))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                // The title floats in a capsule of glass, with the tile of
                // the window's program, as its row in the list shows it; a
                // tap opens the session's sheet.
                ToolbarItem(placement: .principal) {
                    OwnBody { principalTitlePill }
                }
                .sharedBackgroundVisibility(.hidden)
                // The mic where the terminal has no key bar, and the
                // window's menu, New Tab being in the session's sheet and on
                // ⌘T, the split on ⌘D as the desktop's; the bar's glass
                // draws them.
                if #available(iOS 27.1, *) {
                    // The bar's buttons stay in its row: the system would
                    // otherwise sink them into the clock's column, away
                    // from the title, when the status bar is at a side.
                    if sideMode {
                        ToolbarItem(placement: .topBarLeading) {
                            backButton
                        }
                        .axisBehavior(.horizontalOnly)
                    }
                    ToolbarItemGroup(placement: .topBarTrailing) {
                        barTrailing
                    }
                    .axisBehavior(.horizontalOnly)
                } else {
                    ToolbarItemGroup(placement: .topBarTrailing) {
                        barTrailing
                    }
                }
            }
            .navigationBarBackButtonHidden(sideMode)
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

            .onChange(of: scheme, initial: true) { _, s in ScreenStyle.shared.terminal = s }
            .onAppear {
                gone = false
                let s = session ?? TerminalSession(host: host, session: currentTarget.session, block: currentTarget.block)
                session = s
                if pages.isEmpty { pages = [Pane(session: s, block: currentTarget.block, x: 0)] }
                if let key = viewedKey { ViewedBlockStore.shared.set(currentTarget.block, for: key) }
                s.start()
                if let keptPanes { keep(keptPanes) }
                Notifier.shared.onScreen = currentTarget
                host.see(session: currentTarget.session, block: currentTarget.block)
            }
            .onDisappear {
                gone = true
                ScreenStyle.shared.terminal = nil
                paneMapTask?.cancel()
                terminal?.stopDictating()
                dropIncoming()
                host.see(session: currentTarget.session, block: currentTarget.block)
                // Every stream closes; the panes kept off the page open
                // again with the screen.
                for p in pages { p.session.close() }
                pages.removeAll { $0.session !== session }
                if Notifier.shared.onScreen == currentTarget { Notifier.shared.onScreen = nil }
            }
            // The window's other panes open once the current one is live,
            // and follow its panes as they split and close.
            .onChange(of: keptPanes, initial: true) { _, panes in
                if let panes { keep(panes) }
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
                    // A switch still waiting stops; the window's panes suspend.
                    dropIncoming()
                    terminal?.stopDictating()
                    for pane in pages where !pane.leaving { pane.session.suspend() }
                    suspended = true
                }
                if p == .active, suspended {
                    for pane in pages where !pane.leaving { pane.session.resume() }
                    suspended = false
                    if let keptPanes { keep(keptPanes) }
                }
            }
    }

    /// Whether the header sits in a row at the top: a status bar at the
    /// side, in compact width.
    private var sideMode: Bool { sideBar && width != .regular }

    /// The height of the status bar, the window's top inset.
    private static var statusBar: CGFloat { windowInsets.top }

    /// The key window's safe area insets.
    private static var windowInsets: UIEdgeInsets {
        return UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }
            .first { $0.activationState == .foregroundActive }?.keyWindow?.safeAreaInsets ?? .zero
    }

    /// The edges the page reaches: the bar's column at the side, so the
    /// grid fills the window. A phone gives both sides up, its page being the
    /// window; a wide window gives up the column's own side only, or its page
    /// would reach under the list.
    private static func edges(side: Bool, row: Bool, insets: UIEdgeInsets) -> Edge.Set {
        guard side else { return .top }
        if row { return .horizontal }
        let column: Edge.Set = insets.left > insets.right ? .leading : .trailing
        return [.top, column]
    }

    /// Whether the insets put the status bar in a column at one side: a wide
    /// inset on one side only, wider than the top. A phone turned sideways has
    /// both sides alike, and an iPad has none.
    private static func sideStatusBar(_ i: UIEdgeInsets) -> Bool {
        let side = max(i.left, i.right)
        return side >= 40 && min(i.left, i.right) < 10 && i.top < side
    }

    @ViewBuilder private var backButton: some View {
        Button { go(nil) } label: {
            Image(systemName: "chevron.left")
        }
        .accessibilityLabel("Back")
    }

    @ViewBuilder private var barTrailing: some View {
        if !hasBar { mic }
        moreMenu()
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
    private var title: String { Self.windowTitle(host: host, target: currentTarget) }

    /// The title of a target's window, as `title` gives it.
    fileprivate static func windowTitle(host: Host, target: TerminalTarget) -> String {
        let session = host.sessions.first { $0.id == target.session }
        let window = session?.view?.windows?.first { $0.holds(target.block) }
        if let w = window, let pane = host.paneTitle(target.block, in: w), !pane.isEmpty { return pane }
        let live = window.map { host.title($0) } ?? ""
        return [live, target.title].first { !$0.isEmpty } ?? session?.label ?? ""
    }

    private var sessionWindows: [(block: String, window: SessionView.Window)] {
        (hostSession?.view?.windows ?? []).compactMap { w in w.block.map { (block: $0, window: w) } }
    }

    /// Where this device remembers the window's last viewed pane.
    private var viewedKey: ViewedBlockKey? {
        guard let w = window else { return nil }
        return Self.key(for: w, session: currentTarget.session, host: host)
    }

    /// How many panes the window has: as many as `paneCellList` has
    /// cells, read without the panes' names and records.
    private var paneCount: Int { window?.panes.count ?? 0 }

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
        ViewedBlockKey(machine: host.cacheKey, session: session, window: w.window_id)
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

    /// A two-finger drag between panes while the fingers are down: the
    /// current pane's page, the axis it locked to and its way (-1 or 1, as
    /// the fingers go), and the pane it brings beside it. With no pane that
    /// way, at the window's edge, it brings none.
    private struct PaneDrag {
        let page: UUID
        var axis: PaneAxis?
        var sign: CGFloat = 1
        var pane: UUID?
        var next: TerminalTarget?
    }

    /// Follows a two-finger drag: once it locks to an axis, the pane
    /// nearest that way opens beside the current one, and both move with
    /// the fingers. As they lift, the switch is made, far or fast enough,
    /// or the panes spring back.
    private func dragPane(_ state: UIGestureRecognizer.State, _ t: CGPoint, _ v: CGPoint) {
        switch state {
        case .began:
            guard let page = pages.first(where: { $0.session === session })?.id else { return }
            paneDrag = PaneDrag(page: page)
        case .changed:
            guard var d = paneDrag else { return }
            // A switch made elsewhere took the page from under the fingers.
            guard pages.first(where: { $0.id == d.page })?.session === session else {
                paneDrag = nil
                return
            }
            if d.axis == nil {
                guard let axis = paneDragAxis(t) else { return }
                d.axis = axis
                d.sign = (axis == .horizontal ? t.x : t.y) < 0 ? -1 : 1
                if let w = window, let block = paneNeighbor(paneCellList, from: currentTarget.block, direction: paneDragDirection(axis, sign: d.sign)) {
                    let next = TerminalTarget(host: host.id, session: currentTarget.session, block: block, title: host.paneTitle(block, in: w) ?? host.title(w))
                    d.next = next
                    d.pane = openIncoming(next, axis: axis, at: -d.sign)
                }
                paneDrag = d
            }
            follow(d, t)
        case .ended:
            guard let d = paneDrag else { return }
            paneDrag = nil
            guard let axis = d.axis, pages.first(where: { $0.id == d.page })?.session === session else { return }
            let along = axis == .horizontal ? t.x : t.y
            let speed = axis == .horizontal ? v.x : v.y
            let size = axis == .horizontal ? pageSize.width : pageSize.height
            if let pane = d.pane, let next = d.next, incoming == pane,
               paneDragCommits(progress: paneDragProgress(along, sign: d.sign, size: size), velocity: speed * d.sign) {
                if let key = viewedKey { ViewedBlockStore.shared.set(next.block, for: key) }
                promote(pane, to: next, dir: -d.sign, axis: axis)
            } else {
                springBack(d)
            }
        default:
            guard let d = paneDrag else { return }
            paneDrag = nil
            springBack(d)
        }
    }

    /// Puts a drag's panes where the fingers have them, at once: the
    /// current one, and the one it brings right beside it, neither past its
    /// end. With none to bring, the current one gives a little.
    private func follow(_ d: PaneDrag, _ t: CGPoint) {
        guard let axis = d.axis, let i = pages.firstIndex(where: { $0.id == d.page }) else { return }
        let along = axis == .horizontal ? t.x : t.y
        let size = axis == .horizontal ? pageSize.width : pageSize.height
        guard size > 0 else { return }
        var tr = Transaction()
        tr.disablesAnimations = true
        withTransaction(tr) {
            if let p = d.pane, let j = pages.firstIndex(where: { $0.id == p }) {
                let at = d.sign * paneDragProgress(along, sign: d.sign, size: size)
                pages[i].move(axis, to: at)
                pages[j].move(axis, to: at - d.sign)
            } else {
                pages[i].move(axis, to: paneRubberBand(along) / size)
            }
        }
    }

    /// Springs a drag's panes back to their places, the current one on
    /// the page, then drops the one it brought, kept open off the page.
    private func springBack(_ d: PaneDrag) {
        guard let axis = d.axis, pages.first(where: { $0.id == d.page })?.session === session else { return }
        withAnimation(.spring(duration: 0.32, bounce: 0.12), completionCriteria: .removed) {
            if let i = pages.firstIndex(where: { $0.id == d.page }) { pages[i].move(axis, to: 0) }
            if let p = d.pane, let j = pages.firstIndex(where: { $0.id == p }) { pages[j].move(axis, to: -d.sign) }
        } completion: {
            if let p = d.pane, incoming == p { dropIncoming() }
        }
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

    /// A terminal on the page, at x widths and y heights from its place:
    /// -1 off to the left (or above), 0 on the page, 1 off to the right (or
    /// below). Its id is its own, not its block's: a block can come back
    /// while its last pane still slides out.
    private struct Pane: Identifiable {
        let id = UUID()
        let session: TerminalSession
        let block: String
        var x: CGFloat
        var y: CGFloat = 0
        /// Whether it is kept open off the page, hidden, as a pane of the
        /// window open, for a switch to show it at once.
        var kept = false
        /// Whether its stream closed, and it only slides out.
        var leaving = false

        /// Puts it at a place along an axis, and on the page along the other.
        mutating func move(_ axis: PaneAxis, to at: CGFloat) {
            x = axis == .horizontal ? at : 0
            y = axis == .vertical ? at : 0
        }
    }

    /// Opens the window's block in place of this one: it opens off the
    /// page, on the side it comes from, and slides in once its terminal
    /// has drawn what the block shows, or after 300 ms: a pane of the
    /// window kept open has, and slides in at once.
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
        // The same block again, as a second swipe while it opens, waits on;
        // one a drag brings is opened again, as the drag may spring back.
        if paneDrag == nil, let id = incoming, pages.first(where: { $0.id == id })?.block == next.block { return }
        if let s = host.sessions.first(where: { $0.id == next.session }),
           let w = s.view?.windows?.first(where: { $0.holds(next.block) }) {
            ViewedBlockStore.shared.set(next.block, for: Self.key(for: w, session: next.session, host: host))
        }
        let resolvedDir: CGFloat
        if let dir {
            resolvedDir = dir
        } else {
            let all = sessionWindows
            let fromIdx = all.firstIndex { $0.window.holds(currentTarget.block) } ?? 0
            let toIdx = all.firstIndex { $0.window.holds(next.block) } ?? fromIdx
            resolvedDir = toIdx >= fromIdx ? 1 : -1
        }
        let id = openIncoming(next, axis: .horizontal, at: resolvedDir)
        guard let nextSession = pages.first(where: { $0.id == id })?.session else { return }
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

    /// Opens a block's terminal off the page, at a place along an axis, as
    /// the switch that waits: a newer one drops it. It opens without
    /// animation, so that its surface is made before the slide, not in its
    /// first frame. A pane of the window kept open off the page is shown
    /// there, drawn already. It owns its block once it slides in.
    private func openIncoming(_ next: TerminalTarget, axis: PaneAxis, at: CGFloat) -> UUID {
        dropIncoming()
        var t = Transaction()
        t.disablesAnimations = true
        // Kept, or still sliding out: its stream is open, and a block
        // streams once.
        if let i = pages.firstIndex(where: { !$0.leaving && $0.session !== session && $0.block == next.block }) {
            withTransaction(t) {
                pages[i].move(axis, to: at)
                pages[i].kept = false
            }
            incoming = pages[i].id
            return pages[i].id
        }
        let nextSession = TerminalSession(host: host, session: next.session, block: next.block, owns: false)
        nextSession.start()
        var pane = Pane(session: nextSession, block: next.block, x: 0)
        pane.move(axis, to: at)
        withTransaction(t) { pages.append(pane) }
        incoming = pane.id
        return pane.id
    }

    /// The window's panes to keep open, once the current one is live; nil
    /// before, and without the window.
    private var keptPanes: [String]? {
        guard session?.state == .live, let w = window else { return nil }
        return w.panes
    }

    /// Opens the window's panes that have no terminal, off the page and
    /// not owning their blocks, and closes those kept that left it.
    private func keep(_ panes: [String]) {
        guard !gone, !suspended else { return }
        let open = pages.filter { !$0.leaving }
        let change = paneKeepChanges(open: open.map(\.block), kept: open.filter(\.kept).map(\.block), panes: panes)
        guard !change.open.isEmpty || !change.close.isEmpty else { return }
        for p in pages where p.kept && change.close.contains(p.block) { p.session.close() }
        var t = Transaction()
        t.disablesAnimations = true
        withTransaction(t) {
            pages.removeAll { $0.kept && change.close.contains($0.block) }
            for block in change.open {
                let s = TerminalSession(host: host, session: currentTarget.session, block: block, owns: false)
                s.start()
                pages.append(Pane(session: s, block: block, x: 1, kept: true))
            }
        }
    }

    /// The window's menu, in the bar.
    @ViewBuilder private func moreMenu() -> some View {
        Menu {
            // The images the pane showed (docs/ios-pane-gallery.md).
            if let g = session?.gallery { GalleryMenuItem(gallery: g) }
            Button("New Window", systemImage: "plus") { newTab() }
            Button("Split Pane", systemImage: "rectangle.split.2x1") { newSplit() }
            if paneCount > 1 {
                Button("Close Pane", systemImage: "xmark.rectangle", role: .destructive) { closePane(currentTarget.block) }
            }
        } label: {
            Image(systemName: "ellipsis")
        }
        .modifier(TitledGalleryCover(session: session, host: host, target: currentTarget))
        .accessibilityLabel("More")
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
        // Interactive glass swells and lights under the finger, as the
        // host list's capsules do.
        .background { Capsule().fill(.clear).glassEffect(.regular.interactive(), in: .capsule) }
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
        // A tap that opens the sheet taps the hand; closing it does not.
        .sensoryFeedback(trigger: sheet) { _, open in open ? .impact(weight: .light) : nil }
    }

    /// Slides the waiting pane in and the current one out, the way the
    /// switch goes along the axis, from where they are. Within the window,
    /// the current one gives its block back and is kept open off the page;
    /// to another window, its stream and those of the panes kept close.
    private func promote(_ id: UUID, to next: TerminalTarget, dir: CGFloat, axis: PaneAxis = .horizontal) {
        guard let nextSession = pages.first(where: { $0.id == id })?.session else { return }
        incoming = nil
        // A drag still under way loses its page: it ends here.
        paneDrag = nil
        terminal?.stopDictating()
        host.see(session: currentTarget.session, block: currentTarget.block)
        let paneSwitch = next.session == currentTarget.session && (window?.panes.contains(next.block) ?? false)
        if paneSwitch {
            session?.own(false)
        } else {
            for j in pages.indices where pages[j].id != id && !pages[j].leaving {
                pages[j].session.close()
                pages[j].leaving = true
            }
            // The hidden ones go at once; the current one slides out.
            var t = Transaction()
            t.disablesAnimations = true
            withTransaction(t) { pages.removeAll { $0.kept } }
        }
        guard let i = pages.firstIndex(where: { $0.id == id }) else { return }
        nextSession.own(true)
        Notifier.shared.onScreen = next
        host.see(session: next.session, block: next.block)
        switching += 1
        withAnimation(.spring(duration: 0.32, bounce: 0.12), completionCriteria: .removed) {
            slideDir = dir
            if let j = pages.firstIndex(where: { $0.session === session }) { pages[j].move(axis, to: -dir) }
            pages[i].move(axis, to: 0)
            session = nextSession
            currentTarget = next
        } completion: {
            if paneSwitch { flashPaneMap() }
            switching -= 1
            // Once no slide is left, the pages that closed go, and the
            // window's panes beside it are kept off the page, but one that
            // waits to slide in.
            guard switching == 0 else { return }
            var t = Transaction()
            t.disablesAnimations = true
            // A pane no longer in the window closes.
            let panes = window?.panes ?? []
            for p in pages where !p.leaving && p.session !== session && p.id != incoming && !panes.contains(p.block) { p.session.close() }
            withTransaction(t) {
                pages.removeAll { $0.leaving || ($0.session !== session && $0.id != incoming && !panes.contains($0.block)) }
                for j in pages.indices where pages[j].session !== session && pages[j].id != incoming { pages[j].kept = true }
            }
        }
    }

    /// Drops the pane of a switch that still waits: a pane of the window
    /// open is kept off the page, another closes its stream.
    private func dropIncoming() {
        guard let id = incoming else { return }
        incoming = nil
        let keeps = pages.first(where: { $0.id == id }).map { window?.panes.contains($0.block) == true } ?? false
        if !keeps { pages.first(where: { $0.id == id })?.session.close() }
        var t = Transaction()
        t.disablesAnimations = true
        // A drag that brought it ends, its page back in place.
        if let d = paneDrag, d.pane == id {
            paneDrag = nil
            if let axis = d.axis, let i = pages.firstIndex(where: { $0.id == d.page }) {
                withTransaction(t) { pages[i].move(axis, to: 0) }
            }
        }
        withTransaction(t) {
            if keeps, let i = pages.firstIndex(where: { $0.id == id }) {
                pages[i].kept = true
            } else {
                pages.removeAll { $0.id == id }
            }
        }
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

    /// Closes a pane of the window, without asking; when it is the one
    /// shown, goes first to the pane before it, else the one after, so its
    /// stream ending never shows as the window closing.
    private func closePane(_ block: String) {
        // Panes already closing count as gone, so a quick second close
        // neither goes back to one nor closes the window's last.
        guard let w = window, !closing.contains(block) else { return }
        let left = w.panes.filter { !closing.contains($0) }
        guard left.count > 1, let i = left.firstIndex(of: block) else { return }
        let session = currentTarget.session
        closing.insert(block)
        if block == currentTarget.block { selectPane(left[i > 0 ? i - 1 : 1]) }
        Task {
            defer { closing.remove(block) }
            // The switch slides in once the pane has drawn, within 300 ms;
            // one that never came leaves the pane open.
            let deadline = ContinuousClock.now + .seconds(1)
            while currentTarget.block == block, ContinuousClock.now < deadline {
                try? await Task.sleep(for: .milliseconds(16))
            }
            guard currentTarget.block != block else { return }
            do {
                try await host.closePane(block, in: session)
            } catch {
                failure = ("Can't Close the Pane", error.localizedDescription)
            }
        }
    }

    /// Opens a new tab in the session, and goes to it: a project's in its
    /// folder, checked first (docs/projects.md, New tabs and panes).
    private func newTab() {
        let session = currentTarget.session
        let dir = host.hasProjects ? host.sessions.first { $0.id == session }?.project?.dir : nil
        Task {
            do {
                if let dir, host.listsFolders { try await ProjectFolder.ensure(dir) { try await host.folders($0) } }
                if let b = try await host.newWindow(in: session, cwd: dir) {
                    switchTo(TerminalTarget(host: host.id, session: session, block: b, title: ""), dir: 1)
                }
            } catch let error where ProjectFolder.missing(error) {
                failure = ("Folder Not Found", String(localized: "\(dir ?? "") is not on \(host.config.name) any more."))
            } catch {
                failure = ("Can't Open a New Tab", error.localizedDescription)
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
            if session?.reconnecting != true { PrismLoading() }
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

/// What a terminal shows while its stream opens: the app's prism, its
/// beam sweeping through the glass into the fan of colours and drawing
/// back, as the icon holds it still. Reduce Motion shows it lit, still.
private struct PrismLoading: View {
    /// How far the beam's sweep has reached, as a fraction of the icon's
    /// width: under 0 it is all out, over 1 all in.
    @State private var edge: CGFloat = -0.2
    @Environment(\.accessibilityReduceMotion) private var still

    private let size: CGFloat = 80

    var body: some View {
        ZStack {
            // The icon's art layers: the beam and the fan, the glass
            // prism over them, as it draws the light passing through.
            Image("PrismSpectrum").resizable()
                .mask {
                    LinearGradient(stops: [
                        .init(color: .white, location: 0),
                        .init(color: .white, location: max(edge - 0.14, 0)),
                        .init(color: .clear, location: min(max(edge, 0.001), 1)),
                        .init(color: .clear, location: 1),
                    ], startPoint: .leading, endPoint: .trailing)
                }
            Image("PrismBody").resizable().opacity(0.55)
        }
        .frame(width: size, height: size)
        .onAppear {
            guard !still else {
                edge = 1.1
                return
            }
            withAnimation(.easeInOut(duration: 1.5).repeatForever()) { edge = 1.1 }
        }
        .accessibilityLabel("Connecting…")
    }
}

/// The mic: a tap turns it on, and what is said shows as it is spoken,
/// recognised on the phone (Dictation); a tap turns it off and types it
/// into the terminal, unsent, once it spins as its last phrase is. Its
/// first tap asks to download the models, and while they download it shows
/// how far, and a tap asks to stop it. It goes off, typing nothing, as the
/// screen goes or a call comes. It is on while something is said.
///
/// In the key bar it is given again only as what is said, or busy, changes
/// (DictationShown): an input it shows besides those goes in there too.
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
            } else if dictation.scanned {
                turnOn()
            } else {
                // The scan for the models on disk, quick, ends first: the
                // mic never offers to download one already there.
                Task {
                    _ = await dictation.installedModels()
                    turnOn()
                }
            }
        }
        .accessibilityAddTraits(on ? [.isButton, .isSelected] : .isButton)
    }

    /// Turns the mic on, or asks to download its model first.
    private func turnOn() {
        guard !busy, said == nil, dictation.progress == nil else { return }
        if !dictation.installed.contains(dictation.model) {
            asking = true
        } else {
            _ = terminal()?.dictate { said = $0 }
        }
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
///
/// In the key bar it is given again only as what is said, or busy, changes
/// (DictationShown): an input it shows besides those goes in there too.
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

/// Its content, made in a body of its own: what the content reads of an
/// observed model, as the host's titles, draws this view again, not the one
/// that holds it.
private struct OwnBody<Content: View>: View {
    @ViewBuilder let content: () -> Content

    var body: some View { content() }
}

/// The screen's navigation title, read in the modifier's own body.
private struct ScreenTitle: ViewModifier {
    let host: Host
    let target: TerminalTarget

    func body(content: Content) -> some View {
        content.navigationTitle(TerminalScreen.windowTitle(host: host, target: target).asText)
    }
}

/// The pane's gallery, titled as the screen, the title read in the
/// modifier's own body.
private struct TitledGalleryCover: ViewModifier {
    let session: TerminalSession?
    let host: Host
    let target: TerminalTarget

    func body(content: Content) -> some View {
        content.galleryCover(session, title: TerminalScreen.windowTitle(host: host, target: target))
    }
}

/// What the mic and the voice capsule in the key bar show of the screen's
/// state: their views are given again only when it changes.
private struct DictationShown: Equatable {
    let said: Dictation.Said?
    let busy: Bool
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
    /// Whether the terminal shows: one kept off the page draws nothing.
    let onPage: Bool
    /// Whether it is the pane shown, the one that takes touches.
    let current: Bool
    /// Follows a two-finger drag between panes: its state, and how far and
    /// how fast the fingers went.
    let onPaneDrag: (UIGestureRecognizer.State, CGPoint, CGPoint) -> Void
    /// Whether a drag may switch now: no other switch under way, no
    /// selection, no full-screen program with the mouse, no pinch.
    let canSwipe: () -> Bool

    final class Coordinator {
        let mic: UIHostingController<Mic>
        let voice: UIHostingController<VoiceCapsule>
        /// What the two were last given to show. Each update of the screen,
        /// as a frame of a two-finger drag, makes them again; given again
        /// only on a change, they do not lay out for nothing. Their
        /// closures and bindings reach the screen's state, which they read
        /// as they run, so the ones first given stay right.
        var shown: ChangeGate<DictationShown>

        init(mic: UIHostingController<Mic>, voice: UIHostingController<VoiceCapsule>, shown: DictationShown) {
            self.mic = mic
            self.voice = voice
            self.shown = ChangeGate(shown)
        }
    }

    /// The mic and the voice capsule in the key bar.
    func makeCoordinator() -> Coordinator {
        let m = UIHostingController(rootView: mic)
        m.safeAreaRegions = []
        m.view.backgroundColor = .clear
        let v = UIHostingController(rootView: voice)
        v.safeAreaRegions = []
        v.view.backgroundColor = .clear
        return Coordinator(mic: m, voice: v, shown: DictationShown(said: voice.said, busy: voice.busy))
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
        v.terminal.onPage = onPage
        // A pane switched away from gives up the keyboard itself: left to
        // the hit testing turned off, UIKit would give it back as the pane
        // comes back, and a switch never shows the keyboard. With a
        // hardware keyboard, which shows none, the pane switched to takes
        // the keys at once.
        let arrived = current && !v.current
        v.current = current
        if !current, v.terminal.isFirstResponder { _ = v.terminal.resignFirstResponder() }
        if arrived, GCKeyboard.coalesced != nil { _ = v.terminal.becomeFirstResponder() }
        v.onPaneDrag = onPaneDrag
        v.canSwipe = canSwipe
        v.setBottomEdgeColor(UIColor(cgColor: (session.background ?? .black).cgColor))
        if context.coordinator.shown.update(DictationShown(said: voice.said, busy: voice.busy)) {
            context.coordinator.mic.rootView = mic
            context.coordinator.voice.rootView = voice
        }
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
