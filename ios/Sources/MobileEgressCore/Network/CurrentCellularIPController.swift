import Foundation

public struct CurrentCellularIPState: Equatable, Sendable {
    public var snapshot: PublicIPSnapshot
    public var checking: Bool
    public var canRefresh: Bool

    public init(snapshot: PublicIPSnapshot = .init(), checking: Bool = false, canRefresh: Bool = false) {
        self.snapshot = snapshot; self.checking = checking; self.canRefresh = canRefresh
    }
}

/// Foreground display only; never changes sharing intent or rotation state.
@MainActor public final class CurrentCellularIPController {
    public private(set) var state = CurrentCellularIPState()
    private let probe: any CellularPublicIPProbing
    private var active = false
    private var available = false
    private var rotating = false
    private var generation: UInt64 = 0
    private var task: Task<Void, Never>?
    private var changed: (@MainActor (CurrentCellularIPState) -> Void)?

    public init(probe: any CellularPublicIPProbing) { self.probe = probe }

    public func setChangeHandler(_ handler: @escaping @MainActor (CurrentCellularIPState) -> Void) {
        changed = handler; handler(state)
    }

    public func setActive(_ active: Bool) {
        guard self.active != active else { return }
        self.active = active
        invalidate(); refresh()
    }

    public func cellularChanged(available: Bool) {
        self.available = available
        invalidate(); refresh()
    }

    public func setRotating(_ rotating: Bool) {
        guard self.rotating != rotating else { return }
        self.rotating = rotating
        invalidate(); refresh()
    }

    public func refresh() {
        guard active, available, !rotating, !state.checking else { return }
        generation &+= 1
        let expected = generation
        publish(.init(checking: true, canRefresh: true))
        task = Task { [weak self, probe] in
            let snapshot = await probe.probe()
            guard let self, generation == expected, !Task.isCancelled else { return }
            task = nil
            publish(.init(snapshot: snapshot, canRefresh: true))
        }
    }

    private func invalidate() {
        generation &+= 1
        task?.cancel(); task = nil
        publish(.init(canRefresh: active && available && !rotating))
    }

    private func publish(_ state: CurrentCellularIPState) { self.state = state; changed?(state) }
}
