import Foundation

struct ReceiveDeliveryGate: Sendable {
    struct Generation: Equatable, Hashable, Sendable {
        fileprivate let value: UInt64
    }

    struct Delivery: Equatable, Hashable, Sendable {
        fileprivate let generation: Generation
        fileprivate let value: UInt64
        fileprivate let resumesReceiving: Bool
    }

    private var activeGeneration: Generation?
    private var receiveGeneration: Generation?
    private var outstandingDelivery: Delivery?
    private var receivingEnabled = false
    private var nextGeneration: UInt64 = 1
    private var nextDelivery: UInt64 = 1

    var hasDeliveryOutstanding: Bool {
        outstandingDelivery != nil
    }

    mutating func beginGeneration() -> Generation {
        let generation = Generation(value: nextGeneration)
        nextGeneration &+= 1
        activeGeneration = generation
        receiveGeneration = nil
        receivingEnabled = true
        return generation
    }

    mutating func invalidate(_ generation: Generation) {
        guard activeGeneration == generation else { return }
        activeGeneration = nil
        receivingEnabled = false
        if receiveGeneration == generation {
            receiveGeneration = nil
        }
    }

    mutating func stopReceiving(_ generation: Generation) {
        guard activeGeneration == generation else { return }
        receivingEnabled = false
    }

    mutating func beginReceive(_ generation: Generation) -> Bool {
        guard activeGeneration == generation,
              receivingEnabled,
              receiveGeneration == nil,
              outstandingDelivery == nil
        else { return false }
        receiveGeneration = generation
        return true
    }

    mutating func abandonReceive(_ generation: Generation) {
        guard receiveGeneration == generation else { return }
        receiveGeneration = nil
    }

    mutating func beginDelivery(
        _ generation: Generation,
        resumeReceiving: Bool
    ) -> Delivery? {
        guard activeGeneration == generation,
              receiveGeneration == nil,
              outstandingDelivery == nil
        else { return nil }
        return reserveDelivery(generation, resumeReceiving: resumeReceiving)
    }

    mutating func completeReceive(
        _ generation: Generation,
        resumeReceiving: Bool
    ) -> Delivery? {
        guard activeGeneration == generation,
              receiveGeneration == generation,
              outstandingDelivery == nil
        else { return nil }
        receiveGeneration = nil
        return reserveDelivery(generation, resumeReceiving: resumeReceiving)
    }

    mutating func completeDelivery(_ delivery: Delivery) -> Bool {
        guard outstandingDelivery == delivery else { return false }
        outstandingDelivery = nil
        return activeGeneration == delivery.generation &&
            receivingEnabled &&
            delivery.resumesReceiving
    }

    private mutating func reserveDelivery(
        _ generation: Generation,
        resumeReceiving: Bool
    ) -> Delivery {
        let delivery = Delivery(
            generation: generation,
            value: nextDelivery,
            resumesReceiving: resumeReceiving
        )
        nextDelivery &+= 1
        outstandingDelivery = delivery
        return delivery
    }
}

struct TargetDuplexLifecycle: Sendable {
    private enum State: Sendable {
        case idle
        case open(readEnded: Bool)
        case failed
        case canceled
    }

    private var state: State = .idle

    var canRead: Bool {
        if case .open(readEnded: false) = state { return true }
        return false
    }

    var canWrite: Bool {
        if case .open = state { return true }
        return false
    }

    var isTerminal: Bool {
        switch state {
        case .failed, .canceled:
            return true
        case .idle, .open:
            return false
        }
    }

    mutating func markReady() -> Bool {
        guard case .idle = state else { return false }
        state = .open(readEnded: false)
        return true
    }

    mutating func receive(content: Data?, isComplete: Bool) -> [TargetConnectionEvent] {
        guard canRead else { return [] }
        var events: [TargetConnectionEvent] = []
        if let content, !content.isEmpty {
            events.append(.data(content))
        }
        if isComplete {
            state = .open(readEnded: true)
            events.append(.ended)
        }
        return events
    }

    mutating func fail() -> TargetConnectionEvent? {
        switch state {
        case .idle, .open:
            state = .failed
            return .failed
        case .failed, .canceled:
            return nil
        }
    }

    mutating func cancel() -> Bool {
        guard case .canceled = state else {
            state = .canceled
            return true
        }
        return false
    }
}
