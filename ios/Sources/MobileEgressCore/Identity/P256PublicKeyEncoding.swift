import Foundation

enum P256PublicKeyEncoding {
    private static let subjectPublicKeyInfoPrefix = Data([
        0x30, 0x59, 0x30, 0x13, 0x06, 0x07, 0x2A, 0x86, 0x48, 0xCE, 0x3D, 0x02, 0x01,
        0x06, 0x08, 0x2A, 0x86, 0x48, 0xCE, 0x3D, 0x03, 0x01, 0x07, 0x03, 0x42, 0x00,
    ])

    static func subjectPublicKeyInfo(fromX963 x963: Data) throws -> Data {
        guard x963.count == 65, x963.first == 0x04 else { throw IdentityError.invalidIdentity }
        return subjectPublicKeyInfoPrefix + x963
    }

    static func pem(subjectPublicKeyInfo: Data) -> String {
        let encoded = subjectPublicKeyInfo.base64EncodedString()
        var lines: [String] = []
        var offset = 0
        while offset < encoded.count {
            let start = encoded.index(encoded.startIndex, offsetBy: offset)
            let end = encoded.index(start, offsetBy: min(64, encoded.distance(from: start, to: encoded.endIndex)))
            lines.append(String(encoded[start ..< end]))
            offset += 64
        }
        return "-----BEGIN PUBLIC KEY-----\n" + lines.joined(separator: "\n") + "\n-----END PUBLIC KEY-----\n"
    }
}
