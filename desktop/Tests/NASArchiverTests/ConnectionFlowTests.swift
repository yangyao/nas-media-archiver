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
        XCTAssertEqual(try TokenStore.token(for: connection.id), "valid-connection-test-token")
    }

    func testWrongPasswordDoesNotCompleteConnection() async throws {
        try await assertConnectionFailure(token: "wrong-connection-test-token", expected: "連線密碼不正確")
    }

    func testInitialTaskLoadFailureDoesNotCompleteConnection() async throws {
        try await assertConnectionFailure(token: "failing-jobs-test-token", expected: "Tasks unavailable")
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
