import SwiftUI

// The terminal activity's faces (docs/ios-live-activities.md, Terminal
// activity), apart from ActivityKit's configuration, so that a test can
// draw each one.

extension TerminalActivity.ContentState {
    /// Whether it shows as paused: the stream closed, or the state is old.
    func paused(stale: Bool) -> Bool { !connected || stale }
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
        }
    }
}

/// The Lock Screen's card: the program, the host, the state and the timer
/// in a small row; what the program says in large bold type; the
/// window's title under it. It is drawn on a gradient from the program's
/// tile color to near black, with its glyph large and faint at the right,
/// cut by the card's edge; the large line and the title keep clear of it. Paused, all but what
/// brings it back dims. It draws its own padding and background, white
/// on dark in either scheme.
struct TerminalActivityCard: View {
    let attributes: TerminalActivity
    let state: TerminalActivity.ContentState
    let stale: Bool

    /// The width the glyph shows of itself at the right, which the large
    /// line and the title keep clear of; the small top row spans the card.
    static let glyphShown: CGFloat = 84

    var body: some View {
        let paused = state.paused(stale: stale)
        let program = Program.of(state.program)
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                TerminalActivityTile(state: state, scale: 0.8)
                Text(verbatim: [program.name, attributes.hostName].filter { !$0.isEmpty }.joined(separator: " · "))
                    .foregroundStyle(.white.opacity(0.75))
                    .lineLimit(1)
                if let r = state.record {
                    Text(verbatim: TerminalActivityHeading.name(r.state))
                        .fontWeight(.semibold)
                        .foregroundStyle(Self.tint(r.state))
                        .lineLimit(1)
                        .fixedSize()
                }
                Spacer(minLength: 4)
                // First to get its width: a timer takes at most 44 pt.
                TerminalActivityMark(state: state, paused: paused, shell: false, font: .caption.weight(.semibold))
                    .layoutPriority(1)
            }
            .font(.caption)
            .opacity(paused ? 0.5 : 1)
            Text(verbatim: hero)
                .font(.system(size: 21, weight: .bold))
                .foregroundStyle(.white)
                .lineLimit(2)
                .minimumScaleFactor(0.8)
                .privacySensitive()
                .opacity(paused ? 0.5 : 1)
                .padding(.trailing, Self.glyphShown)
            if !footer.isEmpty {
                Text(verbatim: footer)
                    .font(.caption)
                    .foregroundStyle(.white.opacity(0.6))
                    .lineLimit(1)
                    .privacySensitive()
                    .opacity(paused ? 0.5 : 1)
                    .padding(.trailing, Self.glyphShown)
            }
            if paused {
                Text("Paused — open Rex Go")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.white.opacity(0.85))
                    .lineLimit(1)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(16)
        .background { Self.background(program, paused: paused) }
        .overlay {
            if TerminalActivity.tint(state) != nil {
                ContainerRelativeShape().strokeBorder(Color.attention, lineWidth: 2)
            }
        }
        // White on dark whatever the Lock Screen's scheme: secondary
        // styles and the attention color take their dark-mode values.
        .environment(\.colorScheme, .dark)
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

    /// A state's color, as its mark's: the attention color for blocked
    /// and done, red for failed, and a light grey for working.
    static func tint(_ state: String) -> Color {
        switch state {
        case "blocked", "done": .attention
        case "error": .red
        default: .white.opacity(0.75)
        }
    }

    /// The tile's color, darkened to at most a luminance that white text
    /// reads on: a light tile, as Codex's, turns grey.
    static func base(_ rgb: UInt32) -> Color {
        let r = Double(rgb >> 16 & 0xff) / 255, g = Double(rgb >> 8 & 0xff) / 255, b = Double(rgb & 0xff) / 255
        let luma = 0.2126 * r + 0.7152 * g + 0.0722 * b
        let k = min(1, 0.42 / max(luma, 0.001))
        return Color(red: r * k, green: g * k, blue: b * k)
    }

    @ViewBuilder static func background(_ program: Program, paused: Bool) -> some View {
        let near = Color(red: 0.05, green: 0.05, blue: 0.06)
        ZStack {
            LinearGradient(stops: [.init(color: base(program.tileBg), location: 0), .init(color: near, location: 0.85)],
                           startPoint: .topLeading, endPoint: .bottomTrailing)
            GeometryReader { g in
                let size = g.size.height * 1.7
                ProgramGlyph(program: program)
                    .foregroundStyle(.white)
                    .frame(width: size, height: size)
                    .opacity(paused ? 0.06 : 0.12)
                    .position(x: g.size.width - glyphShown + size / 2, y: g.size.height / 2)
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
