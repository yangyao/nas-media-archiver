import Foundation
import XCTest
@testable import NASArchiver

final class ClientTests: XCTestCase {
    func testOnlyCompletedOrUnstartedTasksCanBeDeleted() throws {
        var job = try JSONDecoder().decode(ArchiveJob.self, from: Data(jobFixture.utf8))
        job.started = false
        job.completedFiles = 0
        for status in ["ready", "completed", "completed_with_errors", "failed", "interrupted"] {
            job.status = status
            XCTAssertTrue(job.canDelete, status)
        }
        for status in ["scanning", "running", "queued", "pausing", "paused"] {
            job.status = status
            XCTAssertFalse(job.canDelete, status)
        }
        job.status = "interrupted"; job.started = true
        XCTAssertFalse(job.canDelete)
        job.status = "completed"; job.deletable = false
        XCTAssertFalse(job.canDelete, "The server can retain a recovery journal")
    }

    @MainActor
    func testDeletedTaskCannotReturnThroughStaleEventsOrSnapshots() throws {
        let suite = "delete-events-\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let model = AppModel(defaults: defaults)
        let job = try JSONDecoder().decode(ArchiveJob.self, from: Data(jobFixture.utf8))
        model.jobs = [job]; model.selectedJobID = job.id; model.jobToDelete = job
        var deleted = job; deleted.status = "deleted"
        model.upsert(deleted)
        XCTAssertTrue(model.jobs.isEmpty)
        XCTAssertNil(model.selectedJobID)
        XCTAssertNil(model.jobToDelete)
        model.upsert(job)
        model.replaceJobs([job])
        XCTAssertTrue(model.jobs.isEmpty)
    }

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

    func testSkippedDuplicatesCountTowardProgressWithoutClaimingTheyWereArchived() throws {
        var job = try JSONDecoder().decode(ArchiveJob.self, from: Data(jobFixture.utf8))
        XCTAssertNil(job.skippedFiles) // Compatible with older service responses.
        job.currentFileBytes = 0
        job.skippedFiles = 1
        job.skippedBytes = 500
        job.skipDuplicates = true
        XCTAssertEqual(job.processedFiles, 2)
        XCTAssertEqual(job.progress, 1)
        XCTAssertEqual(job.completedFiles, 1)
        let request = CreateJobRequest(sourcePaths: ["/input"], archiveBase: "/archive", cleanupEmptyDirectories: true, skipDuplicates: true)
        let body = try JSONSerialization.jsonObject(with: JSONEncoder().encode(request)) as! [String: Any]
        XCTAssertEqual(body["skipDuplicates"] as? Bool, true)
    }

    func testAttentionExplainsNextStepsAndRemoteContentsAreVisible() throws {
        let payload = #"{"id":"f","sourcePath":"/upload/a.jpg","targetPath":"/archive/a.jpg","size":100,"status":"planned","metadataSource":"mtime","dateTime":"2026-01-01T00:00:00Z","warning":"No capture date","error":"","sha256":""}"#
        var file = try JSONDecoder().decode(ArchiveFile.self, from: Data(payload.utf8))
        XCTAssertTrue(file.needsDateReview)
        XCTAssertEqual(file.reviewTitle, "使用修改日期")
        XCTAssertTrue(file.reviewAdvice?.contains("设置日期") == true)
        file.warning = ""
        XCTAssertNil(file.reviewTitle)
        file.status = "unsupported"
        XCTAssertTrue(file.reviewAdvice?.contains("另行保存") == true)
        let listing = #"{"path":"/upload","parentPath":null,"entries":[{"name":".stfolder","path":"/upload/.stfolder","isDirectory":true,"size":0,"system":true,"canOpen":true,"symlink":false}]}"#
        let contents = try JSONDecoder().decode(RemoteContents.self, from: Data(listing.utf8))
        XCTAssertEqual(contents.entries[0].note, "系统目录，保留即可")
    }

    func testSourcePickerHidesArchiveAndPreventsSelectingAncestors() throws {
        let payload = #"{"path":"","parentPath":null,"entries":[{"name":"archive","path":"/share/CACHEDEV2_DATA/archive"},{"name":"upload","path":"/share/CACHEDEV1_DATA/ssd/upload"},{"name":"archive-old","path":"/share/CACHEDEV2_DATA/archive-old"}]}"#
        let listing = try JSONDecoder().decode(DirectoryListing.self, from: Data(payload.utf8))
        let source = sourceDirectoryListing(listing, archiveBase: "/share/archive")
        XCTAssertEqual(source.entries.map(\.name), ["upload", "archive-old"])
        XCTAssertFalse(source.selectable ?? true)
        var ancestor = listing
        ancestor.path = "/share"
        XCTAssertFalse(sourceDirectoryListing(ancestor, archiveBase: "/share/archive").selectable ?? true)
        var nested = listing
        nested.path = "/share/archive/photos"
        XCTAssertFalse(sourceDirectoryListing(nested, archiveBase: "/share/archive").selectable ?? true)
        var broad = listing
        broad.entries = [RemoteDirectory(name: "share", path: "/share")]
        XCTAssertFalse(sourceDirectoryListing(broad, archiveBase: "/share/archive").entries[0].selectable ?? true)
    }

    func testEmptyReadyPreviewOffersRescanInsteadOfArchive() throws {
        var job = try JSONDecoder().decode(ArchiveJob.self, from: Data(jobFixture.utf8))
        job.status = "ready"
        job.totalFiles = 0; job.completedFiles = 0; job.failedFiles = 0
        job.totalBytes = 0; job.completedBytes = 0; job.currentFileBytes = 0
        job.started = false
        XCTAssertFalse(job.canStart)
        XCTAssertTrue(job.canRescan)
        XCTAssertEqual(job.statusLabel, "等待文件")
        job.totalFiles = 1
        XCTAssertTrue(job.canStart)
        XCTAssertTrue(job.canRescan)
        job.started = true
        XCTAssertFalse(job.canRescan)
        job.status = "completed"
        XCTAssertFalse(job.canRescan)
    }

    func testSameNamedRunsUseShortOrdinalsWithoutChangingNames() throws {
        let first = try JSONDecoder().decode(ArchiveJob.self, from: Data(jobFixture.utf8))
        var second = first
        second.id = "second-run"
        second.createdAt = "2026-11-01T00:49:00.123456789+08:00"
        second.totalFiles = 4
        XCTAssertNil(taskOrdinal(first, among: [first]))
        XCTAssertEqual(taskOrdinal(first, among: [second, first]), 1)
        XCTAssertEqual(taskOrdinal(second, among: [second, first]), 2)
        XCTAssertEqual(second.name, first.name)
        XCTAssertNotNil(second.createdDate)
        var unrelated = second
        unrelated.id = "different-name"
        unrelated.name = "Other folder"
        XCTAssertNil(taskOrdinal(unrelated, among: [first, second, unrelated]))
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
        XCTAssertFalse(job.canStart, "Unsupported-only tasks cannot start")
        job.unsupportedFiles = 0
        job.unsupportedBytes = 0
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
            XCTAssertEqual(error.localizedDescription, "连接密码不正确，请重新输入。")
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
        client?.urlProtocol(self, didLoad: Data(#"{"error":{"code":"unauthorized","message":"权杖已失效"}}"#.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

private let jobFixture = #"""
{"id":"job-test","name":"Family","sourcePaths":["/share/upload"],"archiveBase":"/share/archive","cleanupEmptyDirectories":true,"status":"running","createdAt":"2026-10-31T00:00:00Z","updatedAt":"2026-10-31T00:00:01Z","totalFiles":2,"completedFiles":1,"failedFiles":0,"attentionFiles":0,"totalBytes":1000,"completedBytes":500,"currentFile":"large.mov","currentFileBytes":250,"dateRangeStart":"2026-01-01T00:00:00Z","dateRangeEnd":"2026-02-01T00:00:00Z","error":"","cleanupStatus":"pending","residualPaths":[]}
"""#
