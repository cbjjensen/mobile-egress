import SwiftUI
import Vision
import VisionKit

struct QRScannerView: View {
    let onCode: @MainActor (String) -> Void
    let onUnavailable: @MainActor () -> Void

    var body: some View {
        if DataScannerViewController.isSupported && DataScannerViewController.isAvailable {
            DataScannerController(onCode: onCode, onUnavailable: onUnavailable)
        } else {
            Color.clear.onAppear(perform: onUnavailable)
        }
    }
}

private struct DataScannerController: UIViewControllerRepresentable {
    let onCode: @MainActor (String) -> Void
    let onUnavailable: @MainActor () -> Void

    func makeCoordinator() -> Coordinator {
        Coordinator(onCode: onCode, onUnavailable: onUnavailable)
    }

    func makeUIViewController(context: Context) -> DataScannerViewController {
        let scanner = DataScannerViewController(
            recognizedDataTypes: [.barcode(symbologies: [.qr])],
            qualityLevel: .balanced,
            recognizesMultipleItems: false,
            isHighFrameRateTrackingEnabled: false,
            isPinchToZoomEnabled: true,
            isGuidanceEnabled: true,
            isHighlightingEnabled: true
        )
        scanner.delegate = context.coordinator
        do {
            try scanner.startScanning()
        } catch {
            context.coordinator.reportUnavailable()
        }
        return scanner
    }

    func updateUIViewController(_ uiViewController: DataScannerViewController, context: Context) {}

    static func dismantleUIViewController(_ uiViewController: DataScannerViewController, coordinator: Coordinator) {
        uiViewController.stopScanning()
    }

    @MainActor
    final class Coordinator: NSObject, DataScannerViewControllerDelegate {
        private let onCode: @MainActor (String) -> Void
        private let onUnavailable: @MainActor () -> Void
        private var finished = false

        init(
            onCode: @escaping @MainActor (String) -> Void,
            onUnavailable: @escaping @MainActor () -> Void
        ) {
            self.onCode = onCode
            self.onUnavailable = onUnavailable
        }

        func dataScanner(
            _ dataScanner: DataScannerViewController,
            didAdd addedItems: [RecognizedItem],
            allItems: [RecognizedItem]
        ) {
            guard !finished else { return }
            for item in addedItems {
                guard case let .barcode(barcode) = item,
                      let payload = barcode.payloadStringValue,
                      !payload.isEmpty
                else {
                    continue
                }
                finished = true
                dataScanner.stopScanning()
                onCode(payload)
                return
            }
        }

        func dataScanner(
            _ dataScanner: DataScannerViewController,
            becameUnavailableWithError error: DataScannerViewController.ScanningUnavailable
        ) {
            reportUnavailable()
        }

        func reportUnavailable() {
            guard !finished else { return }
            finished = true
            onUnavailable()
        }
    }
}
