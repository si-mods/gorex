import CoreImage
import PhotosUI
import SwiftUI
import VisionKit

/// The camera, looking for a QR code. Each code it sees goes to onScan,
/// which returns why it isn't one to pair with, or nil when it is. The
/// toolbar's photo button reads the codes of a picture instead: a user
/// who has the code as a screenshot scans it the same way.
struct QRScanner: View {
    let onScan: (String) -> String?
    @Environment(\.dismiss) private var dismiss
    @State private var problem: String?
    @State private var photo: PhotosPickerItem?

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
                        .glassEffect(.regular, in: .rect(cornerRadius: 20))
                        .padding(20)
                }
            }
            .navigationTitle("Scan QR Code")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel", systemImage: "xmark", role: .cancel) { dismiss() } }
                ToolbarItem(placement: .primaryAction) {
                    PhotosPicker(selection: $photo, matching: .images, photoLibrary: .shared()) {
                        Image(systemName: "photo")
                    }
                    .accessibilityLabel("Choose Photo")
                }
            }
            .onChange(of: photo) { _, item in Task { await picked(item) } }
        }
        #if DEBUG
        // The simulator has no camera: -pair <code> stands in for a scan.
        .task { if let code = UserDefaults.standard.string(forKey: "pair") { scanned(code) } }
        #endif
    }

    private func scanned(_ code: String) {
        problem = onScan(code)
    }

    /// Reads the photo picked: each QR code of it goes to onScan until
    /// one takes it, as the camera's codes do.
    private func picked(_ item: PhotosPickerItem?) async {
        // Back to nil, so picking the same picture again runs too.
        guard let item else { return }
        photo = nil
        guard let data = try? await item.loadTransferable(type: Data.self),
              let image = CIImage(data: data) else { return }
        let detector = CIDetector(ofType: CIDetectorTypeQRCode, context: nil, options: [CIDetectorAccuracy: CIDetectorAccuracyHigh])
        let codes = (detector?.features(in: image) ?? []).compactMap { ($0 as? CIQRCodeFeature)?.messageString }
        guard !codes.isEmpty else {
            problem = String(localized: "No QR code in this photo.")
            return
        }
        for code in codes {
            guard let why = onScan(code) else { return }
            problem = why
        }
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
