import Foundation

/// A terminal of Demo Mode: a shell, Claude Code's prompt, or a dev
/// server's output, run on the phone. It reads the bytes Ghostty encodes
/// for keys and writes what the terminal shows, as a stream's bytes would.
@MainActor
final class DemoTerminal {
    /// What a command typed did, for the host's tiles and inbox to follow:
    /// "claude", "swift", "npm", or "y" as the demo's question is answered.
    var onCommand: (String) -> Void = { _ in }

    var onOutput: (Data) -> Void = { _ in }

    private let scene: DemoScene
    private var mode: DemoScene.Program
    /// The prompt's folder, as "~" or "~/code/gorex".
    private var dir: String
    private var line = ""
    /// Where the cursor is in line, in characters from its start.
    private var cursor = 0
    private var history: [String]
    private var historyAt: Int
    /// What waits for more bytes: an escape sequence cut in two.
    private var pending = ""
    /// Whether the input is a paste, whose text goes in as it is.
    private var pasting = false
    private var started = false
    /// Whether the demo's question was answered.
    private var answered = false

    init(_ scene: DemoScene) {
        self.scene = scene
        mode = scene.program
        dir = scene.dir
        history = scene.history
        historyAt = scene.history.count
    }

    /// Shows what the window had before, then the prompt; a dev server
    /// runs on without one, as in a real terminal.
    func start() {
        guard !started else { return }
        started = true
        out(scene.intro)
        if scene.intro.hasSuffix("\r") {
            out("\n")
        } else if !scene.intro.hasSuffix("\n") {
            out("\r\n")
        }
        if mode != .dev { showPrompt() }
    }

    // MARK: Input

    /// Takes the bytes Ghostty encoded for keys, pastes and the wheel.
    func type(_ bytes: Data) {
        pending += String(decoding: bytes, as: UTF8.self)
        process()
    }

    private func process() {
        var i = pending.startIndex
        while i < pending.endIndex {
            if pasting {
                let end = "\u{1b}[201~"
                if let r = pending.range(of: end, range: i..<pending.endIndex) {
                    insert(pending[i..<r.lowerBound])
                    pasting = false
                    i = r.upperBound
                    continue
                }
                // Keep a part of the end marker for the next read.
                let rest = String(pending[i...])
                let keep = Self.partialSuffix(rest, of: end)
                insert(rest.prefix(rest.count - keep))
                pending = String(rest.suffix(keep))
                return
            }
            let c = pending[i]
            if c == "\u{1b}" {
                if pending.index(after: i) == pending.endIndex {
                    // Escape alone: a key press, not the start of a sequence.
                    i = pending.endIndex
                    continue
                }
                if let (seq, end) = escape(at: i) {
                    handle(seq)
                    i = end
                    continue
                }
                break
            }
            handle(c)
            i = pending.index(after: i)
        }
        pending = String(pending[i...])
    }

    /// How many characters at the end of text begin marker.
    private static func partialSuffix(_ text: String, of marker: String) -> Int {
        for n in stride(from: min(marker.count - 1, text.count), through: 1, by: -1) where text.hasSuffix(marker.prefix(n)) {
            return n
        }
        return 0
    }

    /// The escape sequence at start, and where it ends; nil while its
    /// rest hasn't come.
    private func escape(at start: String.Index) -> (String, String.Index)? {
        let after = pending.index(after: start)
        guard after < pending.endIndex else { return nil }
        let kind = pending[after]
        if kind == "[" {
            var j = pending.index(after: after)
            while j < pending.endIndex {
                if let u = pending[j].unicodeScalars.first, (0x40...0x7E).contains(u.value) {
                    let end = pending.index(after: j)
                    return (String(pending[start..<end]), end)
                }
                j = pending.index(after: j)
            }
            return nil
        }
        if kind == "O" {
            let j = pending.index(after: after)
            guard j < pending.endIndex else { return nil }
            let end = pending.index(after: j)
            return (String(pending[start..<end]), end)
        }
        // Escape and a key, as Alt+b.
        let end = pending.index(after: after)
        return (String(pending[start..<end]), end)
    }

    private func handle(_ c: Character) {
        if mode == .dev {
            if c == "\u{3}" { interrupt() }
            return
        }
        switch c {
        case "\r", "\n": enter()
        case "\u{7f}", "\u{8}": backspace()
        case "\u{3}": interrupt()
        case "\u{4}": endOfFile()
        case "\u{12}": out("\u{1b}[2J\u{1b}[H"); redraw()
        case "\u{1}": cursor = 0; redraw()
        case "\u{5}": cursor = line.count; redraw()
        case "\u{11}": line = String(line.prefix(cursor)); redraw()
        case "\u{21}": line = String(line.dropFirst(cursor)); cursor = 0; redraw()
        case "\u{23}": deleteWord()
        case "\t": tab()
        default:
            if let u = c.unicodeScalars.first, u.value >= 0x20, u.value != 0x7F { insert(c) }
        }
    }

    private func handle(_ seq: String) {
        guard mode != .dev else { return }
        switch seq {
        case "\u{1b}[A": historyUp()
        case "\u{1b}[B": historyDown()
        case "\u{1b}[C": if cursor < line.count { cursor += 1; redraw() }
        case "\u{1b}[D": if cursor > 0 { cursor -= 1; redraw() }
        case "\u{1b}[H", "\u{1b}[1~", "\u{1b}OH": cursor = 0; redraw()
        case "\u{1b}[F", "\u{1b}[4~", "\u{1b}OF": cursor = line.count; redraw()
        case "\u{1b}[3~": deleteForward()
        case "\u{1b}[200~": pasting = true
        case "\u{1b}[201~": pasting = false
        case "\u{1b}\u{7f}": deleteWord()
        default: break
        }
    }

    // MARK: The line

    private func insert(_ c: Character) {
        let at = line.index(line.startIndex, offsetBy: cursor)
        line.insert(c, at: at)
        cursor += 1
        redraw()
    }

    /// Pasted text goes in as it is; a line break of it, as a space.
    private func insert(_ s: Substring) {
        for c in s {
            if c == "\r" || c == "\n" {
                let at = line.index(line.startIndex, offsetBy: cursor)
                line.insert(" ", at: at)
                cursor += 1
            } else if let u = c.unicodeScalars.first, u.value >= 0x20, u.value != 0x7F {
                let at = line.index(line.startIndex, offsetBy: cursor)
                line.insert(c, at: at)
                cursor += 1
            }
        }
        redraw()
    }

    private func backspace() {
        guard cursor > 0 else { return }
        let at = line.index(line.startIndex, offsetBy: cursor)
        line.remove(at: line.index(before: at))
        cursor -= 1
        redraw()
    }

    private func deleteForward() {
        guard cursor < line.count else { return }
        line.remove(at: line.index(line.startIndex, offsetBy: cursor))
        redraw()
    }

    private func deleteWord() {
        var chars = Array(line)
        var i = cursor
        while i > 0, chars[i - 1].isWhitespace { i -= 1 }
        while i > 0, !chars[i - 1].isWhitespace { i -= 1 }
        chars.removeSubrange(i..<cursor)
        line = String(chars)
        cursor = i
        redraw()
    }

    /// A tab completes a command, as a shell does from its commands.
    private func tab() {
        let word = String(line.prefix(cursor))
        guard !word.contains(" ") else { return }
        let commands = ["cat", "cd", "claude", "clear", "echo", "exit", "git", "help", "ls", "npm", "pwd", "swift", "uname", "whoami"]
        guard let done = commands.first(where: { $0.hasPrefix(word) && $0 != word }) else { return }
        line = done + line.dropFirst(cursor)
        cursor = done.count
        redraw()
    }

    private func historyUp() {
        guard !history.isEmpty, historyAt > 0 else { return }
        historyAt -= 1
        line = history[historyAt]
        cursor = line.count
        redraw()
    }

    private func historyDown() {
        guard historyAt < history.count else { return }
        historyAt += 1
        line = historyAt < history.count ? history[historyAt] : ""
        cursor = line.count
        redraw()
    }

    // MARK: Prompt

    private var prompt: String {
        mode == .claude ? "\(DemoText.orange(">")) " : DemoText.prompt(dir)
    }

    private func showPrompt() {
        guard mode != .dev else { return }
        out(prompt)
    }

    private func redraw() {
        guard mode != .dev else { return }
        out("\r\u{1b}[K" + prompt + line)
        let back = line.count - cursor
        if back > 0 { out("\u{1b}[\(back)D") }
    }

    private func out(_ s: String) {
        onOutput(Data(s.utf8))
    }

    private func lineOut(_ s: String = "") {
        out(s + "\r\n")
    }

    // MARK: Commands

    private func enter() {
        let text = line
        line = ""
        cursor = 0
        out("\r\n")
        let trimmed = text.trimmingCharacters(in: .whitespaces)
        if !trimmed.isEmpty {
            history.append(text)
            historyAt = history.count
            onCommand(trimmed)
        }
        switch mode {
        case .shell: run(trimmed)
        case .claude: ask(trimmed)
        case .dev: break
        }
    }

    private func interrupt() {
        line = ""
        cursor = 0
        out("^C\r\n")
        if mode == .dev {
            out(DemoText.dim("npm run dev stopped.") + "\r\n")
            mode = .shell
            showPrompt()
            return
        }
        showPrompt()
    }

    private func endOfFile() {
        guard line.isEmpty else {
            deleteForward()
            return
        }
        out(DemoText.dim("logout") + "\r\n")
        showPrompt()
    }

    private func run(_ text: String) {
        guard !text.isEmpty else {
            showPrompt()
            return
        }
        let parts = text.split(separator: " ", maxSplits: 1, omittingEmptySubsequences: true)
        let command = String(parts[0])
        let arg = parts.count > 1 ? String(parts[1]).trimmingCharacters(in: .whitespaces) : ""
        switch command {
        case "help", "?":
            lineOut(DemoText.bold("Rex Go Demo Mode") + " — this terminal runs on the phone.")
            lineOut()
            lineOut("  \(DemoText.cyan("ls")) [ -l ]      list the folder")
            lineOut("  \(DemoText.cyan("cd")) <dir>       change folder")
            lineOut("  \(DemoText.cyan("cat")) README.md  read the file")
            lineOut("  \(DemoText.cyan("git")) status     the repo's changes")
            lineOut("  \(DemoText.cyan("git")) log        recent commits")
            lineOut("  \(DemoText.cyan("swift")) test     run the tests")
            lineOut("  \(DemoText.cyan("npm")) run dev    start the dashboard")
            lineOut("  \(DemoText.cyan("claude"))         open Claude Code")
            lineOut("  \(DemoText.cyan("clear"))          clear the screen")
            lineOut()
            lineOut(DemoText.dim("Nothing is connected: add a host in Settings › Remote Hosts for the real thing."))
        case "ls": ls(arg)
        case "cd": cd(arg)
        case "pwd": lineOut(abs)
        case "cat": cat(arg)
        case "echo": lineOut(arg)
        case "clear": out("\u{1b}[2J\u{1b}[H"); showPrompt(); return
        case "whoami": lineOut("demo")
        case "hostname": lineOut("demo-mac")
        case "uname": lineOut("Darwin demo-mac 25.2.0 arm64")
        case "date": lineOut(Date.now.formatted(date: .abbreviated, time: .standard))
        case "exit", "logout":
            lineOut(DemoText.dim("logout"))
        case "codex":
            lineOut(DemoText.dim("Codex isn't part of the demo. Add a host to run it."))
        case "claude":
            mode = .claude
            lineOut(DemoText.orange("   ✻ Welcome to Claude Code!"))
            lineOut(DemoText.dim("     /help for help, /exit to leave"))
            lineOut()
            showPrompt()
            return
        case "npm":
            if arg.hasPrefix("run dev") || arg == "dev" {
                startDev()
                return
            }
            lineOut("npm: unknown command '\(arg)'")
        case "swift":
            if arg == "test" {
                lineOut(DemoText.dim("Building for debugging..."))
                lineOut(DemoText.dim("[1/42] Compiling GoRex"))
                lineOut(DemoText.dim("[42/42] Linking GoRexTests"))
                lineOut()
                lineOut(DemoText.green("✔ Test run with 312 tests passed after 4.201 seconds."))
            } else {
                lineOut("swift: unknown command '\(arg)'")
            }
        case "git": git(arg)
        default:
            lineOut("zsh: command not found: \(command)")
        }
        showPrompt()
    }

    private var abs: String {
        dir == "~" ? "/Users/demo" : "/Users/demo" + dir.dropFirst(1)
    }

    private func cd(_ arg: String) {
        if arg.isEmpty || arg == "~" {
            dir = "~"
        } else if arg == ".." {
            if let i = dir.lastIndex(of: "/"), i > dir.startIndex {
                dir = String(dir[..<i])
            } else {
                dir = "~"
            }
        } else if arg.hasPrefix("/") {
            dir = arg.replacingOccurrences(of: "/Users/demo", with: "~")
        } else if arg.hasPrefix("~/") {
            dir = arg
        } else {
            dir = dir == "~" ? "~/" + arg : dir + "/" + arg
        }
    }

    private func ls(_ arg: String) {
        if arg == "-l" || arg == "-la" || arg == "-al" {
            lineOut("drwxr-xr-x   8 demo  staff    256 Nov 13 09:02 \(DemoText.blue("docs"))")
            lineOut("drwxr-xr-x  21 demo  staff    672 Nov 13 09:11 \(DemoText.blue("ios"))")
            lineOut("drwxr-xr-x  14 demo  staff    448 Nov 12 18:40 \(DemoText.blue("web"))")
            lineOut("-rw-r--r--   1 demo  staff   4219 Nov 13 08:52 README.md")
            lineOut("-rw-r--r--   1 demo  staff    815 Nov 11 21:07 go.mod")
            lineOut("-rw-r--r--   1 demo  staff  18240 Nov 13 09:04 main.go")
        } else {
            lineOut("\(DemoText.blue("docs/"))  \(DemoText.blue("ios/"))  \(DemoText.blue("web/"))  README.md  go.mod  main.go")
        }
    }

    private func cat(_ arg: String) {
        guard arg == "README.md" else {
            lineOut("cat: \(arg.isEmpty ? "README.md" : arg): No such file or directory")
            return
        }
        lineOut(DemoText.bold("# GoRex"))
        lineOut()
        lineOut("Run your terminals in one window. Every session, every window")
        lineOut("and every pane of your Mac, on your phone.")
        lineOut()
        lineOut("From the app: add the machine where GoRex runs, in Settings.")
    }

    private func git(_ arg: String) {
        switch arg {
        case "status", "status -s", "status --short":
            lineOut("On branch main")
            lineOut("Your branch is up to date with 'origin/main'.")
            lineOut()
            lineOut("Changes not staged for commit:")
            lineOut("  (use \"git add <file>...\" to update what will be committed)")
            lineOut("\t\(DemoText.red("modified:   ios/GoRex/Views/HostListView.swift"))")
            lineOut()
            lineOut("no changes added to commit (use \"git add\" and/or \"git commit -a\")")
        case "log", "log --oneline", "log --oneline -5":
            lineOut("\(DemoText.yellow("d195e0c")) feat(web): add terminal find and the command palette")
            lineOut("\(DemoText.yellow("05c9296")) fix(rexapi): read pane output at once")
            lineOut("\(DemoText.yellow("34040e2")) Add asc-testflight workspace skill")
            lineOut("\(DemoText.yellow("5942bd2")) fix(web): copy fallback before awaiting")
            lineOut("\(DemoText.yellow("5dcd87c")) feat(web): upload dropped files")
        case "diff":
            lineOut("diff --git a/ios/GoRex/Views/HostListView.swift b/ios/GoRex/Views/HostListView.swift")
            lineOut("@@ -140,3 +140,4 @@")
            lineOut(DemoText.green("+        Try Demo Mode"))
            lineOut(DemoText.red("-        No hosts yet."))
        case "branch", "branch --show-current":
            lineOut("* main")
        default:
            lineOut("git: '\(arg)' is not a git command. See 'git --help'.")
        }
    }

    /// The dev server's output, as the window had it; no prompt while it
    /// runs, and Ctrl-C stops it.
    private func startDev() {
        mode = .dev
        lineOut()
        lineOut(DemoText.dim("> dashboard@0.1.0 dev"))
        lineOut(DemoText.dim("> vite"))
        lineOut()
        lineOut("  \(DemoText.green("VITE v7.1.3"))  ready in 214 ms")
        lineOut()
        lineOut("  \(DemoText.cyan("➜"))  Local:   \(DemoText.cyan("http://localhost:5173/"))")
        lineOut("  \(DemoText.cyan("➜"))  Network: \(DemoText.cyan("http://192.168.1.24:5173/"))")
        lineOut()
        lineOut("  \(DemoText.dim("➜  press h + enter to show help"))")
    }

    // MARK: Claude Code

    private func ask(_ text: String) {
        guard !text.isEmpty else {
            showPrompt()
            return
        }
        if text.hasPrefix("/") {
            switch text {
            case "/help":
                lineOut("  \(DemoText.bold("/exit"))  leave Claude Code")
                lineOut("  This demo answers a question or two; it runs on the phone.")
            case "/exit":
                mode = .shell
                lineOut(DemoText.dim("Goodbye!"))
            default:
                lineOut("Unknown command: \(text)")
            }
            showPrompt()
            return
        }
        let answer = text.lowercased()
        if !answered, answer == "y" || answer == "yes" {
            answered = true
            lineOut(DemoText.green("  ✓ Applied the patch to ios/GoRexTests/TabsTests.swift."))
            lineOut(DemoText.dim("  Run swift test to check it; /exit leaves Claude Code."))
        } else {
            lineOut(DemoText.orange("✻") + " This is a demo: Claude Code runs on your Mac, which isn't connected.")
            lineOut(DemoText.dim("  Add a host in Settings › Remote Hosts to use it for real."))
        }
        showPrompt()
    }
}
