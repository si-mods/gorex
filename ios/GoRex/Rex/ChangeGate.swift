/// The value last passed on, so that only a change is passed on again: a
/// view given to UIKit, say, that is costly to give again unchanged.
struct ChangeGate<Value: Equatable> {
    private(set) var value: Value

    init(_ value: Value) { self.value = value }

    /// Takes the value, and says whether it differs from the last one.
    mutating func update(_ new: Value) -> Bool {
        guard new != value else { return false }
        value = new
        return true
    }
}
