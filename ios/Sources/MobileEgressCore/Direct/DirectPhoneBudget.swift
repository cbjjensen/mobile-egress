import Foundation

public final class DirectPhoneBudget: @unchecked Sendable {
    let readTurns = DirectReadTurns()
    enum Lane: Hashable, Sendable { case inbound, outbound, control }
    private let lock = NSLock()
    private let frameLimit: Int
    private let byteLimit: Int
    private let controlLimit: Int
    private var frames: [Lane: Int] = [:]
    private var bytes: [Lane: Int] = [:]
    private var streamFrames: [Lane: [String: Int]] = [:]
    public init(frameLimit: Int = 8192, byteLimit: Int = 64 * 1024 * 1024, controlLimit: Int = 512) {
        precondition(frameLimit > 0 && byteLimit > 0 && controlLimit > 0)
        self.frameLimit = frameLimit; self.byteLimit = byteLimit; self.controlLimit = controlLimit
    }
    func acquire(_ lane: Lane, bytes count: Int, streamKey: String? = nil) -> DirectBudgetLease? {
        lock.withLock {
            guard count >= 0, frames[lane, default: 0] < (lane == .control ? controlLimit : frameLimit), count <= byteLimit - bytes[lane, default: 0] else { return nil }
            if let streamKey, streamFrames[lane]?[streamKey, default: 0] ?? 0 >= 32 { return nil }
            frames[lane, default: 0] += 1
            bytes[lane, default: 0] += count
            if let streamKey { streamFrames[lane, default: [:]][streamKey, default: 0] += 1 }
            return DirectBudgetLease { [self] in
                lock.withLock {
                    frames[lane, default: 0] -= 1; bytes[lane, default: 0] -= count
                    if let streamKey {
                        let remaining = (streamFrames[lane]?[streamKey] ?? 1) - 1
                        if remaining == 0 { streamFrames[lane]?.removeValue(forKey: streamKey) }
                        else { streamFrames[lane]?[streamKey] = remaining }
                    }
                }
            }
        }
    }
}

/// Owned by the queued payload and captured by the native completion. Cancellation
/// drops queued references, but cannot refund data still owned by Network.framework.
final class DirectBudgetLease: @unchecked Sendable {
    private let lock = NSLock()
    private var release: (@Sendable () -> Void)?
    init(_ release: @escaping @Sendable () -> Void) { self.release = release }
    func complete() {
        let callback = lock.withLock { let callback = release; release = nil; return callback }
        callback?()
    }
    deinit { complete() }
}
