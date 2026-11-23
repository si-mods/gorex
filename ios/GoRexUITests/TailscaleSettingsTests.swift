import XCTest

/// Settings › Tailscale's Device section shows while Built-in Tailscale
/// is off, so a fresh install already carries the suggested
/// <model>-rex-xxxx before the first registration.
final class TailscaleSettingsTests: XCTestCase {
    func testDeviceNameWhileOff() throws {
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
        let settings = app.buttons["Settings"]
        XCTAssertTrue(settings.waitForExistence(timeout: 30))
        settings.tap()
        let row = app.staticTexts["Tailscale"].firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 10))
        row.tap()
        // Built-in Tailscale is off on a fresh install; the Device
        // section and the suggested name are already there.
        XCTAssertTrue(app.staticTexts["Device Name"].waitForExistence(timeout: 10))
        let name = app.textFields.matching(NSPredicate(format: "value CONTAINS '-rex-'")).firstMatch
        XCTAssertTrue(name.waitForExistence(timeout: 10))
        let shot = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        shot.name = "tailscale-device-name-off"
        shot.lifetime = .keepAlways
        add(shot)
    }
}
