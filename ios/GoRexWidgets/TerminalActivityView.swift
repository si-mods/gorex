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
        .font(.subheadline.weight(.semibold))
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

/// The Lock Screen's face, and the Dynamic Island's expanded one: the
/// tile, the heading and the mark, then the line. Paused, all but what
/// brings it back dims.
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

extension TerminalActivity {
    /// The tint of the Dynamic Island's rim: the attention color while
    /// the program waits for the user.
    static func tint(_ s: ContentState) -> Color? {
        s.connected && s.record?.state == "blocked" ? .attention : nil
    }
}
