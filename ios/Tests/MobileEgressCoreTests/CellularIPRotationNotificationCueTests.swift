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

    func testCancellationDuringAuthorizationPreventsAStaleCue() async {
        let authorizationSuspension = TestSuspension()
        let center = NotificationCenterStub(
            status: .authorized,
            requestResult: .success(true),
            authorizationStatusSuspension: authorizationSuspension
        )
        let cue = CellularIPRotationNotificationCue(
            center: center,
            firstUseStore: NotificationFirstUseStoreStub(hasRequestedAuthorization: true)
        )
        let deadline = deadline
        let schedulingTask = Task {
            await cue.schedule(attemptID: 41, holdDeadline: deadline)
        }
        await authorizationSuspension.waitUntilEntered()

        await cue.cancel(attemptID: 41)
        await authorizationSuspension.resume()
        let result = await schedulingTask.value
        let snapshot = await center.snapshot()

        XCTAssertEqual(result.rawValue, "cancelled")
        XCTAssertTrue(snapshot.scheduled.isEmpty)
        XCTAssertEqual(
            snapshot.removedIdentifiers,
            [["com.mobileegress.agent.rotation.41"]]
        )
    }

    func testCancellationDuringAddUndoesTheLateCue() async {
        let schedulingSuspension = TestSuspension()
        let center = NotificationCenterStub(
            status: .authorized,
            requestResult: .success(true),
            schedulingSuspension: schedulingSuspension
        )
        let cue = CellularIPRotationNotificationCue(
            center: center,
            firstUseStore: NotificationFirstUseStoreStub(hasRequestedAuthorization: true)
        )
        let deadline = deadline
        let schedulingTask = Task {
            await cue.schedule(attemptID: 41, holdDeadline: deadline)
        }
        await schedulingSuspension.waitUntilEntered()

        await cue.cancel(attemptID: 41)
        await schedulingSuspension.resume()
        let result = await schedulingTask.value
        let snapshot = await center.snapshot()

        XCTAssertEqual(result.rawValue, "cancelled")
        XCTAssertTrue(snapshot.scheduled.isEmpty)
        XCTAssertEqual(
            snapshot.removedIdentifiers,
            [
                ["com.mobileegress.agent.rotation.41"],
                ["com.mobileegress.agent.rotation.41"],
            ]
        )
    }

    func testCancellationDuringPermissionRequestPreventsAStaleCue() async {
        let permissionSuspension = TestSuspension()
        let center = NotificationCenterStub(
            status: .notDetermined,
            requestResult: .success(true),
            requestAuthorizationSuspension: permissionSuspension
        )
        let cue = CellularIPRotationNotificationCue(
            center: center,
            firstUseStore: NotificationFirstUseStoreStub()
        )
        let deadline = deadline
        let schedulingTask = Task {
            await cue.schedule(attemptID: 41, holdDeadline: deadline)
        }
        await permissionSuspension.waitUntilEntered()

        await cue.cancel(attemptID: 41)
        await permissionSuspension.resume()
        let result = await schedulingTask.value
        let snapshot = await center.snapshot()

        XCTAssertEqual(result.rawValue, "cancelled")
        XCTAssertTrue(snapshot.scheduled.isEmpty)
        XCTAssertEqual(snapshot.authorizationRequestCount, 1)
        XCTAssertEqual(
            snapshot.removedIdentifiers,
            [["com.mobileegress.agent.rotation.41"]]
        )
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
    private let authorizationStatusSuspension: TestSuspension?
    private let requestAuthorizationSuspension: TestSuspension?
    private let schedulingSuspension: TestSuspension?
    private var authorizationRequestCount = 0
    private var scheduled: [CellularIPRotationNotificationRequest] = []
    private var removedIdentifiers: [[String]] = []

    init(
        status: CellularIPRotationNotificationAuthorization,
        requestResult: Result<Bool, NotificationCenterStubError>,
        schedulingError: Bool = false,
        authorizationStatusSuspension: TestSuspension? = nil,
        requestAuthorizationSuspension: TestSuspension? = nil,
        schedulingSuspension: TestSuspension? = nil
    ) {
        self.status = status
        self.requestResult = requestResult
        self.schedulingError = schedulingError
        self.authorizationStatusSuspension = authorizationStatusSuspension
        self.requestAuthorizationSuspension = requestAuthorizationSuspension
        self.schedulingSuspension = schedulingSuspension
    }

    func authorizationStatus() async -> CellularIPRotationNotificationAuthorization {
        if let authorizationStatusSuspension {
            await authorizationStatusSuspension.suspend()
        }
        return status
    }

    func requestAuthorization() async throws -> Bool {
        authorizationRequestCount += 1
        if let requestAuthorizationSuspension {
            await requestAuthorizationSuspension.suspend()
        }
        let granted = try requestResult.get()
        status = granted ? .authorized : .denied
        return granted
    }

    func schedule(_ request: CellularIPRotationNotificationRequest) async throws {
        if let schedulingSuspension {
            await schedulingSuspension.suspend()
        }
        if schedulingError {
            throw NotificationCenterStubError.scheduling
        }
        scheduled.append(request)
    }

    func removePendingRequests(withIdentifiers identifiers: [String]) async {
        removedIdentifiers.append(identifiers)
        scheduled.removeAll { identifiers.contains($0.identifier) }
    }

    func snapshot() -> Snapshot {
        Snapshot(
            authorizationRequestCount: authorizationRequestCount,
            scheduled: scheduled,
            removedIdentifiers: removedIdentifiers
        )
    }
}

private actor TestSuspension {
    private var entered = false
    private var entryWaiters: [CheckedContinuation<Void, Never>] = []
    private var resumeWaiter: CheckedContinuation<Void, Never>?
    private var resumed = false

    func suspend() async {
        entered = true
        let waiters = entryWaiters
        entryWaiters.removeAll()
        waiters.forEach { $0.resume() }
        guard !resumed else { return }
        await withCheckedContinuation { continuation in
            resumeWaiter = continuation
        }
    }

    func waitUntilEntered() async {
        guard !entered else { return }
        await withCheckedContinuation { continuation in
            entryWaiters.append(continuation)
        }
    }

    func resume() {
        resumed = true
        resumeWaiter?.resume()
        resumeWaiter = nil
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
