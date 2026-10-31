import Foundation

struct NASConnection: Codable, Identifiable, Equatable, Sendable {
    var id: UUID = UUID()
    var name: String
    var address: String
}

struct ServerInfo: Codable, Sendable {
    var name: String
    var version: String
    var archiveBase: String
    var roots: [String]
    var freeBytes: Int64
    var tools: [String: Bool]
}

struct RemoteDirectory: Codable, Identifiable, Hashable, Sendable {
    var name: String
    var path: String
    var id: String { path }
}

struct DirectoryListing: Codable, Sendable {
    var path: String
    var parentPath: String?
    var entries: [RemoteDirectory]
}

struct ArchiveJob: Codable, Identifiable, Equatable, Sendable {
    var id: String
    var name: String
    var sourcePaths: [String]
    var archiveBase: String
    var cleanupEmptyDirectories: Bool
    var skipDuplicates: Bool?
    var skippedFiles: Int?
    var skippedBytes: Int64?
    var status: String
    var createdAt: String
    var updatedAt: String
    var totalFiles: Int
    var completedFiles: Int
    var failedFiles: Int
    var attentionFiles: Int
    var unsupportedFiles: Int?
    var unsupportedBytes: Int64?
    var scanComplete: Bool?
    var totalBytes: Int64
    var completedBytes: Int64
    var currentFile: String
    var currentFileBytes: Int64
    var dateRangeStart: String
    var dateRangeEnd: String
    var error: String
    var cleanupStatus: String
    var residualPaths: [String]

    var isTerminal: Bool { ["completed", "completed_with_errors", "failed"].contains(status) }
    var needsRescan: Bool { status == "interrupted" && scanComplete == false }
    var supportedFiles: Int { max(0, totalFiles - (unsupportedFiles ?? 0)) }
    var supportedBytes: Int64 { max(0, totalBytes - (unsupportedBytes ?? 0)) }
    var canStart: Bool { ["ready", "paused", "interrupted"].contains(status) && !needsRescan }
    var processedFiles: Int { completedFiles + (skippedFiles ?? 0) }
    var processedBytes: Int64 { completedBytes + (skippedBytes ?? 0) }
    var canPause: Bool { ["running", "queued"].contains(status) }
    var progress: Double {
        guard supportedBytes > 0 else {
            if supportedFiles == 0 { return isTerminal ? 1 : 0 }
            return min(1, Double(processedFiles) / Double(supportedFiles))
        }
        return min(1, max(0, (Double(processedBytes) + Double(currentFileBytes)) / Double(supportedBytes)))
    }
    var statusLabel: String {
        switch status {
        case "scanning": "正在掃描"
        case "ready": "等待開始"
        case "queued": "排隊中"
        case "running": "正在歸檔"
        case "pausing": "正在暫停"
        case "paused": "已暫停"
        case "completed": "歸檔完成"
        case "completed_with_errors": "部分完成"
        case "failed": "任務失敗"
        case "interrupted": "等待恢復"
        default: status
        }
    }
    var dateRangeLabel: String? {
        guard !dateRangeStart.isEmpty, !dateRangeEnd.isEmpty else { return nil }
        return "\(String(dateRangeStart.prefix(10))) — \(String(dateRangeEnd.prefix(10)))"
    }
}

struct ArchiveFile: Codable, Identifiable, Sendable {
    var id: String
    var sourcePath: String
    var targetPath: String
    var size: Int64
    var status: String
    var metadataSource: String
    var dateTime: String
    var warning: String
    var error: String
    var sha256: String

    var name: String { (sourcePath as NSString).lastPathComponent }
    var statusLabel: String {
        switch status {
        case "discovered": "待掃描"
        case "planned": "待歸檔"
        case "copying": "複製並校驗"
        case "checking": "比對重複"
        case "skipped": "重複，略過"
        case "completed": "已歸檔"
        case "failed": "失敗"
        case "unsupported": "不支援"
        default: status
        }
    }
}

struct FilesPage: Codable, Sendable { var files: [ArchiveFile]; var total: Int }
struct CreateJobRequest: Codable, Sendable {
    var sourcePaths: [String]
    var archiveBase: String
    var cleanupEmptyDirectories: Bool
    var skipDuplicates: Bool = true
}

enum FileFilter: String, CaseIterable, Identifiable {
    case all, attention, failed
    var id: String { rawValue }
    var title: String {
        switch self { case .all: "全部檔案"; case .attention: "需要關注"; case .failed: "失敗檔案" }
    }
}

func formattedBytes(_ bytes: Int64) -> String {
    ByteCountFormatter.string(fromByteCount: bytes, countStyle: .binary)
}
