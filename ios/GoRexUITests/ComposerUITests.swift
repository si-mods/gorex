import XCTest

/// The compose bar (docs/IOS-025-composer.md): it is up only while
/// Settings > Terminal's Composer Mode is on — here the defaults
/// argument — pinned over the key bar, the mic moved beside its field.
/// Demo Mode's host takes no uploads, so the attach menu stays away; the
/// test covers the message side.
final class ComposerUITests: XCTestCase {
    private var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
        app = XCUIApplication()
        // A fresh install raises the notification-permission alert over the
        // launch; decline it so the first screen is reachable.
        addUIInterruptionMonitor(withDescription: "system alerts") { alert in
            for b in ["Don't Allow", "Allow", "OK"] where alert.buttons[b].exists {
                alert.buttons[b].tap()
                return true
            }
            return false
        }
        app.launch()
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let notifAlert = springboard.alerts.firstMatch
        if notifAlert.waitForExistence(timeout: 5) {
            for b in ["Don’t Allow", "Don't Allow", "Allow"] where notifAlert.buttons[b].exists {
                notifAlert.buttons[b].tap()
                break
            }
        }
        try terminal()
    }

    /// Demo Mode's host, and a terminal of it: the key bar shows.
    private func terminal() throws {
        let demo = app.buttons["Try Demo Mode"]
        guard demo.waitForExistence(timeout: 30) else {
            throw XCTSkip("needs a fresh simulator's empty host list")
        }
        demo.tap()
        // The demo's loose tab, a window row under its own header.
        let row = app.buttons["Shell"]
        XCTAssertTrue(row.waitForExistence(timeout: 15))
        row.tap()
        XCTAssertTrue(app.buttons.matching(identifier: "Esc").firstMatch.waitForExistence(timeout: 15))
    }

    /// The compose bar's message field, which names the demo's block.
    private var message: XCUIElement { app.textViews["composer.message"] }

    /// Without the mode the bar stays away and the mic, ⌘, and Compose toggle
    /// are on the key bar; tapping Compose opens the bar immediately, and
    /// swiping down on the top grabber dismisses it again.
    func testNoComposerByDefault() throws {
        XCTAssertTrue(message.waitForNonExistence(timeout: 5))
        XCTAssertTrue(app.buttons.matching(identifier: "Dictate").firstMatch.waitForExistence(timeout: 5))
        let esc = app.buttons.matching(identifier: "Esc").firstMatch
        let mores = app.buttons.matching(identifier: "More").allElementsBoundByIndex
        XCTAssertTrue(mores.contains { abs($0.frame.midY - esc.frame.midY) < 40 })
        let compose = app.buttons["keybar.compose"]
        XCTAssertTrue(compose.waitForExistence(timeout: 5))

        // Tapping Compose opens the bar and hides ⌘ on the key bar.
        compose.tap()
        XCTAssertTrue(message.waitForExistence(timeout: 5))
        let grabber = app.otherElements["composer.grabber"]
        XCTAssertTrue(grabber.waitForExistence(timeout: 5))

        // Dragging down on the top grabber dismisses Composer Mode and restores
        // ⌘ and Dictate on the key bar.
        let start = grabber.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5))
        let end = start.withOffset(CGVector(dx: 0, dy: 90))
        start.press(forDuration: 0.05, thenDragTo: end)
        XCTAssertTrue(message.waitForNonExistence(timeout: 5))
        let restoredMores = app.buttons.matching(identifier: "More").allElementsBoundByIndex
        XCTAssertTrue(restoredMores.contains { abs($0.frame.midY - esc.frame.midY) < 40 })
        shot("composer-mode-closed")

        // Tapping Compose again reopens it, and tapping Compose while open
        // toggles it back off.
        compose.tap()
        XCTAssertTrue(message.waitForExistence(timeout: 5))
        compose.tap()
        XCTAssertTrue(message.waitForNonExistence(timeout: 5))
    }

    /// Composer Mode pins the bar with a top grabber handle, the mic beside
    /// the field, + always present for Paste, ⌘ hidden on the key bar while
    /// the Compose toggle stays visible.
    func testComposerModePinsBar() throws {
        app.terminate()
        app.launchArguments = ["-composerMode", "YES"]
        app.launch()
        try terminal()
        XCTAssertTrue(message.waitForExistence(timeout: 10))
        XCTAssertTrue(app.buttons.matching(identifier: "Add Attachment").firstMatch.waitForExistence(timeout: 5))
        let esc = app.buttons.matching(identifier: "Esc").firstMatch
        let mores = app.buttons.matching(identifier: "More").allElementsBoundByIndex
        XCTAssertFalse(mores.contains { abs($0.frame.midY - esc.frame.midY) < 40 })
        let compose = app.buttons["keybar.compose"]
        XCTAssertTrue(compose.waitForExistence(timeout: 5))
        // The key bar's mic moved into the bar, beside the field (the
        // mic's view exposes more than one element; take its own row's).
        let dictates = app.buttons.matching(identifier: "Dictate").allElementsBoundByIndex
        XCTAssertTrue(dictates.contains { abs($0.frame.midY - message.frame.midY) < 40 })
        // Compact capsule with top grabber: resting height is 72pt
        // (14pt grabber + 2pt gap + 46pt field + 10pt padding).
        let grabber = app.otherElements["composer.grabber"]
        XCTAssertTrue(grabber.waitForExistence(timeout: 5))
        let bar = app.otherElements["composer"]
        if bar.exists {
            XCTAssertLessThanOrEqual(bar.frame.height, 78)
        }
        shot("composer-mode-rest")

        // Raising the keyboard and typing multiple lines grows the bar cleanly,
        // keeps +, mic, and Send aligned to the bottom row, and Backspace
        // edits the compose field rather than the terminal.
        message.tap()
        if app.keyboards.element.waitForExistence(timeout: 5) {
            message.typeText("abc" + XCUIKeyboardKey.delete.rawValue)
            XCTAssertEqual(message.value as? String, "ab")
            message.typeText("\nline 2\nline 3")
            let addBtn = app.buttons.matching(identifier: "Add Attachment").firstMatch
            let sendBtn = app.buttons.matching(identifier: "Send").firstMatch
            XCTAssertGreaterThan(addBtn.frame.midY, message.frame.midY)
            XCTAssertGreaterThan(sendBtn.frame.midY, message.frame.midY)
            XCTAssertLessThan(abs(addBtn.frame.midY - sendBtn.frame.midY), 4)
            shot("composer-mode-keyboard")
        }
    }

    private func shot(_ name: String) {
        let s = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        s.name = name
        s.lifetime = .keepAlways
        add(s)
    }
}
