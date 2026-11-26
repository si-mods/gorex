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

    /// Without the mode the bar stays away and the two-capsule key bar shows
    /// `+` (More), Esc/Ctrl/Tab/Enter, the stick, and Dictate (with Show
    /// Keyboard hidden at rest). Opening Compose from `+` shows the bar, and
    /// swiping down on the top grabber dismisses it again.
    func testNoComposerByDefault() throws {
        XCTAssertTrue(message.waitForNonExistence(timeout: 5))
        XCTAssertTrue(app.buttons.matching(identifier: "Dictate").firstMatch.waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons.matching(identifier: "Tab").firstMatch.waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["Show Keyboard"].exists)
        let more = app.buttons["keybar.more"]
        XCTAssertTrue(more.waitForExistence(timeout: 5))
        shot("keybar-two-capsules-rest")

        // Opening the `+` menu and tapping Compose opens the bar and hides `+`.
        more.tap()
        let composeItem = app.buttons["Compose"]
        XCTAssertTrue(composeItem.waitForExistence(timeout: 5))
        composeItem.tap()
        XCTAssertTrue(message.waitForExistence(timeout: 5))
        let grabber = app.otherElements["composer.grabber"]
        XCTAssertTrue(grabber.waitForExistence(timeout: 5))

        // Dragging down on the top grabber dismisses Composer Mode and restores
        // `+` and Dictate on the key bar.
        let start = grabber.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5))
        let end = start.withOffset(CGVector(dx: 0, dy: 90))
        start.press(forDuration: 0.05, thenDragTo: end)
        XCTAssertTrue(message.waitForNonExistence(timeout: 5))
        XCTAssertTrue(more.waitForExistence(timeout: 5))
        shot("composer-mode-closed")
    }

    /// Composer Mode pins the bar with a top grabber handle, the mic beside
    /// the field, + always present for Paste, `+` hidden on the key bar, and
    /// collapses the key bar while the software keyboard is up.
    func testComposerModePinsBar() throws {
        app.terminate()
        app.launchArguments = ["-composerMode", "YES"]
        app.launch()
        try terminal()
        XCTAssertTrue(message.waitForExistence(timeout: 10))
        XCTAssertTrue(app.buttons.matching(identifier: "Add Attachment").firstMatch.waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["keybar.more"].exists)
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

        // Raising the keyboard collapses the key bar under the compose bar,
        // grows the bar cleanly for multiple lines, keeps +, mic, and Send
        // aligned to the bottom row, and routes Backspace to the compose field.
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
