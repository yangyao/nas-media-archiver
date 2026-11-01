import Foundation
import XCTest
@testable import NASArchiver

final class SplitDirectoryTests: XCTestCase {
    func testSplitPreviewUsesSiblingBatchesEvenBelowLimit() throws {
        let data = Data(#"{"id":"split-test","status":"preview","sourcePath":"/share/ssd/upload/iphone15","prefix":"iphone15","maxBytes":8000000000,"totalFiles":2,"totalBytes":42,"completedFiles":0,"completedBytes":0,"retainedEntries":1,"batches":[{"path":"/share/ssd/upload/iphone15_batch_01","files":2,"bytes":42,"oversized":false}],"error":""}"#.utf8)
        let value = try JSONDecoder().decode(DirectorySplitJob.self, from: data)
        XCTAssertTrue(value.canStart)
        XCTAssertFalse(value.isRunning)
        XCTAssertEqual(value.batches[0].path, "/share/ssd/upload/iphone15_batch_01")
        XCTAssertEqual((value.batches[0].path as NSString).deletingLastPathComponent, (value.sourcePath as NSString).deletingLastPathComponent)
        var empty = value
        empty.totalFiles = 0
        XCTAssertFalse(empty.canStart)
        var running = value
        running.status = "running"
        XCTAssertTrue(running.isRunning)
        XCTAssertFalse(running.canStart)
        running.status = "interrupted"
        XCTAssertTrue(running.canStart)
    }
}
