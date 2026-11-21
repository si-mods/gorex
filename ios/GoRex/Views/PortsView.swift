import SwiftUI
import UIKit

/// The ports a pane listens on, and the process that holds each: a tap
/// opens a port the phone can reach in Safari.
struct PortsView: View {
    let host: Host
    let target: TerminalTarget

    @Environment(\.dismiss) private var dismiss
    @State private var ports: Ports?
    @State private var failure: String?
    /// The relays opened from this sheet, by port.
    @State private var relays: [Int: URL] = [:]
    /// The port whose relay is opening.
    @State private var opening: Int?

    var body: some View {
        NavigationStack {
            List {
                if let failure {
                    Section {
                        Label(failure, systemImage: "exclamationmark.triangle")
                            .foregroundStyle(.secondary)
                    }
                }
                if let ports {
                    if ports.ports.isEmpty {
                        Section {
                            ContentUnavailableView("No listening ports", systemImage: "network.slash",
                                                   description: Text("A program in this pane is not listening on a TCP port."))
                        }
                    } else {
                        Section {
                            ForEach(ports.ports) { port in
                                row(port, in: ports)
                            }
                        } footer: {
                            if ports.status == "partial" {
                                Text("Some processes could not be checked.")
                            }
                        }
                    }
                }
            }
            .navigationTitle("Ports")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", systemImage: "xmark", role: .close) {
                        Task {
                            for port in relays.keys {
                                await host.closePort(session: target.session, block: target.block, port: port)
                            }
                            dismiss()
                        }
                    }
                }
            }
            .refreshable { await fetch() }
            .task { await poll() }
        }
    }

    /// One port: its address and process, and a tap that opens it when the
    /// phone can reach the address.
    @ViewBuilder private func row(_ port: Ports.Port, in ports: Ports) -> some View {
        let process = ports.process(of: port)
        let url = Ports.openURL(port: port, hostAddress: host.config.url.host())
        VStack(alignment: .leading, spacing: 6) {
            Button {
                if let url { UIApplication.shared.open(url) }
            } label: {
                VStack(alignment: .leading, spacing: 3) {
                    HStack(spacing: 6) {
                        Text(String(port.port)).font(.body.weight(.semibold).monospacedDigit())
                        Text("·").foregroundStyle(.tertiary)
                        Text(port.address).font(.subheadline).foregroundStyle(.secondary)
                        Spacer(minLength: 8)
                        if url != nil {
                            Image(systemName: "arrow.up.right.square").foregroundStyle(.secondary)
                        }
                    }
                    if let process {
                        Text(processLine(process, pid: port.pid))
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .disabled(url == nil)
            if url == nil {
                HStack(spacing: 8) {
                    Text("Only on the host")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Spacer(minLength: 8)
                    Button {
                        Task { await openRelay(port.port) }
                    } label: {
                        if opening == port.port {
                            ProgressView().controlSize(.small)
                        } else {
                            Label("Open on the phone", systemImage: "iphone.and.arrow.forward")
                        }
                    }
                    .font(.callout)
                    .disabled(opening != nil)
                }
            }
        }
        .contextMenu {
            Button("Copy Address", systemImage: "doc.on.doc") {
                UIPasteboard.general.string = "\(port.address):\(port.port)"
            }
        }
    }

    /// The line under a port: the program, and its process ID.
    private func processLine(_ process: Ports.Process, pid: Int?) -> String {
        var name = process.name
        if let script = process.script, !script.isEmpty {
            name = (script as NSString).lastPathComponent
        }
        if let pid { return "\(name) (\(pid))" }
        return name
    }

    /// Opens a relay to a loopback port and sends Safari to it.
    private func openRelay(_ port: Int) async {
        opening = port
        defer { opening = nil }
        do {
            let url: URL
            if let open = relays[port] {
                url = open
            } else {
                url = try await host.openPort(session: target.session, block: target.block, port: port)
                relays[port] = url
            }
            failure = nil
            await UIApplication.shared.open(url)
        } catch {
            failure = error.localizedDescription
        }
    }

    private func poll() async {
        while !Task.isCancelled {
            await fetch()
            try? await Task.sleep(for: .seconds(2))
        }
    }

    private func fetch() async {
        do {
            ports = try await host.ports(session: target.session, block: target.block)
            failure = nil
        } catch {
            failure = error.localizedDescription
        }
    }
}
