import CoreGraphics
import UIKit
import XCTest

/// End-to-end simulator UI tests for agent-driven session/pane highlight (#1284):
/// row badge in list, pane outline on multi-pane/iPad, Reduce Motion static
/// outline, VoiceOver accessibility labels, and immediate clear on user input.
final class AgentHighlightUITests: XCTestCase {
    override func setUpWithError() throws {
        continueAfterFailure = false
    }

    override func tearDownWithError() throws {
        XCUIDevice.shared.orientation = .portrait
    }

    private func launchDemo(
        scheme: String = "dark",
        theme: String = "Gruvbox Dark",
        reduceMotion: Bool = false,
        agentHighlight: Bool = true
    ) throws -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = [
            "-scheme", scheme,
            "-theme", theme,
            "-reduceMotion", reduceMotion ? "YES" : "NO",
            "-agentHighlight", agentHighlight ? "YES" : "NO",
            "-composerMode", "NO",
            "-spaces.chosen", "",
        ]
        addUIInterruptionMonitor(withDescription: "system alerts") { alert in
            for b in ["Don't Allow", "Don’t Allow", "Allow", "OK"] where alert.buttons[b].exists {
                alert.buttons[b].tap()
                return true
            }
            return false
        }
        app.launch()
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        func dismissSpringboardAlert() {
            let alert = springboard.alerts.firstMatch
            guard alert.exists || alert.waitForExistence(timeout: 2) else { return }
            for b in ["Don’t Allow", "Don't Allow", "Allow", "OK"] where alert.buttons[b].exists {
                alert.buttons[b].tap()
                return
            }
        }
        dismissSpringboardAlert()
        // Return from any remembered terminal or inbox left by an earlier test,
        // otherwise enter Demo Mode from the welcome screen.
        let hostReady = NSPredicate(format: "label == 'Shell' OR label == 'Demo Mac' OR label == 'alpha-laptop' OR label == 'Needs You' OR label == 'Try Demo Mode' OR identifier == 'BackButton' OR label == 'Show All Tabs'")
        _ = app.buttons.matching(hostReady).firstMatch.waitForExistence(timeout: 30)
        dismissSpringboardAlert()
        if app.buttons["alpha-laptop"].exists || app.staticTexts["alpha-laptop"].exists {
            throw XCTSkip("simulator is seeded with HostDragTests fixtures")
        }
        if app.buttons["BackButton"].exists {
            app.buttons["BackButton"].tap()
        }
        if app.buttons["Show All Tabs"].exists {
            app.buttons["Show All Tabs"].tap()
        }
        let demo = app.buttons["Try Demo Mode"]
        if demo.exists || (!app.buttons["Demo Mac"].exists && demo.waitForExistence(timeout: 25)) {
            dismissSpringboardAlert()
            demo.tap()
            if !app.buttons["Demo Mac"].waitForExistence(timeout: 5), demo.exists {
                dismissSpringboardAlert()
                demo.tap()
            }
        }
        XCTAssertTrue(app.buttons["Demo Mac"].waitForExistence(timeout: 20))
        return app
    }

    private func expandGoRexProject(_ app: XCUIApplication) {
        if app.buttons["BackButton"].exists {
            app.buttons["BackButton"].tap()
        } else if app.navigationBars.buttons["Back"].exists {
            app.navigationBars.buttons["Back"].tap()
        }
        let project = app.scrollViews.buttons.matching(NSPredicate(format: "label == 'GoRex'")).firstMatch
        XCTAssertTrue(project.waitForExistence(timeout: 15))
        let claudeRow = app.scrollViews.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Claude Code'")).firstMatch
        if !claudeRow.waitForExistence(timeout: 8) {
            project.tap()
        }
        XCTAssertTrue(claudeRow.waitForExistence(timeout: 10))
    }

    func testRowBadgeVoiceOverAndClearOnUserInput() throws {
        let isPad = UIDevice.current.userInterfaceIdiom == .pad
        if isPad {
            XCUIDevice.shared.orientation = .landscapeLeft
        } else {
            XCUIDevice.shared.orientation = .portrait
        }
        let prefix = isPad ? "ipad" : "iphone"

        let app = try launchDemo(scheme: "dark", theme: "Gruvbox Dark", reduceMotion: false, agentHighlight: true)
        expandGoRexProject(app)

        // 1. Agent-driven rows expose the VoiceOver agent label, while human rows do not.
        let claudeRow = app.scrollViews.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Claude Code'")).firstMatch
        let dashboardRow = app.scrollViews.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Dashboard'")).firstMatch
        let shellRow = app.scrollViews.buttons["Shell"]
        XCTAssertTrue(claudeRow.exists)
        XCTAssertTrue(dashboardRow.exists)
        XCTAssertTrue(shellRow.exists)
        XCTAssertTrue(claudeRow.label.contains("operated by an agent") || claudeRow.label.contains("由 agent 操作"),
                      "expected agent VoiceOver label on Claude Code row, got: \(claudeRow.label)")
        XCTAssertTrue(dashboardRow.label.contains("operated by an agent") || dashboardRow.label.contains("由 agent 操作"),
                      "expected agent VoiceOver label on Dashboard row, got: \(dashboardRow.label)")
        XCTAssertFalse(shellRow.label.contains("agent"),
                       "human Shell row must not carry agent VoiceOver label, got: \(shellRow.label)")

        saveShot("\(prefix)-dark-list-after")
        saveText("\(prefix)-voiceover-list-dump", app.debugDescription)

        // 2. Open the multi-pane Claude Code window (`[claude, logs, gpu]`).
        claudeRow.tap()
        let term = app.textViews.matching(identifier: "terminal").firstMatch
        XCTAssertTrue(term.waitForExistence(timeout: 15))
        let agentTermShown = app.textViews.matching(NSPredicate(format: "label CONTAINS 'operated by an agent' OR label CONTAINS '由 agent 操作'")).firstMatch.waitForExistence(timeout: 5)
        XCTAssertTrue(agentTermShown, "expected terminal VoiceOver label to mark agent ownership, got: \(term.label)")

        saveShot("\(prefix)-dark-pane-after")
        saveText("\(prefix)-voiceover-terminal-dump", app.debugDescription)

        // 3. Typing in the pane clears the agent highlight immediately.
        term.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.45)).tap()
        _ = app.keyboards.element.waitForExistence(timeout: 2)
        term.typeText("y")
        if !app.textViews.matching(NSPredicate(format: "identifier == 'terminal' AND NOT (label CONTAINS 'agent')")).firstMatch.waitForExistence(timeout: 2),
           app.buttons.matching(identifier: "Esc").firstMatch.exists {
            app.buttons.matching(identifier: "Esc").firstMatch.tap()
        }
        let clearedShown = app.textViews.matching(NSPredicate(format: "identifier == 'terminal' AND NOT (label CONTAINS 'agent')")).firstMatch.waitForExistence(timeout: 5)
        XCTAssertTrue(clearedShown, "expected user input to clear agent VoiceOver label immediately, got: \(term.label)")
        saveShot("\(prefix)-dark-pane-cleared")
    }

    func testReduceMotionDrawsStaticOutlineAndLightMode() throws {
        let isPad = UIDevice.current.userInterfaceIdiom == .pad
        if isPad {
            XCUIDevice.shared.orientation = .landscapeLeft
        } else {
            XCUIDevice.shared.orientation = .portrait
        }
        let prefix = isPad ? "ipad" : "iphone"

        // 1. Reduce Motion ON -> static blue outline (identical border pixels across two captures).
        let app = try launchDemo(scheme: "dark", theme: "Gruvbox Dark", reduceMotion: true, agentHighlight: true)
        expandGoRexProject(app)

        let claudeRow = app.scrollViews.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Claude Code'")).firstMatch
        XCTAssertTrue(claudeRow.waitForExistence(timeout: 10))
        claudeRow.tap()

        let term = app.textViews.matching(identifier: "terminal").firstMatch
        XCTAssertTrue(term.waitForExistence(timeout: 15))
        let reduceTermShown = app.textViews.matching(NSPredicate(format: "label CONTAINS 'operated by an agent' OR label CONTAINS '由 agent 操作'")).firstMatch.waitForExistence(timeout: 5)
        XCTAssertTrue(reduceTermShown)

        let shot1 = XCUIScreen.main.screenshot()
        saveScreenshot(shot1, name: "\(prefix)-reduce-motion-static-1")
        _ = app.navigationBars.firstMatch.exists
        _ = term.frame
        let shot2 = XCUIScreen.main.screenshot()
        saveScreenshot(shot2, name: "\(prefix)-reduce-motion-static-2")

        // Verify the blue outline is rendered near the right edge of the terminal
        // (accounting for side status-bar safe-area insets on foldable devices like
        // iPhone Duo) and stays pixel-identical under Reduce Motion.
        let hit1 = try XCTUnwrap(findRightOutlinePixel(in: shot1.image, termFrame: term.frame),
                                 "expected blue agent outline near right edge of \(term.frame)")
        let p2 = try XCTUnwrap(samplePixel(in: shot2.image, px: hit1.px, py: hit1.py))
        XCTAssertEqual(hit1.rgba, p2, "Reduce Motion outline must stay static without pulsing")

        // 2. Light mode list + pane verification.
        app.terminate()
        let lightApp = try launchDemo(scheme: "light", theme: "Gruvbox Light", reduceMotion: true, agentHighlight: true)
        expandGoRexProject(lightApp)
        saveShot("\(prefix)-light-list-after")
        lightApp.scrollViews.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Claude Code'")).firstMatch.tap()
        let lightTermShown = lightApp.textViews.matching(NSPredicate(format: "label CONTAINS 'operated by an agent' OR label CONTAINS '由 agent 操作'")).firstMatch.waitForExistence(timeout: 15)
        XCTAssertTrue(lightTermShown)
        saveShot("\(prefix)-light-pane-after")
    }

    private func normalizedImage(_ image: UIImage) -> UIImage {
        guard image.imageOrientation != .up else { return image }
        let format = UIGraphicsImageRendererFormat()
        format.scale = image.scale
        return UIGraphicsImageRenderer(size: image.size, format: format).image { _ in
            image.draw(in: CGRect(origin: .zero, size: image.size))
        }
    }

    private func findRightOutlinePixel(in rawImage: UIImage, termFrame: CGRect) -> (px: Int, py: Int, rgba: [UInt8])? {
        let image = normalizedImage(rawImage)
        guard let cg = image.cgImage, termFrame.width > 40, termFrame.height > 40 else { return nil }
        let scale = CGFloat(cg.width) / max(1, image.size.width)
        let py = min(max(0, Int((termFrame.midY * scale).rounded())), cg.height - 1)
        let maxPx = min(max(0, Int((termFrame.maxX * scale).rounded()) - 1), cg.width - 1)
        let minPx = max(0, Int(((termFrame.maxX - 96) * scale).rounded()))
        guard maxPx >= minPx else { return nil }
        for px in stride(from: maxPx, through: minPx, by: -1) {
            if let rgba = samplePixel(cg: cg, px: px, py: py),
               Int(rgba[2]) > Int(rgba[0]) + 35,
               Int(rgba[2]) > 120 {
                return (px, py, rgba)
            }
        }
        return nil
    }

    private func samplePixel(in rawImage: UIImage, px: Int, py: Int) -> [UInt8]? {
        let image = normalizedImage(rawImage)
        guard let cg = image.cgImage else { return nil }
        return samplePixel(cg: cg, px: px, py: py)
    }

    private func samplePixel(cg: CGImage, px: Int, py: Int) -> [UInt8]? {
        guard px >= 0, px < cg.width, py >= 0, py < cg.height else { return nil }
        var rgba = [UInt8](repeating: 0, count: 4)
        guard let ctx = CGContext(
            data: &rgba,
            width: 1,
            height: 1,
            bitsPerComponent: 8,
            bytesPerRow: 4,
            space: CGColorSpaceCreateDeviceRGB(),
            bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue
        ) else { return nil }
        ctx.translateBy(x: CGFloat(-px), y: CGFloat(-(cg.height - 1 - py)))
        ctx.draw(cg, in: CGRect(x: 0, y: 0, width: cg.width, height: cg.height))
        return rgba
    }

    private func saveShot(_ name: String) {
        saveScreenshot(XCUIScreen.main.screenshot(), name: name)
    }

    private func saveScreenshot(_ screenshot: XCUISscreenshotCompat, name: String) {
        let attachment = XCTAttachment(image: normalizedImage(screenshot.image))
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }

    private typealias XCUISscreenshotCompat = XCUIScreenshot

    private func saveText(_ name: String, _ text: String) {
        let attachment = XCTAttachment(string: text)
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
