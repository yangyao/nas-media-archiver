import Foundation

struct NASConnection: Codable, Identifiable, Equatable, Sendable {
    var id: UUID = UUID()
    var name: String
    var address: String
}

struct ServerInfo: Codable, Sendable {
    var capabilities: [String: Bool]?
    var configured: Bool?
    var isConfigured: Bool { configured ?? !archiveBase.isEmpty }
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
    var selectable: Bool?
    var id: String { path }
}

struct DirectoryListing: Codable, Sendable {
    var path: String
    var parentPath: String?
    var entries: [RemoteDirectory]
    var selectable: Bool?
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
    var started: Bool?
    var totalBytes: Int64
    var completedBytes: Int64
    var currentFile: String
    var currentFileBytes: Int64
    var dateRangeStart: String
    var dateRangeEnd: String
    var error: String
    var cleanupStatus: String
    var residualPaths: [String]

    var createdDate: Date? {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter.date(from: createdAt) ?? ISO8601DateFormatter().date(from: createdAt)
    }
    var creationDescription: String {
        guard let date = createdDate else { return createdAt }
        return date.formatted(date: .abbreviated, time: .standard)
    }
    var isTerminal: Bool { ["completed", "completed_with_errors", "failed"].contains(status) }
    var needsRescan: Bool { status == "interrupted" && scanComplete == false }
    var supportedFiles: Int { max(0, totalFiles - (unsupportedFiles ?? 0)) }
    var supportedBytes: Int64 { max(0, totalBytes - (unsupportedBytes ?? 0)) }
    var canStart: Bool { ["ready", "paused", "interrupted"].contains(status) && !needsRescan && supportedFiles > processedFiles + failedFiles }
    var canRescan: Bool { status == "ready" && !(started ?? (processedFiles > 0)) }
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
        case "scanning": "正在扫描"
        case "ready": supportedFiles == 0 ? "等待文件" : "等待开始"
        case "queued": "排队中"
        case "running": "正在归档"
        case "pausing": "正在暂停"
        case "paused": "已暂停"
        case "completed": "归档完成"
        case "completed_with_errors": "部分完成"
        case "failed": "任务失败"
        case "interrupted": "等待恢复"
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
        if status == "failed" { return "归档失败" }
        if status == "unsupported" { return "不支持的格式" }
        if needsDateReview { return "使用修改日期" }
        if warning.localizedCaseInsensitiveContains("suffix") { return "同名文件另存" }
        return warning.isEmpty ? nil : "请确认归档信息"
    }
    var reviewAdvice: String? {
        if status == "failed" { return "来源已保留。确认错误原因后，重试此任务的失败文件。" }
        if status == "unsupported" && name.lowercased().hasSuffix(".gif") { return "此为旧任务的扫描结果；新版已支持 GIF，仍在来源的文件可建立新任务重新扫描。" }
        if status == "unsupported" { return "此格式不会自动归档。可查看来源，另行保存或转换格式。" }
        if needsDateReview { return "未找到拍摄日期，使用了文件修改日期。日期正确可直接确认；尚未归档时也能设置日期。" }
        if warning.localizedCaseInsensitiveContains("suffix") { return "原有文件不会覆盖。不同内容以后缀另存；相同内容可启用跳过重复。" }
        return warning.isEmpty ? nil : warning
    }
    var metadataLabel: String {
        switch metadataSource {
        case "filename": "档名"
        case "native-exif", "exiftool": "照片信息"
        case "ffprobe": "视频信息"
        case "mtime": "修改日期"
        case "manual": "手动设置"
        default: metadataSource
        }
    }
    var statusLabel: String {
        switch status {
        case "discovered": "待扫描"
        case "planned": "待归档"
        case "copying": "复制并校验"
        case "checking": "比对重复"
        case "skipped": "重复，跳过"
        case "completed": "已归档"
        case "failed": "失败"
        case "unsupported": "不支持"
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
        switch self { case .all: "全部文件"; case .attention: "需要关注"; case .failed: "失败"; case .skipped: "跳过重复" }
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
        if system { return "系统目录，保留即可" }
        if symlink { return "符号连结，不会自动处理" }
        if path.contains("/.stfolder/") { return "Syncthing 同步标记，保留即可" }
        if isDirectory { return "文件夹" }
        return "保留在服务端；可复制路径另行处理"
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

// A short visual identifier distinguishes repeated runs without lengthening the
// folder name. Actual job IDs remain the identity for all remote operations.
func taskOrdinal(_ job: ArchiveJob, among jobs: [ArchiveJob]) -> Int? {
    let matching = jobs.filter { $0.name == job.name }
    guard matching.count > 1 else { return nil }
    let matches = matching.sorted {
        let left = $0.createdDate ?? .distantPast
        let right = $1.createdDate ?? .distantPast
        return left == right ? $0.id < $1.id : left < right
    }
    guard let index = matches.firstIndex(where: { $0.id == job.id }) else { return nil }
    return index + 1
}

// Also filters responses from older services that ignore the source-purpose
// query. Canonical QNAP paths and share aliases use the same display form.
func sourceDirectoryListing(_ listing: DirectoryListing, archiveBase: String) -> DirectoryListing {
    func contains(_ parent: String, _ child: String) -> Bool {
        let a = (displayRemotePath(parent) as NSString).standardizingPath
        let b = (displayRemotePath(child) as NSString).standardizingPath
        return a == b || b.hasPrefix(a == "/" ? a : a + "/")
    }
    guard !archiveBase.isEmpty else { return listing }
    var result = listing
    result.entries = listing.entries.filter { !contains(archiveBase, $0.path) }.map {
        var entry = $0
        entry.selectable = ($0.selectable ?? true) && !contains($0.path, archiveBase)
        return entry
    }
    result.selectable = !listing.path.isEmpty && (listing.selectable ?? true) && !contains(archiveBase, listing.path) && !contains(listing.path, archiveBase)
    return result
}
