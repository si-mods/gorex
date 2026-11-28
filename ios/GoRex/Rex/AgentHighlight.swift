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

    /// Whether the session was created by an agent or CLI (`"cli"` or `"delegate"`)
    /// rather than opened interactively by a person (`"human"`).
    var isAgentCreated: Bool {
        createdBy == "cli" || createdBy == "delegate"
    }

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

    /// SF Symbol rendered inside the circular corner mark on a program tile.
    nonisolated static let badgeSymbol = "sparkles"

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
        /// SF Symbol for the circular corner mark on the program tile (`"sparkles"`).
        let symbolName: String
        /// Normalized agent name (`"cli"`, `"agy"`, `"delegate"`, etc.).
        let agentName: String
        /// Tail fade factor in `0...1` (`1` for persistent agent-created panes and before 57 s on human panes).
        let fade: Double
        /// Remaining seconds until the 60 s window expires, or `.infinity` when persistent.
        let remaining: TimeInterval

        /// True for agent-created panes (`createdBy` in `{"cli", "delegate"}`) with
        /// `tookOverAt == nil`: the highlight stays resident without decaying over time.
        var isPersistent: Bool { !remaining.isFinite }
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

    /// Evaluates whether a block's `AgentActor` should highlight at `now`
    /// (`docs/IOS-035-agent-highlight.md`):
    /// 1. `createdBy` in `{"cli", "delegate"}` and `tookOverAt == nil`: highlight
    ///    persists indefinitely (`remaining == .infinity`, `fade == 1.0`) across
    ///    working, idle, and background transitions.
    /// 2. Real human input sets `tookOverAt` (or local `cleared`), permanently
    ///    clearing the highlight; session exit (`exited`) also clears it.
    /// 3. `createdBy == "human"` with `lastCliInputAt != nil` uses the 60 s CLI
    ///    idle window (`57s..60s` 3 s fade-out) for one-shot script writes.
    nonisolated static func state(
        actor: AgentActor?,
        exited: Bool = false,
        cleared: Bool = false,
        enabled: Bool = true,
        now: Date = .now
    ) -> State? {
        guard enabled, !exited, !cleared, let actor, actor.active, actor.tookOverAt == nil else {
            return nil
        }
        let name = actor.agent ?? (actor.createdBy == "delegate" ? "delegate" : "cli")
        if actor.isAgentCreated {
            return State(
                symbolName: badgeSymbol,
                agentName: name,
                fade: 1.0,
                remaining: .infinity
            )
        }
        guard let ref = actor.lastCliInputAt else { return nil }
        let age = max(0, now.timeIntervalSince(ref))
        guard age < windowDuration else { return nil }
        let rem = windowDuration - age
        let fade = rem < fadeDuration ? rem / fadeDuration : 1.0
        return State(
            symbolName: badgeSymbol,
            agentName: name,
            fade: fade,
            remaining: rem
        )
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

    /// Reports whether `bytes` carry genuine human keyboard or paste input rather
    /// than terminal query replies (`DA`, `CPR`, `DSR`, `DECRPM`, `XTWINOPS`,
    /// `OSC`/`DCS`/`APC`), clipboard read denials (`OSC 5522` `EPERM`/`DONE`),
    /// focus reports (`CSI I`, `CSI O`), pointer/mouse/wheel reports (`SGR`,
    /// `URXVT`, `X10`), or Kitty key-release and modifier-only key events.
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
        case 0x5D, 0x50, 0x5F, 0x5E, 0x58: // ']', 'P', '_', '^', 'X'
            // Kitty clipboard (`ESC ] 5522 ;`) is human input only on paste offers
            // or granted paste data (`type=paste`, `status=DATA`, `status=OK`);
            // `status=EPERM` denials and `status=DONE` trailers are replies.
            let kitty: [UInt8] = [0x1B, 0x5D, 0x35, 0x35, 0x32, 0x32, 0x3B]
            if second == 0x5D, count - start >= kitty.count, Array(buf[start..<(start + kitty.count)]) == kitty {
                if hasPrefix(buf, from: start + kitty.count, ascii: "type=paste")
                    || hasPrefix(buf, from: start + kitty.count, ascii: "type=read:status=DATA")
                    || hasPrefix(buf, from: start + kitty.count, ascii: "type=read:status=OK")
                {
                    return 0
                }
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
            if let m = mouseReportLength(buf, from: start) {
                return m
            }
            if let k = kittyNonTypingKeyLength(buf, from: start) {
                return k
            }
            var i = start + 2
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

    private nonisolated static func hasPrefix(_ buf: [UInt8], from start: Int, ascii: StaticString) -> Bool {
        guard start + ascii.utf8CodeUnitCount <= buf.count else { return false }
        return ascii.withUTF8Buffer { ptr in
            for idx in 0..<ptr.count where buf[start + idx] != ptr[idx] {
                return false
            }
            return true
        }
    }

    private nonisolated static func mouseReportLength(_ buf: [UInt8], from start: Int) -> Int? {
        let count = buf.count
        guard count - start >= 6, buf[start] == 0x1B, buf[start + 1] == 0x5B else { return nil }
        let third = buf[start + 2]
        if third == 0x3C { // SGR mouse: ESC [ < Cb ; Cx ; Cy M/m
            var j = start + 3
            var digits = false
            while j < count, (buf[j] >= 0x30 && buf[j] <= 0x39) || buf[j] == 0x3B {
                if buf[j] >= 0x30 && buf[j] <= 0x39 { digits = true }
                j += 1
            }
            if digits, j < count, buf[j] == 0x4D || buf[j] == 0x6D {
                return j - start + 1
            }
            return nil
        }
        if third == 0x4D, buf[start + 3] >= 0x20, buf[start + 4] >= 0x21, buf[start + 5] >= 0x21 {
            return 6 // X10 mouse: ESC [ M cb cx cy
        }
        var j = start + 2
        var semis = 0
        var digits = false
        while j < count, (buf[j] >= 0x30 && buf[j] <= 0x39) || buf[j] == 0x3B {
            if buf[j] == 0x3B { semis += 1 } else { digits = true }
            j += 1
        }
        if digits, semis >= 2, j < count, buf[j] == 0x4D {
            return j - start + 1 // URXVT mouse: ESC [ Cb ; Cx ; Cy M
        }
        return nil
    }

    private nonisolated static func kittyNonTypingKeyLength(_ buf: [UInt8], from start: Int) -> Int? {
        let count = buf.count
        guard count - start >= 4, buf[start] == 0x1B, buf[start + 1] == 0x5B else { return nil }
        var code = 0
        var hasCode = false
        var j = start + 2
        while j < count, buf[j] >= 0x30, buf[j] <= 0x39 {
            code = code * 10 + Int(buf[j] - 0x30)
            hasCode = true
            j += 1
        }
        while j < count, (buf[j] >= 0x30 && buf[j] <= 0x39) || buf[j] == 0x3B || buf[j] == 0x3A {
            j += 1
        }
        guard j < count else { return nil }
        let f = buf[j]
        guard f == 0x75 || f == 0x7E || (f >= 0x41 && f <= 0x5A) else { return nil }
        let params = buf[(start + 2)..<j]
        if let semi = params.firstIndex(of: 0x3B) {
            let afterSemi = params[params.index(after: semi)...]
            let modField = afterSemi.firstIndex(of: 0x3B).map { afterSemi[..<$0] } ?? afterSemi
            if let colon = modField.firstIndex(of: 0x3A) {
                let evType = modField[modField.index(after: colon)...]
                if evType.count == 1, evType.first == 0x33 { // ':3' key release
                    return j - start + 1
                }
            }
        }
        if f == 0x75, hasCode, (57441...57454).contains(code) {
            return j - start + 1
        }
        return nil
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
