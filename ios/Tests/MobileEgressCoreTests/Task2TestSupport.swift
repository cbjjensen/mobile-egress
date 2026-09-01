import Foundation
@testable import MobileEgressCore

enum Task2Fixtures {
    static let caDER = Data([0xCA, 0xFE, 0x01])
    static let otherCaDER = Data([0xCA, 0xFE, 0x02])
    static let leafDER = Data([0x30, 0x03, 0x02, 0x01, 0x01])
    static let publicKeyDER = Data([0x30, 0x59, 0x30, 0x13, 0x01])

    static var caPEM: String { pem(caDER) }
    static var otherCaPEM: String { pem(otherCaDER) }
    static var certificateChainPEM: String { pem(leafDER) + caPEM }

    static func pem(_ der: Data) -> String {
        let encoded = der.base64EncodedString()
        let lines = stride(from: 0, to: encoded.count, by: 64).map { offset -> String in
            let start = encoded.index(encoded.startIndex, offsetBy: offset)
            let end = encoded.index(start, offsetBy: min(64, encoded.distance(from: start, to: encoded.endIndex)))
            return String(encoded[start ..< end])
        }
        return "-----BEGIN CERTIFICATE-----\n" + lines.joined(separator: "\n") + "\n-----END CERTIFICATE-----\n"
    }

    static func pairing() -> PairingBundle {
        PairingBundle(
            version: 1,
            relayOrigin: "https://relay.example:8443",
            certificateAuthority: CertificateAuthority(der: caDER),
            capability: "one-use-enrollment-capability",
            role: "agent",
            expiresAt: Date(timeIntervalSince1970: 2_000_000_000)
        )
    }

    static func key(tag: String = "mobile-egress.agent.key.new") -> IdentityKeyMaterial {
        IdentityKeyMaterial(
            keyTag: tag,
            publicKeyPEM: "-----BEGIN PUBLIC KEY-----\nTEST\n-----END PUBLIC KEY-----\n",
            publicKeySPKIDER: publicKeyDER
        )
    }

    static func identity(
        relayOrigin: String = "https://relay.example:8443",
        keyTag: String = "mobile-egress.agent.key.new",
        serial: String = "A1"
    ) -> AgentIdentity {
        AgentIdentity(
            relayOrigin: relayOrigin,
            role: "agent",
            serial: serial,
            keyTag: keyTag,
            certificatePEM: certificateChainPEM,
            caCertificatePEM: caPEM,
            caCertificateDER: caDER
        )
    }

    static func migration(caDER: Data = caDER) -> EndpointMigration {
        EndpointMigration(
            version: 1,
            relayOrigin: "https://new-relay.example:9443",
            certificateAuthority: CertificateAuthority(der: caDER),
            capability: "one-use-migration-capability",
            expiresAt: Date(timeIntervalSince1970: 2_000_000_000)
        )
    }

    static func enrollmentResponse(
        certificatePEM: String = certificateChainPEM,
        caCertificatePEM: String = caPEM,
        serial: String = "A1",
        role: String = "agent",
        extra: [String: Any] = [:]
    ) throws -> Data {
        var object: [String: Any] = [
            "certificatePem": certificatePEM,
            "caCertificatePem": caCertificatePEM,
            "serial": serial,
            "role": role,
        ]
        extra.forEach { object[$0.key] = $0.value }
        return try JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])
    }
}

struct Task2CertificateAuthorityValidator: CertificateAuthorityValidating {
    func validate(_ pem: String, at date: Date) throws -> CertificateAuthority {
        switch pem {
        case Task2Fixtures.caPEM:
            CertificateAuthority(der: Task2Fixtures.caDER)
        case Task2Fixtures.otherCaPEM:
            CertificateAuthority(der: Task2Fixtures.otherCaDER)
        default:
            throw CoreValidationError.certificateAuthorityInvalid
        }
    }
}

struct FixedEnrollmentCertificateValidator: EnrollmentCertificateValidating {
    var evidence = EnrollmentCertificateEvidence(
        leafIsCurrentlyValid: true,
        leafIsSignedByPinnedAuthority: true,
        publicKeyMatches: true,
        hasClientAuthenticationEKU: true,
        serial: "A1"
    )

    func validate(
        certificateChainDER: [Data],
        pinnedAuthorityDER: Data,
        expectedPublicKeySPKIDER: Data,
        at date: Date
    ) throws -> EnrollmentCertificateEvidence {
        evidence
    }
}

final class RecordingHTTPTransport: HTTPTransporting, @unchecked Sendable {
    var response: HTTPResponse
    private(set) var request: HTTPRequest?
    private(set) var configuration: PinnedCellularTransportConfiguration?

    init(response: HTTPResponse) {
        self.response = response
    }

    func execute(
        _ request: HTTPRequest,
        configuration: PinnedCellularTransportConfiguration
    ) async throws -> HTTPResponse {
        self.request = request
        self.configuration = configuration
        return response
    }
}

final class FakeIdentityKeyManager: IdentityKeyManaging, @unchecked Sendable {
    private var generated: [IdentityKeyMaterial]
    var availableTags: Set<String>
    private(set) var deleteAttempts: [String] = []
    var deleteFailures: Set<String> = []

    init(generated: IdentityKeyMaterial, existingTags: Set<String> = []) {
        self.generated = [generated]
        availableTags = existingTags
    }

    init(generated: [IdentityKeyMaterial], existingTags: Set<String> = []) {
        self.generated = generated
        availableTags = existingTags
    }

    func createKey() throws -> IdentityKeyMaterial {
        guard !generated.isEmpty else { throw TestTask2Error.injected }
        let key = generated.removeFirst()
        availableTags.insert(key.keyTag)
        return key
    }

    func deleteKey(tag: String) throws {
        deleteAttempts.append(tag)
        if deleteFailures.contains(tag) { throw TestTask2Error.injected }
        availableTags.remove(tag)
    }
}

final class FakeAgentIdentityStore: AgentIdentityPersisting, @unchecked Sendable {
    var current: AgentIdentity?
    var failStage = false
    var failSave = false
    var saveFailures: Set<String> = []
    var removeFailures: Set<String> = []
    private(set) var stagedTags: Set<String> = []
    private(set) var events: [String] = []

    init(current: AgentIdentity?) {
        self.current = current
    }

    func load() throws -> AgentIdentity? {
        events.append("load")
        return current
    }

    func stageCertificate(for identity: AgentIdentity) throws {
        events.append("stage:\(identity.keyTag)")
        stagedTags.insert(identity.keyTag)
        if failStage { throw TestTask2Error.injected }
    }

    func save(_ identity: AgentIdentity) throws {
        events.append("save:\(identity.keyTag)")
        if failSave || saveFailures.contains(identity.keyTag) { throw TestTask2Error.injected }
        current = identity
    }

    func removeCertificate(for identity: AgentIdentity) throws {
        events.append("remove:\(identity.keyTag)")
        stagedTags.remove(identity.keyTag)
        if removeFailures.contains(identity.keyTag) { throw TestTask2Error.injected }
    }
}

struct FakeEnrollmentPerformer: EnrollmentPerforming {
    var result: Result<AgentIdentity, Error>

    func enroll(pairing: PairingBundle, key: IdentityKeyMaterial) async throws -> AgentIdentity {
        try result.get()
    }
}

actor InterleavingEnrollmentPerformer: EnrollmentPerforming {
    private let firstCallGate = AsyncTestGate()
    private var callCount = 0
    private var callCountWaiters: [(Int, CheckedContinuation<Void, Never>)] = []

    func enroll(pairing: PairingBundle, key: IdentityKeyMaterial) async throws -> AgentIdentity {
        callCount += 1
        let currentCall = callCount
        resumeSatisfiedCallCountWaiters()
        if currentCall == 1 {
            await firstCallGate.wait()
        }
        return Task2Fixtures.identity(keyTag: key.keyTag, serial: currentCall == 1 ? "A1" : "A2")
    }

    func waitUntilCallCount(_ expected: Int) async {
        if callCount >= expected { return }
        await withCheckedContinuation { continuation in
            callCountWaiters.append((expected, continuation))
        }
    }

    func releaseFirstCall() async {
        await firstCallGate.open()
    }

    private func resumeSatisfiedCallCountWaiters() {
        let satisfied = callCountWaiters.filter { $0.0 <= callCount }
        callCountWaiters.removeAll { $0.0 <= callCount }
        satisfied.forEach { $0.1.resume() }
    }
}

actor AsyncTestGate {
    private var isOpen = false
    private var waiters: [CheckedContinuation<Void, Never>] = []

    func wait() async {
        if isOpen { return }
        await withCheckedContinuation { waiters.append($0) }
    }

    func open() {
        isOpen = true
        let pending = waiters
        waiters.removeAll()
        pending.forEach { $0.resume() }
    }
}

final class RecordingMigrationPerformer: EndpointMigrationPerforming, @unchecked Sendable {
    var result: Result<String, Error>
    private(set) var receivedIdentity: AgentIdentity?
    private(set) var callCount = 0

    init(result: Result<String, Error>) {
        self.result = result
    }

    func consume(migration: EndpointMigration, identity: AgentIdentity) async throws -> String {
        callCount += 1
        receivedIdentity = identity
        return try result.get()
    }
}

enum TestTask2Error: Error {
    case injected
}
