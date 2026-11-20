import SwiftUI
import XCTest

// The list's endless motion (#847): the working dots and the tiles' bounce
// pause out of the front (the dots off screen too), and the key bar's mic and voice
// capsule are given again only when what they show changes.
nonisolated final class ListMotionTests: XCTestCase {
    @MainActor func testListMotionRunsOnlyActiveOnScreenWithMotion() {
        XCTAssertTrue(ListMotion.runs(reduceMotion: false, phase: .active, onScreen: true))
        XCTAssertFalse(ListMotion.runs(reduceMotion: true, phase: .active, onScreen: true))
        XCTAssertFalse(ListMotion.runs(reduceMotion: false, phase: .inactive, onScreen: true))
        XCTAssertFalse(ListMotion.runs(reduceMotion: false, phase: .background, onScreen: true))
        XCTAssertFalse(ListMotion.runs(reduceMotion: false, phase: .active, onScreen: false))
    }

    @MainActor func testTileMotionRepeatsOnlyForAttentionAndDone() {
        XCTAssertTrue(TileMotion(state: "blocked").isRepeating)
        XCTAssertTrue(TileMotion(state: "error").isRepeating)
        XCTAssertTrue(TileMotion(state: "done").isRepeating)
        XCTAssertFalse(TileMotion(state: "working").isRepeating)
        XCTAssertFalse(TileMotion(state: nil).isRepeating)
    }

    @MainActor func testTileBounceKeyChangesAsMotionPausesAndResumes() {
        // A resume (the scene active again, Reduce Motion off) must make a
        // new animator, so the bounce starts again.
        let running = TileBounceKey(motion: .attention, runs: true)
        let paused = TileBounceKey(motion: .attention, runs: false)
        XCTAssertNotEqual(running, paused)
        XCTAssertEqual(running, TileBounceKey(motion: .attention, runs: true))
        XCTAssertNotEqual(running, TileBounceKey(motion: .done, runs: true))
        XCTAssertTrue(running.repeats)
        XCTAssertFalse(paused.repeats)
        XCTAssertFalse(TileBounceKey(motion: .none, runs: true).repeats)
    }

    @MainActor func testChangeGatePassesOnlyChanges() {
        var gate = ChangeGate(Dictation.Said?.none)
        XCTAssertFalse(gate.update(nil), "the same value again is no change")
        let said = Dictation.Said(phrases: ["hello"])
        XCTAssertTrue(gate.update(said))
        XCTAssertEqual(gate.value, said)
        XCTAssertFalse(gate.update(said), "a frame that changes nothing gives nothing again")
        XCTAssertTrue(gate.update(nil))
    }

    @MainActor func testPaneCellsCountIsPanesCount() {
        // TerminalScreen counts the panes without making the cells: a layout
        // that misses a pane, or none, still gives a cell per pane.
        let panes = ["block:a", "block:b", "block:c"]
        let none = paneCells(layout: nil, panes: panes, name: { $0 }, record: { _ in nil })
        XCTAssertEqual(none.count, panes.count)
        let missing = PaneLayout(split: PaneSplit(direction: "horizontal", ratio: 0.5,
                                                  before: PaneLayout(block_id: "block:a"),
                                                  after: PaneLayout(block_id: "block:b")))
        XCTAssertEqual(paneCells(layout: missing, panes: panes, name: { $0 }, record: { _ in nil }).count, panes.count)
        let extra = PaneLayout(split: PaneSplit(direction: "vertical", ratio: 0.5,
                                                before: PaneLayout(block_id: "block:a"),
                                                after: PaneLayout(block_id: "block:z")))
        XCTAssertEqual(paneCells(layout: extra, panes: ["block:a", "block:b"], name: { $0 }, record: { _ in nil }).count, 2)
        XCTAssertEqual(paneCells(layout: nil, panes: [], name: { $0 }, record: { _ in nil }).count, 0)
    }
}
