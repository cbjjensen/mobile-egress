import Foundation
import XCTest
@testable import MobileEgressCore

final class CellularPublicIPProbeAdapterTests: XCTestCase {
    func testBoundedHTTPParserAcceptsStrictExpectedFamilyLiterals() throws {
        let ipv4Response = Data(
            "HTTP/1.1 200 OK\r\nContent-Length: 14\r\nConnection: close\r\n\r\n 198.51.100.7\n".utf8
        )
        let ipv6Response = Data(
            "HTTP/1.1 200 OK\r\nContent-Length: 11\r\nConnection: close\r\n\r\n2001:db8::7".utf8
        )

        XCTAssertEqual(
            try CellularPublicIPHTTPResponseParser.parse(ipv4Response, family: .ipv4),
            "198.51.100.7"
        )
        XCTAssertEqual(
            try CellularPublicIPHTTPResponseParser.parse(ipv6Response, family: .ipv6),
            "2001:db8::7"
        )
    }

    func testBoundedHTTPParserRejectsUnsafeOrAmbiguousResponsesWithFiniteFailures() {
        assertProbeFailure(
            Data("HTTP/1.1 503 Unavailable\r\nContent-Length: 0\r\n\r\n".utf8),
            family: .ipv4,
            classification: .httpStatus,
            httpStatus: 503
        )
        assertProbeFailure(
            Data("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n1\r\na\r\n0\r\n\r\n".utf8),
            family: .ipv4,
            classification: .unsupportedTransferEncoding
        )
        assertProbeFailure(
            Data("HTTP/1.1 200 OK\r\nContent-Length: 7\r\nContent-Length: 7\r\n\r\n1.1.1.1".utf8),
            family: .ipv4,
            classification: .malformedResponse
        )
        assertProbeFailure(
            Data("HTTP/1.1 200 OK\r\nContent-Length: 129\r\n\r\n".utf8),
            family: .ipv4,
            classification: .responseTooLarge
        )
        assertProbeFailure(
            Data("HTTP/1.1 200 OK\r\nContent-Length: 8\r\n\r\n1.1.1.1".utf8),
            family: .ipv4,
            classification: .malformedResponse
        )
        assertProbeFailure(
            Data("HTTP/1.1 200 OK\r\nContent-Length: 11\r\n\r\n2001:db8::7".utf8),
            family: .ipv4,
            classification: .wrongAddressFamily
        )
        assertProbeFailure(
            Data("HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\n999.1.1.01".utf8),
            family: .ipv4,
            classification: .invalidAddress
        )
    }

    func testSafeDiagnosticMapsRelayFailuresWithoutAcceptingRawDetails() {
        XCTAssertEqual(
            SafeNetworkDiagnostic(relayFailure: .authentication, httpStatus: 401),
            SafeNetworkDiagnostic(
                component: .relay,
                failure: .authentication,
                httpStatus: 401
            )
        )
        XCTAssertEqual(
            SafeNetworkDiagnostic(relayFailure: .tls),
            SafeNetworkDiagnostic(component: .relay, failure: .tls)
        )
        XCTAssertEqual(
            SafeNetworkDiagnostic(relayFailure: .unavailable),
            SafeNetworkDiagnostic(component: .relay, failure: .unavailable)
        )
    }

    #if canImport(Network) && canImport(Security)
    func testLiveProbeConfigurationUsesExactDualFamilyCellularOnlySystemTLSRequests() {
        let builder = AppleCellularPublicIPProbeRequestBuilder(timeout: 8, maximumBodyBytes: 128)

        let ipv4 = builder.makeRequest(for: .ipv4)
        let ipv6 = builder.makeRequest(for: .ipv6)
        let parameters = builder.makeParameters()

        XCTAssertEqual(ipv4.hostname, "api.ipify.org")
        XCTAssertEqual(ipv6.hostname, "api6.ipify.org")
        XCTAssertEqual(ipv4.port, 443)
        XCTAssertEqual(ipv6.port, 443)
        XCTAssertEqual(ipv4.timeout, 8)
        XCTAssertEqual(ipv6.timeout, 8)
        XCTAssertEqual(ipv4.maximumBodyBytes, 128)
        XCTAssertEqual(ipv6.maximumBodyBytes, 128)
        XCTAssertEqual(ipv4.httpRequest, Data("GET / HTTP/1.1\r\nHost: api.ipify.org\r\nAccept: text/plain\r\nConnection: close\r\n\r\n".utf8))
        XCTAssertEqual(ipv6.httpRequest, Data("GET / HTTP/1.1\r\nHost: api6.ipify.org\r\nAccept: text/plain\r\nConnection: close\r\n\r\n".utf8))
        XCTAssertEqual(parameters.requiredInterfaceType, .cellular)
        XCTAssertEqual(parameters.prohibitedInterfaceTypes, [.wifi, .wiredEthernet])
        XCTAssertFalse(parameters.includePeerToPeer)
        XCTAssertFalse(parameters.allowLocalEndpointReuse)
        _ = CellularPublicIPProbe()
    }

    func testProbeRunsFamiliesConcurrentlyAndIsolatesFailureWithSanitizedLogging() async {
        let barrier = TwoFamilyBarrier()
        let requester = IsolatingProbeRequester(barrier: barrier)
        let logger = DiagnosticRecorder()
        let probe = CellularPublicIPProbe(requester: requester, logger: logger)

        let snapshot = await probe.probe()
        let requestedFamilies = await requester.requestedFamilies()

        XCTAssertEqual(snapshot, PublicIPSnapshot(ipv4: "198.51.100.8", ipv6: nil))
        XCTAssertEqual(requestedFamilies, Set([.ipv4, .ipv6]))
        XCTAssertEqual(
            logger.events,
            [
                SafeNetworkDiagnostic(
                    component: .publicIPProbe,
                    family: .ipv6,
                    failure: .timedOut
                ),
            ]
        )
    }

    func testCancellingProbePropagatesCancellationToBothFamilyRequests() async {
        let requester = CancellableProbeRequester()
        let logger = DiagnosticRecorder()
        let probe = CellularPublicIPProbe(requester: requester, logger: logger)
        let task = Task { await probe.probe() }

        while await requester.startedCount() < 2 {
            await Task.yield()
        }
        task.cancel()
        let snapshot = await task.value
        let cancelledFamilies = await requester.cancelledFamilies()

        XCTAssertEqual(snapshot, PublicIPSnapshot())
        XCTAssertEqual(cancelledFamilies, Set([.ipv4, .ipv6]))
        XCTAssertEqual(
            Set(logger.events),
            Set([
                SafeNetworkDiagnostic(
                    component: .publicIPProbe,
                    family: .ipv4,
                    failure: .cancelled
                ),
                SafeNetworkDiagnostic(
                    component: .publicIPProbe,
                    family: .ipv6,
                    failure: .cancelled
                ),
            ])
        )
    }
    #endif

    private func assertProbeFailure(
        _ data: Data,
        family: PublicIPFamily,
        classification: SafeNetworkFailureClass,
        httpStatus: Int? = nil,
        file: StaticString = #filePath,
        line: UInt = #line
    ) {
        XCTAssertThrowsError(
            try CellularPublicIPHTTPResponseParser.parse(data, family: family),
            file: file,
            line: line
        ) { error in
            XCTAssertEqual(
                error as? PublicIPProbeFailure,
                PublicIPProbeFailure(classification, httpStatus: httpStatus),
                file: file,
                line: line
            )
        }
    }
}

#if canImport(Network) && canImport(Security)
private actor TwoFamilyBarrier {
    private var waiter: CheckedContinuation<Void, Never>?

    func arrive() async {
        if let waiter {
            self.waiter = nil
            waiter.resume()
            return
        }
        await withCheckedContinuation { continuation in
            waiter = continuation
        }
    }
}

private actor IsolatingProbeRequester: CellularPublicIPFamilyRequesting {
    private let barrier: TwoFamilyBarrier
    private var families: Set<PublicIPFamily> = []

    init(barrier: TwoFamilyBarrier) {
        self.barrier = barrier
    }

    func execute(_ request: CellularPublicIPProbeRequest) async throws -> Data {
        families.insert(request.family)
        await barrier.arrive()
        switch request.family {
        case .ipv4:
            return Data("HTTP/1.1 200 OK\r\nContent-Length: 12\r\n\r\n198.51.100.8".utf8)
        case .ipv6:
            throw PublicIPProbeFailure(.timedOut)
        }
    }

    func requestedFamilies() -> Set<PublicIPFamily> {
        families
    }
}

private actor CancellableProbeRequester: CellularPublicIPFamilyRequesting {
    private var started: Set<PublicIPFamily> = []
    private var cancelled: Set<PublicIPFamily> = []

    func execute(_ request: CellularPublicIPProbeRequest) async throws -> Data {
        started.insert(request.family)
        do {
            try await Task.sleep(for: .seconds(60))
            return Data()
        } catch is CancellationError {
            cancelled.insert(request.family)
            throw CancellationError()
        }
    }

    func startedCount() -> Int {
        started.count
    }

    func cancelledFamilies() -> Set<PublicIPFamily> {
        cancelled
    }
}

private final class DiagnosticRecorder: CellularNetworkDiagnosticLogging, @unchecked Sendable {
    private let lock = NSLock()
    private var recorded: [SafeNetworkDiagnostic] = []

    var events: [SafeNetworkDiagnostic] {
        lock.withLock { recorded }
    }

    func record(_ diagnostic: SafeNetworkDiagnostic) {
        lock.withLock { recorded.append(diagnostic) }
    }
}
#endif
