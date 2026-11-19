import Foundation
import Network
import Observation
import Tailnet
import UIKit

/// Manages sharing the GoRex web client over Personal Hotspot (docs/ios-web-share.md).
@MainActor
@Observable
final class HotspotShare: NSObject, TailnetShareDelegateProtocol {
    static let shared = HotspotShare()
    static let port = 7300

    struct Device: Codable, Identifiable, Equatable {
        let ip: String
        let userAgent: String
        let state: String
        let addedAt: Int64
        var id: String { ip }
    }

    struct PendingPrompt: Identifiable, Equatable {
        let ip: String
        let userAgent: String
        var id: String { ip }
    }

    private struct ShareHostPayload: Encodable {
        let id: String
        let name: String
        let icon: String
        let url: String
        let transport: String
        /// The LAN gateway's token: the share proxy sends it to the host
        /// as Authorization: Bearer and never gives it to the browser.
        let token: String?
    }

    private(set) var isSharing = false
    private(set) var hotspotIP = ""
    private(set) var error: String?
    private(set) var devices: [Device] = []
    var pendingPrompt: PendingPrompt?

    private var pendingQueue: [PendingPrompt] = []
    private var monitor: NWPathMonitor?
    private var bgTask: UIBackgroundTaskIdentifier = .invalid
    private var lastHosts: [HostConfig] = []
    /// The TailnetShare* calls that can block (a 200 ms dial, the network
    /// interfaces, a listener's start and stop) run off the main actor, in
    /// the order they were asked for (#860).
    @ObservationIgnored private let calls = ShareCalls()

    var shareURL: String? {
        guard !hotspotIP.isEmpty else { return nil }
        return "http://\(hotspotIP):\(Self.port)"
    }

    var admittedDevices: [Device] {
        devices.filter { $0.state == "admitted" }.sorted { $0.addedAt < $1.addedAt }
    }

    private override init() {
        super.init()
        refreshHotspotIP()
    }

    func refreshHotspotIP() {
        calls.add { await self.loadHotspotIP() }
    }

    func start(configs: [HostConfig]) async {
        error = nil
        lastHosts = configs
        guard await WebClientStore.shared.ensureReady() else {
            if case .failed(let msg) = WebClientStore.shared.state {
                error = msg
            } else {
                error = String(localized: "Failed to download web client.")
            }
            return
        }

        isSharing = true
        startMonitor()
        await calls.add { await self.startOrRestartListener() }.value
    }

    func stop() {
        isSharing = false
        stopMonitor()
        endBackgroundTask()
        calls.add {
            await ShareCalls.offMain { TailnetShareStop() }
            // A device change the server told before it stopped.
            if !self.isSharing { self.devices = [] }
        }
        devices = []
        pendingQueue = []
        pendingPrompt = nil
        error = nil
    }

    func updateHosts(_ configs: [HostConfig]) {
        lastHosts = configs
        guard isSharing else { return }
        let payload = configs.map { c in
            ShareHostPayload(
                id: c.id.uuidString,
                name: c.name,
                icon: c.icon ?? "laptop",
                url: c.url.absoluteString,
                transport: (c.transport ?? .system).rawValue,
                token: c.auth
            )
        }
        guard let data = try? JSONEncoder().encode(payload),
              let json = String(data: data, encoding: .utf8) else { return }
        var err: NSError?
        TailnetShareSetHosts(json, &err)
    }

    func admit(_ ip: String) {
        TailnetShareAdmit(ip)
        advancePrompt(handled: ip)
        reloadDevices()
    }

    func deny(_ ip: String) {
        TailnetShareDeny(ip)
        advancePrompt(handled: ip)
        reloadDevices()
    }

    func revoke(_ ip: String) {
        TailnetShareRevoke(ip)
        reloadDevices()
    }

    func handleBackground() {
        guard isSharing else { return }
        endBackgroundTask()
        bgTask = UIApplication.shared.beginBackgroundTask(withName: "GoRexHotspotShare") { [weak self] in
            Task { @MainActor in
                self?.endBackgroundTask()
            }
        }
    }

    func handleForeground() {
        endBackgroundTask()
        refreshHotspotIP()
        guard isSharing else { return }
        calls.add {
            guard self.isSharing else { return }
            if !(await ShareCalls.offMain { TailnetShareAlive() }) {
                await self.startOrRestartListener()
            }
            self.reloadDevices()
        }
    }

    // MARK: - TailnetShareDelegateProtocol

    nonisolated func onPending(_ ip: String?, userAgent: String?) {
        guard let ip, !ip.isEmpty else { return }
        let ua = userAgent ?? "Browser"
        Task { @MainActor in
            let prompt = PendingPrompt(ip: ip, userAgent: ua)
            if self.pendingPrompt == nil {
                self.pendingPrompt = prompt
            } else if self.pendingPrompt?.ip != ip && !self.pendingQueue.contains(where: { $0.ip == ip }) {
                self.pendingQueue.append(prompt)
            }
            self.reloadDevices()
        }
    }

    nonisolated func onChange() {
        Task { @MainActor in
            self.reloadDevices()
        }
    }

    // MARK: - Private

    /// Reads the hotspot's address off the main actor. Only a ShareCalls
    /// operation calls it.
    @discardableResult
    private func loadHotspotIP() async -> String {
        hotspotIP = await ShareCalls.offMain { TailnetShareHotspotIP() }
        return hotspotIP
    }

    /// Only a ShareCalls operation calls it, so that it runs in order with
    /// the other Start and Stop calls.
    private func startOrRestartListener() async {
        guard isSharing else { return }
        let ip = await loadHotspotIP()
        guard !ip.isEmpty else {
            await ShareCalls.offMain { TailnetShareStop() }
            return
        }
        let addr = "\(ip):\(Self.port)"
        let webDir = WebClientStore.shared.directory.path
        let phone = UIDevice.current.name
        // Go calls the delegate on its own threads already; its methods are
        // nonisolated.
        let delegate = UncheckedDelegate(value: self)
        let (ok, message) = await ShareCalls.offMain { () -> (Bool, String?) in
            var err: NSError?
            let ok = TailnetShareStart(addr, webDir, phone, delegate.value, &err)
            return (ok, err?.localizedDescription)
        }
        if ok {
            error = nil
            updateHosts(lastHosts)
        } else {
            error = message
        }
    }

    /// A path change: start the listener again on a new address, or when it
    /// stopped listening. Only a ShareCalls operation calls it.
    private func pathChanged() async {
        guard isSharing else { return }
        let oldIP = hotspotIP
        let ip = await loadHotspotIP()
        var restart = ip != oldIP
        if !restart && !ip.isEmpty {
            restart = !(await ShareCalls.offMain { TailnetShareAlive() })
        }
        if restart {
            await startOrRestartListener()
        }
    }

    private func reloadDevices() {
        let raw = TailnetShareDevicesJSON()
        guard let data = raw.data(using: .utf8),
              let decoded = try? JSONDecoder().decode([Device].self, from: data) else {
            return
        }
        devices = decoded
    }

    private func advancePrompt(handled ip: String) {
        if pendingPrompt?.ip == ip {
            pendingPrompt = pendingQueue.isEmpty ? nil : pendingQueue.removeFirst()
        } else {
            pendingQueue.removeAll { $0.ip == ip }
        }
    }

    private func startMonitor() {
        stopMonitor()
        let m = NWPathMonitor()
        m.pathUpdateHandler = { [weak self] _ in
            Task { @MainActor in
                guard let self, self.isSharing else { return }
                self.calls.add { await self.pathChanged() }
            }
        }
        m.start(queue: DispatchQueue(label: "dev.gorex.share.path"))
        monitor = m
    }

    private func stopMonitor() {
        monitor?.cancel()
        monitor = nil
    }

    private func endBackgroundTask() {
        guard bgTask != .invalid else { return }
        UIApplication.shared.endBackgroundTask(bgTask)
        bgTask = .invalid
    }
}

/// Carries the share's delegate into a Go call off the main actor.
private nonisolated struct UncheckedDelegate: @unchecked Sendable {
    let value: any TailnetShareDelegateProtocol
}
