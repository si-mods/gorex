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
        XCTAssertTrue(app.buttons.matching(identifier: "More").firstMatch.waitForExistence(timeout: 15))
    }

    /// The compose bar's message field, which names the demo's block.
    private var message: XCUIElement { app.textFields["composer.message"] }

    /// Without the mode the bar stays away and the mic is the key bar's.
    func testNoComposerByDefault() throws {
        XCTAssertTrue(message.waitForNonExistence(timeout: 5))
        XCTAssertTrue(app.buttons.matching(identifier: "Dictate").firstMatch.waitForExistence(timeout: 5))
    }

    /// Composer Mode (Settings > Terminal, here the defaults argument)
    /// pins the bar: it is up on its own, with the mic beside the field.
    func testComposerModePinsBar() throws {
        app.terminate()
        app.launchArguments = ["-composerMode", "YES"]
        app.launch()
        try terminal()
        XCTAssertTrue(message.waitForExistence(timeout: 10))
        // The key bar's mic moved into the bar, beside the field (the
        // mic's view exposes more than one element; take its own row's).
        let dictates = app.buttons.matching(identifier: "Dictate").allElementsBoundByIndex
        XCTAssertTrue(dictates.contains { abs($0.frame.midY - message.frame.midY) < 40 })
        shot("composer-mode")
    }

    private func shot(_ name: String) {
        let s = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        s.name = name
        s.lifetime = .keepAlways
        add(s)
    }
}
