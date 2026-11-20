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
                    Button("Close", systemImage: "xmark", role: .close) { dismiss() }
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
        Button {
            if let url { UIApplication.shared.open(url) }
        } label: {
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text("\(port.port)").font(.body.weight(.semibold).monospacedDigit())
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
                if url == nil {
                    Text("Only on the host")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(url == nil)
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
