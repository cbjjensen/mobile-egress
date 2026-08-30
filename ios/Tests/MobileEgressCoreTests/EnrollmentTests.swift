import Foundation
import XCTest
@testable import MobileEgressCore

final class EnrollmentTests: XCTestCase {
    func testEnrollmentPostsExactlyPublicKeyCapabilityAndAgentRole() async throws {
        let transport = RecordingHTTPTransport(response: HTTPResponse(
            statusCode: 201,
            headers: ["content-type": ["application/json"]],
            body: try Task2Fixtures.enrollmentResponse()
        ))
        let client = makeClient(transport: transport)

        let identity = try await client.enroll(pairing: Task2Fixtures.pairing(), key: Task2Fixtures.key())

        let request = try XCTUnwrap(transport.request)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: request.body) as? [String: String])
        XCTAssertEqual(Set(object.keys), ["publicKeyPem", "capability", "role"])
        XCTAssertEqual(object["publicKeyPem"], Task2Fixtures.key().publicKeyPEM)
        XCTAssertEqual(object["capability"], "one-use-enrollment-capability")
        XCTAssertEqual(object["role"], "agent")
        XCTAssertEqual(request.path, "/v1/enroll")
        XCTAssertEqual(identity.serial, "A1")
        XCTAssertNil(transport.configuration?.localIdentityKeyTag)
    }

    func testEnrollmentRejectsRedirectsUnexpectedJSONAndOversizedBodies() async throws {
        let responses = [
            HTTPResponse(statusCode: 302, headers: ["content-type": ["application/json"]], body: Data()),
            HTTPResponse(
                statusCode: 201,
                headers: ["content-type": ["application/json"]],
                body: try Task2Fixtures.enrollmentResponse(extra: ["unexpected": true])
            ),
            HTTPResponse(
                statusCode: 201,
                headers: ["content-type": ["application/json"]],
                body: Data(repeating: 0x20, count: HTTP1Limits.maximumBodyBytes + 1)
            ),
        ]

        for response in responses {
            let client = makeClient(transport: RecordingHTTPTransport(response: response))
            do {
                _ = try await client.enroll(pairing: Task2Fixtures.pairing(), key: Task2Fixtures.key())
                XCTFail("Expected strict enrollment rejection")
            } catch {}
        }
    }

    func testEnrollmentRejectsWrongRoleInvalidSerialDifferentCAAndMissingCAChain() async throws {
        let bodies = [
            try Task2Fixtures.enrollmentResponse(role: "client"),
            try Task2Fixtures.enrollmentResponse(serial: ""),
            try Task2Fixtures.enrollmentResponse(serial: String(repeating: "A", count: 65)),
            try Task2Fixtures.enrollmentResponse(serial: "not-hex"),
            try Task2Fixtures.enrollmentResponse(caCertificatePEM: Task2Fixtures.otherCaPEM),
            try Task2Fixtures.enrollmentResponse(certificatePEM: Task2Fixtures.pem(Task2Fixtures.leafDER)),
        ]

        for body in bodies {
            let transport = RecordingHTTPTransport(response: HTTPResponse(
                statusCode: 201,
                headers: ["content-type": ["application/json"]],
                body: body
            ))
            do {
                _ = try await makeClient(transport: transport).enroll(
                    pairing: Task2Fixtures.pairing(),
                    key: Task2Fixtures.key()
                )
                XCTFail("Expected enrollment identity validation failure")
            } catch {}
        }
    }

    func testEnrollmentRequiresLeafValiditySignatureKeyClientAuthAndMatchingSerial() async throws {
        let invalidEvidence = [
            EnrollmentCertificateEvidence(
                leafIsCurrentlyValid: false, leafIsSignedByPinnedAuthority: true,
                publicKeyMatches: true, hasClientAuthenticationEKU: true, serial: "A1"
            ),
            EnrollmentCertificateEvidence(
                leafIsCurrentlyValid: true, leafIsSignedByPinnedAuthority: false,
                publicKeyMatches: true, hasClientAuthenticationEKU: true, serial: "A1"
            ),
            EnrollmentCertificateEvidence(
                leafIsCurrentlyValid: true, leafIsSignedByPinnedAuthority: true,
                publicKeyMatches: false, hasClientAuthenticationEKU: true, serial: "A1"
            ),
            EnrollmentCertificateEvidence(
                leafIsCurrentlyValid: true, leafIsSignedByPinnedAuthority: true,
                publicKeyMatches: true, hasClientAuthenticationEKU: false, serial: "A1"
            ),
            EnrollmentCertificateEvidence(
                leafIsCurrentlyValid: true, leafIsSignedByPinnedAuthority: true,
                publicKeyMatches: true, hasClientAuthenticationEKU: true, serial: "A2"
            ),
        ]

        for evidence in invalidEvidence {
            let transport = RecordingHTTPTransport(response: HTTPResponse(
                statusCode: 201,
                headers: ["content-type": ["application/json"]],
                body: try Task2Fixtures.enrollmentResponse()
            ))
            let client = makeClient(
                transport: transport,
                certificateValidator: FixedEnrollmentCertificateValidator(evidence: evidence)
            )
            do {
                _ = try await client.enroll(pairing: Task2Fixtures.pairing(), key: Task2Fixtures.key())
                XCTFail("Expected strict certificate evidence rejection")
            } catch {}
        }
    }

    private func makeClient(
        transport: RecordingHTTPTransport,
        certificateValidator: FixedEnrollmentCertificateValidator = FixedEnrollmentCertificateValidator()
    ) -> EnrollmentHTTPClient {
        EnrollmentHTTPClient(
            transport: transport,
            certificateAuthorityValidator: Task2CertificateAuthorityValidator(),
            certificateValidator: certificateValidator,
            now: { Date(timeIntervalSince1970: 1_800_000_000) }
        )
    }
}
