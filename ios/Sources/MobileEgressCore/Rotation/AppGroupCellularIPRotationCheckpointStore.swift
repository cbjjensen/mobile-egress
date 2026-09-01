import Foundation

public enum CellularIPRotationCheckpointStoreError: String, Error, Codable, Equatable, Sendable {
    case containerUnavailable
    case inactiveState
    case attemptMismatch
    case expired
    case malformed
    case missingRequiredTiming
    case readFailed
    case writeFailed
}

public protocol CellularIPRotationCheckpointStoring: Sendable {
    func save(_ checkpoint: CellularIPRotationCheckpoint) throws
    func load(at date: Date) throws -> CellularIPRotationCheckpoint?
    func clear() throws
    func retire(attemptID: UInt64) throws
}

public extension CellularIPRotationCheckpointStoring {
    func retire(attemptID: UInt64) throws {
        try clear()
    }

    func load(
        expectedAttemptID: UInt64,
        at date: Date
    ) throws -> CellularIPRotationCheckpoint? {
        guard let checkpoint = try load(at: date) else { return nil }
        guard checkpoint.state.attemptID == expectedAttemptID else {
            throw CellularIPRotationCheckpointStoreError.attemptMismatch
        }
        return checkpoint
    }
}

public final class AppGroupCellularIPRotationCheckpointStore:
    CellularIPRotationCheckpointStoring,
    @unchecked Sendable
{
    private static let filename = "cellular-ip-rotation-checkpoint.json"
    private let lock = NSLock()
    private let encoder: JSONEncoder
    private let decoder: JSONDecoder
    private let fileRemover: @Sendable (URL) throws -> Void
    let checkpointURL: URL
    var retirementURL: URL { checkpointURL }

    private struct RetirementTombstone: Codable {
        let formatVersion: Int
        let retiredAttemptID: UInt64

        init(retiredAttemptID: UInt64) {
            formatVersion = 1
            self.retiredAttemptID = retiredAttemptID
        }
    }

    #if canImport(Darwin)
    public convenience init(appGroupIdentifier: String) throws {
        try self.init(
            appGroupIdentifier: appGroupIdentifier,
            containerResolver: { identifier in
                FileManager.default.containerURL(
                    forSecurityApplicationGroupIdentifier: identifier
                )
            }
        )
    }
    #endif

    convenience init(
        appGroupIdentifier: String,
        containerResolver: (String) -> URL?
    ) throws {
        guard !appGroupIdentifier.isEmpty,
              let containerURL = containerResolver(appGroupIdentifier)
        else {
            throw CellularIPRotationCheckpointStoreError.containerUnavailable
        }
        self.init(containerURL: containerURL)
    }

    init(
        containerURL: URL,
        encoder: JSONEncoder = .init(),
        decoder: JSONDecoder = .init(),
        fileRemover: @escaping @Sendable (URL) throws -> Void = {
            try FileManager.default.removeItem(at: $0)
        }
    ) {
        checkpointURL = containerURL.appendingPathComponent(Self.filename, isDirectory: false)
        self.encoder = encoder
        self.decoder = decoder
        self.fileRemover = fileRemover
    }

    public func save(_ checkpoint: CellularIPRotationCheckpoint) throws {
        lock.lock()
        defer { lock.unlock() }
        try Self.validate(checkpoint)
        let data: Data
        do {
            data = try encoder.encode(checkpoint)
            try data.write(to: checkpointURL, options: .atomic)
        } catch let error as CellularIPRotationCheckpointStoreError {
            throw error
        } catch {
            throw CellularIPRotationCheckpointStoreError.writeFailed
        }
    }

    public func load(at date: Date) throws -> CellularIPRotationCheckpoint? {
        lock.lock()
        defer { lock.unlock() }
        guard FileManager.default.fileExists(atPath: checkpointURL.path) else { return nil }
        let data: Data
        do {
            data = try Data(contentsOf: checkpointURL, options: [.mappedIfSafe])
        } catch {
            throw CellularIPRotationCheckpointStoreError.readFailed
        }
        if let tombstone = try? decoder.decode(RetirementTombstone.self, from: data),
           tombstone.formatVersion == 1 {
            return nil
        }
        let checkpoint: CellularIPRotationCheckpoint
        do {
            checkpoint = try decoder.decode(CellularIPRotationCheckpoint.self, from: data)
        } catch {
            throw CellularIPRotationCheckpointStoreError.malformed
        }
        try Self.validate(checkpoint)
        guard date >= checkpoint.savedAt else {
            throw CellularIPRotationCheckpointStoreError.malformed
        }
        guard !checkpoint.isExpired(at: date) else {
            try? FileManager.default.removeItem(at: checkpointURL)
            throw CellularIPRotationCheckpointStoreError.expired
        }
        return checkpoint
    }

    public func clear() throws {
        lock.lock()
        defer { lock.unlock() }
        guard FileManager.default.fileExists(atPath: checkpointURL.path) else { return }
        do {
            try fileRemover(checkpointURL)
        } catch {
            throw CellularIPRotationCheckpointStoreError.writeFailed
        }
    }

    public func retire(attemptID: UInt64) throws {
        lock.lock()
        defer { lock.unlock() }
        do {
            let tombstone = try encoder.encode(
                RetirementTombstone(retiredAttemptID: attemptID)
            )
            try tombstone.write(to: checkpointURL, options: .atomic)
        } catch {
            throw CellularIPRotationCheckpointStoreError.writeFailed
        }
        try? fileRemover(checkpointURL)
    }

    private static func validate(_ checkpoint: CellularIPRotationCheckpoint) throws {
        guard checkpoint.state.isActive, checkpoint.state.attemptID != nil else {
            throw CellularIPRotationCheckpointStoreError.inactiveState
        }
        switch checkpoint.state {
        case .awaitingAirplaneMode, .awaitingCellularReturn:
            guard let deadline = checkpoint.timeoutDeadline,
                  deadline > checkpoint.savedAt
            else {
                throw CellularIPRotationCheckpointStoreError.missingRequiredTiming
            }
        case .idle, .awaitingConfirmation, .preparing, .holding, .verifying, .completed, .failed:
            guard checkpoint.timeoutDeadline == nil else {
                throw CellularIPRotationCheckpointStoreError.malformed
            }
        }
    }
}
