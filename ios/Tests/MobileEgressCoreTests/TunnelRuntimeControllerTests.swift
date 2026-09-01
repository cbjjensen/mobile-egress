import XCTest
@testable import MobileEgressCore

final class TunnelRuntimeControllerTests: XCTestCase {
    func testRunningRuntimeTerminalFailureCancelsProviderExactlyOnce() async throws {
        let controller = TunnelRuntimeController()
        let runtime = CountingTunnelRuntime()
        let cancellations = LockedProviderErrors()
        await runtime.releaseStart()
        let pendingGeneration = await controller.beginStart()
        let generation = try XCTUnwrap(pendingGeneration)
        let installed = await controller.installAndStart(runtime, generation: generation)
        XCTAssertTrue(installed)

        await controller.handleTerminalFailure(.relayUnavailable, generation: generation) { error in
            cancellations.append(error)
        }
        await controller.handleTerminalFailure(.relayTLS, generation: generation) { error in
            cancellations.append(error)
        }

        let status = await controller.status()
        XCTAssertEqual(cancellations.values, [.relayUnavailable])
        XCTAssertEqual(status.providerState, .failed)
        XCTAssertEqual(status.providerError, .relayUnavailable)
    }

    func testTerminalFailureDuringStartFailsStartWithoutProviderCancellation() async throws {
        let controller = TunnelRuntimeController()
        let runtime = CountingTunnelRuntime()
        let cancellations = LockedProviderErrors()
        let pendingGeneration = await controller.beginStart()
        let generation = try XCTUnwrap(pendingGeneration)
        let installTask = Task {
            await controller.installAndStart(runtime, generation: generation)
        }
        await runtime.waitUntilStartCount(1)

        await controller.handleTerminalFailure(.relayUnavailable, generation: generation) { error in
            cancellations.append(error)
        }
        await runtime.releaseStart()

        let installed = await installTask.value
        let stopCount = await runtime.stopCountValue()
        XCTAssertFalse(installed)
        XCTAssertEqual(cancellations.values, [])
        XCTAssertEqual(stopCount, 1)
        let status = await controller.status()
        XCTAssertEqual(status.providerState, .failed)
        XCTAssertEqual(status.providerError, .relayUnavailable)
    }

    func testExplicitStopRejectsStaleTerminalFailureCallback() async throws {
        let controller = TunnelRuntimeController()
        let runtime = CountingTunnelRuntime()
        let cancellations = LockedProviderErrors()
        await runtime.releaseStart()
        let pendingGeneration = await controller.beginStart()
        let generation = try XCTUnwrap(pendingGeneration)
        let installed = await controller.installAndStart(runtime, generation: generation)
        XCTAssertTrue(installed)

        await controller.stop()
        await controller.handleTerminalFailure(.relayUnavailable, generation: generation) { error in
            cancellations.append(error)
        }

        let stopCount = await runtime.stopCountValue()
        XCTAssertEqual(cancellations.values, [])
        XCTAssertEqual(stopCount, 1)
        let status = await controller.status()
        XCTAssertEqual(status.providerState, .stopped)
        XCTAssertEqual(status.providerError, .none)
    }

    func testOverlappingStopClaimsSuspendedStartingRuntimeExactlyOnce() async throws {
        let controller = TunnelRuntimeController()
        let runtime = CountingTunnelRuntime()
        let pendingGeneration = await controller.beginStart()
        let generation = try XCTUnwrap(pendingGeneration)

        let installTask = Task {
            await controller.installAndStart(runtime, generation: generation)
        }
        await runtime.waitUntilStartCount(1)

        await controller.stop()
        await runtime.releaseStart()

        let installed = await installTask.value
        let stopCount = await runtime.stopCountValue()
        let status = await controller.status()
        XCTAssertFalse(installed)
        XCTAssertEqual(stopCount, 1)
        XCTAssertEqual(status.providerState, .stopped)
        XCTAssertEqual(status.providerError, .none)
    }

    func testInstalledRuntimeStopsOnceOnNormalProviderStop() async throws {
        let controller = TunnelRuntimeController()
        let runtime = CountingTunnelRuntime()
        await runtime.releaseStart()
        let pendingGeneration = await controller.beginStart()
        let generation = try XCTUnwrap(pendingGeneration)

        let installed = await controller.installAndStart(runtime, generation: generation)
        await controller.stop()

        let stopCount = await runtime.stopCountValue()
        XCTAssertTrue(installed)
        XCTAssertEqual(stopCount, 1)
    }
}

private final class LockedProviderErrors: @unchecked Sendable {
    private let lock = NSLock()
    private var errors: [TunnelProviderErrorClass] = []

    var values: [TunnelProviderErrorClass] { lock.withLock { errors } }

    func append(_ error: TunnelProviderErrorClass) {
        lock.withLock { errors.append(error) }
    }
}

private actor CountingTunnelRuntime: TunnelRuntimeLifecycle {
    private let startGate = AsyncTestGate()
    private var startCount = 0
    private var stopCount = 0
    private var startCountWaiters: [(Int, CheckedContinuation<Void, Never>)] = []

    func start() async {
        startCount += 1
        resumeSatisfiedStartWaiters()
        await startGate.wait()
    }

    func stop() async {
        stopCount += 1
    }

    func snapshot() async -> AgentRuntimeSnapshot {
        AgentRuntimeSnapshot(
            connectionState: .stopped,
            activeStreamCount: 0,
            bytesUploaded: 0,
            bytesDownloaded: 0,
            errorClass: .none
        )
    }

    func waitUntilStartCount(_ expected: Int) async {
        if startCount >= expected { return }
        await withCheckedContinuation { continuation in
            startCountWaiters.append((expected, continuation))
        }
    }

    func releaseStart() async {
        await startGate.open()
    }

    func stopCountValue() -> Int {
        stopCount
    }

    private func resumeSatisfiedStartWaiters() {
        let satisfied = startCountWaiters.filter { $0.0 <= startCount }
        startCountWaiters.removeAll { $0.0 <= startCount }
        satisfied.forEach { $0.1.resume() }
    }
}
