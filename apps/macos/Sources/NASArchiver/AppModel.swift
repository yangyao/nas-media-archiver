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
    @Published var showDirectorySplit = false
    @Published var showArchiveSettings = false
    var canCreateArchive: Bool { isConnected && serverInfo?.isConfigured == true }

    private let defaults: UserDefaults
    private let session: URLSession
    private var client: APIClient?
    private var connectionTask: Task<Void, Never>?
    private var updatesTask: Task<Void, Never>?
    private var isStreaming = false
    private let pollingMessage = "即时连接正在重连，每 3 秒更新任务。"
    private var selectedJobForConnection: [UUID: String] = [:]
    private var offeredArchiveOnboarding: Set<UUID> = []
    private var offeredSettingsOnboarding: Set<UUID> = []

    var selectedConnection: NASConnection? { connections.first { $0.id == selectedConnectionID } }
    var selectedJob: ArchiveJob? { jobs.first { $0.id == selectedJobID } }
    var needsArchiveOnboarding: Bool { canCreateArchive && jobs.isEmpty }
    var connectionLabel: String { isConnecting ? "正在连接" : (isConnected ? "已连接" : "未连接") }

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
                guard !token.isEmpty else { throw ServiceError(message: "请在目前服务端设置中填写连接密码。") }
                let api = try APIClient(address: connection.address, token: token, session: session)
                let info = try await api.info()
                let values = try await api.jobs()
                try Task.checkCancellation()
                guard selectedConnectionID == connection.id else { return }
                client = api; serverInfo = info; jobs = values
                isConnected = true; isConnecting = false
                startUpdates(api, connectionID: connection.id)
                presentArchiveOnboardingIfNeeded()
            } catch is CancellationError {} catch {
                guard !Task.isCancelled, selectedConnectionID == connection.id else { return }
                isConnected = false; errorMessage = presentableError(error); isConnecting = false
            }
        }
    }

    func saveConnection(_ connection: NASConnection, token: String) async throws {
        _ = try APIClient(address: connection.address, token: token, session: session)
        guard !token.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { throw ServiceError(message: "请填写服务端生成的连接密码。") }
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
            throw ServiceError(message: errorMessage ?? "无法连接，请检查服务地址与密码。")
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
            showNewArchive = false
            if let first = next.first { connect(first) }
        }
    }

    func api() throws -> APIClient {
        guard let client else { throw ServiceError(message: "请先连接服务端。") }
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
            presentArchiveOnboardingIfNeeded()
        } catch {
            if selectedConnectionID == connectionID {
                isConnected = false
                errorMessage = presentableError(error)
            }
        }
    }

    func presentArchiveOnboardingIfNeeded() {
        if isConnected && serverInfo?.isConfigured == false {
            if connectionEditor == nil, let id = selectedConnectionID, !offeredSettingsOnboarding.contains(id) {
                offeredSettingsOnboarding.insert(id)
                showArchiveSettings = true
            }
            return
        }
        guard !showArchiveSettings else { return }
        // A connection editor may still be finishing its dismissal transition.
        // ContentView calls again from onDismiss, so two sheets never compete.
        guard needsArchiveOnboarding, connectionEditor == nil, !showNewArchive,
              let id = selectedConnectionID, !offeredArchiveOnboarding.contains(id) else { return }
        offeredArchiveOnboarding.insert(id)
        showNewArchive = true
    }

    func createJob(paths: [String], archiveBase: String, cleanup: Bool, skipDuplicates: Bool = true) async throws {
        let api = try api()
        let connectionID = selectedConnectionID
        let job = try await api.create(.init(sourcePaths: paths.sorted(), archiveBase: archiveBase, cleanupEmptyDirectories: cleanup, skipDuplicates: skipDuplicates))
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

    func setSkipDuplicates(_ enabled: Bool, job: ArchiveJob) async {
        guard !isPerformingAction else { return }
        isPerformingAction = true
        let connectionID = selectedConnectionID
        defer { isPerformingAction = false }
        do {
            let updated = try await api().setSkipDuplicates(jobID: job.id, enabled: enabled)
            guard connectionID == selectedConnectionID else { return }
            upsert(updated)
        } catch { if connectionID == selectedConnectionID { errorMessage = presentableError(error) } }
    }

    func reviewFile(_ file: ArchiveFile, job: ArchiveJob, dateTime: String? = nil) async throws {
        guard !isPerformingAction else { throw ServiceError(message: "请等待目前操作完成。") }
        isPerformingAction = true
        let connectionID = selectedConnectionID
        defer { isPerformingAction = false }
        let updated = try await api().reviewFile(jobID: job.id, fileID: file.id, dateTime: dateTime)
        guard connectionID == selectedConnectionID else { return }
        upsert(updated)
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
                    // Losing SSE does not invalidate the authenticated HTTP client.
                    // The fallback poll determines whether the server is reachable.
                    isStreaming = false
                    updatesMessage = "即时连接暂时中断，正在重新连接。\n\(presentableError(error))"
                }
                for _ in 0..<5 {
                    do {
                        try await Task.sleep(for: .seconds(3))
                        let values = try await api.jobs()
                        guard selectedConnectionID == connectionID else { return }
                        replaceJobs(values); isConnected = true
                        updatesMessage = pollingMessage
                        presentArchiveOnboardingIfNeeded()
                    } catch is CancellationError { return } catch {
                        guard !Task.isCancelled, selectedConnectionID == connectionID else { return }
                        isConnected = false
                        updatesMessage = "服务端暂时无法访问，正在重连。任务仍由服务端执行。"
                    }
                }
            }
        }
    }

    private func streamDidConnect(connectionID: UUID) {
        guard !Task.isCancelled, selectedConnectionID == connectionID else { return }
        isStreaming = true; isConnected = true; updatesMessage = nil
        presentArchiveOnboardingIfNeeded()
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
        content.title = job.failedFiles == 0 ? "归档完成" : "归档完成，有文件需要关注"
        content.body = "\(job.name)：已归档 \(job.completedFiles)，跳过重复 \(job.skippedFiles ?? 0)。"
        let request = UNNotificationRequest(identifier: job.id, content: content, trigger: nil)
        Task { try? await UNUserNotificationCenter.current().add(request) }
    }
}

struct ConnectionEditorTarget: Identifiable {
    var connection: NASConnection?
    var id: String { connection?.id.uuidString ?? "new" }
}
