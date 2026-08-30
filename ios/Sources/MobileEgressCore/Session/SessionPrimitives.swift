import Foundation

public final class StreamAdmission: @unchecked Sendable {
    private let lock = NSLock()
    private let limit: Int
    private var reserved: Set<String> = []

    public init(limit: Int) {
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
        lock.withLock { defer { reserved.removeAll() }; return reserved }
    }
}

public final class OutboundMailbox: @unchecked Sendable {
    private let lock = NSLock()
    private let controlCapacity: Int
    private let dataCapacity: Int
    private let perStreamDataCapacity: Int
    private var controls: [Data] = []
    private var dataByStream: [String: [Data]] = [:]
    private var streamOrder: [String] = []

    public init(controlCapacity: Int, dataCapacity: Int, perStreamDataCapacity: Int) {
        self.controlCapacity = controlCapacity
        self.dataCapacity = dataCapacity
        self.perStreamDataCapacity = perStreamDataCapacity
    }

    public func offerRequiredControl(_ frame: Data, streamID: String?, onSaturated: () -> Void) -> Bool {
        let accepted = lock.withLock {
            guard controls.count < controlCapacity else { return false }
            controls.append(frame)
            return true
        }
        if !accepted { onSaturated() }
        return accepted
    }

    public func offerData(_ frame: Data, streamID: String) -> Bool {
        lock.withLock {
            let dataCount = dataByStream.values.reduce(0) { $0 + $1.count }
            guard dataCount < dataCapacity else { return false }
            var streamFrames = dataByStream[streamID, default: []]
            guard streamFrames.count < perStreamDataCapacity else { return false }
            streamFrames.append(frame)
            dataByStream[streamID] = streamFrames
            if !streamOrder.contains(streamID) { streamOrder.append(streamID) }
            return true
        }
    }

    public func poll() -> Data? {
        lock.withLock {
            if !controls.isEmpty { return controls.removeFirst() }
            guard let streamID = streamOrder.first, var frames = dataByStream[streamID] else { return nil }
            let frame = frames.removeFirst()
            streamOrder.removeFirst()
            if frames.isEmpty {
                dataByStream.removeValue(forKey: streamID)
            } else {
                dataByStream[streamID] = frames
                streamOrder.append(streamID)
            }
            return frame
        }
    }
}

public struct TombstoneWindow {
    private let limit: Int
    private var ordered: [String] = []
    private var values: Set<String> = []

    public init(limit: Int) {
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
