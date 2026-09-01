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

    static let nonCAPEM = """
    -----BEGIN CERTIFICATE-----
    MIIDLDCCAhSgAwIBAgIUXLnF/8O+4oQMJGWbB5sTkJr3kyYwDQYJKoZIhvcNAQEL
    BQAwHzEdMBsGA1UEAwwUbW9iaWxlLWVncmVzcy1ub24tY2EwHhcNMjYwODMwMjA1
    MzQwWhcNMzYwODI3MjA1MzQwWjAfMR0wGwYDVQQDDBRtb2JpbGUtZWdyZXNzLW5v
    bi1jYTCCASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEBAJ6rsUKiUpTKvgJu
    LCXJ4Z74IXWHKXFTm+ytqgG12vfdoAM4po48SOBguNHIBdlHW11W2/E1S+m56LBB
    KU51zH8u6roiVeiZhSxd/G7BgoLJWMq5u6TJ64vXaJE4P48REwz+8ygJwsUuAw/x
    k24+WVqS0Sy3vpxr7alWx+4r1/s6GdgZXwro0kx7Ih6ZHT9FvWdgd09Ai0aV2XFS
    sJvdTAkU+E4jOVzKuBMAZjoOS8oS32lfeAfbhsyyll2pwQQiUzEcznxeqITwCVql
    eG/aFr3+fxy9ZEgi+9s7NhEiBrskkhFB+cw50qcXc2lD7Hla+2VRKrSzPR8eCIoL
    b81FJwcCAwEAAaNgMF4wHQYDVR0OBBYEFASbH65cozATP0KYNduAlcRbNLAyMB8G
    A1UdIwQYMBaAFASbH65cozATP0KYNduAlcRbNLAyMAwGA1UdEwEB/wQCMAAwDgYD
    VR0PAQH/BAQDAgeAMA0GCSqGSIb3DQEBCwUAA4IBAQBjX48ewqrWzpR9/aNkfkqt
    x75sHfXVrLNDTlISqud6IMnGWgbsXEWj5cNiFAcuKUsdrtDXiX3tjMPA0LE/S1tw
    ZJ7KdTjz/vhNBypK8OuS6rbC7JRI6MJ25xGx62JNjxb7Vj+U9mNfMXbO1ymVjSMw
    gWxhWPBOS94fBDLzrr1fo+WSMdQu7ADQEwco8n0SaV3Gv5L47MJFxasGuttO2ddM
    +UiFAx0FkmoDSDS7mopNzWNDj3SoK8Q6Z+yG9jmaBpmaI1twHcOpkJfpO1YJpGkY
    HIV4T0+lS6DgahTSflbBcORBgHva32Geq0T43oP7l0dGIq+6IgJV46ZbiCX4NRPd
    -----END CERTIFICATE-----
    """ + "\n"

    static let caWithoutKeyCertSignPEM = """
    -----BEGIN CERTIFICATE-----
    MIIDUzCCAjugAwIBAgIUR5IRsgB18kWRgmX5re5IfUwRWqkwDQYJKoZIhvcNAQEL
    BQAwMTEvMC0GA1UEAwwmbW9iaWxlLWVncmVzcy1jYS13aXRob3V0LWtleS1jZXJ0
    LXNpZ24wHhcNMjYwODMwMjA1MzQwWhcNMzYwODI3MjA1MzQwWjAxMS8wLQYDVQQD
    DCZtb2JpbGUtZWdyZXNzLWNhLXdpdGhvdXQta2V5LWNlcnQtc2lnbjCCASIwDQYJ
    KoZIhvcNAQEBBQADggEPADCCAQoCggEBAKo/Lcuk9LGyvGoQONgSgPUM5KyQz5wZ
    SVvEYPUs21+2GyF80fzVZi/SOgHOG8ly+FDYy7ZP8Vjyx0Yn3ZCcoX/WlSWCOx0v
    VLesfnGwhzveq5wvvW3PFFP93vR923j5P8RiQCy62MrzYDkfYN5XvI53LWdRBphu
    tbUdSO/XNUAzEzg/q92v53mzourjKIrK27yeja/NyGJT+/q45ji3hbktY0L5Nl6j
    b6RkEF48LwWgYWey3Dmc0PNB7aTNKCHrxwbgwHnHyVzKEV1aNsx650VJpyt1yot3
    SyuIxZzhtkhAImXy/D685JzhNnARjy5R31YoOTiVF4UVBL8CRC/6WQcCAwEAAaNj
    MGEwHQYDVR0OBBYEFMxOSwiWAEXHz/q84yoR1rLIGazsMB8GA1UdIwQYMBaAFMxO
    SwiWAEXHz/q84yoR1rLIGazsMA8GA1UdEwEB/wQFMAMBAf8wDgYDVR0PAQH/BAQD
    AgeAMA0GCSqGSIb3DQEBCwUAA4IBAQCpPtewTU66c97T2sWJICKpH6c2Kcil8iA3
    8jBrU30FsoGPPmI2MJADBXRZrV2ZogkvxRvJG5pN+r6rfX+BM2KkGk8wcrNQracM
    rrn5VN4oKdWRk5OZTKAOCq9qaZEr8x4UlB++qCfeqXm+lhTmGkh8iM2VDcaFqO/x
    KUkSNCGRuy8sCprGfcuL4C+AQ4YdKeFK1XcE8oO5TUYW9PunSdeigaCeySVNvhSC
    5t7+AZ/xwSIRocFR0mxUzPBCePSpbMGVFGWMQGg7UPdBF5jm4Gp5VgyxMAEPr9kD
    T6BzaG12JW8cAjvIJohuHFMg8VqU3EZR2//x4dBQuU9/xQBmWKDf
    -----END CERTIFICATE-----
    """ + "\n"

    static func encodeQR(_ object: [String: Any]) throws -> String {
        let data = try JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])
        return data.base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }

    static func replacingVersionLiteral(in encodedQR: String, with version: String) throws -> String {
        try replacingVersionField(in: encodedQR, with: "\"version\":\(version)")
    }

    static func duplicatingVersionLiteral(in encodedQR: String, first: String, second: String) throws -> String {
        try replacingVersionField(in: encodedQR, with: "\"version\":\(first),\"version\":\(second)")
    }

    private static func replacingVersionField(in encodedQR: String, with replacement: String) throws -> String {
        let standard = encodedQR
            .replacingOccurrences(of: "-", with: "+")
            .replacingOccurrences(of: "_", with: "/")
        let padding = String(repeating: "=", count: (4 - standard.utf8.count % 4) % 4)
        guard let data = Data(base64Encoded: standard + padding),
              let json = String(data: data, encoding: .utf8),
              json.contains("\"version\":1")
        else {
            throw FixtureError.invalidQRCode
        }
        let raw = json.replacingOccurrences(of: "\"version\":1", with: replacement)
        return raw.data(using: .utf8)!.base64EncodedString()
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
        capability: String = "one-use-migration-capability",
        expiresAt: String = "2026-05-30T18:10:00Z",
        extraFields: [String: Any] = [:]
    ) throws -> String {
        var object: [String: Any] = [
            "version": 1,
            "type": type,
            "relayUrl": relayURL,
            "caCertificatePem": validCAPEM,
            "capability": capability,
            "expiresAt": expiresAt,
        ]
        extraFields.forEach { object[$0.key] = $0.value }
        return try encodeQR(object)
    }
}

private enum FixtureError: Error {
    case invalidQRCode
}
