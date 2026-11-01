import Foundation

/// App-owned storage, independent of the app's development signing identity.
/// Tokens are plain text inside a user-only directory, never in UserDefaults.
struct LocalTokenStore {
    let directoryURL: URL

    private func url(for id: UUID) -> URL {
        directoryURL.appendingPathComponent(id.uuidString).appendingPathExtension("token")
    }

    func token(for id: UUID) throws -> String {
        let path = url(for: id)
        let data: Data
        do {
            data = try Data(contentsOf: path)
        } catch let error as CocoaError where error.code == .fileReadNoSuchFile {
            return ""
        }
        guard let value = String(data: data, encoding: .utf8) else {
            throw ServiceError(message: "无法读取本机的连接密码，请在连接设置重新填写。")
        }
        return value.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    func save(_ token: String, for id: UUID) throws {
        let manager = FileManager.default
        try manager.createDirectory(at: directoryURL, withIntermediateDirectories: true,
                                    attributes: [.posixPermissions: 0o700])
        try manager.setAttributes([.posixPermissions: 0o700], ofItemAtPath: directoryURL.path)
        let path = url(for: id)
        try Data(token.utf8).write(to: path, options: .atomic)
        try manager.setAttributes([.posixPermissions: 0o600], ofItemAtPath: path.path)
    }

    func remove(_ id: UUID) throws {
        do {
            try FileManager.default.removeItem(at: url(for: id))
        } catch let error as CocoaError where error.code == .fileNoSuchFile {
            return
        }
    }
}

enum TokenStore {
    private static func local() throws -> LocalTokenStore {
        let support = try FileManager.default.url(for: .applicationSupportDirectory,
                                                  in: .userDomainMask,
                                                  appropriateFor: nil, create: true)
        return LocalTokenStore(directoryURL: support
            .appendingPathComponent("NASArchiver", isDirectory: true)
            .appendingPathComponent("credentials", isDirectory: true))
    }

    // Deliberately do not migrate by reading Keychain: that would show the very
    // signing/password prompt this storage replaces. Existing users re-enter once.
    static func token(for id: UUID) throws -> String { try local().token(for: id) }
    static func save(_ token: String, for id: UUID) throws { try local().save(token, for: id) }
    static func remove(_ id: UUID) throws { try local().remove(id) }
}
