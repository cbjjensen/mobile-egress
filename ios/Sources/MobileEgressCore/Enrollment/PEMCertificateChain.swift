import Foundation

enum PEMCertificateChain {
    static func parse(_ pem: String) throws -> [Data] {
        guard !pem.contains("\r") else { throw EnrollmentError.invalidCertificateChain }
        let lines = pem.split(omittingEmptySubsequences: false, whereSeparator: \.isNewline).map(String.init)
        guard lines.last == "" else { throw EnrollmentError.invalidCertificateChain }
        var index = 0
        var certificates: [Data] = []

        while index < lines.count - 1 {
            guard lines[index] == "-----BEGIN CERTIFICATE-----" else {
                throw EnrollmentError.invalidCertificateChain
            }
            index += 1
            var body: [String] = []
            while index < lines.count - 1, lines[index] != "-----END CERTIFICATE-----" {
                let line = lines[index]
                guard !line.isEmpty,
                      line.count <= 64,
                      line.allSatisfy({ $0.isASCII && ($0.isLetter || $0.isNumber || $0 == "+" || $0 == "/" || $0 == "=") })
                else {
                    throw EnrollmentError.invalidCertificateChain
                }
                body.append(line)
                index += 1
            }
            guard !body.isEmpty,
                  index < lines.count - 1,
                  lines[index] == "-----END CERTIFICATE-----",
                  let der = Data(base64Encoded: body.joined())
            else {
                throw EnrollmentError.invalidCertificateChain
            }
            certificates.append(der)
            index += 1
        }
        guard !certificates.isEmpty else { throw EnrollmentError.invalidCertificateChain }
        return certificates
    }
}
