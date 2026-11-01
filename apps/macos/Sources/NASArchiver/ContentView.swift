import SwiftUI

struct ContentView: View {
    @EnvironmentObject private var model: AppModel

    private var activeJobs: [ArchiveJob] { model.jobs.filter { !$0.isTerminal } }
    private var historyJobs: [ArchiveJob] { model.jobs.filter { $0.isTerminal } }

    var body: some View {
        NavigationSplitView {
            List(selection: $model.selectedJobID) {
                if !activeJobs.isEmpty {
                    Section("正在处理") {
                        ForEach(activeJobs) { job in JobRow(job: job).tag(job.id) }
                    }
                }
                if !historyJobs.isEmpty {
                    Section("历史记录") {
                        ForEach(historyJobs) { job in JobRow(job: job).tag(job.id) }
                    }
                }
            }
            .listStyle(.sidebar)
            .navigationSplitViewColumnWidth(min: 240, ideal: 280, max: 340)
            .safeAreaInset(edge: .bottom, spacing: 0) { connectionFooter }
        } detail: {
            VStack(spacing: 0) {
                if let message = model.updatesMessage {
                    StatusBanner(message: message, systemImage: "arrow.triangle.2.circlepath", isError: false)
                }
                if let message = model.errorMessage {
                    StatusBanner(message: message, systemImage: "exclamationmark.triangle", isError: true) { model.errorMessage = nil }
                }
                if let job = model.selectedJob {
                    JobDetailView(job: job).id(job.id)
                } else if model.selectedConnection == nil {
                    Button("连接服务", systemImage: "link") { editConnection() }
                        .buttonStyle(.glassProminent)
                        .controlSize(.large)
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else {
                    Color.clear.frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
        }
        .toolbar {
            ToolbarItem(placement: .automatic) {
                Button("新建归档", systemImage: "plus") { model.showNewArchive = true }
                    .labelStyle(.iconOnly)
                    .help("新建归档（⌘N）")
                    .disabled(!model.isConnected)
            }
        }
        .toolbar(removing: .title)
        .sheet(item: $model.connectionEditor, onDismiss: model.presentArchiveOnboardingIfNeeded) { target in
            ConnectionEditorView(connection: target.connection).environmentObject(model)
        }
        .sheet(isPresented: $model.showNewArchive) {
            NewArchiveView().environmentObject(model)
        }
        .onAppear {
            if !model.isConnected && !model.isConnecting {
                editConnection()
            } else {
                model.presentArchiveOnboardingIfNeeded()
            }
        }
        .onChange(of: model.isConnecting) { _, connecting in
            if !connecting && !model.isConnected && model.connectionEditor == nil {
                editConnection()
            }
        }
    }

    private var connectionFooter: some View {
        VStack(spacing: 0) {
            Divider()
            Button { editConnection() } label: {
                HStack(spacing: 8) {
                    if model.isConnecting {
                        ProgressView().controlSize(.mini)
                    } else {
                        Circle().fill(model.isConnected ? Color.green : Color.secondary.opacity(0.5))
                            .frame(width: 6, height: 6)
                    }
                    Text(model.selectedConnection == nil ? "设置连接…" : model.connectionLabel)
                        .font(.callout).foregroundStyle(.secondary)
                    Spacer()
                    Image(systemName: "gearshape").foregroundStyle(.secondary)
                }
                .contentShape(Rectangle())
                .padding(.horizontal, 16).padding(.vertical, 13)
            }
            .buttonStyle(.plain)
            .accessibilityLabel("服务连接设置，\(model.connectionLabel)")
            .help("设置归档服务的地址与连接密码")
        }
        .background(.bar)
    }

    private func editConnection() {
        model.connectionEditor = .init(connection: model.selectedConnection)
    }
}

struct JobRow: View {
    @EnvironmentObject private var model: AppModel
    var job: ArchiveJob

    var body: some View {
        HStack(alignment: .center, spacing: 10) {
            Image(systemName: job.isTerminal ? (job.failedFiles > 0 ? "exclamationmark.circle" : "archivebox") : "folder")
                .font(.system(size: 17))
                .foregroundStyle(job.failedFiles > 0 ? Color.orange : Color.secondary)
                .frame(width: 23)
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 6) {
                    Text(job.name).font(.system(size: 13, weight: .medium)).lineLimit(1)
                    if let ordinal = taskOrdinal(job, among: model.jobs) {
                        TaskRunBadge(ordinal: ordinal).help("建立于 \(job.creationDescription)")
                    }
                }
                HStack(spacing: 5) {
                    if taskOrdinal(job, among: model.jobs) != nil {
                        Text("\(job.totalFiles.formatted()) 个 ·")
                    }
                    Text(job.statusLabel)
                }.help("建立于 \(job.creationDescription)")
                .font(.system(size: 11)).foregroundStyle(.secondary)
                if ["running", "pausing"].contains(job.status) {
                    ProgressView(value: job.progress).controlSize(.mini)
                }
            }
            Spacer(minLength: 0)
            if job.canStart {
                Button(job.status == "ready" ? "开始归档" : "继续归档", systemImage: "play.fill") {
                    Task { await model.perform("start", job: job) }
                }
                .labelStyle(.iconOnly).buttonStyle(.borderless)
                .foregroundStyle(.secondary)
                .disabled(!model.isConnected || model.isPerformingAction)
            } else if job.canPause {
                Button("暂停归档", systemImage: "pause.fill") {
                    Task { await model.perform("pause", job: job) }
                }
                .labelStyle(.iconOnly).buttonStyle(.borderless)
                .foregroundStyle(.secondary)
                .disabled(!model.isConnected || model.isPerformingAction)
            }
        }
        .padding(.vertical, 6)
        .help(job.name)
    }
}

struct StatusBanner: View {
    var message: String
    var systemImage: String
    var isError: Bool
    var dismiss: (() -> Void)? = nil
    var body: some View {
        HStack(alignment: .top, spacing: 9) {
            Image(systemName: systemImage).foregroundStyle(isError ? Color.orange : Color.secondary)
            VStack(alignment: .leading, spacing: 7) {
                let parts = message.split(separator: "\n", maxSplits: 1, omittingEmptySubsequences: false)
                Text(String(parts.first ?? "")).font(.callout).textSelection(.enabled)
                if parts.count > 1 {
                    DisclosureGroup("详细信息") {
                        Text(String(parts[1])).font(.caption).textSelection(.enabled).foregroundStyle(.secondary)
                    }.font(.caption)
                }
            }
            Spacer(minLength: 0)
            if let dismiss { Button("关闭", systemImage: "xmark", action: dismiss).labelStyle(.iconOnly).buttonStyle(.plain) }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(isError ? Color.orange.opacity(0.08) : Color.secondary.opacity(0.06))
    }
}

struct TaskRunBadge: View {
    var ordinal: Int
    var body: some View {
        Text("#\(ordinal)")
            .font(.system(size: 10, weight: .medium, design: .monospaced))
            .foregroundStyle(.secondary)
            .padding(.horizontal, 5).padding(.vertical, 2)
            .background(.quaternary.opacity(0.5), in: Capsule())
            .fixedSize()
            .accessibilityLabel("第 \(ordinal) 次任务")
    }
}
