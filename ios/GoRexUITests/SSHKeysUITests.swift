import XCTest

/// Settings > SSH Keys: generate an Ed25519 key, inspect its fingerprint and
/// OpenSSH public key in the detail view, and delete it (docs/IOS-026-ssh-keys.md).
final class SSHKeysUITests: XCTestCase {
    func testGenerateInspectAndDeleteSSHKey() throws {
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
        let demo = app.buttons["Try Demo Mode"]
        if demo.waitForExistence(timeout: 15) {
            demo.tap()
        }
        let settings = app.buttons["Settings"]
        XCTAssertTrue(settings.waitForExistence(timeout: 30))
        settings.tap()

        let sshKeysRow = app.staticTexts["SSH Keys"].firstMatch
        XCTAssertTrue(sshKeysRow.waitForExistence(timeout: 10))
        sshKeysRow.tap()

        let addButton = app.buttons["addSSHKey"]
        XCTAssertTrue(addButton.waitForExistence(timeout: 10))
        addButton.tap()

        let generateItem = app.buttons["Generate New Key"]
        XCTAssertTrue(generateItem.waitForExistence(timeout: 5))
        generateItem.tap()

        let nameField = app.textFields["sshKeyName"]
        XCTAssertTrue(nameField.waitForExistence(timeout: 5))
        nameField.tap()
        nameField.typeText("Work Laptop")

        let confirmGenerate = app.buttons["generateSSHKey"]
        XCTAssertTrue(confirmGenerate.waitForExistence(timeout: 5))
        confirmGenerate.tap()

        let keyRow = app.staticTexts["Work Laptop"].firstMatch
        XCTAssertTrue(keyRow.waitForExistence(timeout: 10))
        let listShot = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        listShot.name = "ssh-keys-list"
        listShot.lifetime = .keepAlways
        add(listShot)

        keyRow.tap()
        XCTAssertTrue(app.staticTexts["SHA256"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["OpenSSH Format"].waitForExistence(timeout: 5))
        let detailShot = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        detailShot.name = "ssh-key-detail"
        detailShot.lifetime = .keepAlways
        add(detailShot)

        let deleteBtn = app.buttons["Delete Key"]
        XCTAssertTrue(deleteBtn.waitForExistence(timeout: 5))
        deleteBtn.tap()
        let confirmDelete = app.alerts.buttons["Delete"]
        XCTAssertTrue(confirmDelete.waitForExistence(timeout: 5))
        confirmDelete.tap()
    }
}
