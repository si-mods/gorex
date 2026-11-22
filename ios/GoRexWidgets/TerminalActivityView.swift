import AppIntents
import SwiftUI

// The terminal activity's faces (docs/IOS-006-live-activities.md, Terminal
// activity), apart from ActivityKit's configuration, so that a test can
// draw each one.

extension TerminalActivity.ContentState {
    /// Whether it shows as paused: the stream closed, or the state is old.
    func paused(stale: Bool) -> Bool { !connected || stale }

    /// Whether it takes the buttons: the top record is one a key answers
    /// (TerminalActivity.Record.answerable).
    nonisolated var showsActions: Bool { record?.answerable == true }
}

/// The program's tile, as the inbox's rows show it.
struct TerminalActivityTile: View {
    let state: TerminalActivity.ContentState
    var scale: CGFloat = 1.5

    var body: some View {
        ProgramTile(program: .of(state.program), scale: scale)
            .frame(width: 21 * scale, height: 16 * scale)
    }
}

/// The state at a glance: the needs-input glyph, failed, done, the
/// progress, or a timer since the program began working; paused once the
/// stream closed; a plain shell's terminal glyph, where its tile is not
/// beside it.
struct TerminalActivityMark: View {
    let state: TerminalActivity.ContentState
    let paused: Bool
    var shell = true
    var font = Font.subheadline.weight(.semibold)

    var body: some View {
        Group {
            if paused {
                Image(systemName: "pause.circle.fill").foregroundStyle(.secondary)
            } else if let r = state.record {
                switch r.state {
                case "blocked": Image(systemName: "exclamationmark.bubble.fill").foregroundStyle(Color.attention)
                case "error": Image(systemName: "xmark.circle.fill").foregroundStyle(.red)
                case "done": Image(systemName: "checkmark.circle.fill").foregroundStyle(Color.attention)
                default:
                    if let p = r.progress {
                        ProgressRing(progress: p).scaleEffect(1.25).frame(width: 16, height: 16)
                    } else if let t = r.updated {
                        // A timer moves by itself, without updates.
                        Text(timerInterval: t...Date.distantFuture, countsDown: false)
                            .monospacedDigit()
                            .multilineTextAlignment(.trailing)
                            .frame(maxWidth: 44, alignment: .trailing)
                            .foregroundStyle(.secondary)
                    } else {
                        Image(systemName: "ellipsis").foregroundStyle(.secondary)
                    }
                }
            } else if shell {
                Image(systemName: "terminal").foregroundStyle(.secondary)
            }
        }
        .font(font)
        .lineLimit(1)
    }
}

/// The window's title, then the state, what the record says and the
/// host, in one line cut at its end: a long host name cuts no state.
struct TerminalActivityHeading: View {
    let attributes: TerminalActivity
    let state: TerminalActivity.ContentState

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(verbatim: state.title.isEmpty ? attributes.hostName : state.title)
                .font(.subheadline.weight(.semibold))
                .privacySensitive()
            Text(verbatim: detail)
                .font(.caption)
                .foregroundStyle(.secondary)
                .privacySensitive()
        }
        .lineLimit(1)
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private var detail: String {
        guard let r = state.record else { return attributes.hostName }
        return [Self.name(r.state), r.title, attributes.hostName].compactMap(\.self).joined(separator: " · ")
    }

    /// A state's name, as the status inbox names it (StateName).
    static func name(_ state: String) -> String {
        switch state {
        case "blocked": String(localized: "Needs Input")
        case "error": String(localized: "Failed")
        case "done": String(localized: "Done")
        default: String(localized: "Working")
        }
    }
}

/// The activity's answers to the blocked record it shows
/// (docs/IOS-006-live-activities.md, Actions): Deny sends Escape,
/// Approve Return, each in the app's process, without the app coming to
/// the front. Only while the top record is one they answer; a question
/// kind shows none, a tap opening the terminal as before. They do not
/// show paused: the row then says what brings the app back instead. A
/// suspended app with the host pushing is not paused — pushes hold
/// connected — so the buttons still answer from the Lock Screen.
struct TerminalActivityActions: View {
    let attributes: TerminalActivity
    /// On the Lock Screen card the buttons tint white, so the prominent
    /// one's label goes dark on its white fill; on the dark island the
    /// accent tint keeps its own contrast.
    var onCard = false

    var body: some View {
        HStack(spacing: 8) {
            Button(intent: DenyIntent(host: attributes.host, session: attributes.session, block: attributes.block)) {
                Text("Deny")
            }
            .buttonStyle(.bordered)
            Button(intent: ApproveIntent(host: attributes.host, session: attributes.session, block: attributes.block)) {
                Text("Approve")
            }
            .buttonStyle(.borderedProminent)
            .foregroundStyle(onCard ? .black : .white)
        }
        .controlSize(.small)
    }
}

/// The last line the terminal shows, in a monospaced font; paused, what
/// brings it back.
struct TerminalActivityLine: View {
    let state: TerminalActivity.ContentState
    let paused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            if !state.line.isEmpty {
                Text(verbatim: state.line)
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
                    .privacySensitive()
                    .opacity(paused ? 0.5 : 1)
            }
            if paused {
                Text("Paused — open Rex Go")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
            }
        }
        .lineLimit(1)
        .truncationMode(.tail)
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// The Dynamic Island's expanded face: the tile, the heading and the
/// mark, then the line. Paused, all but what brings it back dims.
struct TerminalActivityFace: View {
    let attributes: TerminalActivity
    let state: TerminalActivity.ContentState
    let stale: Bool

    var body: some View {
        let paused = state.paused(stale: stale)
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 10) {
                TerminalActivityTile(state: state)
                TerminalActivityHeading(attributes: attributes, state: state)
                TerminalActivityMark(state: state, paused: paused, shell: false)
            }
            .opacity(paused ? 0.5 : 1)
            TerminalActivityLine(state: state, paused: paused)
            if !paused, state.showsActions {
                HStack {
                    Spacer(minLength: 0)
                    TerminalActivityActions(attributes: attributes)
                }
            }
        }
    }
}

/// The Lock Screen's card: the program, the host, the state and the timer
/// in a small row; what the program says in large bold type, two lines
/// always kept for it; the window's title, or paused what brings it back,
/// in a small row under it. Each row has a fixed height, so the card is
/// as tall in every state. It is drawn on two shades of the tile's hue,
/// with the glyph large and faint at the right, cut by the card's edge;
/// every row stops before it. It draws its own padding and background,
/// white on dark in either scheme.
struct TerminalActivityCard: View {
    let attributes: TerminalActivity
    let state: TerminalActivity.ContentState
    let stale: Bool

    /// The width at the right that the rows keep clear for the glyph.
    static let glyphClear: CGFloat = 84

    var body: some View {
        let paused = state.paused(stale: stale)
        let program = Program.of(state.program)
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                TerminalActivityTile(state: state, scale: 0.8)
                // The host is hidden on a locked phone, as the island's is;
                // the host is cut first, then the name, then nothing else.
                HStack(spacing: 0) {
                    Text(verbatim: program.name).layoutPriority(1)
                    if !attributes.hostName.isEmpty {
                        Text(verbatim: (program.name.isEmpty ? "" : " · ") + attributes.hostName).privacySensitive()
                    }
                }
                .foregroundStyle(.white.opacity(0.72))
                if let r = state.record {
                    Text(verbatim: TerminalActivityHeading.name(r.state))
                        .fontWeight(.semibold)
                        .foregroundStyle(Self.tint(r.state))
                        .fixedSize()
                        .layoutPriority(1)
                    meta(r)
                        .foregroundStyle(.white.opacity(0.72))
                        .monospacedDigit()
                        .fixedSize()
                        .layoutPriority(1)
                }
            }
            .font(.caption)
            .lineLimit(1)
            .frame(height: 16)
            .opacity(paused ? 0.5 : 1)
            Text(verbatim: hero)
                .font(.system(size: 21, weight: .bold))
                .foregroundStyle(.white)
                .lineLimit(2, reservesSpace: true)
                .privacySensitive()
                .opacity(paused ? 0.5 : 1)
            Group {
                if paused {
                    Text("Paused — open Rex Go")
                        .fontWeight(.semibold)
                        .foregroundStyle(.white.opacity(0.85))
                } else {
                    HStack(spacing: 8) {
                        Text(verbatim: footer)
                            .foregroundStyle(.white.opacity(0.58))
                            .privacySensitive()
                        Spacer(minLength: 0)
                        if state.showsActions {
                            TerminalActivityActions(attributes: attributes, onCard: true)
                                .tint(.white)
                        }
                    }
                }
            }
            .font(.caption)
            .lineLimit(1)
            // The buttons take more than 16 pt: the row grows only while
            // they show.
            .frame(minHeight: 16)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.trailing, Self.glyphClear)
        // Larger text stops at xLarge, so the card stays under the Lock
        // Screen's 160 pt.
        .dynamicTypeSize(...DynamicTypeSize.xLarge)
        .padding(16)
        .background { Self.background(program, paused: paused) }
        .overlay {
            if !paused && state.record?.state == "blocked" {
                ContainerRelativeShape().strokeBorder(Color.attention, lineWidth: 2)
            }
        }
        // White on dark whatever the Lock Screen's scheme: the attention
        // color takes its dark-mode value.
        .environment(\.colorScheme, .dark)
    }

    /// What follows a working state's name: the progress, else a timer
    /// since it began; nothing for the other states, whose color tells.
    @ViewBuilder private func meta(_ r: TerminalActivity.Record) -> some View {
        switch r.state {
        case "blocked", "done", "error": EmptyView()
        default:
            if let p = r.progress {
                Text(verbatim: "· \(max(0, min(100, p)))%")
            } else if let t = r.updated {
                // A timer moves by itself, without updates.
                HStack(spacing: 0) {
                    Text(verbatim: "· ")
                    Text(timerInterval: t...Date.distantFuture, countsDown: false)
                }
            }
        }
    }

    /// What it says largest: the line, else the state's name, else the
    /// window's title or the host for a shell that has said nothing.
    private var hero: String {
        if !state.line.isEmpty { return state.line }
        if let r = state.record { return TerminalActivityHeading.name(r.state) }
        return state.title.isEmpty ? attributes.hostName : state.title
    }

    /// The window's title, unless the hero shows it already.
    private var footer: String {
        state.line.isEmpty && state.record == nil ? "" : state.title
    }

    /// A state's color: the attention color for blocked, Rex's done blue
    /// for done, red for failed, and a light grey for working.
    static func tint(_ state: String) -> Color {
        switch state {
        case "blocked": .attention
        case "done": .done
        case "error": .red
        default: .white.opacity(0.72)
        }
    }

    /// The two shades of the ground, top left and bottom right: the
    /// tile's hue and chroma (at most 0.16) at OKLCH lightness 0.40 and
    /// 0.20. A neutral tile, as Codex's white or Devin's black, gets a
    /// graphite with a cool cast.
    static func ground(_ program: Program) -> (top: OKLCH, bottom: OKLCH) {
        let k = OKLCH(rgb: program.tileBg)
        let (c, h) = k.c < 0.035 ? (0.012, 260.0) : (min(k.c, 0.16), k.h)
        return (OKLCH(l: 0.40, c: c * 0.85, h: h), OKLCH(l: 0.20, c: c * 0.55, h: h))
    }

    @ViewBuilder static func background(_ program: Program, paused: Bool) -> some View {
        let g = ground(program)
        ZStack {
            LinearGradient(colors: [g.top.color, g.bottom.color], startPoint: .topLeading, endPoint: .bottomTrailing)
            // 190 pt, its right edge 56 pt past the card's.
            GeometryReader { r in
                ProgramGlyph(program: program)
                    .foregroundStyle(.white)
                    .frame(width: 190, height: 190)
                    .opacity(paused ? 0.06 : 0.12)
                    .position(x: r.size.width + 56 - 95, y: r.size.height / 2)
            }
        }
        .clipped()
    }
}

extension TerminalActivity {
    /// The tint of the Dynamic Island's rim: the attention color while
    /// the program waits for the user.
    static func tint(_ s: ContentState) -> Color? {
        s.connected && s.record?.state == "blocked" ? .attention : nil
    }
}
