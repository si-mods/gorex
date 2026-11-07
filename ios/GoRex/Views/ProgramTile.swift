import SwiftUI

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

    /// Points to a DIP of the desktop's tiles, which are 21×16.
    static let scale: CGFloat = 1.5

    /// The width of tiles with this many behind the front one.
    static func width(behind: Int) -> CGFloat { CGFloat(21 + 5 * min(behind, 2) + 2) * scale }

    var body: some View {
        let k = Self.scale
        ZStack(alignment: .leading) {
            ForEach(Array(back.prefix(2).enumerated()).reversed(), id: \.offset) { i, p in
                let inset = CGFloat(i + 1) * 0.8 * k
                ProgramTile(program: .of(p), glyph: false)
                    .frame(width: 21 * k, height: 16 * k - 2 * inset)
                    .offset(x: CGFloat(5 * (i + 1)) * k)
            }
            ProgramTile(program: .of(front)).frame(width: 21 * k, height: 16 * k)
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
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        let k = ProgramTiles.scale
        let card = RoundedRectangle(cornerRadius: 4.5 * k)
        card.fill(Color(rgb: program.tileBg))
            .overlay {
                if program.tileBorder { card.strokeBorder(.black.opacity(0.12), lineWidth: 0.5 * k) }
            }
            .overlay {
                card.fill(LinearGradient(stops: [.init(color: .white.opacity(0.18), location: 0), .init(color: .white.opacity(0), location: 0.6)],
                                         startPoint: .top, endPoint: .bottom))
            }
            .overlay {
                if glyph { glyphImage.foregroundStyle(Color(rgb: program.tileFg)).frame(width: 11 * k, height: 11 * k) }
            }
            .background {
                RoundedRectangle(cornerRadius: 5.5 * k).fill(.white.opacity(scheme == .dark ? 0.22 : 0.95)).padding(-k)
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
