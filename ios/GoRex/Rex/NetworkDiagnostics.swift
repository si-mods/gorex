import Darwin
import Foundation
import Network
import Observation

/// Live network path and interface summary for Developer Mode.
/// Only monitors `NWPathMonitor` while Developer Mode is turned on.
@Observable
final class NetworkDiagnostics {
    struct Snapshot: Equatable {
        var isSatisfied = false
        var transportLabel = "Checking…"
        var interfaces: [String] = []
        var supportsIPv4 = false
        var supportsIPv6 = false
        var tailscaleInterface: String?
        var changedAt = Date.now

        /// Identity of the path without `changedAt`, so the age timer only
        /// resets when the network actually changes.
        var fingerprint: String {
            "\(isSatisfied)|\(transportLabel)|\(interfaces.joined(separator: ","))|\(supportsIPv4)|\(supportsIPv6)|\(tailscaleInterface ?? "")"
        }

        var ipLabel: String {
            switch (supportsIPv4, supportsIPv6) {
            case (true, true): "IPv4 + IPv6"
            case (true, false): "IPv4"
            case (false, true): "IPv6"
            case (false, false): "no IP"
            }
        }

        /// One combined path summary string, matching the Developer Mode HUD:
        /// `Wi-Fi + VPN · en0 Wi-Fi, pdp_ip0 cellular, utun5 tunnel · IPv4 · for 81.3s · no Tailscale interface`
        func summaryLine(now: Date, tailnetOn: Bool, tailnetState: String, tailnetIP: String?) -> String {
            var parts: [String] = [transportLabel]
            if !interfaces.isEmpty {
                parts.append(interfaces.joined(separator: ", "))
            }
            parts.append(ipLabel)
            parts.append("for \(DeveloperModeSummary.formatDuration(max(0, now.timeIntervalSince(changedAt))))")
            if tailnetOn {
                if let ip = tailnetIP, !ip.isEmpty {
                    parts.append("Tailnet \(tailnetState) (\(ip))")
                } else {
                    parts.append("Tailnet \(tailnetState)")
                }
            } else if let ts = tailscaleInterface {
                parts.append("Tailscale \(ts)")
            } else {
                parts.append("no Tailscale interface")
            }
            return parts.joined(separator: " · ")
        }
    }

    private(set) var snapshot = Snapshot()
    @ObservationIgnored private var monitor: NWPathMonitor?
    @ObservationIgnored private let queue = DispatchQueue(label: "dev.gorex.ios.devmode.path")

    func start() {
        guard monitor == nil else { return }
        let m = NWPathMonitor()
        m.pathUpdateHandler = { [weak self] path in
            let next = Self.makeSnapshot(from: path)
            Task { @MainActor [weak self] in
                self?.apply(next)
            }
        }
        monitor = m
        m.start(queue: queue)
    }

    func stop() {
        monitor?.cancel()
        monitor = nil
    }

    private func apply(_ next: Snapshot) {
        if next.fingerprint == snapshot.fingerprint { return }
        snapshot = next
    }

    nonisolated private static func makeSnapshot(from path: NWPath) -> Snapshot {
        let inspected = inspectSystemInterfaces()
        var ifaceList: [String] = []
        var hasWifi = false
        var hasCellular = false
        var hasWired = false
        var hasTunnel = !inspected.tunnels.isEmpty

        for iface in path.availableInterfaces {
            let label: String
            switch iface.type {
            case .wifi:
                hasWifi = true
                label = "\(iface.name) Wi-Fi"
            case .cellular:
                hasCellular = true
                label = "\(iface.name) cellular"
            case .wiredEthernet:
                hasWired = true
                label = "\(iface.name) Ethernet"
            case .other:
                if iface.name.hasPrefix("utun") || iface.name.hasPrefix("ipsec") || iface.name.hasPrefix("ppp") {
                    hasTunnel = true
                    label = "\(iface.name) tunnel"
                } else {
                    label = "\(iface.name) other"
                }
            case .loopback:
                continue
            @unknown default:
                label = iface.name
            }
            if !ifaceList.contains(label) {
                ifaceList.append(label)
            }
        }

        for tun in inspected.tunnels {
            let label = "\(tun) tunnel"
            if !ifaceList.contains(label) {
                ifaceList.append(label)
            }
        }

        let transport: String
        if path.status != .satisfied {
            transport = "Offline"
        } else {
            var base: String
            if path.usesInterfaceType(.wifi) || hasWifi {
                base = "Wi-Fi"
            } else if path.usesInterfaceType(.wiredEthernet) || hasWired {
                base = "Ethernet"
            } else if path.usesInterfaceType(.cellular) || hasCellular {
                base = "Cellular"
            } else {
                base = "Online"
            }
            if hasTunnel {
                base += " + VPN"
            }
            transport = base
        }

        return Snapshot(
            isSatisfied: path.status == .satisfied,
            transportLabel: transport,
            interfaces: ifaceList,
            supportsIPv4: path.supportsIPv4 || inspected.hasIPv4,
            supportsIPv6: path.supportsIPv6 || inspected.hasIPv6,
            tailscaleInterface: inspected.tailscaleInterface,
            changedAt: .now
        )
    }

    struct InterfaceInspection: Equatable {
        var tunnels: [String]
        var tailscaleInterface: String?
        var hasIPv4 = false
        var hasIPv6 = false
    }

    /// Reads active interfaces via `getifaddrs` to detect routable VPN tunnels
    /// (`utun*` with a non-link-local address), IPv4/IPv6 presence, and a system
    /// Tailscale VPN interface (100.64.0.0/10 or fd7a:115c:a1e0::/48).
    nonisolated static func inspectSystemInterfaces() -> InterfaceInspection {
        var ifaddr: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&ifaddr) == 0, let first = ifaddr else {
            return InterfaceInspection(tunnels: [], tailscaleInterface: nil)
        }
        defer { freeifaddrs(first) }

        var tunnels: [String] = []
        var tailscale: String?
        var hasIPv4 = false
        var hasIPv6 = false
        var cursor: UnsafeMutablePointer<ifaddrs>? = first
        while let current = cursor {
            defer { cursor = current.pointee.ifa_next }
            let flags = Int32(current.pointee.ifa_flags)
            guard (flags & IFF_UP) != 0, (flags & IFF_RUNNING) != 0, (flags & IFF_LOOPBACK) == 0,
                  let namePtr = current.pointee.ifa_name,
                  let addr = current.pointee.ifa_addr
            else { continue }

            let name = String(cString: namePtr)
            let family = Int32(addr.pointee.sa_family)
            guard family == AF_INET || family == AF_INET6 else { continue }
            guard !isLinkLocal(addr) else { continue }

            if family == AF_INET { hasIPv4 = true }
            if family == AF_INET6 { hasIPv6 = true }

            if name.hasPrefix("utun") {
                if !tunnels.contains(name) {
                    tunnels.append(name)
                }
                if tailscale == nil, isTailscaleAddress(addr) {
                    tailscale = name
                }
            }
        }
        return InterfaceInspection(tunnels: tunnels, tailscaleInterface: tailscale, hasIPv4: hasIPv4, hasIPv6: hasIPv6)
    }

    nonisolated private static func isLinkLocal(_ addr: UnsafeMutablePointer<sockaddr>) -> Bool {
        let family = Int32(addr.pointee.sa_family)
        if family == AF_INET {
            let sin = addr.withMemoryRebound(to: sockaddr_in.self, capacity: 1) { $0.pointee }
            let ip = UInt32(bigEndian: sin.sin_addr.s_addr)
            return ((ip >> 24) & 0xFF) == 169 && ((ip >> 16) & 0xFF) == 254
        }
        if family == AF_INET6 {
            let sin6 = addr.withMemoryRebound(to: sockaddr_in6.self, capacity: 1) { $0.pointee }
            let bytes = withUnsafeBytes(of: sin6.sin6_addr) { Array($0) }
            return bytes.count >= 2 && bytes[0] == 0xFE && (bytes[1] & 0xC0) == 0x80
        }
        return false
    }

    nonisolated private static func isTailscaleAddress(_ addr: UnsafeMutablePointer<sockaddr>) -> Bool {
        let family = Int32(addr.pointee.sa_family)
        if family == AF_INET {
            let sin = addr.withMemoryRebound(to: sockaddr_in.self, capacity: 1) { $0.pointee }
            let ip = UInt32(bigEndian: sin.sin_addr.s_addr)
            let b0 = (ip >> 24) & 0xFF
            let b1 = (ip >> 16) & 0xFF
            return b0 == 100 && (b1 & 0xC0) == 64
        }
        if family == AF_INET6 {
            let sin6 = addr.withMemoryRebound(to: sockaddr_in6.self, capacity: 1) { $0.pointee }
            let bytes = withUnsafeBytes(of: sin6.sin6_addr) { Array($0) }
            return bytes.starts(with: [0xFD, 0x7A, 0x11, 0x5C, 0xA1, 0xE0])
        }
        return false
    }
}

/// Pure summary & report helpers for the Developer Mode overlay.
struct DeveloperModeSummary: Equatable {
    enum Tone: Equatable {
        case ready
        case reconnecting
        case offline
        case idle
    }

    let tone: Tone
    let primaryText: String
    let secondaryText: String?
    let hasActiveTimer: Bool

    static func formatDuration(_ seconds: TimeInterval) -> String {
        String(format: "%.1fs", max(0, seconds))
    }

    static func compute(hosts: [Host], now: Date) -> DeveloperModeSummary {
        guard !hosts.isEmpty else {
            return DeveloperModeSummary(tone: .idle, primaryText: "No hosts", secondaryText: nil, hasActiveTimer: false)
        }
        var ready = 0
        var reconnecting = 0
        var offline = 0
        var maxReconnectElapsed: TimeInterval = 0
        var maxPingMs: Int?

        for h in hosts {
            switch h.devStatus {
            case .ready:
                ready += 1
                if let p = h.pingMs {
                    maxPingMs = max(maxPingMs ?? p, p)
                }
            case .reconnecting, .connecting:
                reconnecting += 1
                if let since = h.reconnectSince {
                    maxReconnectElapsed = max(maxReconnectElapsed, now.timeIntervalSince(since))
                }
            case .offline:
                offline += 1
            }
        }

        if reconnecting > 0 {
            var parts = [formatDuration(maxReconnectElapsed)]
            if ready > 0 { parts.append("+\(ready) ready") }
            if offline > 0 { parts.append("+\(offline) offline") }
            return DeveloperModeSummary(
                tone: .reconnecting,
                primaryText: "\(reconnecting) reconnecting",
                secondaryText: parts.joined(separator: " "),
                hasActiveTimer: true
            )
        }

        if offline > 0 {
            let secondary = ready > 0 ? "+\(ready) ready" : nil
            return DeveloperModeSummary(
                tone: .offline,
                primaryText: "\(offline) offline",
                secondaryText: secondary,
                hasActiveTimer: false
            )
        }

        let secondary = maxPingMs.map { "\($0) ms" }
        return DeveloperModeSummary(
            tone: .ready,
            primaryText: "\(ready) ready",
            secondaryText: secondary,
            hasActiveTimer: false
        )
    }

    /// Plain-text diagnostic report for Copy and Share actions.
    static func diagnosticReport(
        version: String,
        build: String,
        system: String,
        networkLine: String,
        hosts: [Host],
        entries: [ConnectionLog.Entry],
        now: Date = .now
    ) -> String {
        var lines: [String] = []
        lines.append("Rex Go \(version) (\(build)), \(system)")
        lines.append(now.ISO8601Format())
        lines.append("")
        lines.append("Network")
        lines.append(networkLine)
        lines.append("")
        lines.append("Hosts (\(hosts.count))")
        for h in hosts {
            let detail: String
            switch h.devStatus {
            case .ready:
                let conn = h.connectDurationMs.map { "connect \($0) ms" } ?? (h.isDemo ? "demo" : "ready")
                let ping = h.pingMs.map { ", ping \($0) ms" } ?? ""
                detail = "\(conn)\(ping)"
            case .reconnecting:
                let elapsed = h.reconnectSince.map { formatDuration(now.timeIntervalSince($0)) } ?? "0.0s"
                detail = "reconnecting \(elapsed)"
            case .connecting:
                let elapsed = h.reconnectSince.map { formatDuration(now.timeIntervalSince($0)) } ?? "0.0s"
                detail = "connecting \(elapsed)"
            case .offline:
                if case .offline(let why) = h.status, !why.isEmpty {
                    detail = "offline (\(why))"
                } else {
                    detail = "offline"
                }
            }
            let transport = h.config.reach(tailnetOn: TailnetSettings.isOn).rawValue
            lines.append("- \(h.config.name) [\(transport)] \(h.config.url.absoluteString): \(detail)")
        }
        lines.append("")
        lines.append("Events (\(entries.count))")
        for e in entries.reversed() {
            lines.append("\(e.shortTime) \(e.message)")
        }
        return lines.joined(separator: "\n")
    }
}
