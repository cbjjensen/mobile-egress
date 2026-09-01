import Foundation
import XCTest
@testable import MobileEgressCore

final class CellularIPRotationNotificationCueTests: XCTestCase {
    func testFirstUseDenialIsFiniteNonBlockingAndAuthorizationIsNeverRequestedAgain() async {
        let center = NotificationCenterStub(status: .notDetermined, requestResult: .success(false))
        let firstUse = NotificationFirstUseStoreStub()
        let cue = CellularIPRotationNotificationCue(center: center, firstUseStore: firstUse)

        let first = await cue.schedule(attemptID: 41, holdDeadline: deadline)
        let second = await cue.schedule(attemptID: 42, holdDeadline: deadline.addingTimeInterval(10))
        let snapshot = await center.snapshot()

        XCTAssertEqual(first, .denied)
        XCTAssertEqual(second, .denied)
        XCTAssertEqual(snapshot.authorizationRequestCount, 1)
        XCTAssertTrue(firstUse.hasRequestedAuthorization)
        XCTAssertTrue(snapshot.scheduled.isEmpty)
    }

    func testGrantedFirstUseSchedulesExactCueAtHoldDeadline() async {
        let center = NotificationCenterStub(status: .notDetermined, requestResult: .success(true))
        let cue = CellularIPRotationNotificationCue(
            center: center,
            firstUseStore: NotificationFirstUseStoreStub()
        )

        let result = await cue.schedule(attemptID: 41, holdDeadline: deadline)
        let scheduled = (await center.snapshot()).scheduled

        XCTAssertEqual(result, .scheduled)
        XCTAssertEqual(
            scheduled,
            [
                CellularIPRotationNotificationRequest(
                    identifier: "com.mobileegress.agent.rotation.41",
                    title: "ZFNF Mobile Egress",
                    body: "Turn Airplane Mode off",
                    deliveryDate: deadline
                ),
            ]
        )
    }

    func testAuthorizationAndSchedulingErrorsReturnFiniteNonBlockingResults() async {
        let authorizationErrorCenter = NotificationCenterStub(
            status: .notDetermined,
            requestResult: .failure(.authorization)
        )
        let authorizationCue = CellularIPRotationNotificationCue(
            center: authorizationErrorCenter,
            firstUseStore: NotificationFirstUseStoreStub()
        )
        let schedulingErrorCenter = NotificationCenterStub(
            status: .authorized,
            requestResult: .success(true),
            schedulingError: true
        )
        let previouslyUsed = NotificationFirstUseStoreStub(hasRequestedAuthorization: true)
        let schedulingCue = CellularIPRotationNotificationCue(
            center: schedulingErrorCenter,
            firstUseStore: previouslyUsed
        )
        let authorizationResult = await authorizationCue.schedule(
            attemptID: 41,
            holdDeadline: deadline
        )
        let schedulingResult = await schedulingCue.schedule(
            attemptID: 42,
            holdDeadline: deadline
        )
        let requestCount = (await authorizationErrorCenter.snapshot()).authorizationRequestCount

        XCTAssertEqual(authorizationResult, .authorizationFailed)
        XCTAssertEqual(schedulingResult, .schedulingFailed)
        XCTAssertEqual(requestCount, 1)
    }

    func testCancellationRemovesOnlyThisAttemptsPendingCue() async {
        let center = NotificationCenterStub(status: .authorized, requestResult: .success(true))
        let cue = CellularIPRotationNotificationCue(
            center: center,
            firstUseStore: NotificationFirstUseStoreStub(hasRequestedAuthorization: true)
        )
        _ = await cue.schedule(attemptID: 41, holdDeadline: deadline)
        _ = await cue.schedule(attemptID: 42, holdDeadline: deadline)

        await cue.cancel(attemptID: 41)
        let removedIdentifiers = (await center.snapshot()).removedIdentifiers

        XCTAssertEqual(removedIdentifiers, [["com.mobileegress.agent.rotation.41"]])
    }

    #if canImport(UserNotifications)
    func testAppleNotificationAdaptersCanBeConstructedWithoutRequestingPermission() {
        _ = AppleCellularIPRotationNotificationCenter()
        _ = UserDefaultsNotificationFirstUseStore(
            defaults: UserDefaults(suiteName: "CellularIPRotationNotificationCueTests") ?? .standard
        )
    }
    #endif

    private var deadline: Date {
        Date(timeIntervalSince1970: 2_100_000_010)
    }
}

private enum NotificationCenterStubError: Error {
    case authorization
    case scheduling
}

private actor NotificationCenterStub: CellularIPRotationNotificationCenter {
    struct Snapshot: Sendable {
        let authorizationRequestCount: Int
        let scheduled: [CellularIPRotationNotificationRequest]
        let removedIdentifiers: [[String]]
    }

    private var status: CellularIPRotationNotificationAuthorization
    private let requestResult: Result<Bool, NotificationCenterStubError>
    private let schedulingError: Bool
    private var authorizationRequestCount = 0
    private var scheduled: [CellularIPRotationNotificationRequest] = []
    private var removedIdentifiers: [[String]] = []

    init(
        status: CellularIPRotationNotificationAuthorization,
        requestResult: Result<Bool, NotificationCenterStubError>,
        schedulingError: Bool = false
    ) {
        self.status = status
        self.requestResult = requestResult
        self.schedulingError = schedulingError
    }

    func authorizationStatus() async -> CellularIPRotationNotificationAuthorization {
        status
    }

    func requestAuthorization() async throws -> Bool {
        authorizationRequestCount += 1
        let granted = try requestResult.get()
        status = granted ? .authorized : .denied
        return granted
    }

    func schedule(_ request: CellularIPRotationNotificationRequest) async throws {
        if schedulingError {
            throw NotificationCenterStubError.scheduling
        }
        scheduled.append(request)
    }

    func removePendingRequests(withIdentifiers identifiers: [String]) async {
        removedIdentifiers.append(identifiers)
    }

    func snapshot() -> Snapshot {
        Snapshot(
            authorizationRequestCount: authorizationRequestCount,
            scheduled: scheduled,
            removedIdentifiers: removedIdentifiers
        )
    }
}

private final class NotificationFirstUseStoreStub: CellularIPRotationNotificationFirstUseStoring, @unchecked Sendable {
    private let lock = NSLock()
    private var requested: Bool

    init(hasRequestedAuthorization: Bool = false) {
        requested = hasRequestedAuthorization
    }

    var hasRequestedAuthorization: Bool {
        get { lock.withLock { requested } }
        set { lock.withLock { requested = newValue } }
    }
}
