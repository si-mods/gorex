import XCTest

/// Verifies the Needs You inbox toolbar badge and sectioned hierarchy in Demo Mode.
final class InboxUITests: XCTestCase {
    func testInboxBadgeAndHierarchy() throws {
        let app = XCUIApplication()
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
        if notifAlert.waitForExistence(timeout: 3) {
            for b in ["Don’t Allow", "Don't Allow", "Allow"] where notifAlert.buttons[b].exists {
                notifAlert.buttons[b].tap()
                break
            }
        }

        let demo = app.buttons["Try Demo Mode"]
        if demo.waitForExistence(timeout: 8) {
            demo.tap()
        }

        let inboxButton = app.buttons["Needs You"]
        XCTAssertTrue(inboxButton.waitForExistence(timeout: 20))
        if let val = inboxButton.value as? String, !val.isEmpty {
            XCTAssertTrue(val.contains("2"), "expected 2 actionable items in Demo Mode, got \(val)")
        }
        saveShot("list")

        inboxButton.tap()
        XCTAssertTrue(app.staticTexts["Needs Input"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["Done"].exists)
        XCTAssertTrue(app.staticTexts["Working"].exists)
        saveShot("inbox")

        let allTabs = app.buttons["Show All Tabs"]
        XCTAssertTrue(allTabs.waitForExistence(timeout: 10))
        allTabs.tap()
        XCTAssertTrue(app.buttons["Needs You"].waitForExistence(timeout: 10))
    }

    private func saveShot(_ name: String) {
        guard let dir = ProcessInfo.processInfo.environment["GOREX_SHOT_DIR"], !dir.isEmpty else { return }
        let url = URL(fileURLWithPath: dir).appendingPathComponent("\(name).png")
        try? XCUIScreen.main.screenshot().pngRepresentation.write(to: url)
    }
}
