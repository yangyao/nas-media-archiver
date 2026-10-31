import Foundation
import XCTest
@testable import NASArchiver

final class ClientTests: XCTestCase {
    func testRemoteBrowseEscapesPathsAndUsesBearerAuthentication() throws {
        let client = try APIClient(address: "http://192.168.2.196:8787", token: "private-token")
        let request = try client.request("browse", query: [.init(name: "path", value: "/share/照片/旅行 & 家人")])
        let url = try XCTUnwrap(request.url)
        XCTAssertEqual(url.path, "/api/v1/browse")
        XCTAssertEqual(URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems?.first?.value, "/share/照片/旅行 & 家人")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer private-token")
        XCTAssertNil(url.user)
        XCTAssertFalse(url.absoluteString.contains("private-token"))
    }

    func testRejectsCredentialsAndNonHTTPConnectionURLs() {
        for address in ["file:///share/photos", "ssh://nas", "http://user:password@nas:8787", "http://nas:8787?token=secret", "192.168.2.196:8787"] {
            XCTAssertThrowsError(try APIClient(address: address, token: "token"), address)
        }
    }

    func testProgressIncludesCurrentLargeFileAndClampsOvershoot() throws {
        var job = try JSONDecoder().decode(ArchiveJob.self, from: Data(jobFixture.utf8))
        XCTAssertEqual(job.progress, 0.75, accuracy: 0.001)
        XCTAssertTrue(job.canPause)
        XCTAssertFalse(job.canStart)
        job.currentFileBytes = 2_000
        XCTAssertEqual(job.progress, 1)
        job.status = "paused"
        XCTAssertTrue(job.canStart)
        XCTAssertFalse(job.isTerminal)
        job.status = "completed_with_errors"
        XCTAssertTrue(job.isTerminal)
    }

    func testUnsupportedOnlyJobFinishesAndInterruptedScanRequiresRescan() throws {
        var job = try JSONDecoder().decode(ArchiveJob.self, from: Data(jobFixture.utf8))
        job.unsupportedFiles = job.totalFiles
        job.unsupportedBytes = job.totalBytes
        job.completedFiles = 0
        job.completedBytes = 0
        job.currentFileBytes = 0
        job.status = "completed"
        XCTAssertEqual(job.progress, 1)
        job.status = "interrupted"
        job.scanComplete = false
        XCTAssertTrue(job.needsRescan)
        XCTAssertFalse(job.canStart)
        job.scanComplete = true
        XCTAssertTrue(job.canStart)
    }

    func testSSEParserPreservesBlankLinesAndFragmentedUTF8() throws {
        let named = jobFixture.replacingOccurrences(of: "Family", with: "家人照片")
        let stream = ": heartbeat\r\nevent: job\r\ndata: " + named + "\r\n\r\n: another heartbeat\n\nevent: job\ndata: " + named + "\n\n"
        let bytes = Array(stream.utf8)
        var parser = JobEventParser()
        var jobs: [ArchiveJob] = []
        // Chunk boundaries split CRLF sequences and multibyte Chinese text.
        for offset in stride(from: 0, to: bytes.count, by: 7) {
            for byte in bytes[offset..<min(offset + 7, bytes.count)] {
                if let job = try parser.consume(byte) { jobs.append(job) }
            }
        }
        XCTAssertEqual(jobs.count, 2)
        XCTAssertEqual(jobs.map(\.name), ["家人照片", "家人照片"])
        XCTAssertEqual(jobs[1].completedBytes, 500)
    }

    func testSSEParserSupportsMultilineDataAndIgnoresOtherEvents() throws {
        let multiline = jobFixture.replacingOccurrences(of: ",", with: ",\ndata: ")
        var parser = JobEventParser()
        var jobs: [ArchiveJob] = []
        let stream = "event: notice\ndata: not-json\n\nevent: job\ndata: " + multiline + "\n\n"
        for byte in stream.utf8 {
            if let job = try parser.consume(byte) { jobs.append(job) }
        }
        XCTAssertEqual(jobs.count, 1)
        XCTAssertEqual(jobs.first?.id, "job-test")
    }

    func testEmptyEventStreamReportsConnectionBeforeAnyJobSnapshot() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [EmptyEventProtocol.self]
        let client = try APIClient(address: "http://server:8787", token: "token", session: URLSession(configuration: configuration))
        let connected = expectation(description: "Empty SSE stream is connected")
        do {
            for try await _ in client.events(onConnected: { connected.fulfill() }) {
                XCTFail("No jobs are present")
            }
        } catch { /* End-of-stream triggers reconnect, after the connected callback. */ }
        await fulfillment(of: [connected], timeout: 1)
    }

    func testServiceErrorsAreShownWithoutExposingToken() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [UnauthorizedProtocol.self]
        let client = try APIClient(address: "http://nas:8787", token: "private-token", session: URLSession(configuration: configuration))
        do {
            _ = try await client.info()
            XCTFail("Expected server error")
        } catch {
            XCTAssertEqual(error.localizedDescription, "連線密碼不正確，請重新輸入。")
            XCTAssertFalse(error.localizedDescription.contains("private-token"))
        }
    }
}

private final class EmptyEventProtocol: URLProtocol, @unchecked Sendable {
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        let response = HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "text/event-stream"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(": connected\n\n".utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

private final class UnauthorizedProtocol: URLProtocol, @unchecked Sendable {
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        let response = HTTPURLResponse(url: request.url!, statusCode: 401, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(#"{"error":{"code":"unauthorized","message":"權杖已失效"}}"#.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

private let jobFixture = #"""
{"id":"job-test","name":"Family","sourcePaths":["/share/upload"],"archiveBase":"/share/archive","cleanupEmptyDirectories":true,"status":"running","createdAt":"2026-10-31T00:00:00Z","updatedAt":"2026-10-31T00:00:01Z","totalFiles":2,"completedFiles":1,"failedFiles":0,"attentionFiles":0,"totalBytes":1000,"completedBytes":500,"currentFile":"large.mov","currentFileBytes":250,"dateRangeStart":"2026-01-01T00:00:00Z","dateRangeEnd":"2026-02-01T00:00:00Z","error":"","cleanupStatus":"pending","residualPaths":[]}
"""#
