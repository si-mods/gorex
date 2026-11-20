import Observation
import SwiftUI
import WidgetKit
import XCTest

/// The widgets' snapshot: what the app writes of its hosts, how often, how
/// a widget reads it, and the links a widget's tap opens
/// (docs/ios-widgets.md).
nonisolated final class WidgetTests: XCTestCase {
    @MainActor private func temp() -> URL {
        FileManager.default.temporaryDirectory.appending(path: "widgets-\(UUID().uuidString)/snapshot.json")
    }

    /// Demo Mode's host is written as any other: its inbox in Inbox.groups'
    /// order, and every window.
    @MainActor func testSnapshotOfDemoHost() {
        let h = Host(Demo.config())
        h.connect()
        defer { h.stop() }
        let e = WidgetPublisher.entry(h)
        XCTAssertEqual(e.id, Demo.hostID)
        XCTAssertEqual(e.name, "Demo Mac")
        XCTAssertTrue(e.online)
        XCTAssertEqual(e.items.map(\.state), Inbox.groups(h.inbox).flatMap(\.items).map(\.status.state))
        XCTAssertEqual(e.items.map(\.state), ["blocked", "done", "working"])
        XCTAssertEqual(e.items.first?.block, Demo.claude)
        XCTAssertEqual(e.items.first.map { Program.of($0.app).name }, "Claude Code")
        XCTAssertEqual(e.windows.count, 5)
        XCTAssertEqual(e.windows.first?.title, "Claude Code")
    }

    /// At most 20 items and 100 windows a host, text cut to 200
    /// characters; a host gone offline keeps its windows and has no items.
    @MainActor func testSnapshotCapsAndOffline() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        let windows = (0..<105).map { ["window_id": "w\($0)", "layers": [["blocks": [["block_id": "b\($0)"]]]]] as [String: Any] }
        let view: [String: Any] = ["revision": 1, "session_id": "s", "label": "s", "windows": windows]
        rex.respond = { m, _ in
            m == "session.bootstrap" ? ["attach_all": ["sessions": [["session_id": "s", "label": "s"]]], "selected_session": ["view": view]] : [:]
        }
        let h = Host(HostConfig(name: "alice-laptop", url: try await rex.url()))
        h.connect()
        defer { h.stop() }
        await until("online") { h.status == .online && h.sessions.first?.view != nil }
        let long = String(repeating: "x", count: 250)
        for i in 0..<25 {
            var r: [String: Any] = ["id": "claude", "state": "blocked", "updated_at": Date(timeIntervalSince1970: 1_000_000 + Double(i)).formatted(.iso8601)]
            if i == 24 { r["title"] = long; r["msg"] = "y" + long }
            rex.event(["type": "session_event", "payload": ["session_id": "s", "event": [
                "type": "block_event", "payload": ["block_id": "b\(i)", "name": "program_status_changed", "payload": ["records": [r]]],
            ]]])
        }
        await until("records") { h.blocks["b24"]?.statuses != nil && h.inbox.count == 25 }

        var e = WidgetPublisher.entry(h)
        XCTAssertEqual(e.items.count, WidgetSnapshot.maxItems)
        XCTAssertEqual(e.items.first?.block, "b24")
        XCTAssertEqual(e.items.first?.heading.count, 200)
        XCTAssertEqual(e.items.first?.detail.count, 200)
        XCTAssertEqual(e.items.last?.block, "b5")
        XCTAssertEqual(e.windows.count, WidgetSnapshot.maxWindows)
        XCTAssertEqual(e.windows.first.map { [$0.session, $0.block] }, ["s", "b0"])

        FakeRex.close(rex.controls[0], .protocolCode(.goingAway))
        await until("offline") { h.status != .online }
        e = WidgetPublisher.entry(h)
        XCTAssertFalse(e.online)
        XCTAssertEqual(e.items, [])
        XCTAssertEqual(e.windows.count, WidgetSnapshot.maxWindows)
        XCTAssertEqual(WidgetSnapshot.cut("short"), "short")
    }

    /// Writes come at most once an interval, those asked meanwhile joining
    /// one; a write of what was written already is skipped, unless forced,
    /// as going to the background is. A change the hosts observe writes.
    @MainActor func testPublisherCoalescesAndSkips() async throws {
        let url = temp()
        defer { try? FileManager.default.removeItem(at: url.deletingLastPathComponent()) }
        var reloads = 0
        let p = WidgetPublisher(url: url, interval: .milliseconds(300), reload: { reloads += 1 })
        let demo = Host(Demo.config())
        demo.connect()
        defer { demo.stop() }
        var list = [demo]
        p.start(hosts: { list })
        XCTAssertEqual(reloads, 1)
        XCTAssertEqual(WidgetSnapshot.read(from: url)?.hosts.map(\.name), ["Demo Mac"])

        p.publish()
        XCTAssertEqual(reloads, 1, "unchanged, not written")

        list = []
        p.changed()
        p.changed()
        p.changed()
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertEqual(reloads, 1, "within the interval of the last write")
        await until("one write") { reloads == 2 }
        try await Task.sleep(for: .milliseconds(400))
        XCTAssertEqual(reloads, 2, "the changes joined one write")
        XCTAssertEqual(WidgetSnapshot.read(from: url)?.hosts, [])

        let before = try XCTUnwrap(WidgetSnapshot.read(from: url)).written
        try await Task.sleep(for: .milliseconds(1100))
        p.publish(force: true)
        XCTAssertEqual(reloads, 3)
        XCTAssertGreaterThan(try XCTUnwrap(WidgetSnapshot.read(from: url)).written, before)

        // The hosts read are observed: a command typed in the demo
        // terminal changes a tile, and the snapshot follows.
        list = [demo]
        p.publish()
        XCTAssertEqual(reloads, 4)
        demo.demoCommand("swift test", block: Demo.shell)
        await until("observed write") { reloads == 5 }
        XCTAssertEqual(WidgetSnapshot.read(from: url)?.hosts.first?.windows.first { $0.block == Demo.shell }?.app, "swift")
    }

    /// A file of another format, or one that does not decode, reads as
    /// none; fields this build does not know are left alone.
    @MainActor func testReadOtherFormats() throws {
        let url = temp()
        defer { try? FileManager.default.removeItem(at: url.deletingLastPathComponent()) }
        XCTAssertNil(WidgetSnapshot.read(from: url), "no file")

        let s = WidgetSnapshot.sample(now: Date(timeIntervalSince1970: 1_800_000_000))
        try s.write(to: url)
        XCTAssertEqual(WidgetSnapshot.read(from: url), s)

        var json = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: url)) as? [String: Any])
        json["later"] = ["a field": 1]
        XCTAssertEqual(WidgetSnapshot.decode(try JSONSerialization.data(withJSONObject: json)), s)
        json["v"] = 2
        XCTAssertNil(WidgetSnapshot.decode(try JSONSerialization.data(withJSONObject: json)))
        json["v"] = "1"
        XCTAssertNil(WidgetSnapshot.decode(try JSONSerialization.data(withJSONObject: json)))
        XCTAssertNil(WidgetSnapshot.decode(Data(#"{"v":1,"written":"yesterday","hosts":[]}"#.utf8)))
        XCTAssertNil(WidgetSnapshot.decode(Data("not json".utf8)))
        XCTAssertNotNil(WidgetSnapshot.decode(Data(#"{"v":1,"written":"2026-11-20T09:30:00Z","hosts":[]}"#.utf8)))
    }

    /// A widget shows a snapshot as current for ten minutes after it was
    /// written, then stale; one it can't read only asks to open the app.
    @MainActor func testTimeline() {
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        let fresh = InboxEntry.timeline(.sample(now: now - 60), now: now)
        XCTAssertEqual(fresh.map(\.date), [now, now - 60 + 600])
        XCTAssertEqual(fresh.map(\.stale), [false, true])
        let old = InboxEntry.timeline(.sample(now: now - 601), now: now)
        XCTAssertEqual(old.map(\.stale), [true])
        let none = InboxEntry.timeline(nil, now: now)
        XCTAssertEqual(none.count, 1)
        XCTAssertNil(none[0].snapshot)
    }

    /// The counts a widget shows, the most urgent first.
    @MainActor func testCounts() {
        let s = WidgetSnapshot.sample(now: .now)
        XCTAssertEqual(s.counts(), "1 waiting · 1 failed · 1 done · 2 working")
        XCTAssertEqual(s.counts(max: 2), "1 waiting · 1 failed")
        XCTAssertNil(WidgetSnapshot(written: .now, hosts: []).counts())
        XCTAssertEqual(s.items.map(\.item.state), ["blocked", "error", "done", "working", "working"])
        XCTAssertEqual(s.away.map(\.name), ["bob-desktop"])
    }

    /// rexgo://open and rexgo://inbox read back as written; the pairing
    /// code is neither, and they are no pairing code.
    @MainActor func testLinks() throws {
        let id = UUID()
        let open = WidgetLink.open(host: id, session: "s 1", block: "b&2")
        XCTAssertEqual(WidgetLink(url: open.url), open)
        XCTAssertEqual(WidgetLink(url: WidgetLink.inbox.url), .inbox)
        XCTAssertEqual(WidgetLink.inbox.url.absoluteString, "rexgo://inbox")
        XCTAssertEqual(WidgetLink(url: try XCTUnwrap(URL(string: "REXGO://open?block=b&session=s&host=\(id.uuidString.lowercased())"))),
                       .open(host: id, session: "s", block: "b"))
        for bad in ["rexgo://open?host=x&session=s&block=b", "rexgo://open?host=\(id)&session=s", "rexgo://open?host=\(id)&session=&block=b",
                    "rexgo://other", "https://inbox", "rexgo://tailcat?a=tcx&n=mac"] {
            XCTAssertNil(WidgetLink(url: try XCTUnwrap(URL(string: bad))), bad)
        }
        XCTAssertNotNil(HostConfig(tailcat: "rexgo://tailcat?a=tcx&n=mac"))
        XCTAssertNil(HostConfig(tailcat: open.url.absoluteString))
        XCTAssertNil(HostConfig(tailcat: WidgetLink.inbox.url.absoluteString))
    }

    /// Draws each family, light and dark, of a made-up snapshot, as PNGs in
    /// GOREX_WIDGET_SHOTS; skipped without it. Drawn apart from WidgetKit:
    /// no Lock Screen tint, and the system's widget frame drawn here.
    @MainActor func testShots() throws {
        guard let dir = ProcessInfo.processInfo.environment["GOREX_WIDGET_SHOTS"] else { throw XCTSkip("GOREX_WIDGET_SHOTS not set") }
        let now = Date()
        let s = WidgetSnapshot.sample(now: now - 120)
        let families: [(String, WidgetFamily, CGSize)] = [
            ("small", .systemSmall, CGSize(width: 170, height: 170)),
            ("medium", .systemMedium, CGSize(width: 364, height: 170)),
            ("large", .systemLarge, CGSize(width: 364, height: 382)),
            ("circular", .accessoryCircular, CGSize(width: 76, height: 76)),
            ("rectangular", .accessoryRectangular, CGSize(width: 172, height: 76)),
            ("inline", .accessoryInline, CGSize(width: 257, height: 26)),
        ]
        let entries: [(String, InboxEntry)] = [
            ("", InboxEntry(date: now, snapshot: s)),
            ("-stale", InboxEntry(date: now, snapshot: s, stale: true)),
            ("-empty", InboxEntry(date: now, snapshot: WidgetSnapshot(written: now - 60, hosts: [s.hosts[0]].map { var h = $0; h.items = []; return h }))),
            ("-unread", InboxEntry(date: now, snapshot: nil)),
        ]
        try FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        for (name, family, size) in families {
            for (suffix, entry) in entries where suffix == "" || family == .systemMedium || family == .systemSmall {
                for scheme in [ColorScheme.light, .dark] {
                    let accessory = [.accessoryCircular, .accessoryRectangular, .accessoryInline].contains(family)
                    let face = InboxContent(entry: entry, family: family)
                        .padding(accessory ? 0 : 16)
                        .frame(width: size.width, height: size.height)
                        .background(accessory ? AnyShapeStyle(.clear) : AnyShapeStyle(.background), in: RoundedRectangle(cornerRadius: 22, style: .continuous))
                        .foregroundStyle(accessory ? AnyShapeStyle(.white) : AnyShapeStyle(.primary))
                        .padding(12)
                        .background(accessory ? Color(red: 0.16, green: 0.2, blue: 0.32) : scheme == .dark ? Color(white: 0.08) : Color(white: 0.85))
                        .environment(\.colorScheme, accessory ? .dark : scheme)
                    let r = ImageRenderer(content: face)
                    r.scale = 3
                    let png = try XCTUnwrap(r.uiImage?.pngData(), name)
                    try png.write(to: URL(filePath: dir).appending(path: "widget-\(name)\(suffix)-\(scheme == .dark ? "dark" : "light").png"))
                    if accessory { break }
                }
            }
        }
    }
}
