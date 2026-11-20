import SwiftUI

/// The tiles of a window's programs, as GoRex's tabs show them: the
/// focused block's in front, up to two others peeking out behind it.
struct ProgramTiles: View {
    let front: String?
    let back: [String?]
    /// The window's most urgent program state (idle, working, blocked, done,
    /// error), driving both the done dot and the front tile's bounce motion.
    var state: String? = nil

    /// Points to a DIP of the desktop's tiles, which are 21×16.
    static let scale: CGFloat = 1.5

    /// The width of tiles with this many behind the front one.
    static func width(behind: Int) -> CGFloat { CGFloat(21 + 5 * min(behind, 2) + 2) * scale }

    var body: some View {
        let k = Self.scale
        let done = state == "done"
        ZStack(alignment: .leading) {
            ForEach(Array(back.prefix(2).enumerated()).reversed(), id: \.offset) { i, p in
                let inset = CGFloat(i + 1) * 0.8 * k
                ProgramTile(program: .of(p), glyph: false)
                    .frame(width: 21 * k, height: 16 * k - 2 * inset)
                    .offset(x: CGFloat(5 * (i + 1)) * k)
            }
            ProgramTile(program: .of(front)).frame(width: 21 * k, height: 16 * k)
                .overlay(alignment: .topTrailing) {
                    if done { DoneDot().offset(x: 2.5, y: -2.5) }
                }
                .tileBounce(state: state)
        }
        .padding(.leading, k)
        .accessibilityHidden(true)
    }
}

/// Rex's dot of a program done and unseen, on its tile's corner: lit from
/// above, in a rim as the tile's.
private struct DoneDot: View {
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        Circle()
            .fill(LinearGradient(colors: [.white.opacity(0.3), .white.opacity(0)], startPoint: .top, endPoint: .center))
            .background(Circle().fill(Color.done))
            .frame(width: 7, height: 7)
            .padding(0.75)
            .background(Circle().fill(scheme == .dark ? .black.opacity(0.85) : .white.opacity(0.95)))
            .accessibilityLabel("Done")
    }
}

/// Three animated dots shown while a program is working without a
/// percentage progress, pulsing and hopping in a wave.
struct WorkingDots: View {
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.scenePhase) private var phase
    /// Whether the dots are on screen: a row scrolled away, or under the
    /// terminal pushed over the list, disappears.
    @State private var onScreen = true

    var body: some View {
        // Paused, the dots stay as they were: they move only where they show.
        TimelineView(.animation(minimumInterval: 1.0 / 30, paused: !ListMotion.runs(reduceMotion: reduceMotion, phase: phase, onScreen: onScreen))) { tl in
            let t = reduceMotion ? 0 : tl.date.timeIntervalSinceReferenceDate
            HStack(spacing: 3) {
                ForEach(0..<3, id: \.self) { i in
                    let phase = Self.phase(at: t, index: i)
                    Circle()
                        .fill(.secondary)
                        .frame(width: 3.5, height: 3.5)
                        .opacity(reduceMotion ? 0.75 : 0.35 + 0.65 * phase)
                        .scaleEffect(reduceMotion ? 1 : 0.85 + 0.2 * phase)
                        .offset(y: reduceMotion ? 0 : -1.5 * phase)
                }
            }
        }
        .onAppear { onScreen = true }
        .onDisappear { onScreen = false }
        .accessibilityLabel("Working")
    }

    /// Wave value in `0...1` for dot `index` at time `t`, staggered across
    /// a 1.2s cycle so the three dots pulse left-to-right.
    static func phase(at t: TimeInterval, index: Int) -> Double {
        let period = 1.2
        let shifted = (t / period - Double(index) * 0.16).truncatingRemainder(dividingBy: 1)
        let u = shifted < 0 ? shifted + 1 : shifted
        guard u < 0.5 else { return 0 }
        return 0.5 - 0.5 * cos(2 * .pi * (u / 0.5))
    }
}

/// Trailing status indicator for a window or inbox row: attention dot for
/// blocked/error (and optionally done), progress ring or animated working
/// dots while working.
struct ProgramStatusMark: View {
    let state: String?
    let progress: Int?
    var dotSize: CGFloat = 7
    var showDone = false

    var body: some View {
        switch state {
        case "blocked":
            Circle().fill(Color.attention).frame(width: dotSize, height: dotSize).accessibilityLabel("Needs Input")
        case "error":
            Circle().fill(.red).frame(width: dotSize, height: dotSize).accessibilityLabel("Failed")
        case "done" where showDone:
            Circle().fill(Color.attention).frame(width: dotSize, height: dotSize).accessibilityLabel("Done")
        case "working":
            if let progress {
                ProgressRing(progress: progress)
            } else {
                WorkingDots()
            }
        default:
            EmptyView()
        }
    }
}

/// Whether a list's endless motion, the working dots and the tiles'
/// bounce, runs: not with Reduce Motion, nor with the app out of the
/// front (inactive or in the background), nor off screen (the dots only;
/// a tile bounces off screen, see TileBounceModifier). It is the same
/// motion when it runs; paused, nothing is seen moving.
enum ListMotion {
    static func runs(reduceMotion: Bool, phase: ScenePhase, onScreen: Bool) -> Bool {
        !reduceMotion && phase == .active && onScreen
    }
}

/// How a program tile moves for its session's state, modeled on macOS Dock
/// icon bounces:
/// - `attention` (`blocked`, `error`): energetic double-hop repeating on a
///   calm cadence until the user attends to it.
/// - `done`: celebratory double-hop repeating on a relaxed cadence while
///   unseen.
/// - `none` (`working`, `idle`, or `nil`): still (working uses `WorkingDots`).
enum TileMotion: Equatable, Hashable, Sendable {
    case none
    case done
    case attention

    init(state: String?) {
        switch state {
        case "blocked", "error": self = .attention
        case "done": self = .done
        default: self = .none
        }
    }

    /// Whether this state should animate continuously.
    var isRepeating: Bool { self != .none }
}

extension View {
    /// Animates a program tile according to its session state, like a macOS
    /// Dock icon bounce.
    func tileBounce(state: String?) -> some View {
        modifier(TileBounceModifier(motion: TileMotion(state: state)))
    }
}

/// A tile bounce's identity: its motion, and whether the list's motion
/// runs. A change of either makes a new animator.
struct TileBounceKey: Hashable {
    let motion: TileMotion
    let runs: Bool

    /// Whether the animator repeats its keyframes.
    var repeats: Bool { runs && motion.isRepeating }
}

private nonisolated struct TileBounceValues: Sendable {
    var offsetY: CGFloat = 0
    var scaleX: CGFloat = 1
    var scaleY: CGFloat = 1
}

private struct TileBounceModifier: ViewModifier {
    let motion: TileMotion
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.scenePhase) private var phase

    func body(content: Content) -> some View {
        // Out of the front, the tile rests in place. Whether it runs is part
        // of the animator's identity: a resume makes a new animator, which
        // bounces from the start, as on the tile's first appearance. It
        // pauses by Reduce Motion and the scene's phase only, which a change
        // of identity cannot touch; an appearance flag could, as the view
        // replaced fires a disappear and an appear in no set order.
        let key = TileBounceKey(motion: motion, runs: ListMotion.runs(reduceMotion: reduceMotion, phase: phase, onScreen: true))
        content
            .keyframeAnimator(initialValue: TileBounceValues(), repeating: key.repeats) { view, v in
                view
                    .scaleEffect(x: v.scaleX, y: v.scaleY, anchor: .bottom)
                    .offset(y: v.offsetY)
            } keyframes: { _ in
                KeyframeTrack(\.offsetY) {
                    switch motion {
                    case .attention:
                        CubicKeyframe(-6.5, duration: 0.16)
                        CubicKeyframe(0, duration: 0.14)
                        CubicKeyframe(-2.6, duration: 0.12)
                        CubicKeyframe(0, duration: 0.12)
                        LinearKeyframe(0, duration: 1.06)
                    case .done:
                        CubicKeyframe(-5.0, duration: 0.16)
                        CubicKeyframe(0, duration: 0.14)
                        CubicKeyframe(-2.0, duration: 0.11)
                        CubicKeyframe(0, duration: 0.11)
                        LinearKeyframe(0, duration: 1.88)
                    case .none:
                        LinearKeyframe(0, duration: 0.01)
                    }
                }
                KeyframeTrack(\.scaleX) {
                    switch motion {
                    case .attention:
                        CubicKeyframe(0.96, duration: 0.06)
                        CubicKeyframe(1.0, duration: 0.10)
                        CubicKeyframe(1.05, duration: 0.14)
                        CubicKeyframe(1.0, duration: 0.12)
                        CubicKeyframe(1.02, duration: 0.12)
                        CubicKeyframe(1.0, duration: 0.08)
                        LinearKeyframe(1.0, duration: 0.98)
                    case .done:
                        CubicKeyframe(0.97, duration: 0.06)
                        CubicKeyframe(1.0, duration: 0.10)
                        CubicKeyframe(1.04, duration: 0.14)
                        CubicKeyframe(1.0, duration: 0.11)
                        CubicKeyframe(1.0, duration: 0.11)
                        LinearKeyframe(1.0, duration: 1.88)
                    case .none:
                        LinearKeyframe(1.0, duration: 0.01)
                    }
                }
                KeyframeTrack(\.scaleY) {
                    switch motion {
                    case .attention:
                        CubicKeyframe(1.05, duration: 0.06)
                        CubicKeyframe(1.0, duration: 0.10)
                        CubicKeyframe(0.94, duration: 0.14)
                        CubicKeyframe(1.0, duration: 0.12)
                        CubicKeyframe(0.97, duration: 0.12)
                        CubicKeyframe(1.0, duration: 0.08)
                        LinearKeyframe(1.0, duration: 0.98)
                    case .done:
                        CubicKeyframe(1.04, duration: 0.06)
                        CubicKeyframe(1.0, duration: 0.10)
                        CubicKeyframe(0.95, duration: 0.14)
                        CubicKeyframe(1.0, duration: 0.11)
                        CubicKeyframe(1.0, duration: 0.11)
                        LinearKeyframe(1.0, duration: 1.88)
                    case .none:
                        LinearKeyframe(1.0, duration: 0.01)
                    }
                }
            }
            .id(key)
    }
}
