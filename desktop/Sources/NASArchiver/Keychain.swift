import Foundation
import Security

enum TokenStore {
    private static let service = "com.yangyao.nas-media-archiver"
    static func token(for id: UUID) throws -> String {
        var query = baseQuery(id)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        if status == errSecItemNotFound { return "" }
        guard status == errSecSuccess, let data = result as? Data, let token = String(data: data, encoding: .utf8) else {
            throw ServiceError(message: "無法讀取鑰匙串中的 服務端權杖（\(status)）。")
        }
        return token
    }
    static func save(_ token: String, for id: UUID) throws {
        let query = baseQuery(id)
        let update = [kSecValueData as String: Data(token.utf8)]
        let status = SecItemUpdate(query as CFDictionary, update as CFDictionary)
        if status == errSecItemNotFound {
            var insert = query
            insert[kSecValueData as String] = Data(token.utf8)
            insert[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
            let result = SecItemAdd(insert as CFDictionary, nil)
            guard result == errSecSuccess else { throw ServiceError(message: "無法保存 服務端權杖到鑰匙串（\(result)）。") }
        } else if status != errSecSuccess { throw ServiceError(message: "無法更新鑰匙串中的 服務端權杖（\(status)）。") }
    }
    static func remove(_ id: UUID) throws {
        let result = SecItemDelete(baseQuery(id) as CFDictionary)
        guard result == errSecSuccess || result == errSecItemNotFound else { throw ServiceError(message: "無法刪除鑰匙串權杖（\(result)）。") }
    }
    private static func baseQuery(_ id: UUID) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: id.uuidString]
    }
}
