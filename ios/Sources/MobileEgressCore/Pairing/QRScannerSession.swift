public enum QRScannerAvailabilityDecision: Equatable, Sendable {
    case startScanning
    case reportUnavailable
}

public enum QRScannerSessionEvent: Equatable, Sendable {
    case recognizedPayloads([String])
    case scannerUnavailable
}

public enum QRScannerSessionEffect: Equatable, Sendable {
    case none
    case deliverCode(String)
    case reportUnavailable
}

public struct QRScannerSession: Equatable, Sendable {
    public private(set) var isFinished = false

    public init() {}

    public static func availabilityDecision(
        isSupported: Bool,
        isAvailable: Bool
    ) -> QRScannerAvailabilityDecision {
        isSupported && isAvailable ? .startScanning : .reportUnavailable
    }

    public mutating func reduce(_ event: QRScannerSessionEvent) -> QRScannerSessionEffect {
        guard !isFinished else { return .none }

        switch event {
        case let .recognizedPayloads(payloads):
            guard let payload = payloads.first(where: { !$0.isEmpty }) else { return .none }
            isFinished = true
            return .deliverCode(payload)
        case .scannerUnavailable:
            isFinished = true
            return .reportUnavailable
        }
    }
}
