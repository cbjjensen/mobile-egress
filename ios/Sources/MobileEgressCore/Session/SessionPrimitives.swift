import Foundation

final class BoundedDeque<Element>: @unchecked Sendable {
    private var storage: [Element?]
    private var head = 0
    private(set) var count = 0

    init(capacity: Int) {
        precondition(capacity > 0)
        storage = Array(repeating: nil, count: capacity)
    }

    var isEmpty: Bool { count == 0 }
    var isFull: Bool { count == storage.count }
    var backingCapacity: Int { storage.count }
    var occupiedSlotCount: Int { storage.reduce(into: 0) { $0 += $1 == nil ? 0 : 1 } }

    @discardableResult
    func append(_ element: Element) -> Bool {
        guard !isFull else { return false }
        storage[(head + count) % storage.count] = element
        count += 1
        return true
    }

    func popFirst() -> Element? {
        guard count > 0 else { return nil }
        let index = head
        let element = storage[index]
        storage[index] = nil
        head = (head + 1) % storage.count
        count -= 1
        return element
    }

    func removeAll(where shouldRemove: (Element) -> Bool) {
        let originalCount = count
        for _ in 0 ..< originalCount {
            guard let element = popFirst() else { preconditionFailure("Bounded deque count is inconsistent") }
            if !shouldRemove(element) {
                precondition(append(element))
            }
        }
    }

    func removeAll() {
        while popFirst() != nil {}
    }

    func copy() -> BoundedDeque<Element> {
        let duplicate = BoundedDeque<Element>(capacity: storage.count)
        for offset in 0 ..< count {
            guard let element = storage[(head + offset) % storage.count] else {
                preconditionFailure("Bounded deque storage is inconsistent")
            }
            precondition(duplicate.append(element))
        }
        return duplicate
    }
}

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
    var outstandingBytes = 0
    var dataCapacityReleased = false

    init(canceled: Bool) {
        self.canceled = canceled
    }
}

private final class OutboundCompletion: @unchecked Sendable {
    var emission: OutboundEmission?
    var released = false
}

public struct OutboundFrame: @unchecked Sendable {
    public let id: UInt64
    public let bytes: Data
    let streamID: String?
    fileprivate let streamCancellation: OutboundCancellation?
    fileprivate let dataCancellation: OutboundCancellation?
    fileprivate let completion: OutboundCompletion

    fileprivate init(
        id: UInt64,
        bytes: Data,
        streamID: String?,
        streamCancellation: OutboundCancellation?,
        dataCancellation: OutboundCancellation?,
        completion: OutboundCompletion
    ) {
        self.id = id
        self.bytes = bytes
        self.streamID = streamID
        self.streamCancellation = streamCancellation
        self.dataCancellation = dataCancellation
        self.completion = completion
    }
}

public enum OutboundEmission: Equatable, Sendable {
    case emitted
    case canceled
    case failed
}

struct OutboundMailboxRetentionSnapshot: Equatable, Sendable {
    let blockedDataStreams: Int
    let canceledDataStreams: Int
    let canceledStreams: Int
}

struct OutboundMailboxBookkeepingSnapshot: Equatable, Sendable {
    let streamCancellationRecords: Int
    let dataCancellationRecords: Int
    let outstandingStreamFrames: Int
    let outstandingDataFrames: Int
    let outstandingDataBytes: Int

    static let empty = OutboundMailboxBookkeepingSnapshot(
        streamCancellationRecords: 0,
        dataCancellationRecords: 0,
        outstandingStreamFrames: 0,
        outstandingDataFrames: 0,
        outstandingDataBytes: 0
    )
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
    private let dataByteCapacity: Int
    private let controls: BoundedDeque<ControlFrame>
    private var dataByStream: [String: BoundedDeque<OutboundFrame>] = [:]
    private let readyStreams: BoundedDeque<String>
    private let blockedDataStreams: BoundedOrderedSet
    private let canceledDataStreams: BoundedOrderedSet
    private let canceledStreams: BoundedOrderedSet
    private var streamCancellations: [String: OutboundCancellation] = [:]
    private var dataCancellations: [String: OutboundCancellation] = [:]
    private var outstandingDataFrames = 0
    private var outstandingDataBytes = 0
    private var nextFrameID: UInt64 = 1
    private var closed = false

    public init(
        controlCapacity: Int,
        dataCapacity: Int,
        perStreamDataCapacity: Int,
        dataByteCapacity: Int,
        cancellationHistoryCapacity: Int = 1_024
    ) {
        precondition(controlCapacity > 0)
        precondition(dataCapacity > 0)
        precondition((1 ... dataCapacity).contains(perStreamDataCapacity))
        precondition(dataByteCapacity > 0)
        precondition(cancellationHistoryCapacity > 0)
        self.controlCapacity = controlCapacity
        self.dataCapacity = dataCapacity
        self.perStreamDataCapacity = perStreamDataCapacity
        self.dataByteCapacity = dataByteCapacity
        controls = BoundedDeque(capacity: controlCapacity)
        readyStreams = BoundedDeque(capacity: dataCapacity)
        blockedDataStreams = BoundedOrderedSet(limit: cancellationHistoryCapacity)
        canceledDataStreams = BoundedOrderedSet(limit: cancellationHistoryCapacity)
        canceledStreams = BoundedOrderedSet(limit: cancellationHistoryCapacity)
    }

    var retentionSnapshot: OutboundMailboxRetentionSnapshot {
        lock.withLock {
            OutboundMailboxRetentionSnapshot(
                blockedDataStreams: blockedDataStreams.count,
                canceledDataStreams: canceledDataStreams.count,
                canceledStreams: canceledStreams.count
            )
        }
    }

    var bookkeepingSnapshot: OutboundMailboxBookkeepingSnapshot {
        lock.withLock {
            OutboundMailboxBookkeepingSnapshot(
                streamCancellationRecords: streamCancellations.count,
                dataCancellationRecords: dataCancellations.count,
                outstandingStreamFrames: streamCancellations.values.reduce(0) { $0 + $1.outstanding },
                outstandingDataFrames: outstandingDataFrames,
                outstandingDataBytes: outstandingDataBytes
            )
        }
    }

    public func allowData(_ streamID: String) {
        lock.withLock {
            streamCancellations.removeValue(forKey: streamID)?.canceled = true
            if let cancellation = dataCancellations.removeValue(forKey: streamID) {
                cancellation.canceled = true
                refundDataCapacity(cancellation)
            }
            blockedDataStreams.remove(streamID)
            canceledDataStreams.remove(streamID)
            canceledStreams.remove(streamID)
        }
    }

    public func offerData(_ frame: Data, streamID: String) -> Bool {
        lock.withLock {
            guard !closed,
                  !blockedDataStreams.contains(streamID),
                  outstandingDataFrames < dataCapacity,
                  frame.count <= dataByteCapacity - outstandingDataBytes,
                  (dataCancellations[streamID]?.outstanding ?? 0) < perStreamDataCapacity
            else { return false }
            let frames: BoundedDeque<OutboundFrame>
            let insertedStream: Bool
            if let existing = dataByStream[streamID] {
                frames = existing
                insertedStream = false
            } else {
                frames = BoundedDeque(capacity: perStreamDataCapacity)
                guard readyStreams.append(streamID) else { return false }
                dataByStream[streamID] = frames
                insertedStream = true
            }
            let outboundFrame = makeFrame(bytes: frame, streamID: streamID, isData: true)
            guard frames.append(outboundFrame) else {
                release(outboundFrame)
                if insertedStream {
                    dataByStream.removeValue(forKey: streamID)
                    readyStreams.removeAll { $0 == streamID }
                }
                return false
            }
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
            return controls.append(ControlFrame(
                frame: makeFrame(bytes: frame, streamID: streamID, isData: false),
                afterDataStreamID: nil
            ))
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
            return controls.append(ControlFrame(
                frame: makeFrame(bytes: frame, streamID: streamID, isData: false),
                afterDataStreamID: streamID
            ))
        }
        if !accepted { onSaturated() }
        return accepted
    }

    public func blockAndDiscardData(streamID: String) {
        lock.withLock {
            blockDataStream(streamID)
            cancelDataStream(streamID)
            dataCancellations[streamID]?.canceled = true
            refundDataCapacity(dataCancellations[streamID])
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
            refundDataCapacity(dataCancellations[streamID])
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
            if let emission = frame.completion.emission {
                return emission
            }
            let canceled = frame.streamCancellation?.canceled == true || frame.dataCancellation?.canceled == true
            let result: OutboundEmission
            if canceled {
                result = .canceled
            } else if sender(frame.bytes) {
                result = .emitted
            } else {
                result = .failed
            }
            frame.completion.emission = result
            release(frame)
            return result
        }
    }

    public func close() {
        lock.withLock {
            guard !closed else { return }
            closed = true
            while let control = controls.popFirst() {
                release(control.frame)
            }
            for frames in dataByStream.values {
                while let frame = frames.popFirst() {
                    release(frame)
                }
            }
            dataByStream.removeAll(keepingCapacity: false)
            readyStreams.removeAll()
            blockedDataStreams.removeAll()
            canceledDataStreams.removeAll()
            canceledStreams.removeAll()
            streamCancellations.values.forEach { $0.canceled = true }
            dataCancellations.values.forEach {
                $0.canceled = true
                refundDataCapacity($0)
            }
            streamCancellations.removeAll(keepingCapacity: false)
            dataCancellations.removeAll(keepingCapacity: false)
            precondition(outstandingDataFrames == 0)
            precondition(outstandingDataBytes == 0)
        }
    }

    private func pollEligibleControl() -> OutboundFrame? {
        let candidateCount = controls.count
        for _ in 0 ..< candidateCount {
            guard let control = controls.popFirst() else { return nil }
            if control.afterDataStreamID == nil || dataByStream[control.afterDataStreamID!] == nil {
                return control.frame
            }
            precondition(controls.append(control))
        }
        return nil
    }

    private func pollData() -> OutboundFrame? {
        guard let streamID = readyStreams.popFirst(),
              let frames = dataByStream[streamID],
              let frame = frames.popFirst()
        else { return nil }
        if frames.isEmpty {
            dataByStream.removeValue(forKey: streamID)
        } else {
            precondition(readyStreams.append(streamID))
        }
        return frame
    }

    private func discardData(_ streamID: String) {
        if let discarded = dataByStream.removeValue(forKey: streamID) {
            while let frame = discarded.popFirst() {
                release(frame)
            }
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
            cancellation(
                in: &dataCancellations,
                streamID: $0,
                canceled: canceledDataStreams.contains($0),
                byteCount: bytes.count
            )
        } : nil
        if isData {
            outstandingDataFrames += 1
            outstandingDataBytes += bytes.count
        }
        return OutboundFrame(
            id: id,
            bytes: bytes,
            streamID: streamID,
            streamCancellation: streamCancellation,
            dataCancellation: dataCancellation,
            completion: OutboundCompletion()
        )
    }

    private func cancellation(
        in cancellations: inout [String: OutboundCancellation],
        streamID: String,
        canceled: Bool,
        byteCount: Int = 0
    ) -> OutboundCancellation {
        let cancellation = cancellations[streamID] ?? OutboundCancellation(canceled: canceled)
        cancellations[streamID] = cancellation
        cancellation.outstanding += 1
        cancellation.outstandingBytes += byteCount
        return cancellation
    }

    private func release(_ frame: OutboundFrame) {
        guard !frame.completion.released else { return }
        frame.completion.released = true
        guard let streamID = frame.streamID else { return }
        release(in: &streamCancellations, streamID: streamID, cancellation: frame.streamCancellation)
        if let cancellation = frame.dataCancellation, !cancellation.dataCapacityReleased {
            precondition(outstandingDataFrames > 0)
            precondition(outstandingDataBytes >= frame.bytes.count)
            outstandingDataFrames -= 1
            outstandingDataBytes -= frame.bytes.count
        }
        release(
            in: &dataCancellations,
            streamID: streamID,
            cancellation: frame.dataCancellation,
            byteCount: frame.dataCancellation == nil ? 0 : frame.bytes.count
        )
    }

    private func release(
        in cancellations: inout [String: OutboundCancellation],
        streamID: String,
        cancellation: OutboundCancellation?,
        byteCount: Int = 0
    ) {
        guard let cancellation else { return }
        precondition(cancellation.outstanding > 0)
        precondition(cancellation.outstandingBytes >= byteCount)
        cancellation.outstanding -= 1
        cancellation.outstandingBytes -= byteCount
        if cancellation.outstanding == 0, cancellations[streamID] === cancellation {
            cancellations.removeValue(forKey: streamID)
        }
    }

    private func refundDataCapacity(_ cancellation: OutboundCancellation?) {
        guard let cancellation, !cancellation.dataCapacityReleased else { return }
        precondition(outstandingDataFrames >= cancellation.outstanding)
        precondition(outstandingDataBytes >= cancellation.outstandingBytes)
        outstandingDataFrames -= cancellation.outstanding
        outstandingDataBytes -= cancellation.outstandingBytes
        cancellation.dataCapacityReleased = true
    }

    private func blockDataStream(_ streamID: String) {
        blockedDataStreams.insert(streamID)
    }

    private func cancelDataStream(_ streamID: String) {
        canceledDataStreams.insert(streamID)
    }
}

private final class BoundedOrderedSet {
    let limit: Int
    private var values: Set<String> = []
    private let order: BoundedDeque<String>

    init(limit: Int) {
        precondition(limit > 0)
        self.limit = limit
        order = BoundedDeque(capacity: limit)
    }

    var count: Int { values.count }

    func insert(_ value: String) {
        if values.contains(value) {
            order.removeAll { $0 == value }
            precondition(order.append(value))
            return
        }
        if order.isFull, let evicted = order.popFirst() {
            values.remove(evicted)
        }
        values.insert(value)
        precondition(order.append(value))
    }

    func remove(_ value: String) {
        guard values.remove(value) != nil else { return }
        order.removeAll { $0 == value }
    }

    func removeAll() {
        values.removeAll(keepingCapacity: false)
        order.removeAll()
    }

    func contains(_ value: String) -> Bool {
        values.contains(value)
    }
}

private final class TombstoneStorage: @unchecked Sendable {
    let limit: Int
    let ordered: BoundedDeque<String>
    var values: Set<String>

    init(limit: Int, ordered: BoundedDeque<String>? = nil, values: Set<String> = []) {
        self.limit = limit
        self.ordered = ordered ?? BoundedDeque(capacity: limit)
        self.values = values
    }

    func copy() -> TombstoneStorage {
        TombstoneStorage(limit: limit, ordered: ordered.copy(), values: values)
    }
}

public struct TombstoneWindow: @unchecked Sendable {
    private var storage: TombstoneStorage

    public init(limit: Int) {
        precondition(limit > 0)
        storage = TombstoneStorage(limit: limit)
    }

    public var count: Int { storage.values.count }

    public mutating func insert(_ streamID: String) {
        ensureUniqueStorage()
        if storage.values.contains(streamID) {
            storage.ordered.removeAll { $0 == streamID }
            precondition(storage.ordered.append(streamID))
            return
        }
        if storage.ordered.isFull, let evicted = storage.ordered.popFirst() {
            storage.values.remove(evicted)
        }
        storage.values.insert(streamID)
        precondition(storage.ordered.append(streamID))
    }

    public func contains(_ streamID: String) -> Bool {
        storage.values.contains(streamID)
    }

    public mutating func removeAll() {
        ensureUniqueStorage()
        storage.ordered.removeAll()
        storage.values.removeAll(keepingCapacity: false)
    }

    private mutating func ensureUniqueStorage() {
        if !isKnownUniquelyReferenced(&storage) {
            storage = storage.copy()
        }
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
