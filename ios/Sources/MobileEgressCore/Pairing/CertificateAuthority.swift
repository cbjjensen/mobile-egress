import Foundation

#if canImport(Security)
import Security
#endif

public struct CertificateAuthority: Equatable, Sendable {
    public let der: Data

    public init(der: Data) {
        self.der = der
    }
}

public protocol CertificateAuthorityValidating {
    func validate(_ pem: String, at date: Date) throws -> CertificateAuthority
}

public struct CertificateAuthorityValidator: CertificateAuthorityValidating {
    public init() {}

    public func validate(_ pem: String, at date: Date) throws -> CertificateAuthority {
        let der = try decodeSinglePEM(pem)
        guard CertificateDER.isCertificateAuthority(der) else {
            throw CoreValidationError.certificateAuthorityInvalid
        }

        #if canImport(Security)
        guard let certificate = SecCertificateCreateWithData(nil, der as CFData) else {
            throw CoreValidationError.certificateAuthorityInvalid
        }
        var trust: SecTrust?
        guard SecTrustCreateWithCertificates(certificate, SecPolicyCreateBasicX509(), &trust) == errSecSuccess,
              let trust
        else {
            throw CoreValidationError.certificateAuthorityInvalid
        }
        SecTrustSetAnchorCertificates(trust, [certificate] as CFArray)
        SecTrustSetAnchorCertificatesOnly(trust, true)
        SecTrustSetVerifyDate(trust, date as CFDate)
        guard SecTrustEvaluateWithError(trust, nil) else {
            throw CoreValidationError.certificateAuthorityInvalid
        }
        #else
        _ = date
        #endif

        return CertificateAuthority(der: der)
    }
}

private func decodeSinglePEM(_ pem: String) throws -> Data {
    let lines = pem.split(omittingEmptySubsequences: false, whereSeparator: \.isNewline)
    guard lines.count >= 3,
          lines.first == "-----BEGIN CERTIFICATE-----",
          lines.last == "",
          lines.dropLast().last == "-----END CERTIFICATE-----"
    else {
        throw CoreValidationError.certificateAuthorityInvalid
    }

    let bodyLines = lines.dropFirst().dropLast(2).map(String.init)
    guard !bodyLines.isEmpty else { throw CoreValidationError.certificateAuthorityInvalid }
    for line in bodyLines {
        guard !line.isEmpty, line.count <= 64, line.allSatisfy(isBase64Character) else {
            throw CoreValidationError.certificateAuthorityInvalid
        }
    }
    guard let der = Data(base64Encoded: bodyLines.joined()) else {
        throw CoreValidationError.certificateAuthorityInvalid
    }
    return der
}

private func isBase64Character(_ character: Character) -> Bool {
    character.isASCII && (character.isLetter || character.isNumber || character == "+" || character == "/" || character == "=")
}

private enum CertificateDER {
    static func isCertificateAuthority(_ der: Data) -> Bool {
        var root = NodeReader(der)
        guard let certificate = root.read(), certificate.tag == 0x30 else { return false }
        var certificateFields = NodeReader(certificate.value)
        guard let tbs = certificateFields.read(), tbs.tag == 0x30 else { return false }
        var fields = NodeReader(tbs.value)
        if fields.peekTag == 0xA0 { _ = fields.read() }
        for _ in 0 ..< 6 {
            guard fields.read() != nil else { return false }
        }
        while let field = fields.read() {
            guard field.tag == 0xA3 else { continue }
            var extensionContainer = NodeReader(field.value)
            guard let extensionSequence = extensionContainer.read(), extensionSequence.tag == 0x30 else { return false }
            var extensions = NodeReader(extensionSequence.value)
            var basicConstraints = false
            var keyCertSign = false
            while let extensionNode = extensions.read() {
                guard extensionNode.tag == 0x30 else { return false }
                var extensionFields = NodeReader(extensionNode.value)
                guard let oid = extensionFields.read(), oid.tag == 0x06 else { return false }
                var next = extensionFields.read()
                if next?.tag == 0x01 { next = extensionFields.read() }
                guard let value = next, value.tag == 0x04 else { return false }
                if oid.value == [0x55, 0x1D, 0x13] {
                    basicConstraints = isCA(value.value)
                } else if oid.value == [0x55, 0x1D, 0x0F] {
                    keyCertSign = includesKeyCertSign(value.value)
                }
            }
            return basicConstraints && keyCertSign
        }
        return false
    }

    private static func isCA(_ extensionValue: [UInt8]) -> Bool {
        var extensionReader = NodeReader(extensionValue)
        guard let sequence = extensionReader.read(), sequence.tag == 0x30 else { return false }
        var values = NodeReader(sequence.value)
        guard let boolean = values.read() else { return false }
        return boolean.tag == 0x01 && boolean.value == [0xFF]
    }

    private static func includesKeyCertSign(_ extensionValue: [UInt8]) -> Bool {
        var extensionReader = NodeReader(extensionValue)
        guard let bitString = extensionReader.read(), bitString.tag == 0x03,
              bitString.value.count >= 2
        else { return false }
        return bitString.value[1] & 0x04 != 0
    }
}

private struct ASN1Node {
    let tag: UInt8
    let value: [UInt8]
}

private struct NodeReader {
    private var bytes: [UInt8]
    private var index = 0
    init(_ data: Data) { bytes = Array(data) }
    init(_ bytes: [UInt8]) { self.bytes = bytes }

    var peekTag: UInt8? { index < bytes.count ? bytes[index] : nil }

    mutating func read() -> ASN1Node? {
        guard index + 2 <= bytes.count else { return nil }
        let tag = bytes[index]
        index += 1
        var length = Int(bytes[index])
        index += 1
        if length & 0x80 != 0 {
            let count = length & 0x7F
            guard count > 0, count <= 4, index + count <= bytes.count else { return nil }
            length = 0
            for _ in 0 ..< count {
                length = (length << 8) | Int(bytes[index])
                index += 1
            }
        }
        guard index + length <= bytes.count else { return nil }
        let value = Array(bytes[index ..< index + length])
        index += length
        return ASN1Node(tag: tag, value: value)
    }
}
