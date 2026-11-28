import SwiftUI
import UIKit

/// Inset rounded blue border drawn over an agent-driven terminal pane in
/// `TerminalScreen` (iPad split-view detail and multi-pane terminals).
/// Breathes on a 2.4 s cosine wave when active and visible, switches to a
/// static blue outline with zero animation when Reduce Motion is enabled, and
/// pauses completely when off-screen or `scenePhase != .active`.
struct AgentPaneOutline: View {
    let host: Host
    let block: String
    let topInset: CGFloat
    let bottomInset: CGFloat

    @Environment(\.colorScheme) private var scheme
    @Environment(\.scenePhase) private var phase
    @Environment(\.accessibilityReduceMotion) private var envReduceMotion
    @State private var sysReduceMotion = AgentHighlight.isReduceMotionEnabled
    @State private var onScreen = false
    @State private var tick = Date.now

    private var reduceMotion: Bool {
        envReduceMotion || sysReduceMotion || AgentHighlight.isReduceMotionEnabled
    }

    private struct ExpiryKey: Equatable {
        let block: String
        let actor: AgentActor?
        let cleared: Bool
        let exited: Bool
        let activePhase: Bool
        let onScreen: Bool
    }

    var body: some View {
        let currentState = host.agentState(for: block, at: tick)
        let animates = currentState != nil && AgentHighlight.animates(reduceMotion: reduceMotion, phase: phase, visible: onScreen)
        let b = host.blocks[block]
        let key = ExpiryKey(
            block: block,
            actor: b?.agentActor,
            cleared: b?.agentCleared ?? false,
            exited: b?.exited ?? false,
            activePhase: phase == .active,
            onScreen: onScreen
        )

        TimelineView(.animation(minimumInterval: 1.0 / 24, paused: !animates)) { tl in
            let evalDate = animates ? tl.date : tick
            if let st = host.agentState(for: block, at: evalDate) {
                let opacities = AgentHighlight.strokeOpacity(
                    at: evalDate.timeIntervalSinceReferenceDate,
                    reduceMotion: reduceMotion,
                    fade: st.fade
                )
                let col = AgentHighlight.color(for: scheme)
                let shape = RoundedRectangle(cornerRadius: 10, style: .continuous)
                shape
                    .strokeBorder(col.opacity(opacities.glow), lineWidth: 4)
                    .overlay {
                        shape.strokeBorder(col.opacity(opacities.line), lineWidth: 2)
                    }
                    .padding(.top, topInset)
                    .padding(.bottom, bottomInset + 2)
                    .padding(.horizontal, 2)
            }
        }
        .allowsHitTesting(false)
        .accessibilityHidden(true)
        .onAppear {
            onScreen = true
            sysReduceMotion = AgentHighlight.isReduceMotionEnabled
            tick = .now
        }
        .onDisappear {
            onScreen = false
        }
        .onChange(of: phase) { _, p in
            if p == .active {
                sysReduceMotion = AgentHighlight.isReduceMotionEnabled
                tick = .now
            }
        }
        .onReceive(NotificationCenter.default.publisher(for: UIAccessibility.reduceMotionStatusDidChangeNotification)) { _ in
            sysReduceMotion = AgentHighlight.isReduceMotionEnabled
            tick = .now
        }
        .task(id: key) {
            guard key.activePhase, key.onScreen else { return }
            while !Task.isCancelled {
                let now = Date.now
                tick = now
                guard let st = host.agentState(for: block, at: now) else {
                    host.expireAgentIfNeeded(block: block, at: now)
                    return
                }
                let wait = st.remaining > AgentHighlight.fadeDuration
                    ? (st.remaining - AgentHighlight.fadeDuration + 0.02)
                    : (st.remaining + 0.02)
                try? await Task.sleep(for: .seconds(max(0.05, wait)))
            }
        }
    }
}

/// Compact inline `agent` badge shown on agent-driven rows in `WindowRow` and
/// `StatusRow`. Never draws a full-row border; pauses timers when off-screen or
/// outside `scenePhase == .active`, and fades across the final 3 s of the 60 s window.
struct AgentWindowBadge: View {
    let stateProvider: (Date) -> AgentHighlight.State?
    let trackingKey: AnyHashable
    var onExpire: (() -> Void)? = nil

    @Environment(\.colorScheme) private var scheme
    @Environment(\.scenePhase) private var phase
    @Environment(\.accessibilityReduceMotion) private var envReduceMotion
    @State private var sysReduceMotion = AgentHighlight.isReduceMotionEnabled
    @State private var onScreen = false
    @State private var tick = Date.now

    init(host: Host, window: SessionView.Window, onExpire: (() -> Void)? = nil) {
        self.stateProvider = { now in host.windowAgentState(window, at: now) }
        self.onExpire = {
            host.expireWindowAgentsIfNeeded(window, at: .now)
            onExpire?()
        }
        let actors = window.blockIDs.map { id in
            let b = host.blocks[id]
            return "\(id):\(b?.agentActor?.lastCliInputAt?.timeIntervalSince1970 ?? 0):\(b?.agentActor?.active ?? false):\(b?.agentCleared ?? false):\(b?.exited ?? false)"
        }.joined(separator: "|")
        self.trackingKey = AnyHashable(actors)
    }

    init(host: Host, block: String, onExpire: (() -> Void)? = nil) {
        self.stateProvider = { now in host.agentState(for: block, at: now) }
        self.onExpire = {
            host.expireAgentIfNeeded(block: block, at: .now)
            onExpire?()
        }
        let b = host.blocks[block]
        self.trackingKey = AnyHashable("\(block):\(b?.agentActor?.lastCliInputAt?.timeIntervalSince1970 ?? 0):\(b?.agentActor?.active ?? false):\(b?.agentCleared ?? false):\(b?.exited ?? false)")
    }

    private var reduceMotion: Bool {
        envReduceMotion || sysReduceMotion || AgentHighlight.isReduceMotionEnabled
    }

    private struct TaskKey: Equatable {
        let tracking: AnyHashable
        let activePhase: Bool
        let onScreen: Bool
    }

    var body: some View {
        let snap = stateProvider(tick)
        let fading = snap.map { $0.remaining <= AgentHighlight.fadeDuration } ?? false
        let animates = fading && AgentHighlight.animates(reduceMotion: reduceMotion, phase: phase, visible: onScreen)
        let taskKey = TaskKey(tracking: trackingKey, activePhase: phase == .active, onScreen: onScreen)

        TimelineView(.animation(minimumInterval: 1.0 / 24, paused: !animates)) { tl in
            let evalDate = animates ? tl.date : tick
            if let st = stateProvider(evalDate) {
                let fade = reduceMotion ? 1.0 : st.fade
                let col = AgentHighlight.color(for: scheme)
                Text(verbatim: st.badgeText)
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(col.opacity(0.96 * fade))
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(col.opacity(0.16 * fade), in: Capsule())
                    .overlay {
                        Capsule().strokeBorder(col.opacity(0.38 * fade), lineWidth: 0.75)
                    }
                    .fixedSize()
            }
        }
        .allowsHitTesting(false)
        .accessibilityHidden(true)
        .onAppear {
            onScreen = true
            sysReduceMotion = AgentHighlight.isReduceMotionEnabled
            tick = .now
        }
        .onDisappear {
            onScreen = false
        }
        .onChange(of: phase) { _, p in
            if p == .active {
                sysReduceMotion = AgentHighlight.isReduceMotionEnabled
                tick = .now
            }
        }
        .onReceive(NotificationCenter.default.publisher(for: UIAccessibility.reduceMotionStatusDidChangeNotification)) { _ in
            sysReduceMotion = AgentHighlight.isReduceMotionEnabled
            tick = .now
        }
        .task(id: taskKey) {
            guard taskKey.activePhase, taskKey.onScreen else { return }
            while !Task.isCancelled {
                let now = Date.now
                tick = now
                guard let st = stateProvider(now) else {
                    onExpire?()
                    return
                }
                let wait = st.remaining > AgentHighlight.fadeDuration
                    ? (st.remaining - AgentHighlight.fadeDuration + 0.02)
                    : (st.remaining + 0.02)
                try? await Task.sleep(for: .seconds(max(0.05, wait)))
            }
        }
    }
}

/// Observes live takeover edges on an on-screen pane or window row and posts a
/// single non-spamming VoiceOver announcement when the boolean state flips.
struct AgentVoiceOverModifier: ViewModifier {
    let announcerKey: String
    let title: String
    let stateProvider: (Date) -> AgentHighlight.State?
    let consumeTookOver: () -> Bool
    let expireBlocks: (Date) -> Void
    let trackingKey: AnyHashable

    @Environment(\.scenePhase) private var phase
    @State private var onScreen = false
    @State private var tick = Date.now

    init(host: Host, block: String, extraBlocks: [String] = [], title: String) {
        let allIDs = Array(Set([block] + extraBlocks)).sorted()
        self.announcerKey = block
        self.title = title
        self.stateProvider = { now in host.agentState(for: block, at: now) }
        self.consumeTookOver = {
            let b = host.blocks[block]
            let v = b?.tookOverLocally ?? false
            b?.tookOverLocally = false
            return v
        }
        self.expireBlocks = { now in
            for id in allIDs {
                host.expireAgentIfNeeded(block: id, at: now)
            }
        }
        let sig = allIDs.map { id in
            let b = host.blocks[id]
            return "\(id):\(b?.agentActor?.lastCliInputAt?.timeIntervalSince1970 ?? 0):\(b?.agentActor?.active ?? false):\(b?.agentCleared ?? false):\(b?.exited ?? false)"
        }.joined(separator: "|")
        self.trackingKey = AnyHashable(sig)
    }

    init(host: Host, window: SessionView.Window, title: String) {
        self.announcerKey = "window:\(window.window_id)"
        self.title = title
        self.stateProvider = { now in host.windowAgentState(window, at: now) }
        self.consumeTookOver = {
            var tookOver = false
            for id in window.blockIDs {
                if host.blocks[id]?.tookOverLocally == true {
                    tookOver = true
                    host.blocks[id]?.tookOverLocally = false
                }
            }
            return tookOver
        }
        self.expireBlocks = { now in
            host.expireWindowAgentsIfNeeded(window, at: now)
        }
        let sig = window.blockIDs.map { id in
            let b = host.blocks[id]
            return "\(id):\(b?.agentActor?.lastCliInputAt?.timeIntervalSince1970 ?? 0):\(b?.agentActor?.active ?? false):\(b?.agentCleared ?? false):\(b?.exited ?? false)"
        }.joined(separator: "|")
        self.trackingKey = AnyHashable(sig)
    }

    private struct WatchKey: Equatable {
        let tracking: AnyHashable
        let activePhase: Bool
        let onScreen: Bool
    }

    func body(content: Content) -> some View {
        let active = stateProvider(tick) != nil
        let key = WatchKey(
            tracking: trackingKey,
            activePhase: phase == .active,
            onScreen: onScreen
        )
        content
            .onAppear {
                onScreen = true
                tick = .now
                AgentVoiceOverAnnouncer.shared.observe(
                    block: announcerKey,
                    title: title,
                    active: stateProvider(.now) != nil,
                    tookOverByUser: false,
                    sceneActive: phase == .active
                )
            }
            .onDisappear {
                onScreen = false
            }
            .onChange(of: active) { _, nowActive in
                let tookOver = nowActive ? false : consumeTookOver()
                AgentVoiceOverAnnouncer.shared.observe(
                    block: announcerKey,
                    title: title,
                    active: nowActive,
                    tookOverByUser: tookOver,
                    sceneActive: phase == .active && onScreen
                )
            }
            .task(id: key) {
                guard key.activePhase, key.onScreen else { return }
                while !Task.isCancelled {
                    let now = Date.now
                    tick = now
                    expireBlocks(now)
                    guard let st = stateProvider(now) else { return }
                    try? await Task.sleep(for: .seconds(st.remaining + 0.05))
                }
            }
    }
}
