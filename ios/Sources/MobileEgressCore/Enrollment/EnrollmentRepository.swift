public final class EnrollmentRepository: @unchecked Sendable {
    private let keyManager: any IdentityKeyManaging
    private let identityStore: any AgentIdentityPersisting
    private let performer: any EnrollmentPerforming

    public init(
        keyManager: any IdentityKeyManaging,
        identityStore: any AgentIdentityPersisting,
        performer: any EnrollmentPerforming
    ) {
        self.keyManager = keyManager
        self.identityStore = identityStore
        self.performer = performer
    }

    public func replaceIdentity(using pairing: PairingBundle) async throws -> AgentIdentity {
        let previous = try identityStore.load()
        let key = try keyManager.createKey()
        var candidate: AgentIdentity?

        do {
            let enrolled = try await performer.enroll(pairing: pairing, key: key)
            candidate = enrolled
            guard enrolled.keyTag == key.keyTag else { throw EnrollmentError.generatedKeyMismatch }
            try identityStore.stageCertificate(for: enrolled)
            try identityStore.save(enrolled)
        } catch {
            if let candidate { try? identityStore.removeCertificate(for: candidate) }
            try? keyManager.deleteKey(tag: key.keyTag)
            throw error
        }

        guard let candidate else { throw EnrollmentError.invalidResponse }
        if let previous, previous.keyTag != candidate.keyTag {
            try? identityStore.removeCertificate(for: previous)
            try? keyManager.deleteKey(tag: previous.keyTag)
        }
        return candidate
    }
}
