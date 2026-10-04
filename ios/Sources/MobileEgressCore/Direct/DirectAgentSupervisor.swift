#if canImport(Network) && canImport(Security)
import Foundation

public struct DirectPeerStatus: Sendable, Equatable {
    public let clientID: String
    public let state: String
    public let streams: Int
    public let uploaded: UInt64
    public let downloaded: UInt64
    public var recovery: DirectPeerRecovery? = nil
}

/// One actor per tunnel keeps stream identifiers isolated. One shared budget spans
/// every session generation, including native sends finishing after cancellation.
public actor DirectAgentSupervisor {
    private struct Peer {
        let epoch: UUID
        let identity: AgentIdentity?
        let task: Task<Void, Never>
        var runtime: AgentSessionRuntime?
        var status: String = "Connecting"
        var recovery: DirectPeerRecovery?
    }
    private let repository: DirectClientRepository
    private let identityResolver: any SecurityIdentityResolving
    private let budget = DirectPhoneBudget()
    private var peers: [String: Peer] = [:]
    private var enabled = false
    private var revision: UInt64 = 0
    private var suppressedPeers: Set<String> = []

    public init(repository: DirectClientRepository, identityResolver: any SecurityIdentityResolving) {
        self.repository = repository; self.identityResolver = identityResolver
    }
    public func reconcile(serve: Bool) async {
        if !serve { await stop(); return }
        revision &+= 1
        let expectedRevision = revision
        enabled = true
        let document = await repository.snapshot()
        guard enabled, revision == expectedRevision else { return }
        let eligible = document.clients.filter { $0.enabled && !$0.removing && !suppressedPeers.contains($0.id) }
        let ids = Set(eligible.map(\.clientID))
        for id in Array(peers.keys) where !ids.contains(id) {
            guard revision == expectedRevision else { return }
            await stopPeer(id)
        }
        for client in eligible {
            guard enabled, revision == expectedRevision else { return }
            if let peer = peers[client.clientID], peer.identity != nil, peer.identity != client.identity { await stopPeer(client.clientID) }
            guard enabled, revision == expectedRevision else { return }
            guard peers[client.clientID] == nil else { continue }
            let epoch = UUID()
            let task = Task<Void, Never> { [weak self] in await self?.run(client.clientID, epoch: epoch) }
            peers[client.clientID] = Peer(epoch: epoch, identity: client.identity, task: task)
        }
    }
    public func stop() async {
        enabled = false
        revision &+= 1
        let retiring = peers
        peers.removeAll()
        for peer in retiring.values { peer.task.cancel() }
        for peer in retiring.values { await peer.runtime?.stop() }
    }
    public func stopPeer(_ id: String) async {
        guard let peer = peers.removeValue(forKey: id) else { return }
        peer.task.cancel()
        await peer.runtime?.stop()
    }
    public func retryPeer(_ id: String) async {
        revision &+= 1
        await stopPeer(id)
    }
    public func suppressPeerForRemoval(_ id: String) async {
        revision &+= 1
        suppressedPeers.insert(id)
        await stopPeer(id)
    }
    /// Only after durable deletion, or a new explicit pairing. Ordinary Start,
    /// foreground restoration and reconciliation never clear removal intent.
    public func clearRemovalSuppression(_ id: String) {
        revision &+= 1
        suppressedPeers.remove(id)
    }
    public func statuses() async -> [DirectPeerStatus] {
        var values: [DirectPeerStatus] = []
        for (id, peer) in peers {
            let snapshot = await peer.runtime?.snapshot()
            guard peers[id]?.epoch == peer.epoch else { continue }
            values.append(DirectPeerStatus(clientID: id, state: snapshot?.connectionState == .connected ? "Connected" : peer.status, streams: snapshot?.activeStreamCount ?? 0, uploaded: snapshot?.bytesUploaded ?? 0, downloaded: snapshot?.bytesDownloaded ?? 0, recovery: peer.recovery))
        }
        return values
    }
    private func run(_ id: String, epoch: UUID) async {
        var retry = 1
        while current(id, epoch), !Task.isCancelled {
            var recovery = DirectPeerRecovery.retrying
            do {
                try await repository.recover(id)
                try Task.checkCancellation()
                // Poll configuration before opening a session. It may move the peer.
                try await repository.maintain(id)
                try await repository.recover(id)
                try Task.checkCancellation()
                guard let client = await repository.snapshot().clients.first(where: { $0.clientID == id && $0.enabled && !$0.removing }),
                      let identity = client.identity, current(id, epoch) else { break }
                try DirectLocalIdentityReadiness.validate(identity, at: Date())
                // Preserve typed local key/storage errors before the websocket
                // runtime reduces transport failures to bounded diagnostics.
                _ = try identityResolver.securityIdentity(forKeyTag: identity.keyTag)
                let socket = NetworkRelayWebSocket(configuration: try RelayWebSocketConfiguration(directIdentity: identity), identityResolver: identityResolver)
                let runtime = AgentSessionRuntime(relay: socket, targetFactory: NetworkTargetConnectionFactory(), sharedBudget: budget)
                peers[id]?.runtime = runtime
                peers[id]?.status = "Connecting"
                peers[id]?.recovery = nil
                await runtime.start()
                var ticks = 0
                while current(id, epoch), !Task.isCancelled {
                    try await Task.sleep(for: .seconds(1))
                    let snapshot = await runtime.snapshot()
                    if snapshot.connectionState == .stopped || snapshot.connectionState == .stopping { break }
                    if snapshot.connectionState == .connected { retry = 1 }
                    ticks += 1
                    if ticks % 30 == 0 { try await repository.maintain(id) }
                    if await repository.snapshot().clients.first(where: { $0.clientID == id })?.identity != identity { break }
                }
                await runtime.stop()
                if current(id, epoch) { peers[id]?.runtime = nil }
            } catch {
                recovery = DirectPeerRecovery.classify(error)
                if current(id, epoch) {
                    await peers[id]?.runtime?.stop()
                    if current(id, epoch) { peers[id]?.runtime = nil }
                }
            }
            guard current(id, epoch), !Task.isCancelled else { break }
            peers[id]?.status = recovery.guidance
            peers[id]?.recovery = recovery
            // Keep the row and credentials; ordinary reconciliation cannot
            // restart a peer waiting for explicit recovery.
            if !recovery.automaticallyRetries { return }
            do { try await Task.sleep(for: .seconds(retry)) } catch { break }
            retry = min(30, retry * 2)
        }
        if current(id, epoch) { await stopPeer(id) }
    }
    private func current(_ id: String, _ epoch: UUID) -> Bool { enabled && peers[id]?.epoch == epoch }
}
#endif
