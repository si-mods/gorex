import Foundation

/// Where a new window of a project opens (docs/projects.md, Where a new
/// session starts): a project with a folder opens on its home host, in the
/// folder, and nowhere else.
enum ProjectWindow {
    /// A host as the choice reads it.
    struct Candidate {
        let id: UUID
        let name: String
        /// The host's machine, the one last seen while offline
        /// (Host.homeMachineKey).
        let machine: String
        let online: Bool
        let sessions: [Host.Session]
    }

    enum Plan: Equatable {
        /// The project has no folder: a new window opens as before.
        case noFolder
        /// No host of the home's machine is added on this device.
        case unknownHost
        /// The home host is added, and not online.
        case offline(host: UUID, name: String)
        /// A window in the session that keeps the project on the home host.
        /// A nil cwd lets the server start it in the folder it keeps, and
        /// check that the folder is there (folder_missing).
        case window(host: UUID, session: String, cwd: String?)
        /// The home host keeps no session of the project yet: one of its
        /// name, its first window in the folder.
        case session(host: UUID, label: String, cwd: String)
    }

    static func plan(_ space: CentralSpace, hosts: [Candidate]) -> Plan {
        guard let dir = space.folder else { return .noFolder }
        let home = hosts.filter { !$0.machine.isEmpty && $0.machine == space.home_machine }
        // Of two hosts of one machine, as its LAN and tailnet entries, the
        // one online is the home.
        guard let host = home.first(where: \.online) ?? home.first else { return .unknownHost }
        guard host.online else { return .offline(host: host.id, name: host.name) }
        let session = host.sessions.first {
            $0.id != SpaceStore.desktopTerminalsSessionID
                && $0.label.trimmingCharacters(in: .whitespaces).caseInsensitiveCompare(space.name) == .orderedSame
        }
        guard let session else { return .session(host: host.id, label: space.name, cwd: dir) }
        // The server keeps this folder as the session's home here: it
        // starts the window there and checks the folder itself.
        let kept = session.project?.home_machine == host.machine && session.project?.dir == dir
        return .window(host: host.id, session: session.id, cwd: kept ? nil : dir)
    }

    /// Whether an error is the server's note that the project's folder is
    /// gone (conflict, folder_missing).
    static func folderMissing(_ error: Error) -> Bool {
        guard let e = error as? RexError else { return false }
        return e.code == "conflict" && e.message.contains("folder_missing")
    }

    /// Fails as the server's folder_missing (conflict) when dir is not a
    /// folder the host can read, as gorex.dir.list answers it: the server
    /// would start a shell given a cwd that is gone at home, which a
    /// project's window must not.
    static func ensureFolder(_ dir: String, list: (String) async throws -> Host.Folders) async throws {
        if let code = try await list(dir).code {
            throw RexError(code: "conflict", message: "folder_missing: \(dir) (\(code))")
        }
    }

    /// A folder as the strip shows it: the home directory as ~.
    static func short(_ dir: String) -> String {
        for prefix in ["/Users/", "/home/"] where dir.hasPrefix(prefix) {
            let rest = dir.dropFirst(prefix.count)
            guard let slash = rest.firstIndex(of: "/") else { return "~" }
            return "~" + rest[slash...]
        }
        return dir
    }

    /// A project's name by default: its folder's name.
    static func name(of dir: String) -> String {
        let trimmed = dir.trimmingCharacters(in: CharacterSet(charactersIn: "/\\"))
        return trimmed.split(whereSeparator: { $0 == "/" || $0 == "\\" }).last.map(String.init) ?? dir
    }
}
