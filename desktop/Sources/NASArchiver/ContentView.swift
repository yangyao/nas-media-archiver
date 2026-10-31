import SwiftUI

enum TaskSection: String, CaseIterable, Identifiable {
    case active, history
    var id: String { rawValue }
    var title: String { self == .active ? "正在處理" : "歷史記錄" }
    var icon: String { self == .active ? "tray.and.arrow.down" : "clock.arrow.circlepath" }
}

struct ContentView: View {
    @EnvironmentObject private var model: AppModel
    @State private var section: TaskSection? = .active
    @State private var showNASPopover = false

    private var visibleJobs: [ArchiveJob] {
        model.jobs.filter { section == .history ? $0.isTerminal : !$0.isTerminal }
    }

    var body: some View {
        NavigationSplitView {
            List(selection: $section) {
                Section("歸檔") {
                    ForEach(TaskSection.allCases) { item in
                        Label(item.title, systemImage: item.icon).tag(item)
                    }
                }
            }
            .navigationSplitViewColumnWidth(min: 170, ideal: 190, max: 230)
            .safeAreaInset(edge: .bottom) {
                HStack(spacing: 7) {
                    Image(systemName: "server.rack")
                    Text("檔案由服務端安全處理").font(.caption)
                }
                .foregroundStyle(.secondary).padding(16)
            }
        } content: {
            VStack(spacing: 0) {
                HStack {
                    Text(section?.title ?? "正在處理").font(.title2.weight(.semibold))
                    Spacer()
                    Text("\(visibleJobs.count)").foregroundStyle(.secondary).monospacedDigit()
                }.padding(20)
                Divider()
                if model.isConnecting {
                    Spacer(); ProgressView("正在連線服務端…"); Spacer()
                } else if visibleJobs.isEmpty {
                    ContentUnavailableView {
                        Label(model.isConnected ? (section == .history ? "暫無歸檔記錄" : "準備好整理回憶") : "連線歸檔服務端", systemImage: model.isConnected ? "tray" : "server.rack")
                    } description: {
                        Text(model.isConnected ? "選擇服務端上的資料夾，預覽後開始歸檔。" : "歸檔由服務端執行，Mac 負責管理；服務端可安裝在 NAS 上。")
                    } actions: {
                        if model.isConnected && section != .history {
                            Button("新增歸檔", systemImage: "plus") { model.showNewArchive = true }.buttonStyle(.borderedProminent)
                        } else if !model.isConnected {
                            Button(model.selectedConnection == nil ? "新增服務端" : "重新連線") {
                                if let connection = model.selectedConnection { model.connect(connection) }
                                else { model.connectionEditor = .init(connection: nil) }
                            }.buttonStyle(.borderedProminent)
                            if let connection = model.selectedConnection {
                                Button("連線設定…") { model.connectionEditor = .init(connection: connection) }
                            }
                        }
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else {
                    List(visibleJobs, selection: $model.selectedJobID) { job in
                        JobRow(job: job).tag(job.id).padding(.vertical, 6)
                    }.listStyle(.inset)
                }
            }
            .navigationSplitViewColumnWidth(min: 270, ideal: 310, max: 420)
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
                } else {
                    ContentUnavailableView("選擇歸檔任務", systemImage: "folder.badge.gearshape", description: Text("在左側選擇任務，查看進度和檔案詳情。"))
                }
            }
        }
        .toolbar {
            ToolbarItem(placement: .navigation) {
                Button {
                    showNASPopover.toggle()
                } label: {
                    HStack(spacing: 8) {
                        Image(systemName: "server.rack")
                        VStack(alignment: .leading, spacing: 1) {
                            Text(model.selectedConnection?.name ?? "連線服務端").font(.headline)
                            Text(model.connectionLabel).font(.caption2).foregroundStyle(.secondary)
                        }
                        Image(systemName: "chevron.down").font(.caption2.weight(.semibold))
                    }.padding(.horizontal, 4)
                }
                .help("切換服務端 或查看連線設定")
                .popover(isPresented: $showNASPopover, arrowEdge: .bottom) {
                    NASPopover { showNASPopover = false }
                        .environmentObject(model)
                }
            }
            ToolbarItem(placement: .primaryAction) {
                Button("新增歸檔", systemImage: "plus") { model.showNewArchive = true }
                    .disabled(!model.isConnected)
            }
            ToolbarItem(placement: .automatic) {
                Button("刷新", systemImage: "arrow.clockwise") { Task { await model.refresh() } }
                    .disabled(model.isConnecting)
            }
        }
        .sheet(item: $model.connectionEditor) { target in
            ConnectionEditorView(connection: target.connection).environmentObject(model)
        }
        .sheet(isPresented: $model.showNewArchive) {
            NewArchiveView().environmentObject(model)
        }
        .onChange(of: model.selectedJobID) { _, id in
            guard let job = model.jobs.first(where: { $0.id == id }) else { return }
            section = job.isTerminal ? .history : .active
        }
        .onChange(of: model.selectedJob?.isTerminal) { _, terminal in
            if terminal == true { section = .history }
        }
    }
}

struct JobRow: View {
    var job: ArchiveJob
    var body: some View {
        VStack(alignment: .leading, spacing: 9) {
            HStack(alignment: .top, spacing: 9) {
                Image(systemName: job.isTerminal ? (job.failedFiles == 0 ? "checkmark.circle.fill" : "exclamationmark.circle.fill") : "folder.fill")
                    .foregroundStyle(job.failedFiles > 0 ? Color.orange : Color.accentColor)
                VStack(alignment: .leading, spacing: 4) {
                    Text(job.name).font(.headline).lineLimit(2)
                    Text(job.statusLabel).font(.caption).foregroundStyle(.secondary)
                }
                Spacer(minLength: 0)
            }
            if job.status == "scanning" { ProgressView().controlSize(.small) }
            else if !job.isTerminal && job.status != "ready" { ProgressView(value: job.progress) }
            HStack {
                Text("\(job.completedFiles) / \(job.supportedFiles) 個檔案")
                Spacer()
                Text(formattedBytes(job.totalBytes))
            }.font(.caption).foregroundStyle(.secondary).monospacedDigit()
        }
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
                    DisclosureGroup("詳細資訊") {
                        Text(String(parts[1])).font(.caption).textSelection(.enabled).foregroundStyle(.secondary)
                    }.font(.caption)
                }
            }
            Spacer(minLength: 0)
            if let dismiss { Button("關閉", systemImage: "xmark", action: dismiss).labelStyle(.iconOnly).buttonStyle(.plain) }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(isError ? Color.orange.opacity(0.08) : Color.secondary.opacity(0.06))
    }
}
