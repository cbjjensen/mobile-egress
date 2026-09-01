import Foundation

public enum PublicIPFamily: String, Codable, CaseIterable, Hashable, Sendable {
    case ipv4
    case ipv6
}

public enum SafeNetworkDiagnosticComponent: String, Codable, Hashable, Sendable {
    case publicIPProbe
    case relay
}

public enum SafeNetworkFailureClass: String, Codable, Hashable, Sendable {
    case cancelled
    case timedOut
    case unavailable
    case authentication
    case tls
    case httpStatus
    case malformedResponse
    case unsupportedTransferEncoding
    case responseTooLarge
    case invalidAddress
    case wrongAddressFamily
}

public struct SafeNetworkDiagnostic: Codable, Equatable, Hashable, Sendable {
    public let component: SafeNetworkDiagnosticComponent
    public let family: PublicIPFamily?
    public let failure: SafeNetworkFailureClass
    public let httpStatus: Int?

    public init(
        component: SafeNetworkDiagnosticComponent,
        family: PublicIPFamily? = nil,
        failure: SafeNetworkFailureClass,
        httpStatus: Int? = nil
    ) {
        self.component = component
        self.family = family
        self.failure = failure
        self.httpStatus = httpStatus
    }

    public init(relayFailure: RelayConnectionFailure, httpStatus: Int? = nil) {
        let failure: SafeNetworkFailureClass = switch relayFailure {
        case .unavailable: .unavailable
        case .authentication: .authentication
        case .tls: .tls
        }
        self.init(component: .relay, failure: failure, httpStatus: httpStatus)
    }
}

public protocol CellularNetworkDiagnosticLogging: Sendable {
    func record(_ diagnostic: SafeNetworkDiagnostic)
}

public protocol CellularPublicIPProbing: Sendable {
    func probe() async -> PublicIPSnapshot
}

public struct NoopCellularNetworkDiagnosticLogger: CellularNetworkDiagnosticLogging {
    public init() {}

    public func record(_: SafeNetworkDiagnostic) {}
}

public struct PublicIPProbeFailure: Error, Equatable, Sendable {
    public let classification: SafeNetworkFailureClass
    public let httpStatus: Int?

    public init(_ classification: SafeNetworkFailureClass, httpStatus: Int? = nil) {
        self.classification = classification
        self.httpStatus = httpStatus
    }
}

#if canImport(OSLog)
import OSLog

public struct AppleUnifiedNetworkDiagnosticLogger: CellularNetworkDiagnosticLogging {
    private let logger = Logger(subsystem: "com.mobileegress.agent", category: "network")

    public init() {}

    public func record(_ diagnostic: SafeNetworkDiagnostic) {
        if let family = diagnostic.family, let status = diagnostic.httpStatus {
            logger.warning(
                "component=\(diagnostic.component.rawValue, privacy: .public) family=\(family.rawValue, privacy: .public) failure=\(diagnostic.failure.rawValue, privacy: .public) http_status=\(status, privacy: .public)"
            )
        } else if let family = diagnostic.family {
            logger.warning(
                "component=\(diagnostic.component.rawValue, privacy: .public) family=\(family.rawValue, privacy: .public) failure=\(diagnostic.failure.rawValue, privacy: .public)"
            )
        } else if let status = diagnostic.httpStatus {
            logger.warning(
                "component=\(diagnostic.component.rawValue, privacy: .public) failure=\(diagnostic.failure.rawValue, privacy: .public) http_status=\(status, privacy: .public)"
            )
        } else {
            logger.warning(
                "component=\(diagnostic.component.rawValue, privacy: .public) failure=\(diagnostic.failure.rawValue, privacy: .public)"
            )
        }
    }
}
#endif
