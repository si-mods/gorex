import Foundation

/// Runs Hotspot Share's operations one after another, in the order they
/// were added, on the main actor, while each runs its blocking Go calls
/// off the main actor with `ShareCalls.offMain` (#860). An operation
/// starts only when the one before it has ended, so a Stop never runs
/// before an earlier Start.
final class ShareCalls {
    private var tail: Task<Void, Never>?

    /// Adds `op` after every operation added before it.
    @discardableResult
    func add(_ op: @escaping @MainActor () async -> Void) -> Task<Void, Never> {
        let prev = tail
        let task = Task { @MainActor in
            await prev?.value
            await op()
        }
        tail = task
        return task
    }

    /// Runs `work` on a thread other than the main thread.
    @concurrent
    nonisolated static func offMain<T: Sendable>(_ work: @escaping @Sendable () -> T) async -> T {
        work()
    }
}
