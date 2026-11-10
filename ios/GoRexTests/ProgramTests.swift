import XCTest

/// How a host's process names its program.
nonisolated final class ProgramTests: XCTestCase {
    /// A Windows host's argv0 is a path by \ with .exe, as PowerShell runs
    /// claude.exe by its path; it names the program, as on the desktop.
    @MainActor func testArgv0() {
        let cases: [(String, String)] = [
            (#"C:\Users\me\.local\bin\claude.exe"#, "claude"),
            (#"C:\Program Files\PowerShell\7\pwsh.EXE"#, "pwsh"),
            ("/Users/me/.local/bin/claude", "claude"),
            ("claude", "claude"),
        ]
        for (argv0, want) in cases {
            XCTAssertEqual(Host.program(["argv0": argv0, "name": "x"]), want, argv0)
        }
        XCTAssertEqual(Host.program(["name": "claude"]), "claude")
        XCTAssertEqual(Host.program(["argv0": "", "name": "claude"]), "claude")
    }
}
