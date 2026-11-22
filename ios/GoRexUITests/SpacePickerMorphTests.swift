import XCTest

/// Drives the space picker for the screen recording in the PR: open the
/// panel, pick Life, open again and tap beside it to close.
final class SpacePickerMorphTests: XCTestCase {
    func testSpacePanelMorph() throws {
        let app = XCUIApplication()
        app.launch()
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
