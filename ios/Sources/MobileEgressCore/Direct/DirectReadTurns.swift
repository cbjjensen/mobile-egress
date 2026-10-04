import Foundation

/// Schedules already-readable, budgeted chunks. A turn never spans an idle
/// receive or a transport send. A busy peer returns to the FIFO tail after one
/// chunk, so queued work from another peer cannot be starved by its streams.
final class DirectReadTurns: @unchecked Sendable {
    final class Peer: @unchecked Sendable {
        let id = UUID()
        fileprivate var cancelled = false
    }
    private struct Waiting {
        let peer: Peer
        let key: String
        let continuation: CheckedContinuation<DirectBudgetLease?, Never>
    }
    private let lock = NSLock()
    private var owner: (peer: UUID, turn: UUID)?
    private var order: [UUID] = []
    private var pending: [UUID: [Waiting]] = [:]
    var waitingCount: Int { lock.withLock { pending.values.reduce(0) { $0 + $1.count } } }
    func makePeer() -> Peer { Peer() }
    func acquire(_ peer: Peer, key: String = UUID().uuidString) async -> DirectBudgetLease? {
        await withCheckedContinuation { continuation in
            lock.withLock {
                guard !peer.cancelled else { continuation.resume(returning: nil); return }
                if owner == nil { continuation.resume(returning: take(peer)); return }
                if pending[peer.id] == nil { order.append(peer.id) }
                pending[peer.id, default: []].append(Waiting(peer: peer, key: key, continuation: continuation))
            }
        }
    }
    func cancelWaiter(_ peer: Peer, key: String) {
        lock.withLock {
            guard let queue = pending[peer.id] else { return }
            let cancelled = queue.filter { $0.key == key }
            let remaining = queue.filter { $0.key != key }
            if remaining.isEmpty { pending.removeValue(forKey: peer.id); order.removeAll { $0 == peer.id } }
            else { pending[peer.id] = remaining }
            for waiter in cancelled { waiter.continuation.resume(returning: nil) }
        }
    }
    func cancel(_ peer: Peer) {
        lock.withLock {
            peer.cancelled = true
            order.removeAll { $0 == peer.id }
            for waiter in pending.removeValue(forKey: peer.id) ?? [] { waiter.continuation.resume(returning: nil) }
            if owner?.peer == peer.id { owner = nil; advance() }
        }
    }
    private func take(_ peer: Peer) -> DirectBudgetLease {
        let id = UUID(); owner = (peer.id, id)
        return DirectBudgetLease { [weak self] in self?.release(id) }
    }
    private func release(_ id: UUID) {
        lock.withLock {
            guard owner?.turn == id else { return }
            owner = nil; advance()
        }
    }
    private func advance() {
        guard !order.isEmpty else { return }
        let id = order.removeFirst()
        guard var queue = pending.removeValue(forKey: id), !queue.isEmpty else { return }
        let next = queue.removeFirst()
        if !queue.isEmpty { pending[id] = queue; order.append(id) }
        next.continuation.resume(returning: take(next.peer))
    }
}
