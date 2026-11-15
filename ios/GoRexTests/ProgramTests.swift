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

    @MainActor func testTileMotionMapping() {
        XCTAssertEqual(TileMotion(state: "blocked"), .attention)
        XCTAssertEqual(TileMotion(state: "error"), .attention)
        XCTAssertEqual(TileMotion(state: "working"), .none)
        XCTAssertEqual(TileMotion(state: "done"), .done)
        XCTAssertEqual(TileMotion(state: "idle"), .none)
        XCTAssertEqual(TileMotion(state: nil), .none)
        XCTAssertEqual(TileMotion(state: "unknown"), .none)

        XCTAssertTrue(TileMotion.attention.isRepeating)
        XCTAssertTrue(TileMotion.done.isRepeating)
        XCTAssertFalse(TileMotion.none.isRepeating)
    }

    @MainActor func testWorkingDotsPhaseStagger() {
        // Dot 0 peaks at quarter period (0.3s), while dots 1 and 2 are staggered.
        let p0 = WorkingDots.phase(at: 0.3, index: 0)
        let p1 = WorkingDots.phase(at: 0.3, index: 1)
        let p2 = WorkingDots.phase(at: 0.3, index: 2)
        XCTAssertEqual(p0, 1.0, accuracy: 0.001)
        XCTAssertLessThan(p1, p0)
        XCTAssertGreaterThan(p1, 0)
        XCTAssertEqual(p2, 0.0, accuracy: 0.001)

        // All phases stay within 0...1 across the cycle.
        for step in 0..<24 {
            let t = Double(step) * 0.05
            for i in 0..<3 {
                let v = WorkingDots.phase(at: t, index: i)
                XCTAssertGreaterThanOrEqual(v, 0)
                XCTAssertLessThanOrEqual(v, 1)
            }
        }
    }
}
