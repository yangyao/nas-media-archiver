import Foundation
import XCTest
@testable import NASArchiver

@MainActor
final class ConnectionFlowTests: XCTestCase {
    func testSaveWaitsForAuthenticatedConnectionAndInitialTaskLoad() async throws {
        let suite = "connection-flow-\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [ConnectionFlowProtocol.self]
        let session = URLSession(configuration: config)
        let model = AppModel(defaults: defaults, session: session)
        let connection = NASConnection(name: "Test service", address: "http://test-service:8787")
        defer {
            try? model.removeConnection(connection)
            defaults.removePersistentDomain(forName: suite)
            session.invalidateAndCancel()
        }
        try await model.saveConnection(connection, token: "valid-connection-test-token")
        XCTAssertTrue(model.isConnected)
        XCTAssertFalse(model.isConnecting)
        XCTAssertEqual(model.serverInfo?.name, "Test service")
        XCTAssertEqual(model.jobs, [])
        XCTAssertTrue(model.needsArchiveOnboarding)
        XCTAssertTrue(model.showNewArchive)
        XCTAssertEqual(try TokenStore.token(for: connection.id), "valid-connection-test-token")
    }

    func testWrongPasswordDoesNotCompleteConnection() async throws {
        try await assertConnectionFailure(token: "wrong-connection-test-token", expected: "連線密碼不正確")
    }

    func testInitialTaskLoadFailureDoesNotCompleteConnection() async throws {
        try await assertConnectionFailure(token: "failing-jobs-test-token", expected: "Tasks unavailable")
    }

    func testArchiveOnboardingWaitsUntilConnectionSheetIsDismissed() async throws {
        let suite = "connection-flow-\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [ConnectionFlowProtocol.self]
        let session = URLSession(configuration: config)
        let model = AppModel(defaults: defaults, session: session)
        let connection = NASConnection(name: "Test service", address: "http://test-service:8787")
        defer {
            try? model.removeConnection(connection)
            defaults.removePersistentDomain(forName: suite)
            session.invalidateAndCancel()
        }
        model.connectionEditor = .init(connection: nil)
        try await model.saveConnection(connection, token: "valid-connection-test-token")
        XCTAssertTrue(model.needsArchiveOnboarding)
        XCTAssertFalse(model.showNewArchive, "Never present two sheets at once")
        model.connectionEditor = nil
        model.presentArchiveOnboardingIfNeeded()
        XCTAssertTrue(model.showNewArchive)
        model.presentArchiveOnboardingIfNeeded()
        XCTAssertTrue(model.showNewArchive)
    }

    func testExistingActiveOrHistoricalTasksSkipArchiveOnboarding() async throws {
        for token in ["existing-running-test-token", "existing-completed-test-token"] {
            let suite = "connection-flow-\(UUID().uuidString)"
            let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
            let config = URLSessionConfiguration.ephemeral
            config.protocolClasses = [ConnectionFlowProtocol.self]
            let session = URLSession(configuration: config)
            let model = AppModel(defaults: defaults, session: session)
            let connection = NASConnection(name: "Test service", address: "http://test-service:8787")
            defer {
                try? model.removeConnection(connection)
                defaults.removePersistentDomain(forName: suite)
                session.invalidateAndCancel()
            }
            try await model.saveConnection(connection, token: token)
            XCTAssertEqual(model.jobs.count, 1)
            XCTAssertFalse(model.needsArchiveOnboarding)
            XCTAssertFalse(model.showNewArchive)
        }
    }

    private func assertConnectionFailure(token: String, expected: String) async throws {
        let suite = "connection-flow-\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [ConnectionFlowProtocol.self]
        let session = URLSession(configuration: config)
        let model = AppModel(defaults: defaults, session: session)
        let connection = NASConnection(name: "Test service", address: "http://test-service:8787")
        defer {
            try? model.removeConnection(connection)
            defaults.removePersistentDomain(forName: suite)
            session.invalidateAndCancel()
        }
        do {
            try await model.saveConnection(connection, token: token)
            XCTFail("An unsuccessful connection must keep the connection editor open")
        } catch {
            XCTAssertTrue(error.localizedDescription.contains(expected), error.localizedDescription)
        }
        XCTAssertFalse(model.isConnected)
        XCTAssertFalse(model.isConnecting)
        XCTAssertFalse(model.showNewArchive)
    }
}

private final class ConnectionFlowProtocol: URLProtocol, @unchecked Sendable {
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        let credential = request.value(forHTTPHeaderField: "Authorization") ?? ""
        let path = request.url!.path
        let status: Int
        let payload: String
        var type = "application/json"
        if credential == "Bearer wrong-connection-test-token" {
            status = 401
            payload = #"{"error":{"code":"unauthorized","message":"Invalid token"}}"#
        } else if path.hasSuffix("/info") {
            status = 200
            payload = #"{"name":"Test service","version":"0.2.0","archiveBase":"/archive","roots":["/input","/archive"],"freeBytes":1000,"tools":{}}"#
        } else if path.hasSuffix("/jobs"), credential == "Bearer failing-jobs-test-token" {
            status = 503
            payload = #"{"error":{"code":"unavailable","message":"Tasks unavailable"}}"#
        } else if path.hasSuffix("/jobs"), credential.contains("existing-") {
            status = 200
            let state = credential.contains("running") ? "running" : "completed"
            payload = existingOnboardingJob.replacingOccurrences(of: "JOB_STATUS", with: state)
        } else if path.hasSuffix("/events") {
            status = 200; type = "text/event-stream"; payload = ": connected\n\n"
        } else {
            status = 200; payload = "[]"
        }
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": type])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(payload.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

private let existingOnboardingJob = #"""
[{"id":"existing-job","name":"Existing task","sourcePaths":["/input"],"archiveBase":"/archive","cleanupEmptyDirectories":true,"status":"JOB_STATUS","createdAt":"2026-10-31T00:00:00Z","updatedAt":"2026-10-31T00:00:00Z","totalFiles":1,"completedFiles":1,"failedFiles":0,"attentionFiles":0,"totalBytes":10,"completedBytes":10,"currentFile":"","currentFileBytes":0,"dateRangeStart":"","dateRangeEnd":"","error":"","cleanupStatus":"done","residualPaths":[]}]
"""#
