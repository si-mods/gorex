import SwiftUI
import VisionKit

/// The camera, looking for a QR code. Each code it sees goes to onScan,
/// which returns why it isn't one to pair with, or nil when it is.
struct QRScanner: View {
    let onScan: (String) -> String?
    @Environment(\.dismiss) private var dismiss
    @State private var problem: String?

    var body: some View {
        NavigationStack {
            Group {
                if DataScannerViewController.isSupported && DataScannerViewController.isAvailable {
                    Scanner(onCode: scanned).ignoresSafeArea()
                } else {
                    ContentUnavailableView("Camera Unavailable", systemImage: "camera",
                                           description: Text("Allow camera access in Settings, or set up the host manually."))
                }
            }
            .overlay(alignment: .bottom) {
                if let problem {
                    Text(problem).multilineTextAlignment(.center).padding(16)
                        .glass(in: RoundedRectangle(cornerRadius: 20, style: .continuous))
                        .padding(20)
                }
            }
            .navigationTitle("Scan QR Code")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } } }
        }
        #if DEBUG
        // The simulator has no camera: -pair <code> stands in for a scan.
        .task { if let code = UserDefaults.standard.string(forKey: "pair") { scanned(code) } }
        #endif
    }

    private func scanned(_ code: String) {
        problem = onScan(code)
    }
}

private struct Scanner: UIViewControllerRepresentable {
    let onCode: (String) -> Void

    func makeUIViewController(context: Context) -> DataScannerViewController {
        let vc = DataScannerViewController(recognizedDataTypes: [.barcode(symbologies: [.qr])], isHighlightingEnabled: true)
        vc.delegate = context.coordinator
        return vc
    }

    func updateUIViewController(_ vc: DataScannerViewController, context: Context) {
        context.coordinator.onCode = onCode
        if !vc.isScanning { try? vc.startScanning() }
    }

    func makeCoordinator() -> Coordinator { Coordinator(onCode: onCode) }

    final class Coordinator: NSObject, DataScannerViewControllerDelegate {
        var onCode: (String) -> Void

        init(onCode: @escaping (String) -> Void) { self.onCode = onCode }

        func dataScanner(_ scanner: DataScannerViewController, didAdd items: [RecognizedItem], allItems: [RecognizedItem]) {
            for case .barcode(let b) in items {
                if let s = b.payloadStringValue { onCode(s) }
            }
        }
    }
}
