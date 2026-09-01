import MobileEgressCore
import SwiftUI
import Vision
import VisionKit

struct QRScannerView: View {
    let onCode: @MainActor (String) -> Void
    let onUnavailable: @MainActor () -> Void

    var body: some View {
        switch QRScannerSession.availabilityDecision(
            isSupported: DataScannerViewController.isSupported,
            isAvailable: DataScannerViewController.isAvailable
        ) {
        case .startScanning:
            DataScannerController(onCode: onCode, onUnavailable: onUnavailable)
        case .reportUnavailable:
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
        private var session = QRScannerSession()

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
            let payloads = addedItems.compactMap { item -> String? in
                guard case let .barcode(barcode) = item else { return nil }
                return barcode.payloadStringValue
            }
            apply(session.reduce(.recognizedPayloads(payloads)), scanner: dataScanner)
        }

        func dataScanner(
            _ dataScanner: DataScannerViewController,
            becameUnavailableWithError error: DataScannerViewController.ScanningUnavailable
        ) {
            reportUnavailable()
        }

        func reportUnavailable() {
            apply(session.reduce(.scannerUnavailable), scanner: nil)
        }

        private func apply(
            _ effect: QRScannerSessionEffect,
            scanner: DataScannerViewController?
        ) {
            switch effect {
            case .none:
                break
            case let .deliverCode(payload):
                scanner?.stopScanning()
                onCode(payload)
            case .reportUnavailable:
                onUnavailable()
            }
        }
    }
}
