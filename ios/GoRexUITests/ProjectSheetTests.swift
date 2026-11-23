import XCTest

/// New Project's Space row picks the space the project is made in: it
/// opens the spaces, and the choice shows on the row (the bug it fixes
/// left the row a label that no tap answered).
final class ProjectSheetTests: XCTestCase {
    func testSpacePicker() throws {
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
        let make = app.buttons["New Project"]
        XCTAssertTrue(make.waitForExistence(timeout: 30))
        make.tap()
        // The row is a picker: a tap opens the spaces, where Life is the
        // one of the demo's dotfiles.
        let space = app.buttons["projectSpace"]
        XCTAssertTrue(space.waitForExistence(timeout: 10))
        space.tap()
        let life = app.buttons["Life"]
        XCTAssertTrue(life.waitForExistence(timeout: 10))
        let shot = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        shot.name = "project-sheet-spaces"
        shot.lifetime = .keepAlways
        add(shot)
        life.tap()
        // The choice shows on the row.
        XCTAssertTrue(app.staticTexts["Life"].waitForExistence(timeout: 10))
    }
}
