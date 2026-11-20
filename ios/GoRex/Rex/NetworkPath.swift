import Foundation
import Network
import Observation

/// The network the hosts' connections go over, watched from launch for
/// the app's life: a change of the interface or of its gateways, as
/// TailnetEngine.PathMonitor tells them, asks the hosts and the open
/// terminal to reconnect at once (docs/IOS-012-background-and-network-changes.md).
/// The first network told is no change. A change reconnects at once; the
/// changes in the second after it give one more reconnect, at that
/// second's end, not one each. The engine's monitor, Diagnostics' and the
/// hotspot's keep their own, which have other lifetimes.
@Observable final class NetworkPath {
    /// Starts telling each network as it is: its interface's name, "" for
    /// none, and what makes it this network, the name and the gateways.
    /// Gives what to keep for it to go on.
    typealias Source = (_ told: @escaping @MainActor @Sendable (_ name: String, _ network: String) -> Void) -> AnyObject
    typealias Schedule = (TimeInterval, @escaping @MainActor () -> Void) -> Void

    /// The app's: the system's network.
    static let shared = NetworkPath(source: NetworkPath.system)

    /// Goes up on each reconnect a change of network asks for.
    private(set) var changes = 0

    @ObservationIgnored private let source: Source
    @ObservationIgnored private let schedule: Schedule
    @ObservationIgnored private var watching: AnyObject?
    /// The last network told; nil before the first.
    @ObservationIgnored private var told: String?
    /// Within the second after a reconnect.
    @ObservationIgnored private var quiet = false
    /// The interface's name of a change in that second, to reconnect at its end.
    @ObservationIgnored private var pending: String?

    init(source: @escaping Source, schedule: @escaping Schedule = WidgetPublisher.after) {
        self.source = source
        self.schedule = schedule
    }

    /// Starts watching, once.
    func start() {
        guard watching == nil else { return }
        watching = source { [weak self] name, network in self?.see(name: name, network: network) }
    }

    private func see(name: String, network: String) {
        defer { told = network }
        guard let last = told, last != network else { return }
        if quiet {
            pending = name
        } else {
            reconnect(name)
        }
    }

    private func reconnect(_ name: String) {
        ConnectionLog.note("network changed: \(name.isEmpty ? "none" : name)")
        changes += 1
        quiet = true
        schedule(1) { [weak self] in
            guard let self else { return }
            self.quiet = false
            // One more for all the second's changes.
            if let name = self.pending {
                self.pending = nil
                self.reconnect(name)
            }
        }
    }

    /// The system's network, with the interface filter and the notion of a
    /// network of TailnetEngine.PathMonitor.update. The gateways stay out
    /// of the log: they are addresses.
    nonisolated private static func system(_ told: @escaping @MainActor @Sendable (String, String) -> Void) -> AnyObject {
        let m = NWPathMonitor(prohibitedInterfaceTypes: [.other, .loopback])
        m.pathUpdateHandler = { path in
            let name = path.status == .satisfied
                ? path.availableInterfaces.first { [.wifi, .wiredEthernet, .cellular].contains($0.type) }?.name ?? ""
                : ""
            let network = "\(name) \(name.isEmpty ? [] : path.gateways)"
            Task { @MainActor in told(name, network) }
        }
        m.start(queue: DispatchQueue(label: "dev.gorex.ios.network.path"))
        return m
    }
}
