import Foundation

/// Negative trust decisions contain only Client IDs. They remain available when
/// the credential Keychain cannot commit a deletion.
public protocol DirectRemovalIntentPersisting: Sendable {
    func load() throws -> Set<String>
    func save(_ clientIDs: Set<String>) throws
}

public final class FileDirectRemovalIntentStore: DirectRemovalIntentPersisting, @unchecked Sendable {
    private let lock = NSLock()
    private let url: URL
    public init(url: URL) { self.url = url }
    public func load() throws -> Set<String> {
        try lock.withLock {
            guard FileManager.default.fileExists(atPath: url.path) else { return [] }
            let data = try Data(contentsOf: url)
            guard data.count <= 16_384 else { throw DirectAgentError.persistence }
            let values = try JSONDecoder().decode([String].self, from: data)
            let ids = Set(values)
            guard ids.count == values.count else { throw DirectAgentError.persistence }
            try validate(ids)
            return ids
        }
    }
    public func save(_ clientIDs: Set<String>) throws {
        try lock.withLock {
            try validate(clientIDs)
            try JSONEncoder().encode(clientIDs.sorted()).write(to: url, options: .atomic)
        }
    }
    private func validate(_ ids: Set<String>) throws {
        guard ids.count <= 10, ids.allSatisfy({ UUID(uuidString: $0) != nil }) else { throw DirectAgentError.persistence }
    }
}

final class MemoryDirectRemovalIntentStore: DirectRemovalIntentPersisting, @unchecked Sendable {
    private let lock = NSLock()
    private var ids: Set<String> = []
    func load() throws -> Set<String> { lock.withLock { ids } }
    func save(_ clientIDs: Set<String>) throws { lock.withLock { ids = clientIDs } }
}
