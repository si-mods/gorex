import XCTest

/// Settings › Tailscale's Device section shows while Built-in Tailscale
/// is off, with the device's name already in it before the first
/// registration.
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
        // Built-in Tailscale is off; the Device section and the name are
        // already there.
        XCTAssertTrue(app.staticTexts["Device Name"].waitForExistence(timeout: 10))
        // Any valid device name, not only a <model>-rex-xxxx one: the
        // simulator keeps the app's defaults between runs, and makeName
        // keeps a name an earlier build made (rexgo-xxxx before #1027).
        // HostTests.testDeviceName covers the name a fresh install makes.
        // The Control Server field's placeholder, "Tailscale", does not
        // match.
        let name = app.textFields.matching(NSPredicate(format: "value MATCHES '[a-z0-9]+(-[a-z0-9]+)*'")).firstMatch
        XCTAssertTrue(name.waitForExistence(timeout: 10))
        let shot = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        shot.name = "tailscale-device-name-off"
        shot.lifetime = .keepAlways
        add(shot)
    }
}
