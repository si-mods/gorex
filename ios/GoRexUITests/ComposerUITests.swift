import Foundation
import Network
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
            let collapseDeadline = Date().addingTimeInterval(5)
            while bar.frame.height > 56, Date() < collapseDeadline {
                _ = bar.exists
            }
            XCTAssertLessThanOrEqual(bar.frame.height, 56)
            shot("composer-collapsed-after-send")
        }
    }

    /// Swiping left on the compose bar unfurls the inline quick-keys strip
    /// (`Esc`, `Tab`, `Enter`, `Delete`, `Arrow Keys`, `More Keys`, `Hide`)
    /// without opening `+`, and swiping right collapses it back and focuses
    /// the message field (including under Reduce Motion).
    func testSwipeSwitchesQuickKeys() throws {
        app.terminate()
        app.launchArguments = ["-scheme", "dark", "-theme", "Gruvbox Dark", "-composerMode", "YES"]
        app.launch()
        try terminal()

        let bar = app.otherElements["composer"]
        XCTAssertTrue(bar.waitForExistence(timeout: 10))
        XCTAssertTrue(message.waitForExistence(timeout: 5))
        let addBtn = app.buttons.matching(identifier: "Add Attachment").firstMatch
        let sendBtn = app.buttons.matching(identifier: "Send").firstMatch
        XCTAssertTrue(addBtn.waitForExistence(timeout: 5))
        XCTAssertTrue(sendBtn.waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["Tab"].exists)
        shot("composer-swipe-before-dark")

        // Left swipe on the bar expands quick keys inline.
        bar.swipeLeft()
        XCTAssertTrue(app.buttons["Esc"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["Tab"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["composer.keys.collapse"].waitForExistence(timeout: 5))
        shot("composer-swipe-left-dark")

        // Right swipe collapses quick keys and focuses the message field.
        bar.swipeRight()
        XCTAssertTrue(app.buttons["composer.keys.collapse"].waitForNonExistence(timeout: 5))
        XCTAssertTrue(message.waitForExistence(timeout: 5))
        XCTAssertTrue(addBtn.waitForExistence(timeout: 5))
        if app.keyboards.element.waitForExistence(timeout: 5) {
            message.typeText("ok")
            XCTAssertEqual(message.value as? String, "ok")
        }
        shot("composer-swipe-right-dark")

        // Also verify under Reduce Motion and light appearance.
        app.terminate()
        app.launchArguments = ["-scheme", "light", "-composerMode", "YES", "-reduceMotion", "YES", "-theme", "Apple System Colors Light"]
        app.launch()
        try terminal()
        XCTAssertTrue(bar.waitForExistence(timeout: 10))
        shot("composer-swipe-before-light")
        bar.swipeLeft()
        XCTAssertTrue(app.buttons["Esc"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["Tab"].waitForExistence(timeout: 5))
        shot("composer-swipe-left-light-reduce-motion")

        bar.swipeRight()
        XCTAssertTrue(app.buttons["composer.keys.collapse"].waitForNonExistence(timeout: 5))
        XCTAssertTrue(message.waitForExistence(timeout: 5))
        _ = app.keyboards.element.waitForExistence(timeout: 5)
        shot("composer-swipe-right-light-reduce-motion")
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

/// Regression tests for attachment thumbnails in Composer Mode: with two photos
/// attached and the software keyboard raised, each thumbnail's Remove (×) button
/// stays completely inside its thumbnail and composer bounds, meets the 44 × 44 pt
/// minimum hit target in dark and light themes, and removes its attachment on tap.
final class ComposerAttachmentUITests: XCTestCase {
    private var server: LocalUploadRex?
    private var testApp: XCUIApplication?
    private var testPort: UInt16 = 0

    override func tearDownWithError() throws {
        let app = testApp
        let port = testPort
        if let app {
            MainActor.assumeIsolated {
                Self.removeTestHostIfPresent(app, port: port)
                XCUIDevice.shared.orientation = .portrait
            }
        }
        testApp = nil
        testPort = 0
        server?.stop()
        server = nil
    }

    @MainActor
    func testAttachmentRemoveButtonsInsideThumbnailsAndHittable() throws {
        continueAfterFailure = false
        XCUIDevice.shared.orientation = .portrait
        let srv = try LocalUploadRex()
        server = srv
        let port = try srv.waitPort()
        testPort = port
        let app = XCUIApplication()
        testApp = app
        addUIInterruptionMonitor(withDescription: "system alerts") { alert in
            for b in ["Don't Allow", "Don’t Allow", "Allow", "OK"] where alert.buttons[b].exists {
                alert.buttons[b].tap()
                return true
            }
            return false
        }

        // 1. Dark appearance: pair with the local upload-capable fake server,
        // attach two photos, raise the keyboard, and capture screenshot.
        try openUploadTerminal(app: app, port: port, scheme: "dark", theme: "Gruvbox Dark", pairIfNeeded: true)
        try attachTwoPhotos(app: app)
        raiseKeyboardAndTypeDraft(app: app, text: "Check two attached photos")
        shot("composer-attachments-dark")

        let darkFailure = checkTwoAttachmentsGeometryAndTap(app: app, context: "dark")

        // 2. Light appearance: relaunch against the same local fake server,
        // attach two photos with keyboard up, capture screenshot, and verify.
        app.terminate()
        try openUploadTerminal(app: app, port: port, scheme: "light", theme: "Apple System Colors Light", pairIfNeeded: false)
        try attachTwoPhotos(app: app)
        raiseKeyboardAndTypeDraft(app: app, text: "Check light theme badges")
        shot("composer-attachments-light")

        if let darkFailure {
            XCTFail(darkFailure)
        }
        if let lightFailure = checkTwoAttachmentsGeometryAndTap(app: app, context: "light") {
            XCTFail(lightFailure)
        }
    }

    @MainActor
    private func raiseKeyboardAndTypeDraft(app: XCUIApplication, text: String) {
        let msg = app.textViews["composer.message"]
        let composer = app.otherElements["composer"]
        if !app.keyboards.keys.firstMatch.exists {
            msg.tap()
        }
        if app.keyboards.keys.firstMatch.waitForExistence(timeout: 5) {
            msg.typeText(text)
            let maxSettledY = app.frame.height - 200
            let settleDeadline = Date().addingTimeInterval(3)
            while Date() < settleDeadline {
                let kbMinY = app.keyboards.element.frame.minY
                if kbMinY > 0, kbMinY < maxSettledY, composer.frame.maxY <= kbMinY + 12 {
                    break
                }
                _ = composer.exists
            }
        }
    }

    @MainActor
    private func openUploadTerminal(
        app: XCUIApplication,
        port: UInt16,
        scheme: String,
        theme: String,
        pairIfNeeded: Bool
    ) throws {
        app.launchArguments = [
            "-composerMode", "YES",
            "-scheme", scheme,
            "-theme", theme,
            "-spaces.chosen", "",
            "-pair", "http://127.0.0.1:\(port)",
        ]
        app.launch()
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let notifAlert = springboard.alerts.firstMatch
        if notifAlert.waitForExistence(timeout: 3) {
            for b in ["Don’t Allow", "Don't Allow", "Allow"] where notifAlert.buttons[b].exists {
                notifAlert.buttons[b].tap()
                break
            }
        }
        if app.buttons["alpha-laptop"].exists || app.staticTexts["alpha-laptop"].exists {
            throw XCTSkip("simulator is seeded with HostDragTests fixtures")
        }
        if app.buttons["Back"].exists {
            app.buttons["Back"].tap()
        } else if app.buttons["Show Sidebar"].exists {
            app.buttons["Show Sidebar"].tap()
        } else if app.buttons["BackButton"].exists {
            app.buttons["BackButton"].tap()
        } else if app.navigationBars.buttons["Back"].exists {
            app.navigationBars.buttons["Back"].tap()
        }
        let row = app.buttons.matching(NSPredicate(format: "label == 'Upload Shell' OR identifier == 'Upload Shell'")).firstMatch
        if !row.waitForExistence(timeout: 4) {
            if app.buttons["Show Sidebar"].exists {
                app.buttons["Show Sidebar"].tap()
            }
        }
        if pairIfNeeded, !row.exists {
            if app.buttons["Add a Host"].waitForExistence(timeout: 5) {
                app.buttons["Add a Host"].tap()
            } else {
                let settingsBtn = app.buttons["Settings"]
                XCTAssertTrue(settingsBtn.waitForExistence(timeout: 10))
                settingsBtn.tap()
            }
            let remoteHosts = app.buttons["Remote Hosts"].exists ? app.buttons["Remote Hosts"] : app.staticTexts["Remote Hosts"]
            XCTAssertTrue(remoteHosts.waitForExistence(timeout: 10))
            remoteHosts.tap()
            let addHost = app.buttons["Add Host"]
            XCTAssertTrue(addHost.waitForExistence(timeout: 10))
            addHost.tap()
            let qr = app.buttons["Pair via QR Code"]
            XCTAssertTrue(qr.waitForExistence(timeout: 10))
            qr.tap()
            let backBtn = app.buttons["BackButton"]
            if backBtn.waitForExistence(timeout: 10) {
                backBtn.tap()
            } else if app.navigationBars["Remote Hosts"].buttons["Settings"].exists {
                app.navigationBars["Remote Hosts"].buttons["Settings"].tap()
            }
            let close = app.buttons["Close"]
            if close.waitForExistence(timeout: 10) {
                close.tap()
            } else if app.navigationBars.buttons["Close"].exists {
                app.navigationBars.buttons["Close"].tap()
            }
        }
        XCTAssertTrue(row.waitForExistence(timeout: 15))
        row.tap()
        XCTAssertTrue(app.textViews["composer.message"].waitForExistence(timeout: 15))
    }

    @MainActor
    private func attachTwoPhotos(app: XCUIApplication) throws {
        let addBtn = app.buttons.matching(identifier: "Add Attachment").firstMatch
        XCTAssertTrue(addBtn.waitForExistence(timeout: 5))
        addBtn.tap()
        let photoAction = app.buttons["Photo"]
        XCTAssertTrue(photoAction.waitForExistence(timeout: 5))
        photoAction.tap()

        let nav = app.navigationBars["Add Photo"]
        XCTAssertTrue(nav.waitForExistence(timeout: 15))
        let confirmAdd = nav.buttons["Add"].waitForExistence(timeout: 3) ? nav.buttons["Add"] : app.buttons["Add"]
        XCTAssertTrue(confirmAdd.waitForExistence(timeout: 5))

        // Select the first two visible photos in Row 1 of PHPickerViewController's
        // sheet, bounded strictly within the sheet's horizontal and vertical frame.
        let navFrame = nav.frame
        let minX = navFrame.minX - 8
        let maxX = navFrame.maxX + 8
        let minY = navFrame.maxY + 8
        let maxY = app.frame.height - 16
        let picker = XCUIApplication(bundleIdentifier: "com.apple.mobileslideshow.photospicker")
        var chosenFrames: [CGRect] = []
        let gridDeadline = Date().addingTimeInterval(6)
        while Date() < gridDeadline, chosenFrames.count < 2 {
            let localFrames = app.images.allElementsBoundByIndex.compactMap { img -> CGRect? in
                guard img.exists else { return nil }
                let f = img.frame
                return (f.minX >= minX && f.maxX <= maxX && f.minY >= minY && f.maxY <= maxY && f.width >= 40 && f.height >= 40) ? f : nil
            }
            if localFrames.count >= 2 {
                chosenFrames = Array(localFrames.prefix(2))
                break
            }
            let remoteFrames = picker.images.allElementsBoundByIndex.compactMap { img -> CGRect? in
                guard img.exists else { return nil }
                let f = img.frame
                return (f.minX >= minX && f.maxX <= maxX && f.minY >= minY && f.maxY <= maxY && f.width >= 40 && f.height >= 40) ? f : nil
            }
            if remoteFrames.count >= 2 {
                chosenFrames = Array(remoteFrames.prefix(2))
                break
            }
        }
        let origin = app.coordinate(withNormalizedOffset: .zero)
        for rect in chosenFrames {
            origin.withOffset(CGVector(dx: rect.midX, dy: rect.midY)).tap()
        }
        if !confirmAdd.isEnabled {
            let rowY = min(app.frame.height - 40, navFrame.maxY + 64)
            origin.withOffset(CGVector(dx: navFrame.minX + navFrame.width * 0.2, dy: rowY)).tap()
            origin.withOffset(CGVector(dx: navFrame.minX + navFrame.width * 0.5, dy: rowY)).tap()
        }
        let deadline = Date().addingTimeInterval(5)
        while !confirmAdd.isEnabled, Date() < deadline {
            _ = confirmAdd.exists
        }
        guard confirmAdd.isEnabled else {
            throw XCTSkip("simulator photo library contains no selectable photos")
        }
        confirmAdd.tap()
        XCTAssertTrue(nav.waitForNonExistence(timeout: 10))

        let thumbs = app.otherElements.matching(identifier: "composer.attachment")
        let stageDeadline = Date().addingTimeInterval(10)
        while thumbs.count < 2, Date() < stageDeadline {
            _ = thumbs.firstMatch.exists
        }
        XCTAssertEqual(thumbs.count, 2, "expected 2 staged photo thumbnails")
    }

    @MainActor
    private func checkTwoAttachmentsGeometryAndTap(app: XCUIApplication, context: String) -> String? {
        let composer = app.otherElements["composer"]
        let thumbs = app.otherElements.matching(identifier: "composer.attachment").allElementsBoundByIndex
        let removes = app.buttons.matching(identifier: "composer.attachment.remove").allElementsBoundByIndex
        guard thumbs.count == 2, removes.count == 2 else {
            return "[\(context)] expected 2 thumbnails and 2 remove buttons, got thumbs=\(thumbs.count) removes=\(removes.count)"
        }
        let composerFrame = composer.frame
        let pairs = zip(
            thumbs.sorted { $0.frame.minX < $1.frame.minX },
            removes.sorted { $0.frame.minX < $1.frame.minX }
        )
        for (idx, pair) in pairs.enumerated() {
            let thumbFrame = pair.0.frame
            let removeFrame = pair.1.frame
            let allowedThumb = thumbFrame.insetBy(dx: -1, dy: -1)
            if !allowedThumb.contains(removeFrame) {
                return "[\(context)] remove[\(idx)] frame \(removeFrame) protrudes outside thumbnail frame \(thumbFrame)"
            }
            if !composerFrame.insetBy(dx: -1, dy: -1).contains(removeFrame) {
                return "[\(context)] remove[\(idx)] frame \(removeFrame) protrudes outside composer frame \(composerFrame)"
            }
            if removeFrame.width < 44 || removeFrame.height < 44 {
                return "[\(context)] remove[\(idx)] frame \(removeFrame) is smaller than 44x44 pt minimum hit target"
            }
            if !pair.1.isHittable {
                return "[\(context)] remove[\(idx)] is not hittable"
            }
        }
        // Tap near the top-right badge of the first thumbnail's remove button and verify removal.
        let firstRemove = removes.sorted { $0.frame.minX < $1.frame.minX }[0]
        firstRemove.coordinate(withNormalizedOffset: CGVector(dx: 0.78, dy: 0.22)).tap()
        let remaining = app.otherElements.matching(identifier: "composer.attachment")
        let deadline = Date().addingTimeInterval(5)
        while remaining.count != 1, Date() < deadline {
            _ = remaining.firstMatch.exists
        }
        if remaining.count != 1 {
            return "[\(context)] tapping first remove badge left \(remaining.count) attachments instead of 1"
        }
        shot("composer-attachments-\(context)-after-remove")
        return nil
    }

    @MainActor
    private static func removeTestHostIfPresent(_ app: XCUIApplication, port: UInt16) {
        guard app.state == .runningForeground, port != 0 else { return }
        if app.navigationBars["Add Photo"].buttons["Close"].exists {
            app.navigationBars["Add Photo"].buttons["Close"].tap()
        }
        // If Remote Hosts is still open from an interrupted run, delete only this port's cell.
        if app.navigationBars["Remote Hosts"].exists {
            let cell = app.collectionViews.cells.containing(.staticText, identifier: "http://127.0.0.1:\(port)").firstMatch
            if cell.exists, cell.buttons["Edit Host"].exists {
                cell.buttons["Edit Host"].tap()
                if app.collectionViews.buttons["Delete Host"].waitForExistence(timeout: 3) {
                    app.collectionViews.buttons["Delete Host"].tap()
                    let confirm = app.sheets.buttons["Delete Host"].exists
                        ? app.sheets.buttons["Delete Host"]
                        : app.buttons.matching(identifier: "Delete Host").allElementsBoundByIndex.last
                    if let confirm, confirm.waitForExistence(timeout: 3) {
                        confirm.tap()
                    }
                }
            }
            if app.buttons["BackButton"].exists {
                app.buttons["BackButton"].tap()
            }
            if app.buttons["Close"].waitForExistence(timeout: 3) {
                app.buttons["Close"].tap()
            }
            return
        }
        if app.buttons["Close"].exists {
            app.buttons["Close"].tap()
        }
        if app.buttons["Back"].waitForExistence(timeout: 2) {
            app.buttons["Back"].tap()
        } else if app.buttons["Show Sidebar"].exists {
            app.buttons["Show Sidebar"].tap()
        } else if app.buttons["BackButton"].exists {
            app.buttons["BackButton"].tap()
        }
        let ipQuery = app.buttons.matching(NSPredicate(format: "label == '127.0.0.1' OR identifier == '127.0.0.1'"))
        for _ in 0..<4 {
            let ipLabel = ipQuery.firstMatch
            guard ipLabel.waitForExistence(timeout: 3) else { break }
            let targetY = ipLabel.frame.midY
            let beforeCount = ipQuery.count
            let matchingMenu = app.buttons.matching(identifier: "Host Menu").allElementsBoundByIndex.first { btn in
                btn.exists && abs(btn.frame.midY - targetY) < 28
            } ?? app.buttons.matching(NSPredicate(format: "label == 'Host Menu'")).allElementsBoundByIndex.first { btn in
                btn.exists && abs(btn.frame.midY - targetY) < 28
            }
            guard let matchingMenu else { break }
            matchingMenu.tap()
            let remove = app.buttons["Remove"].firstMatch
            guard remove.waitForExistence(timeout: 3) else { break }
            remove.tap()
            let waitDeadline = Date().addingTimeInterval(3)
            while ipQuery.count >= beforeCount, Date() < waitDeadline {
                _ = ipQuery.firstMatch.exists
            }
        }
    }

    @MainActor
    private func shot(_ name: String) {
        let s = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        s.name = name
        s.lifetime = .keepAlways
        add(s)
    }
}

/// Minimal localhost WebSocket Rex server (`api_revision: 11`) so XCUITests can
/// open a live upload-capable terminal without external hosts or secrets.
private final class LocalUploadRex: @unchecked Sendable {
    private let queue = DispatchQueue(label: "test.local-upload-rex")
    private let ready = DispatchSemaphore(value: 0)
    private var signaled = false
    private let listener: NWListener
    private var connections: [NWConnection] = []
    private static let welcome = Data([0x81] + [UInt8](repeating: 7, count: 19) + [0x60, 0xEA, 0, 0] + [1, 0, 0, 0, 0, 0, 0, 0])

    init() throws {
        let ws = NWProtocolWebSocket.Options()
        ws.autoReplyPing = true
        ws.maximumMessageSize = 1 << 20
        ws.setClientRequestHandler(queue) { protocols, _ in
            NWProtocolWebSocket.Response(status: .accept, subprotocol: protocols.first)
        }
        let params = NWParameters.tcp
        params.defaultProtocolStack.applicationProtocols.insert(ws, at: 0)
        listener = try NWListener(using: params, on: .any)
        listener.stateUpdateHandler = { [weak self] state in
            guard let self, !self.signaled else { return }
            switch state {
            case .ready, .failed, .cancelled:
                self.signaled = true
                self.ready.signal()
            default:
                break
            }
        }
        listener.newConnectionHandler = { [weak self] c in
            self?.accept(c)
        }
        listener.start(queue: queue)
    }

    func waitPort() throws -> UInt16 {
        guard ready.wait(timeout: .now() + 5) == .success,
              let p = queue.sync(execute: { listener.port?.rawValue }),
              p != 0 else {
            throw URLError(.cannotConnectToHost)
        }
        return p
    }

    func stop() {
        queue.sync {
            listener.cancel()
            connections.forEach { $0.cancel() }
            connections.removeAll()
        }
    }

    private func accept(_ c: NWConnection) {
        connections.append(c)
        c.start(queue: queue)
        receive(c, first: true)
    }

    private func receive(_ c: NWConnection, first: Bool) {
        c.receiveMessage { [weak self] content, _, _, error in
            guard let self, error == nil, let content else { return }
            if first, content.first == 0x01 || content.first == 0x02 {
                Self.sendBinary(c, Self.welcome)
                Self.sendBinary(c, Data("alice@laptop:~ $ ".utf8))
                return
            }
            self.handleControl(c, content)
            self.receive(c, first: false)
        }
    }

    private func handleControl(_ c: NWConnection, _ d: Data) {
        guard let m = try? JSONSerialization.jsonObject(with: d) as? [String: Any] else { return }
        switch m["type"] as? String {
        case "hello":
            Self.sendJSON(c, [
                "type": "welcome",
                "protocol": "rex.control.v1",
                "api_revision": 11,
                "client": ["id": "c1", "reconnect_token": "t1"],
                "features": [],
            ])
        case "request":
            let id = m["id"] ?? ""
            let method = m["method"] as? String ?? ""
            let view: [String: Any] = [
                "revision": 1,
                "session_id": "s1",
                "label": "",
                "windows": [[
                    "window_id": "w1",
                    "focused_block_id": "b1",
                    "layers": [[
                        "blocks": [["block_id": "b1"]],
                    ]],
                ]],
            ]
            let payload: [String: Any]
            switch method {
            case "session.bootstrap":
                payload = [
                    "attach_all": ["sessions": [["session_id": "s1", "label": ""]]],
                    "selected_session": ["view": view],
                ]
            case "session.view":
                payload = view
            case "com.superlogical.terminal.title":
                payload = ["title": "Upload Shell"]
            case "data.connect", "com.superlogical.terminal.upload_begin":
                payload = ["ticket": "uH4mJKwzMarlTNT-UiyMog"]
            default:
                payload = [:]
            }
            Self.sendJSON(c, ["type": "response", "id": id, "seq": 1, "payload": payload])
        default:
            break
        }
    }

    private static func sendJSON(_ c: NWConnection, _ obj: [String: Any]) {
        guard let data = try? JSONSerialization.data(withJSONObject: obj) else { return }
        let meta = NWProtocolWebSocket.Metadata(opcode: .text)
        c.send(content: data, contentContext: .init(identifier: "json", metadata: [meta]), isComplete: true, completion: .idempotent)
    }

    private static func sendBinary(_ c: NWConnection, _ data: Data) {
        let meta = NWProtocolWebSocket.Metadata(opcode: .binary)
        c.send(content: data, contentContext: .init(identifier: "bin", metadata: [meta]), isComplete: true, completion: .idempotent)
    }
}
