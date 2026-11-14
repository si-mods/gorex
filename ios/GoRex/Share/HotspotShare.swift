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
        hotspotIP = TailnetShareHotspotIP()
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
        startOrRestartListener()
    }

    func stop() {
        isSharing = false
        stopMonitor()
        endBackgroundTask()
        TailnetShareStop()
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
                transport: (c.transport ?? .system).rawValue
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
        if !TailnetShareAlive() {
            startOrRestartListener()
        }
        reloadDevices()
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

    private func startOrRestartListener() {
        refreshHotspotIP()
        guard !hotspotIP.isEmpty else {
            TailnetShareStop()
            return
        }
        let addr = "\(hotspotIP):\(Self.port)"
        let webDir = WebClientStore.shared.directory.path
        let phone = UIDevice.current.name
        var err: NSError?
        if TailnetShareStart(addr, webDir, phone, self, &err) {
            error = nil
            updateHosts(lastHosts)
        } else {
            error = err?.localizedDescription
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
                let oldIP = self.hotspotIP
                self.refreshHotspotIP()
                if self.hotspotIP != oldIP || (!self.hotspotIP.isEmpty && !TailnetShareAlive()) {
                    self.startOrRestartListener()
                }
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
