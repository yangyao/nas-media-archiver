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
    var duplicateOf: String?

    var name: String { (sourcePath as NSString).lastPathComponent }
    var needsDateReview: Bool { metadataSource == "mtime" && !warning.isEmpty }
    var reviewTitle: String? {
        if status == "failed" { return "歸檔失敗" }
        if status == "unsupported" { return "不支援的格式" }
        if needsDateReview { return "使用修改日期" }
        if warning.localizedCaseInsensitiveContains("suffix") { return "同名檔案另存" }
        return warning.isEmpty ? nil : "請確認歸檔資訊"
    }
    var reviewAdvice: String? {
        if status == "failed" { return "來源已保留。確認錯誤原因後，重試此任務的失敗檔案。" }
        if status == "unsupported" { return "此格式不會自動歸檔。可查看來源，另行保存或轉換格式。" }
        if needsDateReview { return "未找到拍攝日期，使用了檔案修改日期。日期正確可直接確認；尚未歸檔時也能設定日期。" }
        if warning.localizedCaseInsensitiveContains("suffix") { return "原有檔案不會覆蓋。不同內容以後綴另存；相同內容可啟用略過重複。" }
        return warning.isEmpty ? nil : warning
    }
    var metadataLabel: String {
        switch metadataSource {
        case "filename": "檔名"
        case "native-exif", "exiftool": "照片資訊"
        case "ffprobe": "影片資訊"
        case "mtime": "修改日期"
        case "manual": "手動設定"
        default: metadataSource
        }
    }
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
    case all, attention, failed, skipped
    var id: String { rawValue }
    var title: String {
        switch self { case .all: "全部檔案"; case .attention: "需要關注"; case .failed: "失敗"; case .skipped: "略過重複" }
    }
}

func formattedBytes(_ bytes: Int64) -> String {
    ByteCountFormatter.string(fromByteCount: bytes, countStyle: .binary)
}

struct RemoteContentEntry: Decodable, Identifiable, Sendable {
    var name: String
    var path: String
    var isDirectory: Bool
    var size: Int64
    var system: Bool
    var canOpen: Bool
    var symlink: Bool
    var id: String { path }
    var note: String {
        if system { return "系統目錄，保留即可" }
        if symlink { return "符號連結，不會自動處理" }
        if path.contains("/.stfolder/") { return "Syncthing 同步標記，保留即可" }
        if name.lowercased().hasSuffix(".gif") { return "GIF 尚未支援自動歸檔，可另行保存或轉換格式" }
        if isDirectory { return "資料夾" }
        return "保留在服務端；可複製路徑另行處理"
    }
}
struct RemoteContents: Decodable, Sendable {
    var path: String
    var parentPath: String?
    var entries: [RemoteContentEntry]
}

func displayRemotePath(_ path: String) -> String {
    path.replacingOccurrences(of: #"^/share/CACHEDEV[0-9]+_DATA/"#, with: "/share/", options: .regularExpression)
}
