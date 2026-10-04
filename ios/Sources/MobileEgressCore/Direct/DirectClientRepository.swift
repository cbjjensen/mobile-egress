#if canImport(Security)
import Foundation

public actor DirectClientRepository {
    private let store: any DirectIdentityVault
    private let keys: any DirectIdentityKeyManaging
    private let control: any DirectControlServing
    private let now: @Sendable () -> Date
    private let removalIntents: any DirectRemovalIntentPersisting
    private var removedClientIDs: Set<String>
    private var document: DirectRegistryDocument
    private var busy: Set<String> = []

    public init(store: any DirectIdentityVault, keys: any DirectIdentityKeyManaging, control: any DirectControlServing, now: @escaping @Sendable () -> Date = Date.init, removalIntents: (any DirectRemovalIntentPersisting)? = nil) throws {
        self.store = store; self.keys = keys; self.control = control
        self.now = now
        let removalIntents = removalIntents ?? MemoryDirectRemovalIntentStore()
        self.removalIntents = removalIntents
        removedClientIDs = try removalIntents.load()
        document = try store.load()
        // Registry absence is authoritative: credential deletion precedes the
        // record commit. Stale negative markers cannot reserve future capacity.
        removedClientIDs.formIntersection(Set(document.clients.map(\.id)))
    }
    public func snapshot() -> DirectRegistryDocument {
        try? expireUnattemptedInvitations()
        var visible = document
        for index in visible.clients.indices where removedClientIDs.contains(visible.clients[index].id) {
            visible.clients[index].enabled = false; visible.clients[index].removing = true
        }
        return visible
    }
    private func commit(_ next: DirectRegistryDocument) throws { try store.save(next); document = next }
    public func preferences(startIntent: Bool, keepAwake: Bool) throws {
        var next = document; next.startIntent = startIntent; next.keepAwake = keepAwake; try commit(next)
    }
    public func add(_ encoded: String) throws -> String {
        try expireUnattemptedInvitations()
        removedClientIDs.formIntersection(Set(document.clients.map(\.id)))
        let invitation = try DirectInvitation.parse(encoded, now: now())
        guard !removedClientIDs.contains(invitation.clientId) else { throw DirectAgentError.cancelled }
        if let existing = document.clients.first(where: { $0.clientID == invitation.clientId }) {
            guard existing.invitation == invitation, !existing.removing else { throw DirectAgentError.duplicateClient }
            return existing.clientID
        }
        guard document.clients.count < 10 else { throw DirectAgentError.capacity }
        // Persist pruning before a fresh key/record can reuse a Client ID. An
        // obsolete on-disk marker must never distrust that new pairing later.
        do { try removalIntents.save(removedClientIDs) }
        catch { throw DirectAgentError.removalIntentPersistence }
        let key = try keys.createKey()
        do {
            var client = DirectClientRecord(clientID: invitation.clientId, displayName: invitation.displayName, endpoint: try RelayOrigin.parse(invitation.endpoint))
            client.transport = invitation.transport
            client.invitation = invitation; client.key = key
            client.csrPEM = try keys.createCSR(key: key, clientID: invitation.clientId)
            var next = document; try next.insert(client); try commit(next)
            return client.clientID
        } catch { try? keys.deleteKey(tag: key.keyTag); throw error }
    }
    public func setEnabled(_ id: String, enabled: Bool) throws {
        guard !removedClientIDs.contains(id) else { throw DirectAgentError.cancelled }
        guard let index = document.clients.firstIndex(where: { $0.clientID == id && !$0.removing }) else { throw DirectAgentError.unavailable }
        var next = document; next.clients[index].enabled = enabled; try commit(next)
    }
    public func remove(_ id: String) throws {
        removedClientIDs.formIntersection(Set(document.clients.map(\.id)))
        guard let index = document.clients.firstIndex(where: { $0.clientID == id }) else { return }
        // Suppress before either persistence boundary; failure cannot turn a
        // removal back into an enabled record in this process.
        removedClientIDs.insert(id)
        do { try removalIntents.save(removedClientIDs) }
        catch { throw DirectAgentError.removalIntentPersistence }
        var next = document; next.clients[index].removing = true; next.clients[index].enabled = false; try commit(next)
        let client = document.clients[index]
        if let identity = client.identity { try store.remove(identity) }
        if let key = client.key { try keys.deleteKey(tag: key.keyTag) }
        next = document; next.clients.removeAll { $0.clientID == id }; try commit(next)
        // Trust and credentials are already durably gone. A failure to erase an
        // obsolete marker is housekeeping, not an unfinished removal UI row.
        try? clearRemovalIntent(id)
    }
    private func clearRemovalIntent(_ id: String) throws {
        guard removedClientIDs.contains(id) else { return }
        var remaining = removedClientIDs; remaining.remove(id)
        do { try removalIntents.save(remaining) }
        catch { throw DirectAgentError.removalIntentPersistence }
        removedClientIDs = remaining
    }

    /// The pending key/CSR and issued credentials are committed before either network step.
    /// Cancellation keeps them recoverable; no different key may replace an issued identity.
    public func recover(_ id: String) async throws {
        guard busy.insert(id).inserted else { throw DirectAgentError.unavailable }
        defer { busy.remove(id) }
        var client = try current(id)
        if client.identity == nil {
            try expireUnattemptedInvitations()
            client = try current(id)
            client.enrollmentAttempted = true
            try saveClient(client)
            let originalKey = client.key
            let issued: DirectIssuedIdentity
            do { issued = try await control.issue(client, renewal: false) }
            catch let rejection as DirectAgentError where rejection == .invitationInvalid || rejection == .expiredInvitation {
                try discardRejectedInvitation(client)
                throw rejection
            }
            try Task.checkCancellation()
            client = try current(id)
            guard client.key == originalKey else { throw DirectAgentError.cancelled }
            try install(issued, into: &client)
            try saveClient(client)
        }
        if client.needsAcknowledgement {
            try await control.acknowledge(client)
            try Task.checkCancellation()
            var current = try self.current(id)
            guard current.identity == client.identity, current.generation == client.generation,
                  current.endpoint == client.endpoint, current.transport == client.transport else { return }
            current.needsAcknowledgement = false; current.invitation = nil
            try saveClient(current)
        }
    }
    /// A late rejection may belong to a removed/replaced request. Never delete
    /// credentials installed since that request or a newer invitation's key.
    func discardRejectedInvitation(_ attempted: DirectClientRecord) throws {
        guard attempted.identity == nil, let key = attempted.key, let invitation = attempted.invitation,
              let index = document.clients.firstIndex(where: { $0.id == attempted.id }) else { return }
        let current = document.clients[index]
        guard !removedClientIDs.contains(current.id), !current.removing, current.identity == nil, current.pairingID == nil,
              current.key == key, current.invitation == invitation,
              current.csrPEM == attempted.csrPEM, current.generation == attempted.generation else { return }
        var next = document; next.clients.remove(at: index)
        try commit(next)
        try keys.deleteKey(tag: key.keyTag)
    }
    private func expireUnattemptedInvitations() throws {
        let expired = document.clients.filter {
            $0.identity == nil && $0.enrollmentAttempted != true &&
            $0.invitation.flatMap { ISO8601DateFormatter().date(from: $0.expiresAt) }.map { $0 <= now() } == true
        }.map(\.id)
        for id in expired { try remove(id) }
    }

    public func maintain(_ id: String) async throws {
        try await recover(id)
        guard busy.insert(id).inserted else { return }
        defer { busy.remove(id) }
        var client = try current(id)
        let update = try await control.configuration(client)
        try Task.checkCancellation()
        if !update.isEmpty { try importUpdate(update); return }
        if let pem = client.identity?.certificatePEM, try DirectCertificateExpiration.date(pem).timeIntervalSince(now()) < 7 * 86400 {
            let issued = try await control.issue(client, renewal: true)
            try Task.checkCancellation()
            let latest = try current(id)
            guard latest.identity == client.identity, latest.generation == client.generation,
                  latest.endpoint == client.endpoint, latest.transport == client.transport,
                  issued.generation == client.generation else { return }
            client = latest
            try install(issued, into: &client)
            try saveClient(client)
            try await control.acknowledge(client)
            try Task.checkCancellation()
            var acknowledged = try current(id)
            guard acknowledged.identity == client.identity, acknowledged.generation == client.generation,
                  acknowledged.endpoint == client.endpoint, acknowledged.transport == client.transport else { return }
            acknowledged.needsAcknowledgement = false; try saveClient(acknowledged)
        }
    }
    public func importUpdate(_ bundle: String, expectedClientID: String? = nil) throws {
        // Try only the ten pinned peer authorities. Identity binding is checked inside verification.
        for client in document.clients where !client.removing && !removedClientIDs.contains(client.id) {
            if let expectedClientID, client.clientID != expectedClientID { continue }
            guard let identity = client.identity,
                  let update = try? DirectEndpointUpdate.parse(bundle, for: client, verify: { try DirectSecurity.verify($0, signature: $1, authority: identity.caCertificateDER) }) else { continue }
            var changed = client; changed.endpoint = update.endpoint; changed.generation = update.generation
            changed.transport = update.transport
            changed.identity = identity.replacingRelayOrigin(update.endpoint)
            if update.generation > client.generation { changed.needsAcknowledgement = true }
            if changed != client { try saveClient(changed) }
            return
        }
        throw DirectAgentError.staleUpdate
    }
    private func current(_ id: String) throws -> DirectClientRecord {
        guard !removedClientIDs.contains(id) else { throw DirectAgentError.cancelled }
        guard let client = document.clients.first(where: { $0.clientID == id && !$0.removing }) else { throw DirectAgentError.cancelled }
        return client
    }
    private func saveClient(_ client: DirectClientRecord) throws {
        guard !removedClientIDs.contains(client.id) else { throw DirectAgentError.cancelled }
        guard let index = document.clients.firstIndex(where: { $0.clientID == client.clientID && !$0.removing }) else { throw DirectAgentError.cancelled }
        var next = document; next.clients[index] = client; try commit(next)
    }
    private func install(_ issued: DirectIssuedIdentity, into client: inout DirectClientRecord) throws {
        guard let key = client.key else { throw DirectAgentError.unavailable }
        let ca = try CertificateAuthorityValidator().validate(issued.caCertificatePem, at: Date())
        let identity = AgentIdentity(relayOrigin: client.endpoint, role: "agent", serial: issued.serial, keyTag: key.keyTag, certificatePEM: issued.certificatePem, caCertificatePEM: issued.caCertificatePem, caCertificateDER: ca.der)
        try store.stage(identity)
        client.identity = identity; client.pairingID = issued.pairingId; client.generation = issued.generation; client.needsAcknowledgement = true
    }
}

enum DirectCertificateExpiration {
    static func date(_ pem: String) throws -> Date {
        try validity(pem).notAfter
    }
    static func validity(_ pem: String) throws -> (notBefore: Date, notAfter: Date) {
        guard let der = try PEMCertificateChain.parse(pem).first else { throw DirectAgentError.trust }
        let certificate = try nodes(der).first
        guard let certificate else { throw DirectAgentError.trust }
        let fields = try nodes(certificate.1)
        guard let tbs = fields.first else { throw DirectAgentError.trust }
        let tbsFields = try nodes(tbs.1)
        let offset = tbsFields.first?.0 == 0xA0 ? 1 : 0
        guard tbsFields.count > offset + 3 else { throw DirectAgentError.trust }
        let validity = try nodes(tbsFields[offset + 3].1)
        guard validity.count == 2 else { throw DirectAgentError.trust }
        return (try dateNode(validity[0]), try dateNode(validity[1]))
    }
    private static func dateNode(_ node: (UInt8, Data)) throws -> Date {
        guard node.0 == 0x17 || node.0 == 0x18, let raw = String(data: node.1, encoding: .ascii) else { throw DirectAgentError.trust }
        let formatter = DateFormatter(); formatter.locale = Locale(identifier: "en_US_POSIX"); formatter.timeZone = TimeZone(secondsFromGMT: 0)
        formatter.dateFormat = node.0 == 0x17 ? "yyMMddHHmmss'Z'" : "yyyyMMddHHmmss'Z'"
        guard let value = formatter.date(from: raw) else { throw DirectAgentError.trust }; return value
    }
    private static func nodes(_ data: Data) throws -> [(UInt8, Data)] {
        let bytes = Array(data); var index = 0; var result: [(UInt8, Data)] = []
        while index < bytes.count {
            guard index + 2 <= bytes.count else { throw DirectAgentError.trust }
            let tag = bytes[index]; index += 1; let initial = bytes[index]; index += 1
            var length = Int(initial)
            if initial >= 128 {
                let count = Int(initial & 127); guard (1...4).contains(count), index + count <= bytes.count else { throw DirectAgentError.trust }
                length = 0; for _ in 0..<count { length = length * 256 + Int(bytes[index]); index += 1 }
            }
            guard length <= bytes.count - index else { throw DirectAgentError.trust }
            result.append((tag, Data(bytes[index..<index + length]))); index += length
        }
        return result
    }
}
#endif
