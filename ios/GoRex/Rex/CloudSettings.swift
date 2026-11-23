import Foundation

/// The cloud's key-value store, as CloudSettings needs it.
protocol CloudStore: AnyObject {
    func object(forKey key: String) -> Any?
    func set(_ value: Any?, forKey key: String)
    func removeObject(forKey key: String)
    func synchronize() -> Bool
}

extension NSUbiquitousKeyValueStore: CloudStore {}

/// The settings that follow the user to their other devices through iCloud
/// (docs/IOS-008-icloud-sync.md): each is mirrored between UserDefaults, which
/// the app reads, and the iCloud key-value store. The API key is not here:
/// it goes through the Keychain.
final class CloudSettings {
    /// A value as both stores keep it: a string or a number.
    enum Value: Equatable {
        case string(String)
        case double(Double)

        init?(_ any: Any?, number: Bool) {
            if number {
                guard let n = any as? NSNumber else { return nil }
                self = .double(n.doubleValue)
            } else {
                guard let s = any as? String else { return nil }
                self = .string(s)
            }
        }

        var object: Any {
            switch self {
            case .string(let s): return s
            case .double(let d): return d
            }
        }
    }

    /// The keys that sync, whether each holds a number, and what the
    /// terminals are told when its value arrives.
    static let keys: [(key: String, number: Bool, tell: Notification.Name?)] = [
        ("scheme", false, nil),
        ("theme", false, .terminalTheme),
        ("fontSize", true, .terminalFont),
        ("fontFamily", false, .terminalFont),
        ("fontLigatures", true, .terminalFont),
        ("minimumContrast", true, .minimumContrast),
        (Polish.baseKey, false, nil),
        (Polish.modelKey, false, nil),
    ]

    /// The instance the app runs, started at launch.
    static let shared = CloudSettings()

    private let cloud: CloudStore
    private let defaults: UserDefaults
    /// What each key held in defaults when last looked at, so that only a
    /// change made here goes to the cloud, and what came from it goes back
    /// nowhere.
    private var known: [String: Value] = [:]
    private var started = false
    /// The push that waits for a change to settle.
    private var pending: Task<Void, Never>?
    /// How long a change settles before it is sent.
    static let settle = Duration.milliseconds(1500)

    init(cloud: CloudStore = NSUbiquitousKeyValueStore.default, defaults: UserDefaults = .standard) {
        self.cloud = cloud
        self.defaults = defaults
    }

    /// Takes what the cloud has, or gives what this device has, then follows
    /// both. Once.
    func start() {
        guard !started else { return }
        started = true
        _ = cloud.synchronize()
        for k in Self.keys { reconcile(k.key) }
        let center = NotificationCenter.default
        center.addObserver(forName: NSUbiquitousKeyValueStore.didChangeExternallyNotification, object: nil, queue: .main) { [weak self] n in
            let keys = n.userInfo?[NSUbiquitousKeyValueStoreChangedKeysKey] as? [String] ?? []
            let reason = n.userInfo?[NSUbiquitousKeyValueStoreChangeReasonKey] as? Int
            MainActor.assumeIsolated { self?.cloudChanged(keys: keys, reason: reason) }
        }
        center.addObserver(forName: UserDefaults.didChangeNotification, object: defaults, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.defaultsChanged() }
        }
    }

    /// The cloud's value of a key if it has one, else this device's, which
    /// is then sent; never a deletion.
    private func reconcile(_ key: String) {
        if let value = cloudValue(key) {
            apply(value, to: key)
        } else {
            known[key] = localValue(key)
            push(key)
        }
    }

    /// The cloud changed under us. A server change names its keys: each is
    /// taken, or removed here when another device removed it. After the
    /// first download, an account change or a quota violation, the cloud may
    /// lack what this device wrote before, so every key is reconciled: taken
    /// from the cloud, or sent from here.
    func cloudChanged(keys changed: [String], reason: Int?) {
        if reason == nil || reason == NSUbiquitousKeyValueStoreServerChange {
            for k in Self.keys where changed.contains(k.key) {
                if let value = cloudValue(k.key) {
                    apply(value, to: k.key)
                } else if localValue(k.key) != nil {
                    // Removed on another device.
                    known[k.key] = nil
                    defaults.removeObject(forKey: k.key)
                    tell(k.key)
                }
            }
        } else {
            for k in Self.keys { reconcile(k.key) }
        }
    }

    /// Defaults changed: send what this device changed once it settles, as
    /// a pinch zoom writes the size at each step and a field at each
    /// keystroke, and a half-typed value must not reach other devices.
    func defaultsChanged() {
        guard Self.keys.contains(where: { localValue($0.key) != known[$0.key] }) else { return }
        pending?.cancel()
        pending = Task { [weak self] in
            try? await Task.sleep(for: Self.settle)
            guard !Task.isCancelled else { return }
            self?.flush()
        }
    }

    /// Sends the synced keys that this device changed.
    func flush() {
        pending?.cancel()
        pending = nil
        for k in Self.keys where localValue(k.key) != known[k.key] {
            known[k.key] = localValue(k.key)
            push(k.key)
        }
    }

    private func localValue(_ key: String) -> Value? {
        Value(defaults.object(forKey: key), number: Self.number(key))
    }

    private func cloudValue(_ key: String) -> Value? {
        Value(cloud.object(forKey: key), number: Self.number(key))
    }

    private static func number(_ key: String) -> Bool {
        keys.first { $0.key == key }?.number ?? false
    }

    /// Writes this device's value to the cloud, or removes the cloud's, when
    /// they differ.
    private func push(_ key: String) {
        let local = localValue(key)
        guard local != cloudValue(key) else { return }
        if let local { cloud.set(local.object, forKey: key) } else { cloud.removeObject(forKey: key) }
    }

    /// Writes the cloud's value to defaults, when it differs, and tells the
    /// terminals.
    private func apply(_ value: Value, to key: String) {
        let changed = localValue(key) != value
        known[key] = value
        guard changed else { return }
        defaults.set(value.object, forKey: key)
        tell(key)
    }

    private func tell(_ key: String) {
        if let name = Self.keys.first(where: { $0.key == key })?.tell {
            NotificationCenter.default.post(name: name, object: nil)
        }
    }
}
