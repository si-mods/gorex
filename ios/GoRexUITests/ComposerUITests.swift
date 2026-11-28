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

    /// Without the mode the bar stays away and the key bar shows ⌘, Compose,
    /// the continuous-scroll terminal keys, stick, Dictate, and Keyboard.
    /// Tapping Compose opens the bar in one tap, and swiping down on the top
    /// grabber dismisses it and restores ⌘ and Dictate.
    func testNoComposerByDefault() throws {
        XCTAssertTrue(message.waitForNonExistence(timeout: 5))
        XCTAssertTrue(app.buttons.matching(identifier: "Dictate").firstMatch.waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["keybar.more"].waitForExistence(timeout: 5))
        let compose = app.buttons["keybar.compose"]
        XCTAssertTrue(compose.waitForExistence(timeout: 5))
        shot("keybar-rex-rest")

        // Tapping Compose opens the unified Composer-centric card (IOS-030)
        // with the quiet quick-keys marker centered in the bottom action row.
        compose.tap()
        XCTAssertTrue(message.waitForExistence(timeout: 5))
        let grabber = app.otherElements["composer.grabber"]
        XCTAssertTrue(grabber.waitForExistence(timeout: 5))
        let addBtn = app.buttons.matching(identifier: "Add Attachment").firstMatch
        let sendBtn = app.buttons.matching(identifier: "Send").firstMatch
        let toggleBtn = app.buttons["composer.keys.toggle"]
        XCTAssertTrue(addBtn.waitForExistence(timeout: 5))
        XCTAssertTrue(sendBtn.waitForExistence(timeout: 5))
        XCTAssertTrue(toggleBtn.waitForExistence(timeout: 5))
        XCTAssertGreaterThan(addBtn.frame.midY, message.frame.midY)
        XCTAssertLessThan(abs(addBtn.frame.midY - sendBtn.frame.midY), 4)
        let actionRowY = sendBtn.frame.midY
        shot("composer-centric-collapsed")

        // Tapping the quiet marker expands Esc, Tab, Enter, Delete, Arrow Keys,
        // More Keys, and the collapse button full-width across the action row
        // (temporarily fading +, mic, and Send) so every key meets Apple HIG's
        // 44 pt minimum tap width.
        toggleBtn.tap()
        let moreKeys = app.buttons["composer.keys"]
        let collapseBtn = app.buttons["composer.keys.collapse"]
        let deleteBtn = app.buttons["Delete"]
        XCTAssertTrue(moreKeys.waitForExistence(timeout: 5))
        XCTAssertTrue(collapseBtn.waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["Tab"].waitForExistence(timeout: 5))
        XCTAssertTrue(deleteBtn.waitForExistence(timeout: 5))
        XCTAssertGreaterThanOrEqual(deleteBtn.frame.width, 44)
        XCTAssertLessThan(abs(moreKeys.frame.midY - actionRowY), 4)
        shot("composer-centric-expanded")

        // Tapping collapse springs back to the quiet marker.
        collapseBtn.tap()
        XCTAssertTrue(toggleBtn.waitForExistence(timeout: 5))

        // Dragging down on the top grabber dismisses Composer Mode and restores
        // the traditional key bar below.
        let start = grabber.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5))
        let end = start.withOffset(CGVector(dx: 0, dy: 90))
        start.press(forDuration: 0.05, thenDragTo: end)
        XCTAssertTrue(message.waitForNonExistence(timeout: 5))
        XCTAssertTrue(app.buttons["keybar.more"].waitForExistence(timeout: 5))
        shot("composer-mode-closed")

        // Tapping Compose reopens it, expanding quick keys -> More Keys ->
        // Show Key Bar closes Composer back to KeyBar.
        compose.tap()
        XCTAssertTrue(message.waitForExistence(timeout: 5))
        toggleBtn.tap()
        XCTAssertTrue(moreKeys.waitForExistence(timeout: 5))
        moreKeys.tap()
        let showBar = app.buttons["Show Key Bar"]
        XCTAssertTrue(showBar.waitForExistence(timeout: 5))
        showBar.tap()
        XCTAssertTrue(message.waitForNonExistence(timeout: 5))
    }

    /// Composer Mode pins the two-row bar with a top grabber handle, full-width
    /// field above circular `+`, mic, and Send, while keeping the left capsule
    /// stable below.
    func testComposerModePinsBar() throws {
        app.terminate()
        app.launchArguments = ["-composerMode", "YES"]
        app.launch()
        try terminal()
        XCTAssertTrue(message.waitForExistence(timeout: 10))
        XCTAssertTrue(app.buttons.matching(identifier: "Add Attachment").firstMatch.waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["keybar.more"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["keybar.compose"].waitForExistence(timeout: 5))
        // The key bar's mic moved into the bar's bottom action row.
        let addBtn = app.buttons.matching(identifier: "Add Attachment").firstMatch
        let dictates = app.buttons.matching(identifier: "Dictate").allElementsBoundByIndex
        XCTAssertTrue(dictates.contains { abs($0.frame.midY - addBtn.frame.midY) < 20 })
        let grabber = app.otherElements["composer.grabber"]
        XCTAssertTrue(grabber.waitForExistence(timeout: 5))
        let bar = app.otherElements["composer"]
        if bar.exists {
            XCTAssertLessThanOrEqual(bar.frame.height, 120)
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

/// Regression tests for regular-width split view on iPhone (iPhone Duo
/// unfolded or large iPhone landscape) with Composer Mode active.
final class SplitViewComposerUITests: XCTestCase {
    func testSplitViewSearchDoesNotOverlapComposer() throws {
        continueAfterFailure = false
        addTeardownBlock {
            XCUIDevice.shared.orientation = .portrait
        }
        let app = XCUIApplication()
        app.launchArguments = ["-composerMode", "YES"]
        app.launch()

        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let notifAlert = springboard.alerts.firstMatch
        if notifAlert.waitForExistence(timeout: 3) {
            for b in ["Don’t Allow", "Don't Allow", "Allow"] where notifAlert.buttons[b].exists {
                notifAlert.buttons[b].tap()
                break
            }
        }

        let demo = app.buttons["Try Demo Mode"]
        if demo.waitForExistence(timeout: 15) {
            demo.tap()
        }
        XCUIDevice.shared.orientation = .landscapeLeft
        let row = app.buttons["Shell"]
        XCTAssertTrue(row.waitForExistence(timeout: 15))
        row.tap()

        let composer = app.otherElements["composer"]
        let sendBtn = app.buttons.matching(identifier: "Send").firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: 10))
        XCTAssertTrue(sendBtn.waitForExistence(timeout: 5))
        shot("split-sidebar-open")

        func assertNoSearchOverlap(_ context: String) {
            let searches = app.buttons.matching(NSPredicate(format: "label == 'Search' OR identifier == 'magnifyingglass'")).allElementsBoundByIndex
            for btn in searches where btn.exists && !btn.frame.isEmpty {
                XCTAssertFalse(btn.frame.intersects(composer.frame),
                               "Search button \(btn.frame) overlaps composer \(composer.frame) (\(context))")
                XCTAssertFalse(btn.frame.intersects(sendBtn.frame),
                               "Search button \(btn.frame) overlaps Send \(sendBtn.frame) (\(context))")
            }
        }

        assertNoSearchOverlap("sidebar open")

        let hideSidebar = app.buttons["Hide Sidebar"]
        if hideSidebar.waitForExistence(timeout: 3) {
            hideSidebar.tap()
            _ = hideSidebar.waitForNonExistence(timeout: 5)
            shot("split-sidebar-hidden")
            assertNoSearchOverlap("sidebar hidden")
        }
    }

    private func shot(_ name: String) {
        let s = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        s.name = name
        s.lifetime = .keepAlways
        add(s)
    }
}
