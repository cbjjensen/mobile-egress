import Foundation

public enum PublicAddressPolicy {
    public static func validate(ipLiteral: String, port: Int) throws -> String {
        guard (1 ... 65_535).contains(port), ipLiteral == ipLiteral.trimmingCharacters(in: .whitespacesAndNewlines) else {
            throw CoreValidationError.invalidRelayOrigin
        }
        if let ipv4 = IPv4Literal.parse(ipLiteral) {
            guard !ipv4.isIn(forbiddenIPv4Prefixes) else { throw CoreValidationError.invalidRelayOrigin }
            return ipLiteral
        }
        if let ipv6 = IPv6Literal.parse(ipLiteral) {
            guard ipv6.isIn([([0x20], 3)]), !ipv6.isIn(forbiddenIPv6Prefixes) else {
                throw CoreValidationError.invalidRelayOrigin
            }
            return ipLiteral
        }
        throw CoreValidationError.invalidRelayOrigin
    }

    private static let forbiddenIPv4Prefixes: [([UInt8], Int)] = [
        ([0, 0, 0, 0], 8), ([10, 0, 0, 0], 8), ([100, 64, 0, 0], 10), ([127, 0, 0, 0], 8),
        ([169, 254, 0, 0], 16), ([172, 16, 0, 0], 12), ([192, 0, 0, 0], 24), ([192, 0, 2, 0], 24),
        ([192, 88, 99, 0], 24), ([192, 168, 0, 0], 16), ([198, 18, 0, 0], 15), ([198, 51, 100, 0], 24),
        ([203, 0, 113, 0], 24), ([224, 0, 0, 0], 4), ([240, 0, 0, 0], 4),
    ]

    private static let forbiddenIPv6Prefixes: [([UInt8], Int)] = [
        (Array(repeating: 0, count: 16), 128), ([0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01], 128),
        ipv6Prefix([0x01, 0x00], bits: 64), ipv6Prefix([0x20, 0x01], bits: 23), ipv6Prefix([0x20, 0x01, 0x00, 0x02], bits: 48), ipv6Prefix([0x20, 0x01, 0x0d, 0xb8], bits: 32),
        ipv6Prefix([0x20, 0x02], bits: 16), ipv6Prefix([0x3f, 0xff], bits: 20), ipv6Prefix([0xfc], bits: 7), ipv6Prefix([0xfe, 0x80], bits: 10), ipv6Prefix([0xff], bits: 8),
    ]

    private static func ipv6Prefix(_ leadingBytes: [UInt8], bits: Int) -> ([UInt8], Int) {
        (leadingBytes + Array(repeating: 0, count: 16 - leadingBytes.count), bits)
    }
}

private enum IPv4Literal {
    static func parse(_ value: String) -> [UInt8]? {
        let parts = value.split(separator: ".", omittingEmptySubsequences: false)
        guard parts.count == 4 else { return nil }
        var bytes: [UInt8] = []
        for part in parts {
            guard !part.isEmpty, part.count <= 3, part.allSatisfy(\.isNumber),
                  !(part.count > 1 && part.first == "0"), let number = UInt8(part)
            else { return nil }
            bytes.append(number)
        }
        return bytes
    }
}

private enum IPv6Literal {
    static func parse(_ value: String) -> [UInt8]? {
        guard !value.isEmpty, !value.contains("%"), value.allSatisfy({ $0.isHexDigit || $0 == ":" || $0 == "." }) else { return nil }
        let components = value.components(separatedBy: "::")
        guard components.count <= 2 else { return nil }
        let hasCompression = components.count == 2
        let left = tryParseGroups(components[0])
        let right = hasCompression ? tryParseGroups(components[1]) : []
        guard var left, var right else { return nil }
        let usesIPv4 = value.contains(".")
        if usesIPv4 {
            guard let ipv4Text = (right.last ?? left.last), let ipv4 = IPv4Literal.parse(ipv4Text) else { return nil }
            if right.last != nil { right.removeLast() } else { left.removeLast() }
            let ipv4Groups = [String((UInt16(ipv4[0]) << 8) | UInt16(ipv4[1]), radix: 16), String((UInt16(ipv4[2]) << 8) | UInt16(ipv4[3]), radix: 16)]
            if right.isEmpty && hasCompression { right = ipv4Groups } else { left.append(contentsOf: ipv4Groups) }
        }
        let groupCount = left.count + right.count
        guard (hasCompression && groupCount < 8) || (!hasCompression && groupCount == 8) else { return nil }
        let groups = left + Array(repeating: "0", count: 8 - groupCount) + right
        var bytes: [UInt8] = []
        for group in groups {
            guard let value = UInt16(group, radix: 16) else { return nil }
            bytes.append(UInt8(value >> 8))
            bytes.append(UInt8(value & 0xff))
        }
        return bytes
    }

    private static func tryParseGroups(_ side: String) -> [String]? {
        if side.isEmpty { return [] }
        let groups = side.split(separator: ":", omittingEmptySubsequences: false).map(String.init)
        guard groups.allSatisfy({ !$0.isEmpty && ($0.contains(".") || ($0.count <= 4 && $0.allSatisfy(\.isHexDigit))) }) else { return nil }
        return groups
    }
}

private extension Array where Element == UInt8 {
    func isIn(_ prefixes: [([UInt8], Int)]) -> Bool {
        prefixes.contains { prefix, bits in
            guard count == 4 || count == 16 else { return false }
            let fullBytes = bits / 8
            let remainder = bits % 8
            let requiredPrefixBytes = fullBytes + (remainder == 0 ? 0 : 1)
            guard prefix.count >= requiredPrefixBytes,
                  zip(prefix.prefix(fullBytes), self.prefix(fullBytes)).allSatisfy(==)
            else { return false }
            guard remainder != 0 else { return true }
            let mask = UInt8(truncatingIfNeeded: 0xff << (8 - remainder))
            return (prefix[fullBytes] & mask) == (self[fullBytes] & mask)
        }
    }
}
