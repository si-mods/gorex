import SwiftUI
import WidgetKit

/// Rex Go's widgets (docs/ios-widgets.md). They show the snapshot the app
/// writes (WidgetSnapshot); they reach no host.
@main
struct GoRexWidgets: WidgetBundle {
    var body: some Widget {
        InboxWidget()
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
