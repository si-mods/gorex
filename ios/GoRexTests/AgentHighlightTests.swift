import Foundation
import SwiftUI
import XCTest

/// Unit tests for iOS agent-driven session/pane highlight (#1284, IOS-035):
/// `agentActor` decoding, 60 s window and 3 s fade-out, immediate clear on
/// human input, query/focus/wheel byte filtering, Reduce Motion and scenePhase
/// battery gating, OKLCH same-hue color contrast, and non-spamming VoiceOver.
nonisolated final class AgentHighlightTests: XCTestCase {
    @MainActor func testAgentActorDecodingSupports1281And1284Shapes() throws {
        // 1. Minimal #1284 shape: {createdBy, lastCliInputAt} with no explicit `active`.
        let minimal = try XCTUnwrap(AgentActor(json: [
            "createdBy": "cli",
            "lastCliInputAt": "2026-11-28T08:30:12.401Z",
        ]))
        XCTAssertTrue(minimal.active)
        XCTAssertEqual(minimal.createdBy, "cli")
        XCTAssertNotNil(minimal.lastCliInputAt)

        // 2. Full #1281 active shape.
        let full = try XCTUnwrap(AgentActor(json: [
            "createdBy": "cli",
            "lastCliInputAt": "2026-11-28T08:30:12.401234Z",
            "agent": "agy",
            "active": true,
            "parentSession": "010203040506",
        ]))
        XCTAssertTrue(full.active)
        XCTAssertEqual(full.agent, "agy")
        XCTAssertEqual(full.parentSession, "010203040506")

        // 3. #1281 human-takeover shape: Go omits `active: false` (`omitempty`) and sets `tookOverAt`.
        let tookOver = try XCTUnwrap(AgentActor(json: [
            "createdBy": "cli",
            "lastCliInputAt": "2026-11-28T08:30:12.401Z",
            "agent": "agy",
            "tookOverAt": "2026-11-28T08:30:19.105Z",
        ]))
        XCTAssertFalse(tookOver.active)
        XCTAssertNotNil(tookOver.tookOverAt)

        // 4. Plain human session with Go zero time: inactive.
        let human = try XCTUnwrap(AgentActor(json: [
            "createdBy": "human",
            "lastCliInputAt": "0001-01-01T00:00:00Z",
        ]))
        XCTAssertFalse(human.active)
        XCTAssertNil(human.lastCliInputAt)

        // 5. SessionView.Block JSON decoding.
        let viewData = """
        {
          "payload": {
            "revision": 2,
            "session_id": "session:one",
            "windows": [{
              "window_id": "window:one",
              "focused_block_id": "block:a",
              "layers": [{
                "blocks": [{
                  "block_id": "block:a",
                  "label": "build",
                  "agentActor": {
                    "createdBy": "cli",
                    "lastCliInputAt": "2026-11-28T08:30:00Z",
                    "agent": "codex"
                  }
                }]
              }]
            }]
          }
        }
        """.data(using: .utf8)!
        let view = try SessionView(response: viewData)
        let decoded = try XCTUnwrap(view.windows?.first?.layers?.first?.blocks?.first?.agentActor)
        XCTAssertTrue(decoded.active)
        XCTAssertEqual(decoded.agent, "codex")
    }

    @MainActor func testSixtySecondWindowAndThreeSecondFadeOut() throws {
        let t0 = Date(timeIntervalSince1970: 1_760_000_000)
        let actor = AgentActor(createdBy: "cli", lastCliInputAt: t0, agent: "agy", active: true)

        // Fresh at t0: full opacity, 60 s remaining, compact "agent" row badge.
        let s0 = try XCTUnwrap(AgentHighlight.state(actor: actor, now: t0))
        XCTAssertEqual(s0.badgeText, "agent")
        XCTAssertEqual(s0.agentName, "agy")
        XCTAssertEqual(s0.fade, 1.0, accuracy: 0.0001)
        XCTAssertEqual(s0.remaining, 60.0, accuracy: 0.0001)

        // At 56.9 s: still full opacity before the 3 s fade window.
        let s56 = try XCTUnwrap(AgentHighlight.state(actor: actor, now: t0.addingTimeInterval(56.9)))
        XCTAssertEqual(s56.fade, 1.0, accuracy: 0.0001)

        // At 58.5 s (1.5 s remaining of 3 s fade): half faded.
        let s585 = try XCTUnwrap(AgentHighlight.state(actor: actor, now: t0.addingTimeInterval(58.5)))
        XCTAssertEqual(s585.fade, 0.5, accuracy: 0.001)
        XCTAssertEqual(s585.remaining, 1.5, accuracy: 0.001)

        // At 60.0 s and beyond: expired -> nil.
        XCTAssertNil(AgentHighlight.state(actor: actor, now: t0.addingTimeInterval(60.0)))
        XCTAssertNil(AgentHighlight.state(actor: actor, now: t0.addingTimeInterval(90.0)))

        // Exited or disabled -> nil immediately even inside 60 s.
        XCTAssertNil(AgentHighlight.state(actor: actor, exited: true, now: t0.addingTimeInterval(5)))
        XCTAssertNil(AgentHighlight.state(actor: actor, enabled: false, now: t0.addingTimeInterval(5)))
    }

    @MainActor func testUserInputClearsImmediatelyAndNewerCLIInputRearms() throws {
        let h = Host(Demo.config())
        h.connect()
        defer { h.stop() }

        let t0 = Date.now
        let b = try XCTUnwrap(h.blocks[Demo.dashboard])
        b.applyAgentActor(AgentActor(createdBy: "cli", lastCliInputAt: t0, agent: "agy", active: true))
        XCTAssertNotNil(h.agentState(for: Demo.dashboard, at: t0.addingTimeInterval(1)))

        // Terminal query replies, focus reports, and scroll-wheel motion do NOT clear.
        let session = TerminalSession(host: h, session: "s-work", block: Demo.dashboard)
        defer { session.close() }
        session.start()
        session.type(Data("\u{1B}[I\u{1B}[?62;22c\u{1B}[12;34R\u{1B}[<64;10;20M".utf8))
        XCTAssertNotNil(h.agentState(for: Demo.dashboard, at: t0.addingTimeInterval(1)),
                        "terminal replies, focus reports, and wheel events must not clear agent state")

        // Real user typing clears on the same turn without waiting for a server round-trip.
        session.type(Data("a".utf8))
        XCTAssertNil(h.agentState(for: Demo.dashboard, at: t0.addingTimeInterval(1)),
                     "user input in the pane must clear the agent highlight immediately")

        // Stale poll carrying the old lastCliInputAt stays suppressed.
        b.applyAgentActor(AgentActor(createdBy: "cli", lastCliInputAt: t0, agent: "agy", active: true))
        XCTAssertNil(h.agentState(for: Demo.dashboard, at: t0.addingTimeInterval(2)))

        // Newer CLI input (`lastCliInputAt > clearedAt`) re-arms the highlight.
        let t1 = t0.addingTimeInterval(5)
        b.applyAgentActor(AgentActor(createdBy: "cli", lastCliInputAt: t1, agent: "agy", active: true))
        XCTAssertNotNil(h.agentState(for: Demo.dashboard, at: t1.addingTimeInterval(1)))
    }

    @MainActor func testHostReceivesAgentActorOverRexProtocolEvents() async throws {
        let server = try FakeRex()
        defer { server.stop() }
        let iso = Date.now.ISO8601Format()
        server.respond = { method, payload in
            switch method {
            case "session.bootstrap":
                return [
                    "attach_all": ["sessions": [["session_id": "s1", "label": "Work"]]],
                    "selected_session": [
                        "view": [
                            "revision": 1,
                            "session_id": "s1",
                            "label": "Work",
                            "windows": [[
                                "window_id": "w1",
                                "focused_block_id": "b1",
                                "layers": [["blocks": [["block_id": "b1"]]]],
                            ]],
                        ],
                    ],
                ]
            case "com.superlogical.terminal.title":
                return ["title": "Agent Task"]
            case "com.superlogical.terminal.process":
                return ["last_exit": NSNull(), "foreground": ["name": "zsh", "pid": 42]]
            case "com.superlogical.terminal.program_status":
                return [
                    "records": [],
                    "agentActor": [
                        "createdBy": "cli",
                        "lastCliInputAt": iso,
                        "agent": "agy",
                        "active": true,
                    ],
                ]
            default:
                _ = payload
                return [:]
            }
        }

        let host = Host(HostConfig(name: "test-mac", url: try await server.url()))
        host.connect()
        defer { host.stop() }

        await until("block agentActor loaded") {
            host.agentState(for: "b1") != nil
        }
        let w = try XCTUnwrap(host.sessions.first?.view?.windows?.first)
        XCTAssertEqual(host.windowAgentState(w)?.badgeText, "agent")
        XCTAssertEqual(host.windowAgentState(w)?.agentName, "agy")

        // Server pushes human takeover via program_status_changed (`tookOverAt` set, `active` omitted).
        server.event([
            "type": "session_event",
            "payload": [
                "session_id": "s1",
                "event": [
                    "type": "block_event",
                    "payload": [
                        "block_id": "b1",
                        "name": "program_status_changed",
                        "payload": [
                            "records": [],
                            "agentActor": [
                                "createdBy": "cli",
                                "lastCliInputAt": iso,
                                "tookOverAt": Date.now.ISO8601Format(),
                            ],
                        ],
                    ],
                ],
            ],
        ])
        await until("takeover event clears highlight") {
            host.agentState(for: "b1") == nil
        }
    }

    @MainActor func testReduceMotionAndScenePhasePauseAnimation() {
        XCTAssertGreaterThanOrEqual(AgentHighlight.pulsePeriod, 2.0)

        // Animates only when Reduce Motion is off, scenePhase == .active, and on screen.
        XCTAssertTrue(AgentHighlight.animates(reduceMotion: false, phase: .active, visible: true))
        XCTAssertFalse(AgentHighlight.animates(reduceMotion: true, phase: .active, visible: true))
        XCTAssertFalse(AgentHighlight.animates(reduceMotion: false, phase: .inactive, visible: true))
        XCTAssertFalse(AgentHighlight.animates(reduceMotion: false, phase: .background, visible: true))
        XCTAssertFalse(AgentHighlight.animates(reduceMotion: false, phase: .active, visible: false))

        // Under Reduce Motion, stroke opacity is static across all timestamps with zero glow pulse.
        let a = AgentHighlight.strokeOpacity(at: 0.0, reduceMotion: true, fade: 1.0)
        let b = AgentHighlight.strokeOpacity(at: 0.6, reduceMotion: true, fade: 1.0)
        let c = AgentHighlight.strokeOpacity(at: 1.2, reduceMotion: true, fade: 1.0)
        XCTAssertEqual(a.line, b.line, accuracy: 0.0001)
        XCTAssertEqual(b.line, c.line, accuracy: 0.0001)
        XCTAssertEqual(a.glow, 0)

        // With motion enabled, cosine wave breathes smoothly between trough (t=0) and peak (t=period/2).
        let trough = AgentHighlight.strokeOpacity(at: 0.0, reduceMotion: false, fade: 1.0)
        let peak = AgentHighlight.strokeOpacity(at: AgentHighlight.pulsePeriod / 2, reduceMotion: false, fade: 1.0)
        XCTAssertGreaterThan(peak.line, trough.line)
    }

    @MainActor func testColorContrastAndSameHueDesaturatedLightMode() {
        // 1. Dark mode contrast >= 3:1 against page, quaternary card, card, and lifted pill,
        //    both at full strength AND at the minimum pulse stroke opacity (0.72).
        let darkSurfaces: [(String, UInt32)] = [
            ("darkPage", 0x22252F),
            ("darkQuaternaryCard", 0x2C2F3B),
            ("darkCard", 0x363A47),
            ("darkLiftedPill", 0x414552),
        ]
        let minPulseAlpha = AgentHighlight.strokeOpacity(at: 0, reduceMotion: false, fade: 1.0).line
        for (name, bg) in darkSurfaces {
            let fullContrast = AgentHighlight.contrastRatio(AgentHighlight.darkRGB, bg)
            XCTAssertGreaterThanOrEqual(fullContrast, 3.0, "\(name) full contrast \(fullContrast) < 3:1")
            let troughRGB = AgentHighlight.blendRGB(AgentHighlight.darkRGB, over: bg, alpha: minPulseAlpha)
            let troughContrast = AgentHighlight.contrastRatio(troughRGB, bg)
            XCTAssertGreaterThanOrEqual(troughContrast, 3.0, "\(name) pulse-trough contrast \(troughContrast) < 3:1")
        }

        // 2. Light mode uses the exact same OKLCH hue, lower chroma (desaturated), and >= 3:1 contrast.
        let darkH = AgentHighlight.darkOKLCH.h
        let lightParsed = OKLCH(rgb: AgentHighlight.lightRGB)
        let hueDelta = min(abs(lightParsed.h - darkH), 360 - abs(lightParsed.h - darkH))
        XCTAssertLessThan(hueDelta, 2.0, "light mode must keep the same hue as dark mode")
        XCTAssertLessThan(lightParsed.c, AgentHighlight.darkOKLCH.c, "light mode must be a desaturated version of the same hue")
        XCTAssertGreaterThanOrEqual(AgentHighlight.contrastRatio(AgentHighlight.lightRGB, 0xEAEAEF), 3.0)
        XCTAssertGreaterThanOrEqual(AgentHighlight.contrastRatio(AgentHighlight.lightRGB, 0xFFFFFF), 3.0)
    }

    @MainActor func testVoiceOverLabelsAndNonSpammingAnnouncements() {
        XCTAssertEqual(AgentHighlight.accessibilityLabel(for: "Shell", active: false), "Shell")
        let labeled = AgentHighlight.accessibilityLabel(for: "Build", active: true)
        XCTAssertTrue(labeled.contains("Build"))
        XCTAssertTrue(labeled.contains("agent"))

        let announcer = AgentVoiceOverAnnouncer(cooldown: 2.0)
        var spoken: [String] = []
        announcer.isVoiceOverRunning = { true }
        announcer.postAnnouncement = { spoken.append($0) }

        let t0 = Date(timeIntervalSince1970: 1_760_000_000)
        // Initial mount with active=true records state without announcing (no mount spam).
        XCTAssertNil(announcer.observe(block: "b1", title: "Build", active: true, now: t0))
        XCTAssertTrue(spoken.isEmpty)

        // Repeated CLI writes keeping active=true do NOT spam announcements.
        XCTAssertNil(announcer.observe(block: "b1", title: "Build", active: true, now: t0.addingTimeInterval(3)))
        XCTAssertNil(announcer.observe(block: "b1", title: "Build", active: true, now: t0.addingTimeInterval(6)))
        XCTAssertTrue(spoken.isEmpty)

        // Human takeover edge (true -> false) announces once.
        let m1 = announcer.observe(block: "b1", title: "Build", active: false, tookOverByUser: true, now: t0.addingTimeInterval(7))
        XCTAssertNotNil(m1)
        XCTAssertEqual(spoken.count, 1)

        // Further human keystrokes keeping active=false do NOT announce again.
        XCTAssertNil(announcer.observe(block: "b1", title: "Build", active: false, tookOverByUser: true, now: t0.addingTimeInterval(10)))
        XCTAssertEqual(spoken.count, 1)

        // Agent re-taking control after cooldown (false -> true) announces once.
        let m2 = announcer.observe(block: "b1", title: "Build", active: true, now: t0.addingTimeInterval(12))
        XCTAssertNotNil(m2)
        XCTAssertEqual(spoken.count, 2)

        // Rapid flip inside cooldown window is suppressed.
        XCTAssertNil(announcer.observe(block: "b1", title: "Build", active: false, now: t0.addingTimeInterval(12.5)))
        XCTAssertEqual(spoken.count, 2)
    }

    @MainActor func testHostExpiresActiveFlagAfterSixtySeconds() {
        let host = Host(Demo.config())
        host.connect()
        defer { host.stop() }
        XCTAssertEqual(host.blocks[Demo.claude]?.agentActor?.active, true)
        let future = Date.now.addingTimeInterval(61)
        host.expireAgentIfNeeded(block: Demo.claude, at: future)
        XCTAssertEqual(host.blocks[Demo.claude]?.agentActor?.active, false)
        XCTAssertNil(host.agentState(for: Demo.claude))
    }
}
