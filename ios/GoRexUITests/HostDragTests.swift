import XCTest

/// A host's header lifts and drags: let go over another host, the list
/// takes the drag's order (docs/IOS-021-host-drag.md). It needs three
/// hosts, which CI's fresh simulator has not — it skips there. On a
/// seeded simulator it runs; seed `hosts` in the app's defaults with
/// HostConfigs named alpha-laptop, bravo-desktop and charlie-server.
final class HostDragTests: XCTestCase {
    func testDragHostReorders() throws {
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
        if notifAlert.waitForExistence(timeout: 5) {
            for b in ["Don’t Allow", "Don't Allow", "Allow"] where notifAlert.buttons[b].exists {
                notifAlert.buttons[b].tap()
                break
            }
        }
        let first = host(app, "alpha-laptop")
        let third = host(app, "charlie-server")
        guard first.waitForExistence(timeout: 30), third.waitForExistence(timeout: 10) else {
            throw XCTSkip("needs a simulator seeded with the alpha/bravo/charlie hosts")
        }
        XCTAssertLessThan(first.frame.midY, third.frame.midY)
        // A long press on the header lifts the section; over the third
        // host it takes that place, and the drop saves the order. A slow
        // drag with a hold at the end gives the drop targets time to
        // take the drag before the lift lands.
        let from = first.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5))
        let to = third.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5))
        from.press(forDuration: 1.0, thenDragTo: to, withVelocity: .slow, thenHoldForDuration: 1.0)
        Thread.sleep(forTimeInterval: 2)
        XCTAssertGreaterThan(first.frame.midY, third.frame.midY)
    }

    private func host(_ app: XCUIApplication, _ name: String) -> XCUIElement {
        let b = app.buttons[name]
        return b.exists ? b : app.staticTexts[name]
    }
}
