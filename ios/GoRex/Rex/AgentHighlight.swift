import Foundation
import SwiftUI
import UIKit

/// Who created or last drove a terminal block via the CLI or peer delegation
/// (`agentActor` from #1281 / docs/IOS-035-agent-highlight.md).
nonisolated struct AgentActor: Equatable, Sendable, Decodable {
    var createdBy: String
    var lastCliInputAt: Date?
    var agent: String?
    var active: Bool
    var parentSession: String?
    var tookOverAt: Date?
    var createdAt: Date?

    init(
        createdBy: String = "human",
        lastCliInputAt: Date? = nil,
        agent: String? = nil,
        active: Bool? = nil,
        parentSession: String? = nil,
        tookOverAt: Date? = nil,
        createdAt: Date? = nil
    ) {
        let normalizedCreator = createdBy.trimmingCharacters(in: .whitespacesAndNewlines)
        self.createdBy = normalizedCreator.isEmpty ? "human" : normalizedCreator
        self.lastCliInputAt = lastCliInputAt
        self.agent = Self.normalizeAgentName(agent)
        self.parentSession = parentSession.flatMap { $0.isEmpty ? nil : $0 }
        self.tookOverAt = tookOverAt
        self.createdAt = createdAt
        if let active {
            self.active = active
        } else if tookOverAt != nil {
            self.active = false
        } else {
            self.active = lastCliInputAt != nil || (self.createdBy != "human" && !self.createdBy.isEmpty)
        }
    }

    private enum CodingKeys: String, CodingKey {
        case createdBy
        case created_by
        case lastCliInputAt
        case lastCLIInputAt
        case last_cli_input_at
        case agent
        case active
        case parentSession
        case parent_session
        case tookOverAt
        case took_over_at
        case createdAt
        case created
    }

    init(from decoder: Decoder) throws {
        let c = try? decoder.container(keyedBy: CodingKeys.self)
        func str(_ keys: CodingKeys...) -> String? {
            for k in keys {
                if let v = (try? c?.decodeIfPresent(String.self, forKey: k)) ?? nil, !v.isEmpty {
                    return v
                }
            }
            return nil
        }
        let createdBy = str(.createdBy, .created_by) ?? "human"
        let lastCli = Self.parseTime(str(.lastCliInputAt, .lastCLIInputAt, .last_cli_input_at))
        let agent = str(.agent)
        let active = (try? c?.decodeIfPresent(Bool.self, forKey: .active)) ?? nil
        let parent = str(.parentSession, .parent_session)
        let tookOver = Self.parseTime(str(.tookOverAt, .took_over_at))
        let created = Self.parseTime(str(.createdAt, .created))
        self.init(
            createdBy: createdBy,
            lastCliInputAt: lastCli,
            agent: agent,
            active: active,
            parentSession: parent,
            tookOverAt: tookOver,
            createdAt: created
        )
    }

    /// Parses an `agentActor` dictionary from a Rex API event or block method
    /// payload (`agentActor` or `agent_actor`), returning nil when absent.
    init?(json: Any?, fallbackCreatedAt: Date? = nil) {
        guard let d = json as? [String: Any] else { return nil }
        func str(_ keys: String...) -> String? {
            for k in keys {
                if let v = d[k] as? String, !v.isEmpty { return v }
            }
            return nil
        }
        let createdBy = str("createdBy", "created_by") ?? "human"
        let lastCli = Self.parseTime(str("lastCliInputAt", "lastCLIInputAt", "last_cli_input_at"))
        let agent = str("agent")
        let active = d["active"] as? Bool
        let parent = str("parentSession", "parent_session")
        let tookOver = Self.parseTime(str("tookOverAt", "took_over_at"))
        let created = Self.parseTime(str("createdAt", "created")) ?? fallbackCreatedAt
        self.init(
            createdBy: createdBy,
            lastCliInputAt: lastCli,
            agent: agent,
            active: active,
            parentSession: parent,
            tookOverAt: tookOver,
            createdAt: created
        )
    }

    /// Trims and bounds an agent name to 24 non-control characters, matching
    /// `rex.NormalizeAgentName` on the Go server.
    static func normalizeAgentName(_ raw: String?) -> String? {
        guard let raw else { return nil }
        var out = ""
        var count = 0
        for scalar in raw.trimmingCharacters(in: .whitespacesAndNewlines).unicodeScalars {
            if CharacterSet.controlCharacters.contains(scalar) { continue }
            if count >= 24 { break }
            out.unicodeScalars.append(scalar)
            count += 1
        }
        let trimmed = out.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? nil : trimmed
    }

    private static let isoFractional = Date.ISO8601FormatStyle(includingFractionalSeconds: true)
    private static let isoPlain = Date.ISO8601FormatStyle(includingFractionalSeconds: false)

    /// Parses RFC 3339 timestamps with or without fractional seconds, treating
    /// Go's zero time (`0001-01-01T00:00:00Z`) as nil.
    static func parseTime(_ s: String?) -> Date? {
        guard let s = s?.trimmingCharacters(in: .whitespacesAndNewlines), !s.isEmpty else { return nil }
        if let d = try? isoFractional.parse(s), d.timeIntervalSince1970 > 0 { return d }
        if let d = try? isoPlain.parse(s), d.timeIntervalSince1970 > 0 { return d }
        let stripped = s.replacing(/\.[0-9]+/, with: "", maxReplacements: 1)
        if let d = try? isoPlain.parse(stripped), d.timeIntervalSince1970 > 0 { return d }
        return nil
    }
}

/// Pure rules for the agent-driven session/pane highlight on iOS: 60 s window,
/// 3 s fade-out, 2.4 s cosine pulse, Reduce Motion & scenePhase gating, OKLCH
/// same-hue color derivation, human-input byte filtering, and VoiceOver labels
/// (docs/IOS-035-agent-highlight.md).
enum AgentHighlight {
    /// Idle CLI window after which the highlight fades out completely.
    nonisolated static let windowDuration: TimeInterval = 60
    /// Calm breathing cycle (>= 2 s; 0.42 Hz stays far below 1 flash/s and WCAG 2.3.1's 3/s).
    nonisolated static let pulsePeriod: TimeInterval = 2.4
    /// Smooth fade-out duration at the tail of `windowDuration` (`57s..60s`).
    nonisolated static let fadeDuration: TimeInterval = 3
    /// Minimum interval between VoiceOver takeover announcements for the same pane.
    nonisolated static let announcementCooldown: TimeInterval = 2

    /// Desktop dark-mode agent blue (`#6cb2ff` in `style.go`).
    nonisolated static let darkRGB: UInt32 = 0x6CB2FF
    /// Dark-mode color in OKLCH (`h ≈ 252.05°`).
    static let darkOKLCH = OKLCH(rgb: darkRGB)
    /// Light-mode color derived in OKLCH with the exact same hue (`darkOKLCH.h`)
    /// and 25% lower chroma so no second hue is introduced while keeping > 5:1 contrast.
    static let lightOKLCH = OKLCH(l: 0.48, c: darkOKLCH.c * 0.75, h: darkOKLCH.h)
    static let lightRGB: UInt32 = lightOKLCH.rgb

    /// Active highlight snapshot at a point in time.
    nonisolated struct State: Equatable, Sendable {
        /// Compact inline pill text for list rows (`"agent"`).
        let badgeText: String
        /// Normalized agent name (`"cli"`, `"agy"`, etc.).
        let agentName: String
        /// Tail fade factor in `0...1` (`1` before 57 s, linear to `0` at 60 s).
        let fade: Double
        /// Remaining seconds until the 60 s window expires.
        let remaining: TimeInterval
    }

    /// Whether agent highlight rendering is enabled (`agentHighlight` default; true when unset).
    nonisolated static var isEnabled: Bool {
        UserDefaults.standard.object(forKey: "agentHighlight") == nil
            || UserDefaults.standard.bool(forKey: "agentHighlight")
    }

    /// Whether OS Reduce Motion (`UIAccessibility.isReduceMotionEnabled`) or the
    /// documented `-reduceMotion YES` test default is active.
    static var isReduceMotionEnabled: Bool {
        UIAccessibility.isReduceMotionEnabled || UserDefaults.standard.bool(forKey: "reduceMotion")
    }

    /// Evaluates whether a block's `AgentActor` should highlight at `now`.
    nonisolated static func state(
        actor: AgentActor?,
        exited: Bool = false,
        cleared: Bool = false,
        enabled: Bool = true,
        now: Date = .now
    ) -> State? {
        guard enabled, !exited, !cleared, let actor, actor.active else { return nil }
        var ref = actor.lastCliInputAt
        if !actor.createdBy.isEmpty, actor.createdBy != "human", let created = actor.createdAt {
            if let cur = ref {
                if created > cur { ref = created }
            } else {
                ref = created
            }
        }
        guard let ref else { return nil }
        let age = max(0, now.timeIntervalSince(ref))
        guard age < windowDuration else { return nil }
        let rem = windowDuration - age
        let fade = rem < fadeDuration ? rem / fadeDuration : 1.0
        let name = actor.agent ?? (actor.createdBy == "delegate" ? "delegate" : "cli")
        return State(badgeText: "agent", agentName: name, fade: fade, remaining: rem)
    }

    /// Whether the continuous breathing animation may run: reuses `ListMotion.runs`
    /// so Reduce Motion, `scenePhase`, and off-screen rules stay identical.
    static func animates(reduceMotion: Bool, phase: ScenePhase, visible: Bool) -> Bool {
        ListMotion.runs(reduceMotion: reduceMotion, phase: phase, onScreen: visible)
    }

    /// Smooth `0...1...0` cosine wave with period `pulsePeriod` (2.4 s).
    nonisolated static func pulseWave(at t: TimeInterval) -> Double {
        0.5 - 0.5 * cos(2 * .pi * (t / pulsePeriod))
    }

    /// Stroke and outer-glow opacities for `AgentPaneOutline`. Under Reduce Motion
    /// the stroke stays steady (`glow == 0`) with no pulse oscillation.
    nonisolated static func strokeOpacity(at t: TimeInterval, reduceMotion: Bool, fade: Double) -> (line: Double, glow: Double) {
        if reduceMotion {
            return (line: 0.90, glow: 0)
        }
        let wave = pulseWave(at: t)
        return (
            line: (0.76 + 0.22 * wave) * fade,
            glow: (0.12 + 0.16 * wave) * fade
        )
    }

    /// Palette color for `scheme`: `#6cb2ff` in dark mode, same-hue desaturated
    /// `#2f6094` in light mode.
    static func color(for scheme: ColorScheme) -> Color {
        Color(rgb: scheme == .dark ? darkRGB : lightRGB)
    }

    /// Blends `fg` over `bg` at `alpha` (`0...1`) in sRGB for contrast checks.
    nonisolated static func blendRGB(_ fg: UInt32, over bg: UInt32, alpha: Double) -> UInt32 {
        let a = min(max(alpha, 0), 1)
        func ch(_ shift: UInt32) -> UInt32 {
            let f = Double((fg >> shift) & 0xFF)
            let b = Double((bg >> shift) & 0xFF)
            return UInt32((f * a + b * (1 - a)).rounded())
        }
        return (ch(16) << 16) | (ch(8) << 8) | ch(0)
    }

    /// WCAG 2.1 relative luminance contrast ratio (`1...21`) between two `0xRRGGBB` colors.
    nonisolated static func contrastRatio(_ a: UInt32, _ b: UInt32) -> Double {
        func lum(_ rgb: UInt32) -> Double {
            func lin(_ byte: UInt32) -> Double {
                let c = Double(byte & 0xFF) / 255.0
                return c <= 0.04045 ? c / 12.92 : pow((c + 0.055) / 1.055, 2.4)
            }
            return 0.2126 * lin(rgb >> 16) + 0.7152 * lin(rgb >> 8) + 0.0722 * lin(rgb)
        }
        let l1 = lum(a), l2 = lum(b)
        return (max(l1, l2) + 0.05) / (min(l1, l2) + 0.05)
    }

    /// Localized VoiceOver label for a row, pane-bar tile, or terminal view.
    nonisolated static func accessibilityLabel(for title: String, active: Bool) -> String {
        let trimmed = title.trimmingCharacters(in: .whitespacesAndNewlines)
        let base = trimmed.isEmpty ? String(localized: "Terminal") : trimmed
        guard active else { return base }
        return String(localized: "\(base), operated by an agent")
    }

    /// Reports whether `bytes` carry human keyboard/paste input rather than
    /// terminal query replies (`DA`, `CPR`, `DECRPM`, `OSC`), focus reports
    /// (`CSI I`, `CSI O`), or pointer wheel/motion reports.
    nonisolated static func hasHumanInputBytes(_ bytes: Data) -> Bool {
        guard !bytes.isEmpty else { return false }
        let buf = [UInt8](bytes)
        if !buf.contains(0x1B) { return true }
        var i = 0
        while i < buf.count {
            let rem = buf.count - i
            if rem >= 3, buf[i] == 0x1B, buf[i + 1] == 0x5B, buf[i + 2] == 0x49 || buf[i + 2] == 0x4F {
                i += 3
                continue
            }
            let n = nonHumanSequenceLength(buf, from: i)
            if n > 0 {
                i += n
                continue
            }
            return true
        }
        return false
    }

    private nonisolated static func nonHumanSequenceLength(_ buf: [UInt8], from start: Int) -> Int {
        let count = buf.count
        guard count - start >= 3, buf[start] == 0x1B else { return 0 }
        let second = buf[start + 1]
        switch second {
        case 0x5D, 0x50, 0x5F: // ']', 'P', '_'
            // Kitty clipboard paste (`ESC ] 5522 ;`) is real user paste input.
            let kitty: [UInt8] = [0x1B, 0x5D, 0x35, 0x35, 0x32, 0x32, 0x3B]
            if count - start >= kitty.count && Array(buf[start..<(start + kitty.count)]) == kitty {
                return 0
            }
            var i = start + 2
            while i < count {
                if buf[i] == 0x07 && second == 0x5D {
                    return i - start + 1
                }
                if buf[i] == 0x1B {
                    if i + 1 < count && buf[i + 1] == 0x5C { // ST (`ESC \`)
                        return i - start + 2
                    }
                    return 0
                }
                i += 1
            }
            return 0
        case 0x5B: // '[' (CSI)
            var i = start + 2
            if i < count && buf[i] == 0x3C { // '<' SGR mouse: ESC [ < Cb ; Cx ; Cy M/m
                var code = 0
                var hasDigits = false
                var j = i + 1
                while j < count, buf[j] >= 0x30, buf[j] <= 0x39 {
                    code = code * 10 + Int(buf[j] - 0x30)
                    hasDigits = true
                    j += 1
                }
                while j < count, (buf[j] >= 0x30 && buf[j] <= 0x39) || buf[j] == 0x3B {
                    j += 1
                }
                if hasDigits, j < count, buf[j] == 0x4D || buf[j] == 0x6D {
                    // Wheel (bit 64) or pointer motion (bit 32) or release ('m') does not take over.
                    if (code & 64) != 0 || (code & 32) != 0 || buf[j] == 0x6D {
                        return j - start + 1
                    }
                }
                return 0
            }
            var mark: UInt8 = 0
            if i < count && (buf[i] == 0x3F || buf[i] == 0x3E) { // '?' or '>'
                mark = buf[i]
                i += 1
            }
            while i < count && ((buf[i] >= 0x30 && buf[i] <= 0x39) || buf[i] == 0x3B) {
                i += 1
            }
            guard i < count else { return 0 }
            let f = buf[i]
            if f == 0x24 { // '$' -> DECRPM `$y`
                if i + 1 < count && buf[i + 1] == 0x79 { return i - start + 2 }
                return 0
            }
            if f == 0x52 || f == 0x6E // 'R' (CPR), 'n' (DSR)
                || (f == 0x63 && mark != 0) // 'c' (DA1/DA2)
                || (f == 0x74 && mark == 0) // 't' (XTWINOPS reply)
                || (f == 0x75 && mark == 0x3F) // '?...u' (Kitty keyboard flags reply)
            {
                return i - start + 1
            }
            return 0
        default:
            return 0
        }
    }
}

/// Edge-triggered, cooldown-gated VoiceOver announcer for agent takeover state
/// changes. Never announces on initial mount or when repeated CLI writes only
/// refresh `lastCliInputAt`, preventing VoiceOver spam.
final class AgentVoiceOverAnnouncer {
    static let shared = AgentVoiceOverAnnouncer()

    private var seenState: [String: Bool] = [:]
    private var lastAnnouncedAt: [String: Date] = [:]
    private let cooldown: TimeInterval
    var isVoiceOverRunning: () -> Bool = { UIAccessibility.isVoiceOverRunning }
    var postAnnouncement: (String) -> Void = { message in
        UIAccessibility.post(notification: .announcement, argument: message)
    }

    init(cooldown: TimeInterval = AgentHighlight.announcementCooldown) {
        self.cooldown = cooldown
    }

    /// Observes a block's active agent state. Announces only on a live boolean
    /// edge (`false -> true` or `true -> false` after initial mount) while the
    /// scene is active and outside the per-block cooldown window.
    @discardableResult
    func observe(
        block: String,
        title: String,
        active: Bool,
        tookOverByUser: Bool = false,
        sceneActive: Bool = true,
        now: Date = .now
    ) -> String? {
        guard let previous = seenState[block] else {
            seenState[block] = active
            return nil
        }
        guard previous != active else { return nil }
        seenState[block] = active
        guard sceneActive, isVoiceOverRunning() else { return nil }
        if let last = lastAnnouncedAt[block], now.timeIntervalSince(last) < cooldown {
            return nil
        }
        let clean = title.trimmingCharacters(in: .whitespacesAndNewlines)
        let target = clean.isEmpty ? String(localized: "Terminal") : clean
        let message: String
        if active {
            message = String(localized: "\(target), operated by an agent")
        } else if tookOverByUser {
            message = String(localized: "You took over \(target)")
        } else {
            message = String(localized: "Agent stopped operating \(target)")
        }
        lastAnnouncedAt[block] = now
        postAnnouncement(message)
        return message
    }

    func reset(block: String? = nil) {
        if let block {
            seenState.removeValue(forKey: block)
            lastAnnouncedAt.removeValue(forKey: block)
        } else {
            seenState.removeAll()
            lastAnnouncedAt.removeAll()
        }
    }
}
