import Foundation

struct ServiceError: LocalizedError, Sendable {
    var message: String
    var errorDescription: String? { message }
}

struct APIClient: Sendable {
    let baseURL: URL
    let token: String
    let session: URLSession

    init(address: String, token: String, session: URLSession = .shared) throws {
        guard let url = URL(string: address.trimmingCharacters(in: .whitespacesAndNewlines)),
              ["http", "https"].contains(url.scheme?.lowercased() ?? ""),
              url.host != nil, url.user == nil, url.password == nil,
              url.query == nil, url.fragment == nil else {
            throw ServiceError(message: "请输入完整的 归档服务端地址，例如 http://192.168.2.196:8787。")
        }
        self.baseURL = url
        self.token = token
        self.session = session
    }

    func request(_ path: String, method: String = "GET", query: [URLQueryItem] = [], body: Data? = nil) throws -> URLRequest {
        var components = URLComponents(url: baseURL.appendingPathComponent("api/v1/" + path), resolvingAgainstBaseURL: false)!
        if !query.isEmpty { components.queryItems = query }
        guard let url = components.url else { throw ServiceError(message: "无法生成请求地址。") }
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        request.timeoutInterval = 20
        if let body { request.httpBody = body; request.setValue("application/json", forHTTPHeaderField: "Content-Type") }
        return request
    }

    private func check(_ response: URLResponse, data: Data? = nil) throws {
        guard let response = response as? HTTPURLResponse else { throw ServiceError(message: "服务端返回了无法识别的响应。") }
        guard (200..<300).contains(response.statusCode) else {
            if response.statusCode == 401 { throw ServiceError(message: "连接密码不正确，请重新输入。") }
            struct Envelope: Decodable { struct Detail: Decodable { var message: String; var code: String? }; var error: Detail }
            if let data, let error = try? JSONDecoder().decode(Envelope.self, from: data) {
                if error.error.code == "invalid_source" { throw ServiceError(message: "归档目录不能作为来源，请选择其他文件夹。") }
                if error.error.code == "unreadable" { throw ServiceError(message: "无法读取目录，请检查服务端的访问权限。") }
                throw ServiceError(message: error.error.message)
            }
            throw ServiceError(message: "服务端请求失败（HTTP \(response.statusCode)）。")
        }
    }

    func fetch<T: Decodable & Sendable>(_ type: T.Type, path: String, method: String = "GET", query: [URLQueryItem] = [], body: Data? = nil) async throws -> T {
        let (data, response) = try await session.data(for: request(path, method: method, query: query, body: body))
        try check(response, data: data)
        return try JSONDecoder().decode(type, from: data)
    }

    func archiveSettings() async throws -> ArchiveSettings { try await fetch(ArchiveSettings.self, path: "settings") }
    func saveArchiveSettings(_ value: ArchiveSettings) async throws -> ArchiveSettings {
        try await fetch(ArchiveSettings.self, path: "settings", method: "PUT", body: JSONEncoder().encode(value))
    }
    func previewSplit(source: String) async throws -> DirectorySplitJob {
        try await fetch(DirectorySplitJob.self, path: "tools/splits/preview", method: "POST", body: JSONEncoder().encode(["sourcePath": source]))
    }
    func startSplit(_ id: String) async throws -> DirectorySplitJob { try await fetch(DirectorySplitJob.self, path: "tools/splits/\(id)/start", method: "POST") }
    func splitJob(_ id: String) async throws -> DirectorySplitJob { try await fetch(DirectorySplitJob.self, path: "tools/splits/\(id)") }
    func splitJobs() async throws -> [DirectorySplitJob] { try await fetch([DirectorySplitJob].self, path: "tools/splits") }
    func info() async throws -> ServerInfo { try await fetch(ServerInfo.self, path: "info") }
    func deleteJob(_ id: String) async throws {
        struct Deleted: Decodable, Sendable { var id: String; var status: String }
        _ = try await fetch(Deleted.self, path: "jobs/\(id)", method: "DELETE")
    }
    func jobs() async throws -> [ArchiveJob] { try await fetch([ArchiveJob].self, path: "jobs") }
    func browse(_ path: String) async throws -> DirectoryListing { try await fetch(DirectoryListing.self, path: "browse", query: [.init(name: "path", value: path)]) }
    func browseSources(_ path: String, archiveBase: String) async throws -> DirectoryListing {
        let result = try await fetch(DirectoryListing.self, path: "browse", query: [.init(name: "path", value: path), .init(name: "purpose", value: "source"), .init(name: "archiveBase", value: archiveBase)])
        return sourceDirectoryListing(result, archiveBase: archiveBase)
    }
    func create(_ value: CreateJobRequest) async throws -> ArchiveJob {
        try await fetch(ArchiveJob.self, path: "jobs", method: "POST", body: JSONEncoder().encode(value))
    }
    func action(_ action: String, jobID: String) async throws -> ArchiveJob {
        try await fetch(ArchiveJob.self, path: "jobs/\(jobID)/\(action)", method: "POST")
    }
    func setSkipDuplicates(jobID: String, enabled: Bool) async throws -> ArchiveJob {
        try await fetch(ArchiveJob.self, path: "jobs/\(jobID)/skip-duplicates", method: "POST", body: JSONEncoder().encode(["enabled": enabled]))
    }
    func contents(_ path: String) async throws -> RemoteContents {
        try await fetch(RemoteContents.self, path: "browse", query: [.init(name: "path", value: path), .init(name: "includeFiles", value: "true")])
    }
    func reviewFile(jobID: String, fileID: String, dateTime: String? = nil) async throws -> ArchiveJob {
        let action = dateTime == nil ? "acknowledge" : "date"
        let body = try dateTime.map { try JSONEncoder().encode(["dateTime": $0]) }
        return try await fetch(ArchiveJob.self, path: "jobs/\(jobID)/files/\(fileID)/\(action)", method: "POST", body: body)
    }
    func files(jobID: String, filter: FileFilter, offset: Int = 0) async throws -> FilesPage {
        try await fetch(FilesPage.self, path: "jobs/\(jobID)/files", query: [.init(name: "filter", value: filter.rawValue), .init(name: "offset", value: "\(offset)"), .init(name: "limit", value: "200")])
    }

    func events(onConnected: @escaping @Sendable () async -> Void = {}) -> AsyncThrowingStream<ArchiveJob, Error> {
        AsyncThrowingStream { continuation in
            let task = Task {
                do {
                    var request = try request("events")
                    request.setValue("text/event-stream", forHTTPHeaderField: "Accept")
                    request.timeoutInterval = 86_400
                    let (bytes, response) = try await session.bytes(for: request)
                    try check(response)
                    await onConnected()
                    var parser = JobEventParser()
                    for try await byte in bytes {
                        try Task.checkCancellation()
                        if let job = try parser.consume(byte) { continuation.yield(job) }
                    }
                    throw ServiceError(message: "即时进度连接已断开。")
                } catch { continuation.finish(throwing: error) }
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }
}

// URLSession.AsyncBytes.lines omits empty lines on some Foundation versions.
// SSE framing requires those blank lines, so preserve LF/CRLF at the byte level.
struct JobEventParser: Sendable {
    private var lineBytes: [UInt8] = []
    private var eventName = ""
    private var dataLines: [String] = []
    private var eventBytes = 0

    mutating func consume(_ byte: UInt8) throws -> ArchiveJob? {
        guard byte == 10 else {
            guard lineBytes.count < 1_048_576 else { throw ServiceError(message: "服务端即时更新超出允许大小。") }
            lineBytes.append(byte)
            return nil
        }
        if lineBytes.last == 13 { lineBytes.removeLast() }
        let line = String(decoding: lineBytes, as: UTF8.self)
        lineBytes.removeAll(keepingCapacity: true)
        if line.isEmpty {
            defer { eventName = ""; dataLines.removeAll(keepingCapacity: true); eventBytes = 0 }
            guard eventName == "job", !dataLines.isEmpty else { return nil }
            return try JSONDecoder().decode(ArchiveJob.self, from: Data(dataLines.joined(separator: "\n").utf8))
        }
        if line.hasPrefix(":") { return nil }
        let parts = line.split(separator: ":", maxSplits: 1, omittingEmptySubsequences: false)
        var value = parts.count > 1 ? String(parts[1]) : ""
        if value.hasPrefix(" ") { value.removeFirst() }
        switch parts[0] {
        case "event": eventName = value
        case "data":
            eventBytes += value.utf8.count
            guard eventBytes <= 1_048_576 else { throw ServiceError(message: "服务端即时更新超出允许大小。") }
            dataLines.append(value)
        default: break
        }
        return nil
    }
}

func presentableError(_ error: Error) -> String {
    if let error = error as? URLError {
        return "无法连接服务，请检查地址和网络（\(error.errorCode)）。"
    }
    if error is DecodingError { return "服务端返回的数据格式与客户端不兼容。\n\(String(reflecting: error))" }
    return error.localizedDescription
}
