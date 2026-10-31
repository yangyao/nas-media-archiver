import AppKit
import Combine
import Foundation
import UserNotifications

@MainActor
final class AppModel: ObservableObject {
    @Published var connections: [NASConnection] = []
    @Published var selectedConnectionID: UUID?
    @Published var serverInfo: ServerInfo?
    @Published var jobs: [ArchiveJob] = []
    @Published var selectedJobID: String?
    @Published var isConnecting = false
    @Published var isConnected = false
    @Published var isPerformingAction = false
    @Published var errorMessage: String?
    @Published var updatesMessage: String?
    @Published var connectionEditor: ConnectionEditorTarget?
    @Published var showNewArchive = false

    private let defaults: UserDefaults
    private let session: URLSession
    private var client: APIClient?
    private var connectionTask: Task<Void, Never>?
    private var updatesTask: Task<Void, Never>?
    private var isStreaming = false
    private let pollingMessage = "即時連線正在重連，每 3 秒更新任務。"
    private var selectedJobForConnection: [UUID: String] = [:]

    var selectedConnection: NASConnection? { connections.first { $0.id == selectedConnectionID } }
    var selectedJob: ArchiveJob? { jobs.first { $0.id == selectedJobID } }
    var connectionLabel: String { isConnecting ? "正在連線" : (isConnected ? "已連線" : "未連線") }

    init(defaults: UserDefaults = .standard, session: URLSession = .shared) {
        self.defaults = defaults
        self.session = session
        if let data = defaults.data(forKey: "nasConnections"),
           let values = try? JSONDecoder().decode([NASConnection].self, from: data) { connections = values }
        let storedID = defaults.string(forKey: "selectedNAS").flatMap(UUID.init(uuidString:))
        if let connection = connections.first(where: { $0.id == storedID }) ?? connections.first { connect(connection) }
    }

    func connect(_ connection: NASConnection) {
        if let selectedConnectionID, let selectedJobID { selectedJobForConnection[selectedConnectionID] = selectedJobID }
        connectionTask?.cancel(); updatesTask?.cancel()
        selectedConnectionID = connection.id
        defaults.set(connection.id.uuidString, forKey: "selectedNAS")
        selectedJobID = selectedJobForConnection[connection.id]
        serverInfo = nil; jobs = []; isConnected = false; isConnecting = true
        client = nil; isStreaming = false; updatesMessage = nil; errorMessage = nil
        connectionTask = Task {
            do {
                let token = try TokenStore.token(for: connection.id)
                guard !token.isEmpty else { throw ServiceError(message: "請在目前服務端設定中填寫連線密碼。") }
                let api = try APIClient(address: connection.address, token: token, session: session)
                let info = try await api.info()
                let values = try await api.jobs()
                try Task.checkCancellation()
                guard selectedConnectionID == connection.id else { return }
                client = api; serverInfo = info; jobs = values
                isConnecting = false; isConnected = true
                startUpdates(api, connectionID: connection.id)
            } catch is CancellationError {} catch {
                guard !Task.isCancelled, selectedConnectionID == connection.id else { return }
                isConnecting = false; isConnected = false; errorMessage = presentableError(error)
            }
        }
    }

    func saveConnection(_ connection: NASConnection, token: String) async throws {
        _ = try APIClient(address: connection.address, token: token, session: session)
        guard !token.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { throw ServiceError(message: "請填寫服務端生成的連線密碼。") }
        try TokenStore.save(token.trimmingCharacters(in: .whitespacesAndNewlines), for: connection.id)
        var next = connections
        if let index = next.firstIndex(where: { $0.id == connection.id }) { next[index] = connection }
        else { next.append(connection) }
        let encoded = try JSONEncoder().encode(next)
        defaults.set(encoded, forKey: "nasConnections")
        connections = next
        connect(connection)
        let attempt = connectionTask
        await attempt?.value
        try Task.checkCancellation()
        guard selectedConnectionID == connection.id, isConnected else {
            throw ServiceError(message: errorMessage ?? "無法連線，請檢查服務地址與密碼。")
        }
    }

    func removeConnection(_ connection: NASConnection) throws {
        try TokenStore.remove(connection.id)
        let next = connections.filter { $0.id != connection.id }
        defaults.set(try JSONEncoder().encode(next), forKey: "nasConnections")
        connections = next
        if selectedConnectionID == connection.id {
            connectionTask?.cancel(); updatesTask?.cancel(); client = nil
            jobs = []; serverInfo = nil; selectedJobID = nil; isConnected = false; isConnecting = false; selectedConnectionID = nil
            if let first = next.first { connect(first) }
        }
    }

    func api() throws -> APIClient {
        guard let client, isConnected else { throw ServiceError(message: "請先連線服務端。") }
        return client
    }

    func refresh() async {
        guard let api = client, let connectionID = selectedConnectionID else {
            if let connection = selectedConnection { connect(connection) }
            return
        }
        do {
            let info = try await api.info()
            let values = try await api.jobs()
            guard selectedConnectionID == connectionID else { return }
            serverInfo = info; replaceJobs(values); isConnected = true
            errorMessage = nil
            updatesMessage = isStreaming ? nil : pollingMessage
        } catch {
            if selectedConnectionID == connectionID {
                isConnected = false
                errorMessage = presentableError(error)
            }
        }
    }

    func createJob(paths: [String], archiveBase: String, cleanup: Bool) async throws {
        let api = try api()
        let connectionID = selectedConnectionID
        let job = try await api.create(.init(sourcePaths: paths.sorted(), archiveBase: archiveBase, cleanupEmptyDirectories: cleanup))
        guard connectionID == selectedConnectionID else { return }
        upsert(job); selectedJobID = job.id
    }

    func perform(_ action: String, job: ArchiveJob) async {
        guard !isPerformingAction else { return }
        isPerformingAction = true
        let connectionID = selectedConnectionID
        defer { isPerformingAction = false }
        do {
            let job = try await api().action(action, jobID: job.id)
            guard connectionID == selectedConnectionID else { return }
            upsert(job)
        } catch { if connectionID == selectedConnectionID { errorMessage = presentableError(error) } }
    }

    private func startUpdates(_ api: APIClient, connectionID: UUID) {
        updatesTask = Task {
            while !Task.isCancelled && selectedConnectionID == connectionID {
                do {
                    for try await job in api.events(onConnected: { [weak self = self] in
                        await self?.streamDidConnect(connectionID: connectionID)
                    }) {
                        guard !Task.isCancelled, selectedConnectionID == connectionID else { return }
                        updatesMessage = nil; isConnected = true; upsert(job)
                    }
                } catch is CancellationError { return } catch {
                    guard !Task.isCancelled, selectedConnectionID == connectionID else { return }
                    isStreaming = false; isConnected = false
                    updatesMessage = "即時連線暫時中斷，正在重新連線。\n\(presentableError(error))"
                }
                for _ in 0..<5 {
                    do {
                        try await Task.sleep(for: .seconds(3))
                        let values = try await api.jobs()
                        guard selectedConnectionID == connectionID else { return }
                        replaceJobs(values); isConnected = true
                        updatesMessage = pollingMessage
                    } catch is CancellationError { return } catch {
                        guard !Task.isCancelled, selectedConnectionID == connectionID else { return }
                        isConnected = false
                        updatesMessage = "服務端暫時無法存取，正在重連。任務仍由服務端執行。"
                    }
                }
            }
        }
    }

    private func streamDidConnect(connectionID: UUID) {
        guard !Task.isCancelled, selectedConnectionID == connectionID else { return }
        isStreaming = true; isConnected = true; updatesMessage = nil
    }

    private func replaceJobs(_ values: [ArchiveJob]) {
        for job in values { notifyIfCompleted(job, previous: jobs.first { $0.id == job.id }) }
        jobs = values.sorted { $0.createdAt > $1.createdAt }
    }

    private func upsert(_ job: ArchiveJob) {
        if let index = jobs.firstIndex(where: { $0.id == job.id }) {
            notifyIfCompleted(job, previous: jobs[index]); jobs[index] = job
        } else { jobs.insert(job, at: 0) }
        jobs.sort { $0.createdAt > $1.createdAt }
    }

    private func notifyIfCompleted(_ job: ArchiveJob, previous: ArchiveJob?) {
        guard defaults.bool(forKey: "completionNotifications"), let previous,
              !previous.isTerminal, job.isTerminal else { return }
        let content = UNMutableNotificationContent()
        content.title = job.failedFiles == 0 ? "歸檔完成" : "歸檔完成，有檔案需要關注"
        content.body = "\(job.name)：\(job.completedFiles) 個檔案已歸檔。"
        let request = UNNotificationRequest(identifier: job.id, content: content, trigger: nil)
        Task { try? await UNUserNotificationCenter.current().add(request) }
    }
}

struct ConnectionEditorTarget: Identifiable {
    var connection: NASConnection?
    var id: String { connection?.id.uuidString ?? "new" }
}
