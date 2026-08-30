import Foundation

enum TestFixtures {
    static let now = Date(timeIntervalSince1970: 1_777_580_800)
    static let validCAPEM = """
    -----BEGIN CERTIFICATE-----
    MIIBUDCB+KADAgECAgEBMAoGCCqGSM49BAMCMCAxHjAcBgNVBAMTFW1vYmlsZS1l
    Z3Jlc3MtdGVzdC1jYTAeFw0yNTAxMDEwMDAwMDBaFw0zMDAxMDEwMDAwMDBaMCAx
    HjAcBgNVBAMTFW1vYmlsZS1lZ3Jlc3MtdGVzdC1jYTBZMBMGByqGSM49AgEGCCqG
    SM49AwEHA0IABPKnio8xSOMTFYGVYFy9NuxyxyXyK/lCeU1DB/5hDUfxYd8+WQcz
    rGz1TtG3J11GXflH0oMPmtr6DY9Jy4KTBjyjIzAhMA8GA1UdEwEB/wQFMAMBAf8w
    DgYDVR0PAQH/BAQDAgKEMAoGCCqGSM49BAMCA0cAMEQCIBYKNKGPpfddOF30Xv62
    D9n4+F7xwJL/1aa/Se1PwAfeAiAe6X3wKgMwZG/B/zZ8IeH3sZb3yfs5MP/p/Rou
    g+91EA==
    -----END CERTIFICATE-----
    """ + "\n"

    static func encodeQR(_ object: [String: Any]) throws -> String {
        let data = try JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])
        return data.base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }

    static func pairingQR(
        relayURL: String = "https://relay.example:8443",
        capability: String = "one-use-high-entropy-capability",
        role: String = "agent",
        expiresAt: String = "2026-05-30T18:10:00Z",
        extraFields: [String: Any] = [:]
    ) throws -> String {
        var object: [String: Any] = [
            "version": 1,
            "relayUrl": relayURL,
            "caCertificatePem": validCAPEM,
            "capability": capability,
            "role": role,
            "expiresAt": expiresAt,
        ]
        extraFields.forEach { object[$0.key] = $0.value }
        return try encodeQR(object)
    }

    static func migrationQR(
        type: String = "agent-endpoint-migration",
        relayURL: String = "https://new-name.ts.net:8443",
        expiresAt: String = "2026-05-30T18:10:00Z",
        extraFields: [String: Any] = [:]
    ) throws -> String {
        var object: [String: Any] = [
            "version": 1,
            "type": type,
            "relayUrl": relayURL,
            "caCertificatePem": validCAPEM,
            "capability": "one-use-migration-capability",
            "expiresAt": expiresAt,
        ]
        extraFields.forEach { object[$0.key] = $0.value }
        return try encodeQR(object)
    }
}
