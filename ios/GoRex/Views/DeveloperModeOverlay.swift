import SwiftUI
import UIKit

/// Floating Developer Mode HUD: collapses to a compact glass pill below the
/// navigation bar and expands into a live connection card with network path
/// summary, per-host status/RTT, scrolling event log, and Share/Copy/Clear.
struct DeveloperModeOverlay: View {
    @Environment(HostStore.self) private var store
    @Environment(\.colorScheme) private var colorScheme
    @AppStorage(TailnetSettings.onKey) private var tailnetOn = false
    @State private var expanded = false
    @State private var diagnostics = NetworkDiagnostics()
    @State private var copied = false

    private let logStore = ConnectionLog.store
    private let tailnet = TailnetEngine.shared

    private static let amber = Color(red: 0.92, green: 0.68, blue: 0.30)
    private static let green = Color(red: 0.36, green: 0.85, blue: 0.48)
    private static let red = Color(red: 0.96, green: 0.38, blue: 0.38)

    private var needsLiveTick: Bool {
        expanded || store.hosts.contains { $0.devStatus == .reconnecting || $0.devStatus == .connecting }
    }

    var body: some View {
        Group {
            if needsLiveTick {
                TimelineView(.periodic(from: .now, by: 0.1)) { context in
                    content(now: context.date)
                }
            } else {
                content(now: .now)
            }
        }
        .onAppear { diagnostics.start() }
        .onDisappear { diagnostics.stop() }
        .task(id: expanded) {
            guard expanded else { return }
            while !Task.isCancelled {
                await pingConnectedHosts()
                try? await Task.sleep(for: .seconds(5))
            }
        }
        .task(id: copied) {
            guard copied else { return }
            try? await Task.sleep(for: .seconds(1.5))
            copied = false
        }
        .sensoryFeedback(.success, trigger: copied) { _, new in new }
    }

    @ViewBuilder
    private func content(now: Date) -> some View {
        let summary = DeveloperModeSummary.compute(hosts: store.hosts, now: now)
        if expanded {
            expandedCard(summary: summary, now: now)
                .transition(.opacity.combined(with: .scale(scale: 0.96, anchor: .top)))
        } else {
            collapsedPill(summary: summary)
                .transition(.opacity.combined(with: .scale(scale: 0.96, anchor: .top)))
        }
    }

    private func collapsedPill(summary: DeveloperModeSummary) -> some View {
        Button {
            withAnimation(.spring(duration: 0.28, bounce: 0.14)) {
                expanded = true
            }
        } label: {
            HStack(spacing: 8) {
                statusDot(color: toneColor(summary.tone), halo: true)
                Text(summary.primaryText)
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(.primary)
                if let secondary = summary.secondaryText {
                    Text(secondary)
                        .font(.system(size: 13, weight: .regular).monospacedDigit())
                        .foregroundStyle(.secondary)
                }
                Image(systemName: "chevron.down")
                    .font(.system(size: 11, weight: .semibold))
                    .foregroundStyle(.secondary)
            }
            .lineLimit(1)
            .padding(.horizontal, 14)
            .padding(.vertical, 8)
            .glassEffect(.regular.interactive(), in: .capsule)
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("developer-mode-pill")
    }

    private func expandedCard(summary: DeveloperModeSummary, now: Date) -> some View {
        let networkLine = diagnostics.snapshot.summaryLine(
            now: now,
            tailnetOn: tailnetOn,
            tailnetState: tailnet.status.state,
            tailnetIP: tailnet.status.ips?.first
        )
        let entries = logStore.entries
        let report = makeReport(networkLine: networkLine, entries: entries, now: now)

        return VStack(alignment: .leading, spacing: 12) {
            // Header row
            HStack(spacing: 8) {
                statusDot(color: toneColor(summary.tone), halo: true)
                Text(summary.primaryText)
                    .font(.system(size: 15, weight: .semibold))
                    .foregroundStyle(.primary)
                if let secondary = summary.secondaryText {
                    Text(secondary)
                        .font(.system(size: 15, weight: .regular).monospacedDigit())
                        .foregroundStyle(.secondary)
                }
                Spacer(minLength: 8)
                Button {
                    withAnimation(.spring(duration: 0.26, bounce: 0.12)) {
                        expanded = false
                    }
                } label: {
                    Image(systemName: "chevron.up")
                        .font(.system(size: 12, weight: .semibold))
                        .foregroundStyle(.secondary)
                        .frame(width: 28, height: 28)
                        .background(Color.primary.opacity(0.10), in: Circle())
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Collapse Developer Mode")
                .accessibilityIdentifier("developer-mode-collapse")
            }

            // Network path summary
            Text(networkLine)
                .font(.system(size: 12).monospacedDigit())
                .foregroundStyle(.secondary)
                .lineSpacing(2)
                .fixedSize(horizontal: false, vertical: true)

            // Hosts list card
            if !store.hosts.isEmpty {
                VStack(spacing: 0) {
                    ForEach(Array(store.hosts.enumerated()), id: \.element.id) { index, host in
                        if index > 0 {
                            Divider().overlay(Color.primary.opacity(0.08))
                        }
                        hostRow(host, now: now)
                    }
                }
                .background(
                    Color.primary.opacity(colorScheme == .dark ? 0.055 : 0.04),
                    in: RoundedRectangle(cornerRadius: 14, style: .continuous)
                )
                .overlay(
                    RoundedRectangle(cornerRadius: 14, style: .continuous)
                        .strokeBorder(Color.primary.opacity(0.08), lineWidth: 0.5)
                )
            }

            // Live connection log (newest first)
            ScrollView {
                if entries.isEmpty {
                    Text("No connection events yet.")
                        .font(.system(size: 11, design: .monospaced))
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.vertical, 4)
                } else {
                    LazyVStack(alignment: .leading, spacing: 6) {
                        ForEach(entries.reversed()) { entry in
                            HStack(alignment: .top, spacing: 10) {
                                Text(entry.shortTime)
                                    .font(.system(size: 11, design: .monospaced))
                                    .foregroundStyle(.secondary)
                                    .frame(width: 68, alignment: .leading)
                                Text(entry.message)
                                    .font(.system(size: 11, design: .monospaced))
                                    .foregroundStyle(.primary.opacity(0.88))
                                    .fixedSize(horizontal: false, vertical: true)
                                    .frame(maxWidth: .infinity, alignment: .leading)
                            }
                        }
                    }
                    .padding(.vertical, 2)
                }
            }
            .frame(maxHeight: 140)

            // Bottom action bar: Share / Copy / Clear
            HStack(spacing: 10) {
                ShareLink(item: report) {
                    Label("Share", systemImage: "square.and.arrow.up")
                        .font(.system(size: 14, weight: .medium))
                        .foregroundStyle(.primary)
                        .frame(maxWidth: .infinity, minHeight: 42)
                        .background(
                            Color.primary.opacity(colorScheme == .dark ? 0.11 : 0.07),
                            in: RoundedRectangle(cornerRadius: 12, style: .continuous)
                        )
                }
                .buttonStyle(.plain)
                .accessibilityIdentifier("developer-mode-share")

                Button {
                    UIPasteboard.general.string = report
                    copied = true
                } label: {
                    Label(copied ? "Copied" : "Copy", systemImage: copied ? "checkmark" : "doc.on.doc")
                        .font(.system(size: 14, weight: .medium))
                        .foregroundStyle(.primary)
                        .frame(maxWidth: .infinity, minHeight: 42)
                        .background(
                            Color.primary.opacity(colorScheme == .dark ? 0.11 : 0.07),
                            in: RoundedRectangle(cornerRadius: 12, style: .continuous)
                        )
                }
                .buttonStyle(.plain)
                .accessibilityIdentifier("developer-mode-copy")

                Button {
                    ConnectionLog.clear()
                } label: {
                    Text("Clear")
                        .font(.system(size: 14, weight: .medium))
                        .foregroundStyle(Self.red)
                        .frame(maxWidth: .infinity, minHeight: 42)
                        .background(
                            Self.red.opacity(colorScheme == .dark ? 0.16 : 0.12),
                            in: RoundedRectangle(cornerRadius: 12, style: .continuous)
                        )
                }
                .buttonStyle(.plain)
                .accessibilityIdentifier("developer-mode-clear")
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 12)
        .frame(maxWidth: 480)
        .background {
            RoundedRectangle(cornerRadius: 22, style: .continuous)
                .fill(.ultraThinMaterial)
                .overlay(
                    RoundedRectangle(cornerRadius: 22, style: .continuous)
                        .fill(colorScheme == .dark
                            ? Color(red: 0.11, green: 0.12, blue: 0.14).opacity(0.72)
                            : Color(.systemBackground).opacity(0.65))
                )
        }
        .overlay(
            RoundedRectangle(cornerRadius: 22, style: .continuous)
                .strokeBorder(Color.primary.opacity(colorScheme == .dark ? 0.16 : 0.10), lineWidth: 0.5)
        )
        .shadow(color: .black.opacity(colorScheme == .dark ? 0.35 : 0.12), radius: 18, y: 8)
        .padding(.horizontal, 12)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("developer-mode-panel")
    }

    private func hostRow(_ host: Host, now: Date) -> some View {
        let metric = hostMetric(host, now: now)
        return HStack(spacing: 12) {
            statusDot(color: hostColor(host.devStatus), halo: false)
            VStack(alignment: .leading, spacing: 2) {
                Text(host.config.name)
                    .font(.system(size: 15, weight: .semibold))
                    .foregroundStyle(.primary)
                    .lineLimit(1)
                Text(hostSubtitle(host, now: now))
                    .font(.system(size: 12).monospacedDigit())
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 8)
            if let metric {
                Text(metric.text)
                    .font(.system(size: 14, weight: .medium).monospacedDigit())
                    .foregroundStyle(metric.color)
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 10)
    }

    private func statusDot(color: Color, halo: Bool) -> some View {
        ZStack {
            if halo {
                Circle()
                    .fill(color.opacity(0.28))
                    .frame(width: 16, height: 16)
            }
            Circle()
                .fill(color)
                .frame(width: 8, height: 8)
        }
        .frame(width: halo ? 16 : 8, height: halo ? 16 : 8)
    }

    private func toneColor(_ tone: DeveloperModeSummary.Tone) -> Color {
        switch tone {
        case .ready: Self.green
        case .reconnecting: Self.amber
        case .offline: Self.red
        case .idle: .secondary
        }
    }

    private func hostColor(_ status: Host.DevStatus) -> Color {
        switch status {
        case .ready: Self.green
        case .reconnecting, .connecting: Self.amber
        case .offline: Self.red
        }
    }

    private func hostSubtitle(_ host: Host, now: Date) -> String {
        switch host.devStatus {
        case .ready:
            if host.isDemo { return "demo" }
            if let ms = host.connectDurationMs { return "connect \(ms) ms" }
            return "ready"
        case .reconnecting:
            let elapsed = host.reconnectSince.map { DeveloperModeSummary.formatDuration(now.timeIntervalSince($0)) } ?? "0.0s"
            return "reconnecting \(elapsed)"
        case .connecting:
            let elapsed = host.reconnectSince.map { DeveloperModeSummary.formatDuration(now.timeIntervalSince($0)) } ?? "0.0s"
            return "connecting \(elapsed)"
        case .offline:
            if case .offline(let why) = host.status, !why.isEmpty { return why }
            return "offline"
        }
    }

    private func hostMetric(_ host: Host, now: Date) -> (text: String, color: Color)? {
        switch host.devStatus {
        case .ready:
            if let ms = host.pingMs { return ("\(ms) ms", .primary) }
            if host.isDemo { return ("0 ms", .primary) }
            return nil
        case .reconnecting, .connecting:
            let elapsed = host.reconnectSince.map { DeveloperModeSummary.formatDuration(now.timeIntervalSince($0)) } ?? "0.0s"
            return (elapsed, Self.amber)
        case .offline:
            return nil
        }
    }

    private func pingConnectedHosts() async {
        let connections = store.hosts.compactMap { $0.status == .online ? $0.control : nil }
        await withTaskGroup(of: Void.self) { group in
            for c in connections {
                group.addTask { _ = await c.measurePing() }
            }
        }
    }

    private func makeReport(networkLine: String, entries: [ConnectionLog.Entry], now: Date) -> String {
        let build = Bundle.main.infoDictionary?["CFBundleVersion"] as? String ?? "0"
        let system = "\(UIDevice.current.systemName) \(UIDevice.current.systemVersion)"
        return DeveloperModeSummary.diagnosticReport(
            version: Bundle.main.version,
            build: build,
            system: system,
            networkLine: networkLine,
            hosts: store.hosts,
            entries: entries,
            now: now
        )
    }
}
