import XCTest

/// Drives the space picker for the screen recording in the PR: open the
/// panel, pick Life, open again and tap beside it to close.
final class SpacePickerMorphTests: XCTestCase {
    func testSpacePanelMorph() throws {
        let app = XCUIApplication()
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
        let demo = app.buttons["Try Demo Mode"]
        XCTAssertTrue(demo.waitForExistence(timeout: 30))
        demo.tap()
        let capsule = app.buttons["Space"]
        XCTAssertTrue(capsule.waitForExistence(timeout: 30))
        sleep(3)
        capsule.tap()
        XCTAssertTrue(app.staticTexts["Spaces"].waitForExistence(timeout: 10))
        app.buttons["Life, 1 project"].tap()
        XCTAssertTrue(app.staticTexts["Spaces"].waitForNonExistence(timeout: 10))
        capsule.tap()
        XCTAssertTrue(app.staticTexts["Spaces"].waitForExistence(timeout: 10))
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.9)).tap()
        XCTAssertTrue(app.staticTexts["Spaces"].waitForNonExistence(timeout: 10))
    }
}
