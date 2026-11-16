import XCTest

nonisolated final class PaneSwitcherTests: XCTestCase {
    private let window = """
    {
      "payload": {
        "revision": 3,
        "session_id": "session:one",
        "windows": [{
          "window_id": "window:one",
          "focused_block_id": "block:a",
          "layers": [{
            "blocks": [{"block_id": "block:a", "label": "train"},
                       {"block_id": "block:b", "label": "logs"},
                       {"block_id": "block:c", "label": "gpu"}],
            "layout": {
              "rect": [0, 0, 1, 1],
              "split": {
                "split_id": "split:one",
                "direction": "horizontal",
                "ratio": 0.4,
                "before": {"rect": [0, 0, 0.4, 1], "block_id": "block:a"},
                "after": {
                  "rect": [0.4, 0, 0.6, 1],
                  "split": {
                    "split_id": "split:two",
                    "direction": "vertical",
                    "ratio": 0.5,
                    "before": {"rect": [0.4, 0, 0.6, 0.5], "block_id": "block:b"},
                    "after": {"rect": [0.4, 0.5, 0.6, 0.5], "block_id": "block:c"}
                  }
                }
              }
            }
          }]
        }]
      }
    }
    """.data(using: .utf8)!

    @MainActor func testPanesAndLayout() throws {
        let view = try SessionView(response: window)
        let w = try XCTUnwrap(view.windows?.first)
        XCTAssertEqual(w.panes, ["block:a", "block:b", "block:c"])
        let split = try XCTUnwrap(w.layout?.split)
        XCTAssertEqual(split.direction, "horizontal")
        XCTAssertEqual(split.ratio, 0.4, accuracy: 0.0001)
        XCTAssertEqual(split.before.block_id, "block:a")
        XCTAssertEqual(split.after.split?.direction, "vertical")
    }

    @MainActor func testCellsFollowTheTree() throws {
        let view = try SessionView(response: window)
        let w = try XCTUnwrap(view.windows?.first)
        let cells = paneCells(layout: w.layout, panes: w.panes, name: { $0 }, record: { _ in nil })
        XCTAssertEqual(cells.map(\.block), ["block:a", "block:b", "block:c"])
        XCTAssertEqual(Double(cells[0].rect.origin.x), 0, accuracy: 0.0001)
        XCTAssertEqual(Double(cells[0].rect.width), 0.4, accuracy: 0.0001)
        XCTAssertEqual(Double(cells[1].rect.minY), 0, accuracy: 0.0001)
        XCTAssertEqual(Double(cells[1].rect.height), 0.5, accuracy: 0.0001)
        XCTAssertEqual(Double(cells[2].rect.minY), 0.5, accuracy: 0.0001)
    }

    @MainActor func testCellsWithoutATreeGoInARow() {
        let cells = paneCells(layout: nil, panes: ["a", "b", "c"], name: { $0 }, record: { _ in nil })
        XCTAssertEqual(cells.map(\.block), ["a", "b", "c"])
        XCTAssertEqual(Double(cells[1].rect.origin.x), 1.0 / 3, accuracy: 0.0001)
        XCTAssertEqual(Double(cells[2].rect.maxX), 1, accuracy: 0.0001)
    }

    @MainActor func testCellsLeaveOutBlockTheWindowDoesNotHold() throws {
        let view = try SessionView(response: window)
        let w = try XCTUnwrap(view.windows?.first)
        let cells = paneCells(layout: w.layout, panes: ["block:a", "block:c"], name: { $0 }, record: { _ in nil })
        XCTAssertEqual(cells.map(\.block), ["block:a", "block:c"])
    }

    @MainActor func testViewedBlockStore() throws {
        let suite = "PaneSwitcherTests.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let store = ViewedBlockStore(defaults: defaults)
        let key = ViewedBlockKey(machine: "m", session: "s", window: "w")
        XCTAssertNil(store.block(for: key))
        store.set("block:a", for: key)
        XCTAssertEqual(store.block(for: key), "block:a")
        store.set("block:b", for: key)
        XCTAssertEqual(store.block(for: key), "block:b")
        let other = ViewedBlockKey(machine: "m", session: "s", window: "w2")
        XCTAssertNil(store.block(for: other))
        store.forget(key)
        XCTAssertNil(store.block(for: key))
    }


    @MainActor func testCellsComputeMissingRects() {
        let layout = PaneLayout(split: PaneSplit(direction: "horizontal", ratio: 0.25,
                                                 before: PaneLayout(block_id: "a"),
                                                 after: PaneLayout(block_id: "b")))
        let cells = paneCells(layout: layout, panes: ["a", "b"], name: { $0 }, record: { _ in nil })
        XCTAssertEqual(Double(cells[0].rect.width), 0.25, accuracy: 0.0001)
        XCTAssertEqual(Double(cells[1].rect.minX), 0.25, accuracy: 0.0001)
    }


    @MainActor func testCellsFallBackWhenTreeMissesAHeldPane() {
        let layout = PaneLayout(block_id: "a")
        let cells = paneCells(layout: layout, panes: ["a", "b"], name: { $0 }, record: { _ in nil })
        XCTAssertEqual(cells.map(\.block), ["a", "b"])
        XCTAssertEqual(Double(cells[0].rect.width), 0.5, accuracy: 0.0001)
    }

    @MainActor func testPaneNeighborFollowsTheLayout() {
        let cells = [
            PaneCell(block: "left", rect: CGRect(x: 0, y: 0, width: 0.55, height: 1), name: "left", record: nil),
            PaneCell(block: "top", rect: CGRect(x: 0.55, y: 0, width: 0.45, height: 0.5), name: "top", record: nil),
            PaneCell(block: "bottom", rect: CGRect(x: 0.55, y: 0.5, width: 0.45, height: 0.5), name: "bottom", record: nil),
        ]
        XCTAssertEqual(paneNeighbor(cells, from: "top", direction: .left), "left")
        XCTAssertEqual(paneNeighbor(cells, from: "top", direction: .down), "bottom")
        XCTAssertNil(paneNeighbor(cells, from: "top", direction: .up))
        XCTAssertNil(paneNeighbor(cells, from: "top", direction: .right))
        XCTAssertEqual(paneNeighbor(cells, from: "bottom", direction: .up), "top")
        XCTAssertEqual(paneNeighbor(cells, from: "bottom", direction: .left), "left")
        XCTAssertNil(paneNeighbor(cells, from: "left", direction: .down))
        XCTAssertNotNil(paneNeighbor(cells, from: "left", direction: .right))
    }
}
