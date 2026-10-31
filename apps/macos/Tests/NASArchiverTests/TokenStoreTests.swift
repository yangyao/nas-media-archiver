import Foundation
import XCTest
@testable import NASArchiver

final class TokenStoreTests: XCTestCase {
    func testLocalStorageSurvivesReopeningAndRestrictsPermissions() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }
        let id = UUID(), otherID = UUID()
        let store = LocalTokenStore(directoryURL: directory)
        XCTAssertEqual(try store.token(for: id), "")
        try store.save("server-test-token", for: id)
        try store.save("other-test-token", for: otherID)
        let reopened = LocalTokenStore(directoryURL: directory)
        XCTAssertEqual(try reopened.token(for: id), "server-test-token")
        let file = directory.appendingPathComponent(id.uuidString).appendingPathExtension("token")
        let directoryMode = try FileManager.default.attributesOfItem(atPath: directory.path)[.posixPermissions] as? NSNumber
        let fileMode = try FileManager.default.attributesOfItem(atPath: file.path)[.posixPermissions] as? NSNumber
        XCTAssertEqual(directoryMode?.intValue, 0o700)
        XCTAssertEqual(fileMode?.intValue, 0o600)
        try reopened.save("updated-test-token", for: id)
        XCTAssertEqual(try store.token(for: id), "updated-test-token")
        try reopened.remove(id)
        XCTAssertEqual(try store.token(for: id), "")
        XCTAssertEqual(try store.token(for: otherID), "other-test-token")
        try reopened.remove(id)
    }

    func testInvalidLocalDataIsReportedWithoutOverwritingIt() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let id = UUID()
        let file = directory.appendingPathComponent(id.uuidString).appendingPathExtension("token")
        let invalid = Data([0xFF, 0xFE])
        try invalid.write(to: file)
        XCTAssertThrowsError(try LocalTokenStore(directoryURL: directory).token(for: id))
        XCTAssertEqual(try Data(contentsOf: file), invalid)
    }
}
