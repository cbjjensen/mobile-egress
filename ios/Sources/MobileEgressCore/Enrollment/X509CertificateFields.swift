import Foundation

struct X509CertificateFields: Equatable {
    let serial: String
    let subjectPublicKeyInfoDER: Data
    let hasClientAuthenticationEKU: Bool

    init(der: Data) throws {
        var root = DERReader(data: der)
        let certificate = try root.read(expectedTag: 0x30)
        guard root.isAtEnd else { throw EnrollmentError.invalidCertificateChain }
        var certificateFields = DERReader(data: certificate.value)
        let tbsCertificate = try certificateFields.read(expectedTag: 0x30)
        var fields = DERReader(data: tbsCertificate.value)

        if fields.peekTag == 0xA0 { _ = try fields.read() }
        let serialNode = try fields.read(expectedTag: 0x02)
        serial = try Self.canonicalSerial(serialNode.value)
        for _ in 0 ..< 4 { _ = try fields.read() }
        let subjectPublicKeyInfo = try fields.read(expectedTag: 0x30)
        subjectPublicKeyInfoDER = subjectPublicKeyInfo.encoded

        var clientAuthentication = false
        while !fields.isAtEnd {
            let field = try fields.read()
            guard field.tag == 0xA3 else { continue }
            clientAuthentication = try Self.extensionsContainClientAuthentication(field.value)
        }
        hasClientAuthenticationEKU = clientAuthentication
    }

    private static func canonicalSerial(_ integer: Data) throws -> String {
        guard !integer.isEmpty, integer.first! & 0x80 == 0 else {
            throw EnrollmentError.invalidCertificateChain
        }
        var bytes = Array(integer)
        while bytes.count > 1, bytes.first == 0 { bytes.removeFirst() }
        var hex = bytes.map { String(format: "%02X", $0) }.joined()
        while hex.count > 1, hex.first == "0" { hex.removeFirst() }
        guard !hex.isEmpty, hex.count <= 64 else { throw EnrollmentError.invalidSerial }
        return hex
    }

    private static func extensionsContainClientAuthentication(_ explicitValue: Data) throws -> Bool {
        var explicitReader = DERReader(data: explicitValue)
        let extensionSequence = try explicitReader.read(expectedTag: 0x30)
        guard explicitReader.isAtEnd else { throw EnrollmentError.invalidCertificateChain }
        var extensions = DERReader(data: extensionSequence.value)
        let extendedKeyUsageOID = Data([0x55, 0x1D, 0x25])
        let clientAuthenticationOID = Data([0x2B, 0x06, 0x01, 0x05, 0x05, 0x07, 0x03, 0x02])

        while !extensions.isAtEnd {
            let item = try extensions.read(expectedTag: 0x30)
            var itemFields = DERReader(data: item.value)
            let oid = try itemFields.read(expectedTag: 0x06)
            var value = try itemFields.read()
            if value.tag == 0x01 { value = try itemFields.read() }
            guard value.tag == 0x04, itemFields.isAtEnd else {
                throw EnrollmentError.invalidCertificateChain
            }
            guard oid.value == extendedKeyUsageOID else { continue }

            var usageContainer = DERReader(data: value.value)
            let usagesNode = try usageContainer.read(expectedTag: 0x30)
            guard usageContainer.isAtEnd else { throw EnrollmentError.invalidCertificateChain }
            var usages = DERReader(data: usagesNode.value)
            while !usages.isAtEnd {
                if try usages.read(expectedTag: 0x06).value == clientAuthenticationOID { return true }
            }
            return false
        }
        return false
    }
}

private struct DERNode {
    let tag: UInt8
    let value: Data
    let encoded: Data
}

private struct DERReader {
    private let data: Data
    private var index: Data.Index

    init(data: Data) {
        self.data = data
        index = data.startIndex
    }

    var isAtEnd: Bool { index == data.endIndex }
    var peekTag: UInt8? { isAtEnd ? nil : data[index] }

    mutating func read(expectedTag: UInt8? = nil) throws -> DERNode {
        let start = index
        guard index < data.endIndex else { throw EnrollmentError.invalidCertificateChain }
        let tag = data[index]
        index = data.index(after: index)
        guard tag & 0x1F != 0x1F, index < data.endIndex else {
            throw EnrollmentError.invalidCertificateChain
        }

        let firstLength = data[index]
        index = data.index(after: index)
        let length: Int
        if firstLength & 0x80 == 0 {
            length = Int(firstLength)
        } else {
            let count = Int(firstLength & 0x7F)
            guard count > 0, count <= 4, data.distance(from: index, to: data.endIndex) >= count else {
                throw EnrollmentError.invalidCertificateChain
            }
            var parsedLength = 0
            for offset in 0 ..< count {
                let byteIndex = data.index(index, offsetBy: offset)
                if offset == 0, data[byteIndex] == 0 { throw EnrollmentError.invalidCertificateChain }
                parsedLength = (parsedLength << 8) | Int(data[byteIndex])
            }
            guard parsedLength >= 128 else { throw EnrollmentError.invalidCertificateChain }
            index = data.index(index, offsetBy: count)
            length = parsedLength
        }
        guard data.distance(from: index, to: data.endIndex) >= length else {
            throw EnrollmentError.invalidCertificateChain
        }
        let valueEnd = data.index(index, offsetBy: length)
        let value = data.subdata(in: index ..< valueEnd)
        let encoded = data.subdata(in: start ..< valueEnd)
        index = valueEnd
        guard expectedTag == nil || expectedTag == tag else { throw EnrollmentError.invalidCertificateChain }
        return DERNode(tag: tag, value: value, encoded: encoded)
    }
}
