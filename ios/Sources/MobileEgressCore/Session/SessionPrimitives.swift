import Foundation

public final class StreamAdmission: @unchecked Sendable {
    private let lock = NSLock()
    private let limit: Int
    private var reserved: Set<String> = []

    public init(limit: Int) {
        precondition(limit > 0)
        self.limit = limit
    }

    public var count: Int {
        lock.withLock { reserved.count }
    }

    public func tryReserve(_ streamID: String) -> Bool {
        lock.withLock {
            guard reserved.count < limit, !reserved.contains(streamID) else { return false }
            reserved.insert(streamID)
            return true
        }
    }

    public func release(_ streamID: String) {
        _ = lock.withLock { reserved.remove(streamID) }
    }

    public func clear() -> Set<String> {
        lock.withLock {
            let previous = reserved
            reserved.removeAll(keepingCapacity: false)
            return previous
        }
    }
}

private final class OutboundCancellation: @unchecked Sendable {
    var canceled: Bool
    var outstanding = 0

    init(canceled: Bool) {
        self.canceled = canceled
    }
}

public struct OutboundFrame: @unchecked Sendable {
    public let id: UInt64
    public let bytes: Data
    let streamID: String?
    fileprivate let streamCancellation: OutboundCancellation?
    fileprivate let dataCancellation: OutboundCancellation?

    fileprivate init(
        id: UInt64,
        bytes: Data,
        streamID: String?,
        streamCancellation: OutboundCancellation?,
        dataCancellation: OutboundCancellation?
    ) {
        self.id = id
        self.bytes = bytes
        self.streamID = streamID
        self.streamCancellation = streamCancellation
        self.dataCancellation = dataCancellation
    }
}

public enum OutboundEmission: Equatable, Sendable {
    case emitted
    case canceled
    case failed
}

public final class OutboundMailbox: @unchecked Sendable {
    private struct ControlFrame {
        let frame: OutboundFrame
        let afterDataStreamID: String?
    }

    private let lock = NSLock()
    private let controlCapacity: Int
    private let dataCapacity: Int
    private let perStreamDataCapacity: Int
    private var controls: [ControlFrame] = []
    private var dataByStream: [String: [OutboundFrame]] = [:]
    private var readyStreams: [String] = []
    private var blockedDataStreams = BoundedOrderedSet(limit: 128)
    private var canceledDataStreams = BoundedOrderedSet(limit: 128)
    private var canceledStreams = BoundedOrderedSet(limit: 128)
    private var streamCancellations: [String: OutboundCancellation] = [:]
    private var dataCancellations: [String: OutboundCancellation] = [:]
    private var dataCount = 0
    private var nextFrameID: UInt64 = 1
    private var closed = false

    public init(controlCapacity: Int, dataCapacity: Int, perStreamDataCapacity: Int) {
        precondition(controlCapacity > 0)
        precondition(dataCapacity > 0)
        precondition((1 ... dataCapacity).contains(perStreamDataCapacity))
        self.controlCapacity = controlCapacity
        self.dataCapacity = dataCapacity
        self.perStreamDataCapacity = perStreamDataCapacity
    }

    public func allowData(_ streamID: String) {
        lock.withLock {
            streamCancellations.removeValue(forKey: streamID)?.canceled = true
            dataCancellations.removeValue(forKey: streamID)?.canceled = true
            blockedDataStreams.remove(streamID)
            canceledDataStreams.remove(streamID)
            canceledStreams.remove(streamID)
        }
    }

    public func offerData(_ frame: Data, streamID: String) -> Bool {
        lock.withLock {
            guard !closed, !blockedDataStreams.contains(streamID), dataCount < dataCapacity else { return false }
            var frames = dataByStream[streamID, default: []]
            guard frames.count < perStreamDataCapacity else { return false }
            if frames.isEmpty { readyStreams.append(streamID) }
            frames.append(makeFrame(bytes: frame, streamID: streamID, isData: true))
            dataByStream[streamID] = frames
            dataCount += 1
            return true
        }
    }

    public func offerRequiredControl(
        _ frame: Data,
        streamID: String?,
        onSaturated: () -> Void
    ) -> Bool {
        let accepted = lock.withLock {
            guard !closed, controls.count < controlCapacity else { return false }
            controls.append(ControlFrame(
                frame: makeFrame(bytes: frame, streamID: streamID, isData: false),
                afterDataStreamID: nil
            ))
            return true
        }
        if !accepted { onSaturated() }
        return accepted
    }

    public func offerRequiredControlAfterData(
        _ frame: Data,
        streamID: String,
        onSaturated: () -> Void
    ) -> Bool {
        let accepted = lock.withLock {
            guard !closed, controls.count < controlCapacity else { return false }
            blockDataStream(streamID)
            controls.append(ControlFrame(
                frame: makeFrame(bytes: frame, streamID: streamID, isData: false),
                afterDataStreamID: streamID
            ))
            return true
        }
        if !accepted { onSaturated() }
        return accepted
    }

    public func blockAndDiscardData(streamID: String) {
        lock.withLock {
            blockDataStream(streamID)
            cancelDataStream(streamID)
            dataCancellations[streamID]?.canceled = true
            discardData(streamID)
        }
    }

    @discardableResult
    public func cancelStream(_ streamID: String) -> Bool {
        lock.withLock {
            let hadOutstandingFrame = (streamCancellations[streamID]?.outstanding ?? 0) > 0
            blockDataStream(streamID)
            cancelDataStream(streamID)
            streamCancellations[streamID]?.canceled = true
            dataCancellations[streamID]?.canceled = true
            discardData(streamID)
            canceledStreams.insert(streamID)
            controls.removeAll { control in
                guard control.frame.streamID == streamID else { return false }
                release(control.frame)
                return true
            }
            return hadOutstandingFrame
        }
    }

    public func poll() -> OutboundFrame? {
        lock.withLock {
            pollEligibleControl() ?? pollData()
        }
    }

    public func emit(_ frame: OutboundFrame, sender: (Data) -> Bool) -> OutboundEmission {
        lock.withLock {
            let canceled = frame.streamCancellation?.canceled == true || frame.dataCancellation?.canceled == true
            let result: OutboundEmission
            if canceled {
                result = .canceled
            } else if sender(frame.bytes) {
                result = .emitted
            } else {
                result = .failed
            }
            release(frame)
            return result
        }
    }

    public func close() {
        lock.withLock {
            guard !closed else { return }
            closed = true
            controls.removeAll(keepingCapacity: false)
            dataByStream.removeAll(keepingCapacity: false)
            readyStreams.removeAll(keepingCapacity: false)
            blockedDataStreams.removeAll()
            canceledDataStreams.removeAll()
            canceledStreams.removeAll()
            streamCancellations.values.forEach { $0.canceled = true }
            dataCancellations.values.forEach { $0.canceled = true }
            streamCancellations.removeAll(keepingCapacity: false)
            dataCancellations.removeAll(keepingCapacity: false)
            dataCount = 0
        }
    }

    private func pollEligibleControl() -> OutboundFrame? {
        for _ in controls.indices {
            let control = controls.removeFirst()
            if control.afterDataStreamID == nil || dataByStream[control.afterDataStreamID!] == nil {
                return control.frame
            }
            controls.append(control)
        }
        return nil
    }

    private func pollData() -> OutboundFrame? {
        guard !readyStreams.isEmpty else { return nil }
        let streamID = readyStreams.removeFirst()
        guard var frames = dataByStream[streamID], !frames.isEmpty else { return nil }
        let frame = frames.removeFirst()
        dataCount -= 1
        if frames.isEmpty {
            dataByStream.removeValue(forKey: streamID)
        } else {
            dataByStream[streamID] = frames
            readyStreams.append(streamID)
        }
        return frame
    }

    private func discardData(_ streamID: String) {
        if let discarded = dataByStream.removeValue(forKey: streamID) {
            dataCount -= discarded.count
            discarded.forEach(release)
        }
        readyStreams.removeAll { $0 == streamID }
    }

    private func makeFrame(bytes: Data, streamID: String?, isData: Bool) -> OutboundFrame {
        let id = nextFrameID
        nextFrameID &+= 1
        let streamCancellation = streamID.map {
            cancellation(in: &streamCancellations, streamID: $0, canceled: canceledStreams.contains($0))
        }
        let dataCancellation = isData ? streamID.map {
            cancellation(in: &dataCancellations, streamID: $0, canceled: canceledDataStreams.contains($0))
        } : nil
        return OutboundFrame(
            id: id,
            bytes: bytes,
            streamID: streamID,
            streamCancellation: streamCancellation,
            dataCancellation: dataCancellation
        )
    }

    private func cancellation(
        in cancellations: inout [String: OutboundCancellation],
        streamID: String,
        canceled: Bool
    ) -> OutboundCancellation {
        let cancellation = cancellations[streamID] ?? OutboundCancellation(canceled: canceled)
        cancellations[streamID] = cancellation
        cancellation.outstanding += 1
        return cancellation
    }

    private func release(_ frame: OutboundFrame) {
        guard let streamID = frame.streamID else { return }
        release(in: &streamCancellations, streamID: streamID, cancellation: frame.streamCancellation)
        release(in: &dataCancellations, streamID: streamID, cancellation: frame.dataCancellation)
    }

    private func release(
        in cancellations: inout [String: OutboundCancellation],
        streamID: String,
        cancellation: OutboundCancellation?
    ) {
        guard let cancellation else { return }
        cancellation.outstanding -= 1
        if cancellation.outstanding == 0, cancellations[streamID] === cancellation {
            cancellations.removeValue(forKey: streamID)
        }
    }

    private func blockDataStream(_ streamID: String) {
        blockedDataStreams.insert(streamID)
    }

    private func cancelDataStream(_ streamID: String) {
        canceledDataStreams.insert(streamID)
    }
}

private struct BoundedOrderedSet {
    let limit: Int
    private var values: Set<String> = []
    private var order: [String] = []

    init(limit: Int) {
        self.limit = limit
    }

    mutating func insert(_ value: String) {
        guard values.insert(value).inserted else { return }
        order.append(value)
        if order.count > limit {
            values.remove(order.removeFirst())
        }
    }

    mutating func remove(_ value: String) {
        guard values.remove(value) != nil else { return }
        order.removeAll { $0 == value }
    }

    mutating func removeAll() {
        values.removeAll(keepingCapacity: false)
        order.removeAll(keepingCapacity: false)
    }

    func contains(_ value: String) -> Bool {
        values.contains(value)
    }
}

public struct TombstoneWindow: Sendable {
    private let limit: Int
    private var ordered: [String] = []
    private var values: Set<String> = []

    public init(limit: Int) {
        precondition(limit > 0)
        self.limit = limit
    }

    public var count: Int { values.count }

    public mutating func insert(_ streamID: String) {
        guard values.insert(streamID).inserted else { return }
        ordered.append(streamID)
        if ordered.count > limit {
            values.remove(ordered.removeFirst())
        }
    }

    public func contains(_ streamID: String) -> Bool {
        values.contains(streamID)
    }

    public mutating func removeAll() {
        ordered.removeAll(keepingCapacity: false)
        values.removeAll(keepingCapacity: false)
    }
}

public final class SessionCloseGate: @unchecked Sendable {
    private let lock = NSLock()
    private var closed = false

    public init() {}

    public var isClosed: Bool { lock.withLock { closed } }

    public func tryClose() -> Bool {
        lock.withLock {
            guard !closed else { return false }
            closed = true
            return true
        }
    }
}
