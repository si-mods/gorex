import Foundation

/// Demo Mode: a Mac's windows, and the terminals in them, drawn on the
/// phone for a user who has added no host yet. Nothing is reached: the
/// host answers from these pages, and its terminals from DemoTerminal.
enum Demo {
    /// The demo host's id, the same each time the mode opens, so that the
    /// terminal background it remembers is the same too.
    static let hostID = UUID(uuidString: "00000000-0000-4000-8000-0000000000d3")!

    /// The host as the list shows it. Its address resolves nowhere, as it
    /// is never reached.
    static func config() -> HostConfig {
        var c = HostConfig(id: hostID, name: "Demo Mac",
                           url: URL(string: "http://demo-mac.gorex.invalid:7297")!, desktop: true)
        c.icon = "laptopcomputer"
        c.demo = true
        return c
    }

    // The blocks the windows open, and what DemoTerminal shows in each.
    static let claude = "b-claude"
    static let dashboard = "b-dashboard"
    static let tests = "b-tests"
    static let git = "b-git"
    static let shell = "b-shell"

    /// The host's sessions, as session.bootstrap would list them.
    static func sessions() -> [Host.Session] {
        [
            Host.Session(id: "s-work", label: "GoRex", view: SessionView(revision: 1, session_id: "s-work", label: "GoRex", windows: [
                window("w-claude", claude),
                window("w-dashboard", dashboard),
                window("w-tests", tests),
            ])),
            Host.Session(id: "s-dotfiles", label: "dotfiles", view: SessionView(revision: 1, session_id: "s-dotfiles", label: "dotfiles", windows: [
                window("w-git", git),
                window("w-shell", shell),
            ])),
        ]
    }

    private static func window(_ id: String, _ block: String) -> SessionView.Window {
        SessionView.Window(window_id: id, focused_block_id: block,
                           layers: [.init(blocks: [.init(block_id: block)])])
    }

    /// What each block shows: its title, its program's tile, and what its
    /// programs reported, as the list, the inbox and the tiles read them.
    static func blocks() -> [String: Host.Block] {
        let now = Date.now
        return [
            claude: block("Claude Code", "claude", [
                .init(id: "claude", state: "blocked", kind: "question", app: "claude",
                      title: "Claude Code", msg: "Apply the patch to Tabs.swift?", updated: now.addingTimeInterval(-120)),
            ]),
            dashboard: block("Dashboard", "node", [
                .init(id: "vite", state: "working", progress: 65, app: "node",
                      title: "Dashboard", msg: "Compiling App.tsx", updated: now.addingTimeInterval(-30)),
            ]),
            tests: block("Tests", "swift", [
                .init(id: "swift-test", state: "done", app: "swift",
                      title: "Tests", msg: "312 tests passed in 4.2s", updated: now.addingTimeInterval(-300)),
            ]),
            git: block("git log", "git", []),
            shell: block("Shell", "zsh", []),
        ]
    }

    private static func block(_ title: String, _ program: String, _ statuses: [Host.ProgramStatus]) -> Host.Block {
        let b = Host.Block()
        b.title = title
        b.program = program
        b.statuses = statuses.isEmpty ? nil : statuses
        return b
    }

    /// What a window's terminal shows before the user types.
    static func scene(_ block: String) -> DemoScene {
        switch block {
        case claude:
            return DemoScene(program: .claude, dir: "~/code/gorex", history: ["Fix the flaky tab tests"], intro: """
                \(DemoText.dim("Last login: Thu Nov 13 09:12:04 on ttys002"))\r
                \(DemoText.prompt("~/code/gorex", command: "claude"))\r
                \(DemoText.orange("   ✻ Welcome to Claude Code!"))\r
                \r
                \(DemoText.dim("     /help for help, /status for your status"))\r
                \(DemoText.dim("     cwd: /Users/demo/code/gorex"))\r
                \r
                \(DemoText.dim("> Fix the flaky tab tests"))\r
                \r
                  ✻ I found a race in TabsTests: the test reads the tabs before the\r
                    main actor's update lands. I'll make it wait for the expectation.\r
                \r
                \(DemoText.green("  ✓ Read ios/GoRexTests/HostTests.swift"))\r
                \(DemoText.green("  ✓ Edited ios/GoRexTests/TabsTests.swift"))\r
                \r
                  Apply the patch? (y/n)\r
                """)
        case dashboard:
            return DemoScene(program: .dev, dir: "~/code/dashboard", history: ["npm run dev"], intro: """
                \(DemoText.prompt("~/code/dashboard", command: "npm run dev"))\r
                \r
                \(DemoText.dim("> dashboard@0.1.0 dev"))\r
                \(DemoText.dim("> vite"))\r
                \r
                  \(DemoText.green("VITE v7.1.3"))  ready in 214 ms\r
                \r
                  \(DemoText.cyan("➜"))  Local:   \(DemoText.cyan("http://localhost:5173/"))\r
                  \(DemoText.cyan("➜"))  Network: \(DemoText.cyan("http://192.168.1.24:5173/"))\r
                \r
                  \(DemoText.dim("➜  press h + enter to show help"))\r
                """)
        case tests:
            return DemoScene(program: .shell, dir: "~/code/gorex", history: ["swift test"], intro: """
                \(DemoText.prompt("~/code/gorex", command: "swift test"))\r
                \(DemoText.dim("Building for debugging..."))\r
                \(DemoText.dim("[1/42] Compiling GoRex"))\r
                \(DemoText.dim("[42/42] Linking GoRexTests"))\r
                \r
                Test Suite 'All tests' started at 10:41:07.\r
                Test Suite 'All tests' passed at 10:41:11.\r
                \(DemoText.green("✔ Test run with 312 tests passed after 4.201 seconds."))\r
                """)
        case git:
            return DemoScene(program: .shell, dir: "~/dotfiles", history: ["git log --oneline -5"], intro: """
                \(DemoText.prompt("~/dotfiles", command: "git log --oneline -5"))\r
                \(DemoText.yellow("e4f2a91")) zsh: bind ⌘K to the command palette\r
                \(DemoText.yellow("7c1d0b4")) nvim: two spaces, no tabs\r
                \(DemoText.yellow("2b9a6e0")) git: rebase by default\r
                \(DemoText.yellow("a03c5f1")) tmux: keep the status bar quiet\r
                \(DemoText.yellow("6d84e2a")) zsh: a prompt that tells the branch\r
                """)
        default:
            return DemoScene(program: .shell, dir: "~", history: [], intro:
                DemoText.dim("Last login: Thu Nov 13 09:12:04 on ttys002") + "\r")
        }
    }
}

/// A demo terminal's program, its directory and what it shows first.
struct DemoScene {
    enum Program { case shell, claude, dev }

    let program: Program
    let dir: String
    /// The commands typed before, for the up arrow to bring back.
    let history: [String]
    /// What the window shows before the first prompt, lines ending in \r.
    let intro: String
}

/// The colors the demo's terminals write, in the theme's 8 basic colors.
enum DemoText {
    static func green(_ s: String) -> String { "\u{1b}[32m\(s)\u{1b}[0m" }
    static func blue(_ s: String) -> String { "\u{1b}[34m\(s)\u{1b}[0m" }
    static func cyan(_ s: String) -> String { "\u{1b}[36m\(s)\u{1b}[0m" }
    static func yellow(_ s: String) -> String { "\u{1b}[33m\(s)\u{1b}[0m" }
    static func red(_ s: String) -> String { "\u{1b}[31m\(s)\u{1b}[0m" }
    static func orange(_ s: String) -> String { "\u{1b}[38;5;209m\(s)\u{1b}[0m" }
    static func dim(_ s: String) -> String { "\u{1b}[2m\(s)\u{1b}[0m" }
    static func bold(_ s: String) -> String { "\u{1b}[1m\(s)\u{1b}[0m" }

    /// The shell's prompt, as the demo's windows show it.
    static func prompt(_ dir: String, command: String? = nil) -> String {
        let p = "\(green("demo@demo-mac")) \(blue(dir)) %"
        return command.map { "\(p) \($0)" } ?? p + " "
    }
}
