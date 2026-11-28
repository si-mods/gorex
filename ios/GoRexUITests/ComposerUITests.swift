import XCTest

/// The compose bar (docs/IOS-025-composer.md, docs/IOS-030-composer-centric-bar.md):
/// it is up only while Settings > Terminal's Composer Mode is on — here the
/// defaults argument or Compose toggle — pinned in the key bar's place on an
/// iPhone as a compact single-row pill (`+`, message field, mic, and Send on
/// one line). Demo Mode's host takes no uploads, so the test covers layout,
/// inline quick keys, swipe-down, and multi-line growth.
final class ComposerUITests: XCTestCase {
    private var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
        XCUIDevice.shared.orientation = .portrait
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

    /// Demo Mode's host, and a terminal of it: the key bar shows, or in
    /// Composer Mode the compose bar in its place (IOS-030).
    private func terminal() throws {
        let shown = app.descendants(matching: .any).matching(NSPredicate(
            format: "identifier == 'Esc' OR label == 'Esc' OR identifier == 'composer.message'")).firstMatch
        if shown.waitForExistence(timeout: 3) { return }
        if !app.buttons["Shell"].exists {
            let demo = app.buttons["Try Demo Mode"]
            guard demo.waitForExistence(timeout: 30) else {
                throw XCTSkip("needs a fresh simulator's empty host list")
            }
            demo.tap()
        }
        // The demo's loose tab, a window row under its own header.
        let row = app.buttons["Shell"]
        XCTAssertTrue(row.waitForExistence(timeout: 15))
        row.tap()
        XCTAssertTrue(shown.waitForExistence(timeout: 15))
    }

    /// Turns Composer Mode off where an earlier run left it on: the
    /// runner's simulator keeps the app's defaults, and Compose writes the
    /// mode there. A launch argument cannot set it, as the argument would
    /// also hide every write the test makes. With the keyboard down, a
    /// swipe down on the compose bar closes the mode, as a user does.
    private func composerModeOff() {
        guard message.exists else { return }
        let bar = app.otherElements["composer"]
        XCTAssertTrue(bar.waitForExistence(timeout: 5))
        func swipeDown() {
            let start = bar.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5))
            start.press(forDuration: 0.05, thenDragTo: start.withOffset(CGVector(dx: 0, dy: 90)))
        }
        // The first swipe lowers the keyboard where it is up, the next
        // closes the mode.
        if app.keyboards.element.exists {
            swipeDown()
            XCTAssertTrue(app.keyboards.element.waitForNonExistence(timeout: 5))
        }
        if message.exists {
            swipeDown()
        }
        XCTAssertTrue(message.waitForNonExistence(timeout: 5))
    }

    /// The compose bar's message field, which names the demo's block.
    private var message: XCUIElement { app.textViews["composer.message"] }

    /// Without the mode the bar stays away and the key bar shows ⌘, Compose,
    /// the continuous-scroll terminal keys, stick, Dictate, and Keyboard.
    /// Tapping Compose opens the single-row Composer pill (`[ +, field, Mic, Send ]`),
    /// `+` › `More Keys` unfurls the 7-key strip inline without changing height,
    /// and swiping down or choosing `Show Key Bar` closes Composer back to `KeyBar`.
    func testNoComposerByDefault() throws {
        composerModeOff()
        XCTAssertTrue(app.buttons.matching(identifier: "Esc").firstMatch.waitForExistence(timeout: 5))
        XCTAssertTrue(message.waitForNonExistence(timeout: 5))
        XCTAssertTrue(app.buttons.matching(identifier: "Dictate").firstMatch.waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["keybar.more"].waitForExistence(timeout: 5))
        let compose = app.buttons["keybar.compose"]
        XCTAssertTrue(compose.waitForExistence(timeout: 5))
        shot("keybar-rex-rest")

        // Tapping Compose opens the single-row 48 pt pill with +, message,
        // Dictate, and Send aligned on one horizontal line.
        compose.tap()
        XCTAssertTrue(message.waitForExistence(timeout: 5))
        let bar = app.otherElements["composer"]
        XCTAssertTrue(bar.waitForExistence(timeout: 5))
        XCTAssertLessThanOrEqual(bar.frame.height, 56)
        let addBtn = app.buttons.matching(identifier: "Add Attachment").firstMatch
        let sendBtn = app.buttons.matching(identifier: "Send").firstMatch
        XCTAssertTrue(addBtn.waitForExistence(timeout: 5))
        XCTAssertTrue(sendBtn.waitForExistence(timeout: 5))
        XCTAssertLessThan(abs(addBtn.frame.midY - message.frame.midY), 4)
        XCTAssertLessThan(abs(sendBtn.frame.midY - message.frame.midY), 4)
        XCTAssertLessThan(abs(addBtn.frame.midY - sendBtn.frame.midY), 4)
        let restRowY = sendBtn.frame.midY
        shot("composer-single-row-rest")

        // Tapping + -> More Keys unfurls Esc, Tab, Enter, Delete, Arrow Keys,
        // More Keys, and the collapse button full-width across the same single
        // row so every key meets Apple HIG's 44 pt minimum tap width.
        addBtn.tap()
        let toggleMenuBtn = app.buttons["composer.keys.toggle"].exists ? app.buttons["composer.keys.toggle"] : app.buttons["More Keys"]
        XCTAssertTrue(toggleMenuBtn.waitForExistence(timeout: 5))
        toggleMenuBtn.tap()
        let moreKeys = app.buttons["composer.keys"]
        let collapseBtn = app.buttons["composer.keys.collapse"]
        let deleteBtn = app.buttons["Delete"]
        XCTAssertTrue(moreKeys.waitForExistence(timeout: 5))
        XCTAssertTrue(collapseBtn.waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["Tab"].waitForExistence(timeout: 5))
        XCTAssertTrue(deleteBtn.waitForExistence(timeout: 5))
        XCTAssertGreaterThanOrEqual(deleteBtn.frame.width, 44)
        XCTAssertLessThan(abs(moreKeys.frame.midY - restRowY), 4)
        shot("composer-single-row-expanded")

        // Tapping collapse restores the single-row compose bar.
        collapseBtn.tap()
        XCTAssertTrue(addBtn.waitForExistence(timeout: 5))

        // Dragging down anywhere on the bar dismisses Composer Mode and restores
        // the traditional key bar below.
        let start = bar.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5))
        let end = start.withOffset(CGVector(dx: 0, dy: 90))
        start.press(forDuration: 0.05, thenDragTo: end)
        XCTAssertTrue(message.waitForNonExistence(timeout: 5))
        XCTAssertTrue(app.buttons["keybar.more"].waitForExistence(timeout: 5))
        shot("composer-mode-closed")

        // Tapping Compose reopens it; + -> Show Key Bar closes Composer directly.
        compose.tap()
        XCTAssertTrue(message.waitForExistence(timeout: 5))
        addBtn.tap()
        let showBar = app.buttons["Show Key Bar"]
        XCTAssertTrue(showBar.waitForExistence(timeout: 5))
        showBar.tap()
        XCTAssertTrue(message.waitForNonExistence(timeout: 5))
    }

    /// Composer Mode pins the single-row 48 pt pill with `+`, message field,
    /// mic, and Send on one line while hiding the separate phone key bar, and
    /// grows upward for multi-line text while keeping `+` and Send bottom-aligned.
    func testComposerModePinsBar() throws {
        app.terminate()
        app.launchArguments = ["-composerMode", "YES"]
        app.launch()
        try terminal()
        XCTAssertTrue(message.waitForExistence(timeout: 10))
        let addBtn = app.buttons.matching(identifier: "Add Attachment").firstMatch
        let sendBtn = app.buttons.matching(identifier: "Send").firstMatch
        XCTAssertTrue(addBtn.waitForExistence(timeout: 5))
        XCTAssertTrue(sendBtn.waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["keybar.more"].exists)
        XCTAssertFalse(app.buttons["keybar.compose"].exists)
        let dictates = app.buttons.matching(identifier: "Dictate").allElementsBoundByIndex
        XCTAssertTrue(dictates.contains { abs($0.frame.midY - addBtn.frame.midY) < 8 })
        let bar = app.otherElements["composer"]
        XCTAssertTrue(bar.waitForExistence(timeout: 5))
        XCTAssertLessThanOrEqual(bar.frame.height, 56)
        shot("composer-mode-rest")

        // Raising the keyboard keeps the key bar hidden under the compose bar,
        // stays single-row for a short line, reflows into the Grok-style
        // full-width top text field above `[ +, …, Mic, Send ]` once text has
        // multiple lines, never collapses height once scrollable past fieldMax,
        // and collapses back to the 48 pt single-row pill once cleared.
        message.tap()
        if app.keyboards.element.waitForExistence(timeout: 5) {
            message.typeText("abc" + XCUIKeyboardKey.delete.rawValue)
            XCTAssertEqual(message.value as? String, "ab")
            XCTAssertLessThanOrEqual(bar.frame.height, 56)
            XCTAssertLessThan(abs(addBtn.frame.midY - message.frame.midY), 4)

            message.typeText("\nline 2\nline 3: full-width multiline compose layout")
            XCTAssertGreaterThan(bar.frame.height, 96)
            // Full-width top message field spanning above both + (left) and Send (right).
            XCTAssertLessThanOrEqual(message.frame.minX, addBtn.frame.midX)
            XCTAssertGreaterThanOrEqual(message.frame.maxX, sendBtn.frame.midX)
            XCTAssertLessThanOrEqual(message.frame.maxY, addBtn.frame.minY + 2)
            XCTAssertLessThan(abs(addBtn.frame.midY - sendBtn.frame.midY), 4)
            shot("composer-multiline-dark")

            // Exceeding fieldMax (~7 lines) enables scrolling without collapsing
            // the message field's height back to a single line.
            message.typeText("\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9")
            XCTAssertGreaterThan(bar.frame.height, 180)
            XCTAssertGreaterThan(message.frame.height, 130)
            shot("composer-mode-scrollable")

            // Sending clears the draft and collapses back to the 48 pt single-row pill.
            sendBtn.tap()
            let collapsed = app.otherElements.matching(NSPredicate(
                format: "identifier == 'composer' AND frame.size.height <= 56")).firstMatch
            XCTAssertTrue(collapsed.waitForExistence(timeout: 5))
            shot("composer-collapsed-after-send")
        }
    }

    /// Verifies Composer Mode over a light terminal theme ("Apple System Colors Light")
    /// renders a crisp dark Send button with white arrow and full-width multi-line CJK text.
    func testLightThemeMultiline() throws {
        app.terminate()
        app.launchArguments = ["-composerMode", "YES", "-theme", "Apple System Colors Light"]
        app.launch()
        try terminal()
        let bar = app.otherElements["composer"]
        XCTAssertTrue(bar.waitForExistence(timeout: 10))
        shot("composer-light-rest")
        message.tap()
        if app.keyboards.element.waitForExistence(timeout: 5) {
            message.typeText("测试一下\n\n多行文字现在自动展开为全宽编辑区\n下方独立放置附件、麦克风与发送按钮")
            XCTAssertGreaterThan(bar.frame.height, 110)
            shot("composer-light-multiline-zh")
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
