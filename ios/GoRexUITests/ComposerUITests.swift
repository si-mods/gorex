import XCTest

/// The compose bar (docs/IOS-025-composer.md): the key bar's ⌘ menu, its
/// Compose key by ⌘, and a flick up on the bar each open the capsule over
/// it, with the message field that names the block. Demo Mode's host
/// takes no uploads, so the attach menu stays away; the test covers the
/// message side.
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
        XCTAssertTrue(more.waitForExistence(timeout: 15))
    }

    /// The key bar's ⌘ button: the bar's "More", under the navigation
    /// bar's own, is the lowest one on screen.
    private var more: XCUIElement {
        app.buttons.matching(identifier: "More").allElementsBoundByIndex
            .sorted { $0.frame.midY > $1.frame.midY }.first ?? app.buttons["More"].firstMatch
    }

    /// The compose bar's message field, which names the demo's block.
    private var message: XCUIElement { app.textViews["composer.message"] }

    /// ⌘'s menu holds Compose first, before Paste; a second one closes.
    func testMenuTogglesComposer() throws {
        more.tap()
        // The menu's item, not the key bar's own Compose key.
        let compose = app.collectionViews.buttons["Compose"]
        XCTAssertTrue(compose.waitForExistence(timeout: 5))
        compose.tap()
        XCTAssertTrue(message.waitForExistence(timeout: 10))
        shot("composer")
        XCTAssertTrue(app.buttons["Send"].exists)
        more.tap()
        XCTAssertTrue(app.collectionViews.buttons["Compose"].waitForExistence(timeout: 5))
        app.collectionViews.buttons["Compose"].tap()
        XCTAssertTrue(message.waitForNonExistence(timeout: 5))
    }

    /// The Compose key, next to ⌘ in the left capsule.
    func testComposeKey() throws {
        shot("keybar-compose")
        app.buttons["Compose"].tap()
        XCTAssertTrue(message.waitForExistence(timeout: 10))
    }

    /// A flick up on the key bar opens the composer, as the Compose key does.
    func testSwipeUpOpensComposer() throws {
        // From the bar's middle, nowhere near the stick's drag.
        let bar = more.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5))
            .withOffset(CGVector(dx: 150, dy: 0))
        let up = bar.withOffset(CGVector(dx: 0, dy: -120))
        bar.press(forDuration: 0.05, thenDragTo: up, withVelocity: .fast, thenHoldForDuration: 0)
        XCTAssertTrue(message.waitForExistence(timeout: 10))
        shot("composer-swipe")
    }

    private func shot(_ name: String) {
        let s = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        s.name = name
        s.lifetime = .keepAlways
        add(s)
    }
}
