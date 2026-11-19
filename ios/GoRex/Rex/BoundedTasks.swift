/// Work run a few at a time, in the order it came; the rest waits its
/// turn. Work for a key already waiting is not added again. cancelAll()
/// cancels the work that runs and drops the work that waits, as when the
/// connection the work uses ends.
final class BoundedTasks {
    let limit: Int
    private var waiting: [(key: String, work: @MainActor () async -> Void)] = []
    private var running: [Int: Task<Void, Never>] = [:]
    private var next = 0

    init(limit: Int) {
        self.limit = max(limit, 1)
    }

    /// The work that runs now.
    var runningCount: Int { running.count }
    /// The work that waits for its turn.
    var waitingCount: Int { waiting.count }

    func add(_ key: String, _ work: @escaping @MainActor () async -> Void) {
        guard !waiting.contains(where: { $0.key == key }) else { return }
        waiting.append((key, work))
        pump()
    }

    func cancelAll() {
        let r = running
        running = [:]
        waiting = []
        r.values.forEach { $0.cancel() }
    }

    private func pump() {
        while running.count < limit, !waiting.isEmpty {
            let work = waiting.removeFirst().work
            let id = next
            next += 1
            running[id] = Task { [weak self] in
                await work()
                self?.finish(id)
            }
        }
    }

    /// Work cancelled by cancelAll() is gone from running already: its end
    /// starts nothing.
    private func finish(_ id: Int) {
        guard running.removeValue(forKey: id) != nil else { return }
        pump()
    }
}
