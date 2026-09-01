import XCTest
@testable import MobileEgressCore

final class QRScannerSessionTests: XCTestCase {
    func testAvailabilityRequiresSupportedAndAvailableVisionKitScanner() {
        XCTAssertEqual(
            QRScannerSession.availabilityDecision(isSupported: true, isAvailable: true),
            .startScanning
        )
        XCTAssertEqual(
            QRScannerSession.availabilityDecision(isSupported: false, isAvailable: true),
            .reportUnavailable
        )
        XCTAssertEqual(
            QRScannerSession.availabilityDecision(isSupported: true, isAvailable: false),
            .reportUnavailable
        )
        XCTAssertEqual(
            QRScannerSession.availabilityDecision(isSupported: false, isAvailable: false),
            .reportUnavailable
        )
    }

    func testFirstNonemptyPayloadFinishesSessionAndSuppressesLaterCallbacks() {
        var session = QRScannerSession()

        XCTAssertEqual(
            session.reduce(.recognizedPayloads(["", "enrollment-payload", "migration-payload"])),
            .deliverCode("enrollment-payload")
        )
        XCTAssertTrue(session.isFinished)
        XCTAssertEqual(session.reduce(.recognizedPayloads(["late-payload"])), .none)
        XCTAssertEqual(session.reduce(.scannerUnavailable), .none)
    }

    func testInvalidPayloadsKeepScanningUntilUnavailableThenSuppressLateCode() {
        var session = QRScannerSession()

        XCTAssertEqual(session.reduce(.recognizedPayloads(["", ""])), .none)
        XCTAssertFalse(session.isFinished)
        XCTAssertEqual(session.reduce(.scannerUnavailable), .reportUnavailable)
        XCTAssertTrue(session.isFinished)
        XCTAssertEqual(session.reduce(.recognizedPayloads(["late-payload"])), .none)
    }
}
