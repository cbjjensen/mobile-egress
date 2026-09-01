import Foundation
import XCTest
@testable import MobileEgressCore

final class AppleSecurityPrimitiveTests: XCTestCase {
    func testP256X963PublicKeyBecomesPKIXSubjectPublicKeyInfoAndPEM() throws {
        let x963 = Data([0x04] + Array(1 ... 64).map(UInt8.init))
        let expectedPrefix = Data([
            0x30, 0x59, 0x30, 0x13, 0x06, 0x07, 0x2A, 0x86, 0x48, 0xCE, 0x3D, 0x02, 0x01,
            0x06, 0x08, 0x2A, 0x86, 0x48, 0xCE, 0x3D, 0x03, 0x01, 0x07, 0x03, 0x42, 0x00,
        ])

        let spki = try P256PublicKeyEncoding.subjectPublicKeyInfo(fromX963: x963)
        let pem = P256PublicKeyEncoding.pem(subjectPublicKeyInfo: spki)

        XCTAssertEqual(spki, expectedPrefix + x963)
        XCTAssertTrue(pem.hasPrefix("-----BEGIN PUBLIC KEY-----\n"))
        XCTAssertTrue(pem.hasSuffix("\n-----END PUBLIC KEY-----\n"))
        XCTAssertEqual(pem.split(separator: "\n").dropFirst().dropLast().map(\.count), [64, 60])
    }

    func testP256EncodingRejectsMalformedX963Keys() {
        XCTAssertThrowsError(try P256PublicKeyEncoding.subjectPublicKeyInfo(fromX963: Data(repeating: 0, count: 65)))
        XCTAssertThrowsError(try P256PublicKeyEncoding.subjectPublicKeyInfo(fromX963: Data([0x04])))
    }

    func testX509FieldsExtractCanonicalSerialSPKIAndClientAuthEKU() throws {
        let spki = try P256PublicKeyEncoding.subjectPublicKeyInfo(
            fromX963: Data([0x04] + Array(repeating: 0x11, count: 64))
        )
        let fields = try X509CertificateFields(der: certificate(spki: spki, ekuLastByte: 0x02))

        XCTAssertEqual(fields.serial, "A1")
        XCTAssertEqual(fields.subjectPublicKeyInfoDER, spki)
        XCTAssertTrue(fields.hasClientAuthenticationEKU)
    }

    func testX509FieldsDoNotConfuseServerAuthWithClientAuth() throws {
        let spki = try P256PublicKeyEncoding.subjectPublicKeyInfo(
            fromX963: Data([0x04] + Array(repeating: 0x22, count: 64))
        )
        let fields = try X509CertificateFields(der: certificate(spki: spki, ekuLastByte: 0x01))

        XCTAssertFalse(fields.hasClientAuthenticationEKU)
    }

    private func certificate(spki: Data, ekuLastByte: UInt8) -> Data {
        let algorithm = node(0x30, Data())
        let name = node(0x30, Data())
        let validity = node(0x30, node(0x17, Data("260101000000Z".utf8)) + node(0x17, Data("270101000000Z".utf8)))
        let ekuOID = node(0x06, Data([0x55, 0x1D, 0x25]))
        let usageOID = node(0x06, Data([0x2B, 0x06, 0x01, 0x05, 0x05, 0x07, 0x03, ekuLastByte]))
        let ekuValue = node(0x04, node(0x30, usageOID))
        let extensions = node(0xA3, node(0x30, node(0x30, ekuOID + ekuValue)))
        let tbs = node(
            0x30,
            node(0xA0, node(0x02, Data([0x02]))) +
                node(0x02, Data([0x00, 0xA1])) +
                algorithm + name + validity + name + spki + extensions
        )
        return node(0x30, tbs + algorithm + node(0x03, Data([0x00])))
    }

    private func node(_ tag: UInt8, _ value: Data) -> Data {
        var result = Data([tag])
        if value.count < 128 {
            result.append(UInt8(value.count))
        } else if value.count <= 255 {
            result.append(contentsOf: [0x81, UInt8(value.count)])
        } else {
            result.append(contentsOf: [0x82, UInt8(value.count >> 8), UInt8(value.count & 0xFF)])
        }
        result.append(value)
        return result
    }
}
