import Foundation
#if canImport(zlib)
import zlib
#endif

/// Normalizes optical input only; wire fields and signatures use DirectBundle unchanged.
public enum CompactQRInput {
    public static func normalize(_ text: String) throws -> String {
        guard text.utf8.count <= 87_384 else { throw DirectAgentError.invalidBundle }
        let value = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard value.hasPrefix("MEQR") else { return value }
        guard value.hasPrefix("MEQR1:") else { throw DirectAgentError.invalidBundle }
        #if canImport(zlib)
        let compressed = try DirectBundle.decode(String(value.dropFirst(6)))
        var stream = z_stream()
        guard inflateInit_(&stream, ZLIB_VERSION, Int32(MemoryLayout<z_stream>.size)) == Z_OK else {
            throw DirectAgentError.invalidBundle
        }
        defer { inflateEnd(&stream) }
        // One extra byte detects overflow without ever allocating an unbounded
        // expansion. Z_FINISH must consume one complete, checksum-valid stream.
        var output = [UInt8](repeating: 0, count: 65_537)
        let expanded: Data = try compressed.withUnsafeBytes { source in
            try output.withUnsafeMutableBytes { destination in
                stream.next_in = UnsafeMutablePointer(mutating: source.bindMemory(to: Bytef.self).baseAddress!)
                stream.avail_in = uInt(compressed.count)
                stream.next_out = destination.bindMemory(to: Bytef.self).baseAddress!
                stream.avail_out = uInt(destination.count)
                guard inflate(&stream, Z_FINISH) == Z_STREAM_END,
                      stream.avail_in == 0, stream.total_out > 0, stream.total_out <= 65_536 else {
                    throw DirectAgentError.invalidBundle
                }
                return Data(destination.prefix(Int(stream.total_out)))
            }
        }
        return DirectBundle.encode(expanded)
        #else
        // Supported Apple platforms supply zlib. Portable builds must fail
        // closed, never reinterpret a compact envelope as a wire credential.
        throw DirectAgentError.invalidBundle
        #endif
    }
}
