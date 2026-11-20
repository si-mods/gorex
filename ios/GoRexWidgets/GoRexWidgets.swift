import ActivityKit
import SwiftUI
import WidgetKit

/// Rex Go's widgets (docs/ios-widgets.md). They show the snapshot the app
/// writes (WidgetSnapshot); they reach no host.
@main
struct GoRexWidgets: WidgetBundle {
    var body: some Widget {
        InboxWidget()
        TerminalActivityWidget()
    }
}

/// The terminal open when the app left, on the Lock Screen and in the
/// Dynamic Island (docs/ios-live-activities.md, Terminal activity); a
/// tap opens it.
struct TerminalActivityWidget: Widget {
    var body: some WidgetConfiguration {
        ActivityConfiguration(for: TerminalActivity.self) { c in
            TerminalActivityFace(attributes: c.attributes, state: c.state, stale: c.isStale)
                .padding(16)
                .widgetURL(c.attributes.url)
        } dynamicIsland: { c in
            let paused = c.state.paused(stale: c.isStale)
            return DynamicIsland {
                // All in the bottom region, under the camera, as on the Lock
                // Screen: the leading region sits in the island's top corner,
                // which cut the tile and set it apart from the heading.
                DynamicIslandExpandedRegion(.bottom) {
                    TerminalActivityFace(attributes: c.attributes, state: c.state, stale: c.isStale)
                        .padding(.horizontal, 8)
                }
            } compactLeading: {
                TerminalActivityTile(state: c.state, scale: 1).opacity(paused ? 0.5 : 1)
            } compactTrailing: {
                TerminalActivityMark(state: c.state, paused: paused)
            } minimal: {
                TerminalActivityTile(state: c.state, scale: 1).opacity(paused ? 0.5 : 1)
            }
            .widgetURL(c.attributes.url)
            .keylineTint(TerminalActivity.tint(c.state))
        }
    }
}

/// The status inbox on the Home Screen and the Lock Screen.
struct InboxWidget: Widget {
    var body: some WidgetConfiguration {
        StaticConfiguration(kind: "Inbox", provider: InboxProvider()) { entry in
            InboxWidgetView(entry: entry)
        }
        .configurationDisplayName("Activity")
        .description("What your agents wait for, the most urgent first.")
        .supportedFamilies([.systemSmall, .systemMedium, .systemLarge, .accessoryCircular, .accessoryRectangular, .accessoryInline])
    }
}

nonisolated struct InboxProvider: TimelineProvider {
    func placeholder(in context: Context) -> InboxEntry {
        InboxEntry(date: .now, snapshot: .sample(now: .now))
    }

    func getSnapshot(in context: Context, completion: @escaping (InboxEntry) -> Void) {
        // The gallery shows what the widget looks like, before the app wrote any.
        let s = WidgetSnapshot.url.flatMap(WidgetSnapshot.read)
        completion(InboxEntry(date: .now, snapshot: context.isPreview && s == nil ? .sample(now: .now) : s))
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<InboxEntry>) -> Void) {
        let s = WidgetSnapshot.url.flatMap(WidgetSnapshot.read)
        completion(Timeline(entries: InboxEntry.timeline(s, now: .now), policy: .never))
    }
}
