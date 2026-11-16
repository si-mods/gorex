import SwiftUI

extension Color {
    /// The desktop's dot of a program that waits for the user.
    static let attention = Color(UIColor { $0.userInterfaceStyle == .dark
        ? UIColor(red: 0xFB / 255, green: 0xBF / 255, blue: 0x24 / 255, alpha: 1)
        : UIColor(red: 0xF5 / 255, green: 0x9E / 255, blue: 0x0B / 255, alpha: 1) })
    /// Rex's dot of a program done that the user has not seen.
    static let done = Color(red: 0x7A / 255, green: 0xA0 / 255, blue: 0xC2 / 255)
}

/// How a program shows: its name and the tile of its windows, as GoRex's
/// programs.go has them.
struct Program {
    var name = ""
    /// glyph is the program's icon; tileGlyph the one on its tile, glyph
    /// when empty. "brand:x" is a mark of Assets.xcassets/Brands.
    var glyph: String
    var tileBg: UInt32
    var tileFg: UInt32
    var tileGlyph = ""
    /// tileBorder outlines light tiles.
    var tileBorder = false
    /// shell is a shell at its prompt.
    var shell = false

    static let shellTile = Program(glyph: "square-terminal", tileBg: 0x1d2420, tileFg: 0x5fd38d, tileGlyph: "terminal", shell: true)

    static let programs: [String: Program] = {
        var p: [String: Program] = [
            "codex": .init(name: "Codex", glyph: "brand:openai", tileBg: 0xf7f7f7, tileFg: 0x1a1a1a, tileBorder: true),
            "claude": .init(name: "Claude Code", glyph: "brand:claude", tileBg: 0xd97757, tileFg: 0xffffff),
            "agy": .init(name: "Antigravity", glyph: "brand:antigravity", tileBg: 0x1a73e8, tileFg: 0xffffff),
            "node": .init(name: "Node", glyph: "node-hex", tileBg: 0x5fa04e, tileFg: 0xffffff, tileGlyph: "brand:nodedotjs"),
            "bun": .init(name: "Bun", glyph: "brand:bun", tileBg: 0xfbf0df, tileFg: 0x3b2a20, tileBorder: true),
            "deno": .init(name: "Deno", glyph: "brand:deno", tileBg: 0x1b1b1b, tileFg: 0xffffff),
            "python": .init(name: "Python", glyph: "brand:python", tileBg: 0x3776ab, tileFg: 0xffd43b),
            "lazygit": .init(name: "Git Changes", glyph: "plus-minus-circle", tileBg: 0x3f8f4f, tileFg: 0xffffff, tileGlyph: "diff"),
            "tig": .init(name: "Git Log", glyph: "git-branch", tileBg: 0xf05032, tileFg: 0xffffff),
            "git": .init(name: "Git", glyph: "brand:git", tileBg: 0xf05032, tileFg: 0xffffff),
            "vim": .init(name: "Vim", glyph: "brand:vim", tileBg: 0x019733, tileFg: 0xffffff),
            "nvim": .init(name: "Neovim", glyph: "brand:neovim", tileBg: 0x2b7a3d, tileFg: 0xffffff),
            "hx": .init(name: "Helix", glyph: "brand:helix", tileBg: 0x281733, tileFg: 0xc7a3f5),
            "emacs": .init(name: "Emacs", glyph: "brand:gnuemacs", tileBg: 0x7f5ab6, tileFg: 0xffffff),
            "htop": .init(name: "Activity", glyph: "activity", tileBg: 0x1f6f43, tileFg: 0xa7f3c4),
            "btop": .init(name: "Activity", glyph: "activity", tileBg: 0x1f6f43, tileFg: 0xa7f3c4),
            "top": .init(name: "Activity", glyph: "activity", tileBg: 0x1f6f43, tileFg: 0xa7f3c4),
            "ssh": .init(name: "SSH", glyph: "globe", tileBg: 0x5b4bd6, tileFg: 0xffffff),
            "mosh": .init(name: "Mosh", glyph: "globe", tileBg: 0x5b4bd6, tileFg: 0xffffff),
            "docker": .init(name: "Docker", glyph: "brand:docker", tileBg: 0x2496ed, tileFg: 0xffffff),
            "go": .init(name: "Go", glyph: "brand:go", tileBg: 0x00add8, tileFg: 0xffffff),
            "cargo": .init(name: "Cargo", glyph: "brand:rust", tileBg: 0x2b2b2b, tileFg: 0xf4a261),
            "rustc": .init(name: "Rust", glyph: "brand:rust", tileBg: 0x2b2b2b, tileFg: 0xf4a261),
            "npm": .init(name: "npm", glyph: "brand:npm", tileBg: 0xcb3837, tileFg: 0xffffff),
            "pnpm": .init(name: "pnpm", glyph: "brand:pnpm", tileBg: 0xf69220, tileFg: 0xffffff),
            "yarn": .init(name: "Yarn", glyph: "brand:yarn", tileBg: 0x2c8ebb, tileFg: 0xffffff),
            "ruby": .init(name: "Ruby", glyph: "brand:ruby", tileBg: 0xcc342d, tileFg: 0xffffff),
            "irb": .init(name: "Ruby", glyph: "brand:ruby", tileBg: 0xcc342d, tileFg: 0xffffff),
            "lua": .init(name: "Lua", glyph: "brand:lua", tileBg: 0x2c2d72, tileFg: 0xffffff),
            "php": .init(name: "PHP", glyph: "brand:php", tileBg: 0x777bb4, tileFg: 0xffffff),
            "psql": .init(name: "PostgreSQL", glyph: "brand:postgresql", tileBg: 0x336791, tileFg: 0xffffff),
            "mysql": .init(name: "MySQL", glyph: "brand:mysql", tileBg: 0x00758f, tileFg: 0xffffff),
            "redis-cli": .init(name: "Redis", glyph: "brand:redis", tileBg: 0xd82c20, tileFg: 0xffffff),
            "tmux": .init(name: "tmux", glyph: "brand:tmux", tileBg: 0x1bb91f, tileFg: 0xffffff),
            "make": .init(name: "Make", glyph: "cpu", tileBg: 0x6b7280, tileFg: 0xffffff),
            "swift": .init(name: "Swift", glyph: "brand:swift", tileBg: 0xf05138, tileFg: 0xffffff),
            "kotlin": .init(name: "Kotlin", glyph: "brand:kotlin", tileBg: 0x7f52ff, tileFg: 0xffffff),
            "omp": .init(name: "omp", glyph: "brand:omp", tileBg: 0x0f0a14, tileFg: 0xb66cff),
            // Windows shells: at their prompt they show as shells, with their own tile.
            "pwsh": .init(name: "PowerShell", glyph: "brand:powershell", tileBg: 0x5391fe, tileFg: 0xffffff, shell: true),
            "powershell": .init(name: "PowerShell", glyph: "brand:powershell", tileBg: 0x5391fe, tileFg: 0xffffff, shell: true),
            "cmd": .init(name: "Command Prompt", glyph: "square-terminal", tileBg: 0x0c0c0c, tileFg: 0xcccccc, tileGlyph: "terminal", shell: true),
        ]
        p["python3"] = p["python"]
        p["ipython"] = p["python"]
        p["view"] = p["vim"]
        p["vi"] = p["vim"]
        p["less"] = .init(name: "Pager", glyph: "file-code", tileBg: 0x475569, tileFg: 0xffffff)
        p["man"] = p["less"]
        // Claude Code reports itself as claude-code in the status protocol.
        p["claude-code"] = p["claude"]
        p["antigravity"] = p["agy"]
        p["antigravity-cli"] = p["agy"]
        return p
    }()

    static let shells: Set<String> = ["zsh", "bash", "fish", "sh", "dash", "nu", "pwsh", "elvish", "xonsh", "tcsh", "csh", "ksh", "cmd", "powershell"]

    /// The SF Symbols of the glyphs on tiles, as symbol.go maps them.
    static let symbols = [
        "activity": "waveform.path.ecg",
        "cpu": "cpu",
        "diff": "plus.forwardslash.minus",
        "file-code": "doc.text",
        "git-branch": "arrow.triangle.branch",
        "globe": "globe",
        "terminal": "terminal",
    ]

    /// How a program shows, by the name of its process; none is the shell
    /// at its prompt.
    static func of(_ process: String?) -> Program {
        guard let process, !process.isEmpty else { return shellTile }
        var name = (process as NSString).lastPathComponent
        if name.hasPrefix("-") { name.removeFirst() }
        if let p = programs[name.lowercased()] { return p }
        if shells.contains(name) {
            var p = shellTile
            p.name = name
            return p
        }
        // Any other program: its own name, on a tile of a color of its own.
        return Program(name: name, glyph: "square-terminal", tileBg: hashColor(name), tileFg: 0xffffff, tileGlyph: "terminal")
    }

    /// A color for a name, the same every time: FNV-1a over its bytes.
    static func hashColor(_ s: String) -> UInt32 {
        let palette: [UInt32] = [0x6d5bd0, 0x2f80ed, 0x0f9d8a, 0xd9822b, 0xc2417d, 0x3a7d44, 0x8a5a44, 0x4e5d94]
        var h: UInt32 = 2166136261
        for b in s.utf8 {
            h ^= UInt32(b)
            h = h &* 16777619
        }
        return palette[Int(h % UInt32(palette.count))]
    }
}

extension Color {
    init(rgb: UInt32) {
        self.init(red: Double(rgb >> 16 & 0xff) / 255, green: Double(rgb >> 8 & 0xff) / 255, blue: Double(rgb & 0xff) / 255)
    }
}

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

/// A program's tile: a rounded card of its color with a light rim and a
/// sheen along the top, and its glyph when it is in front.
struct ProgramTile: View {
    let program: Program
    var glyph = true
    /// Points to a DIP of the tile: lists use ProgramTiles.scale, and the
    /// phone's pane bar draws the same tile at its own 21x16 size.
    var scale: CGFloat = ProgramTiles.scale
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        let k = scale
        let card = RoundedRectangle(cornerRadius: 4.5 * k)
        card.fill(Color(rgb: program.tileBg))
            .overlay {
                if program.tileBorder { card.strokeBorder(.black.opacity(0.12), lineWidth: 0.5 * k) }
            }
            // Lit from above, as Rex's tiles: a sheen along the top that
            // darkens a little to the foot, and a lit inner edge, brightest
            // at the top.
            .overlay {
                card.fill(LinearGradient(stops: [.init(color: .white.opacity(0.18), location: 0), .init(color: .white.opacity(0), location: 0.5),
                                                 .init(color: .black.opacity(0.06), location: 1)],
                                         startPoint: .top, endPoint: .bottom))
            }
            .overlay {
                card.strokeBorder(LinearGradient(colors: [.white.opacity(0.45), .white.opacity(0.15)], startPoint: .top, endPoint: .bottom),
                                  lineWidth: 0.5)
            }
            .overlay {
                if glyph { glyphImage.foregroundStyle(Color(rgb: program.tileFg)).frame(width: 11 * k, height: 11 * k) }
            }
            .background {
                // A dark rim on a dark page, a light one on a light page.
                RoundedRectangle(cornerRadius: 4.5 * k + 0.75).fill(scheme == .dark ? .black.opacity(0.7) : .white.opacity(0.95))
                    .padding(scheme == .dark ? -0.75 : -k)
            }
            .shadow(color: .black.opacity(0.18), radius: 0.75 * k, y: 0.5 * k)
    }

    /// An SF Symbol where one is mapped, inset as symbol.go insets them,
    /// else the brand's mark.
    @ViewBuilder private var glyphImage: some View {
        let g = program.tileGlyph.isEmpty ? program.glyph : program.tileGlyph
        if let sf = Program.symbols[g] {
            Image(systemName: sf).resizable().scaledToFit().scaleEffect(0.85)
        } else {
            Image("Brands/" + g.replacingOccurrences(of: "brand:", with: "")).resizable().scaledToFit()
        }
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

    var body: some View {
        TimelineView(.animation(minimumInterval: 1.0 / 30, paused: reduceMotion)) { tl in
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

/// Circular progress indicator for a working program that reports a percentage.
struct ProgressRing: View {
    let progress: Int

    var body: some View {
        let clamped = max(0, min(100, progress))
        Circle().trim(from: 0, to: CGFloat(clamped) / 100)
            .stroke(.secondary, style: StrokeStyle(lineWidth: 2, lineCap: .round))
            .rotationEffect(.degrees(-90))
            .background(Circle().stroke(.quaternary, lineWidth: 2))
            .frame(width: 12, height: 12)
            .accessibilityLabel(Text(verbatim: "\(clamped)%"))
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

private nonisolated struct TileBounceValues: Sendable {
    var offsetY: CGFloat = 0
    var scaleX: CGFloat = 1
    var scaleY: CGFloat = 1
}

private struct TileBounceModifier: ViewModifier {
    let motion: TileMotion
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    func body(content: Content) -> some View {
        content
            .keyframeAnimator(initialValue: TileBounceValues(), repeating: !reduceMotion && motion.isRepeating) { view, v in
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
            .id(motion)
    }
}
