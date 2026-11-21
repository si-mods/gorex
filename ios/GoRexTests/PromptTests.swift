import Foundation
import XCTest

/// What a `client_prompt_requested` event becomes, and the result a
/// `client.reply` carries (docs/MOD-005-client-prompts.md).
nonisolated final class PromptTests: XCTestCase {
    func testInputPrompt() throws {
        let payload: [String: Any] = [
            "call_id": "7", "action": "New worktree",
            "timeout_ms": 90_000,
            "prompt": ["kind": "input", "title": "Branch name", "placeholder": "main-",
                       "value": "main", "confirm": "Create"],
        ]
        let prompt = try XCTUnwrap(Prompt.from(payload))
        XCTAssertEqual(prompt.callID, "7")
        XCTAssertEqual(prompt.action, "New worktree")
        XCTAssertEqual(prompt.spec.kind, .input)
        XCTAssertEqual(prompt.spec.title, "Branch name")
        XCTAssertEqual(prompt.spec.placeholder, "main-")
        XCTAssertEqual(prompt.spec.value, "main")
        XCTAssertEqual(prompt.spec.confirm, "Create")
        XCTAssertFalse(prompt.spec.multiple)
        XCTAssertFalse(prompt.spec.allowOther)
        XCTAssertTrue(prompt.spec.items.isEmpty)
        XCTAssertEqual(prompt.deadline.timeIntervalSinceNow, 90, accuracy: 2)
    }

    func testPickPrompt() throws {
        let payload: [String: Any] = [
            "call_id": "8",
            "prompt": [
                "kind": "pick", "title": "Project", "multiple": true, "allow_other": true,
                "items": [
                    ["id": "a", "title": "Alpha", "subtitle": "~/alpha", "selected": true],
                    ["id": "b", "title": "Beta", "icon": "folder", "detail": "Last used"],
                    ["title": "No ID"],
                ],
            ],
        ]
        let prompt = try XCTUnwrap(Prompt.from(payload))
        XCTAssertEqual(prompt.action, "")
        XCTAssertEqual(prompt.spec.kind, .pick)
        XCTAssertTrue(prompt.spec.multiple)
        XCTAssertTrue(prompt.spec.allowOther)
        XCTAssertEqual(prompt.spec.items.map(\.id), ["a", "b"])
        XCTAssertEqual(prompt.spec.items[0].subtitle, "~/alpha")
        XCTAssertTrue(prompt.spec.items[0].selected)
        XCTAssertEqual(prompt.spec.items[1].detail, "Last used")
        // No timeout_ms: the host's default of five minutes.
        XCTAssertEqual(prompt.deadline.timeIntervalSinceNow, 300, accuracy: 2)
    }

    func testOtherEvent() {
        XCTAssertNil(Prompt.from(["call_id": "9", "prompt": ["kind": "input"]]))
        XCTAssertNil(Prompt.from(["call_id": "9", "prompt": ["kind": "slider", "title": "Size"]]))
        XCTAssertNil(Prompt.from(["prompt": ["kind": "input", "title": "Name"]]))
    }

    func testAnswerResults() throws {
        func result(_ answer: Prompt.Answer) -> [String: Any] {
            answer.result
        }
        XCTAssertEqual(result(.text("alpha")) as? [String: String], ["text": "alpha"])
        XCTAssertEqual(result(.ids(["b", "a"])) as? [String: [String]], ["ids": ["b", "a"]])
        XCTAssertEqual(result(.cancelled) as? [String: Bool], ["cancelled": true])
    }
}
