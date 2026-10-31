import SwiftUI

struct JobDetailView: View {
    @EnvironmentObject private var model: AppModel
    var job: ArchiveJob
    @State private var filter: FileFilter = .all
    @State private var files: [ArchiveFile] = []
    @State private var totalFiles = 0
    @State private var loadingFiles = false
    @State private var headerHeight: CGFloat = 260
    @State private var fileError: String?
    @State private var fileLoadTask: Task<Void, Never>?

    var body: some View {
        VStack(spacing: 0) {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    HStack(alignment: .top) {
                        VStack(alignment: .leading, spacing: 6) {
                            Text(job.name).font(.title2.weight(.semibold)).textSelection(.enabled)
                            if let range = job.dateRangeLabel { Text(range).font(.callout).foregroundStyle(.secondary) }
                            Label(job.statusLabel, systemImage: statusIcon)
                                .font(.callout.weight(.medium)).foregroundStyle(job.failedFiles > 0 ? Color.orange : (job.status == "running" ? Color.accentColor : Color.secondary))
                        }
                        Spacer()
                        actionButtons.controlSize(.small)
                    }
                    if job.status == "scanning" {
                        HStack(spacing: 14) {
                            ProgressView().controlSize(.small)
                            VStack(alignment: .leading, spacing: 5) {
                                Text("正在掃描與讀取拍攝日期").font(.headline)
                                Text("已發現 \(job.totalFiles) 個檔案。掃描完成後可檢查預覽並開始歸檔。")
                                    .font(.callout).foregroundStyle(.secondary)
                            }
                        }.padding(.vertical, 6).frame(maxWidth: .infinity, alignment: .leading)
                    } else {
                        VStack(alignment: .leading, spacing: 11) {
                            HStack {
                                Text(job.status == "ready" ? "預覽已準備好" : "\(job.completedFiles) / \(job.supportedFiles) 個檔案")
                                    .font(.headline).monospacedDigit()
                                Spacer()
                                if job.status != "ready" { Text(job.progress, format: .percent.precision(.fractionLength(0))).font(.callout.weight(.medium)).monospacedDigit() }
                            }
                            if job.status != "ready" && !job.isTerminal { ProgressView(value: job.progress).tint(job.failedFiles > 0 ? Color.orange : Color.accentColor) }
                            HStack {
                                Text(job.status == "ready" ? "共 \(formattedBytes(job.totalBytes))" : "\(formattedBytes(min(job.supportedBytes, job.completedBytes + job.currentFileBytes))) / \(formattedBytes(job.supportedBytes))")
                                Spacer()
                                Text("失敗 \(job.failedFiles)").foregroundStyle(job.failedFiles > 0 ? Color.orange : Color.secondary)
                            }.font(.callout).foregroundStyle(.secondary).monospacedDigit()
                            if !job.currentFile.isEmpty {
                                Label(job.currentFile, systemImage: "doc").font(.caption).foregroundStyle(.secondary)
                                    .lineLimit(1).truncationMode(.middle).help(job.currentFile)
                            }
                        }
                    }
                    if job.status == "ready" {
                        Label("開始後會移動這些檔案，校驗完成後清理來源。", systemImage: "checkmark.shield")
                            .font(.callout).foregroundStyle(.secondary)
                    }
                    if (job.unsupportedFiles ?? 0) > 0 {
                        Label("\(job.unsupportedFiles ?? 0) 個不支援的檔案會保留在來源目錄，不計入歸檔進度。", systemImage: "doc.badge.ellipsis")
                            .font(.callout).foregroundStyle(.secondary)
                    }
                    if job.attentionFiles > 0 {
                        Button { filter = .attention } label: {
                            HStack(spacing: 10) {
                                Image(systemName: "exclamationmark.circle")
                                Text("\(job.attentionFiles) 個檔案需要關注")
                                Spacer()
                                Image(systemName: "chevron.right").font(.caption)
                            }.font(.callout).padding(.vertical, 3)
                        }.buttonStyle(.plain).foregroundStyle(.orange)
                    }
                    if !job.error.isEmpty { StatusBanner(message: job.error, systemImage: "exclamationmark.triangle", isError: true) }
                    if job.isTerminal { cleanupResult }
                    DisclosureGroup("來源與歸檔位置") {
                        VStack(alignment: .leading, spacing: 10) {
                            ForEach(job.sourcePaths, id: \.self) { path in Label(path, systemImage: "folder").textSelection(.enabled) }
                            Divider()
                            Label(job.archiveBase, systemImage: "archivebox").textSelection(.enabled)
                        }.font(.caption).foregroundStyle(.secondary).padding(.top, 10)
                    }.font(.callout)
                }.padding(22)
                    .onGeometryChange(for: CGFloat.self) { geometry in geometry.size.height } action: { height in
                        headerHeight = height
                    }
            }.frame(height: min(headerHeight, 340))
            Divider()
            HStack {
                Text("檔案").font(.headline)
                Text("\(totalFiles)").font(.caption).foregroundStyle(.secondary).monospacedDigit()
                Spacer()
                Menu {
                    Picker("顯示檔案", selection: $filter) {
                        ForEach(FileFilter.allCases) { value in Text(value.title).tag(value) }
                    }
                    .pickerStyle(.inline)
                    .labelsHidden()
                } label: {
                    Label(filter.title, systemImage: "line.3.horizontal.decrease")
                        .font(.callout)
                }
                .menuStyle(.borderlessButton)
                .fixedSize()
                if loadingFiles { ProgressView().controlSize(.small) }
                Button("重新整理檔案", systemImage: "arrow.clockwise") { loadFiles(reset: true) }
                    .labelStyle(.iconOnly).buttonStyle(.plain).disabled(loadingFiles)
            }.padding(.horizontal, 22).padding(.vertical, 10)
            if let fileError { StatusBanner(message: fileError, systemImage: "exclamationmark.triangle", isError: true) }
            if files.isEmpty && !loadingFiles {
                ContentUnavailableView(filter == .all ? "尚無檔案" : "沒有這類檔案", systemImage: "doc.text.magnifyingglass")
            } else {
                List {
                    ForEach(files) { file in FileRow(file: file) }
                    if files.count < totalFiles {
                        HStack {
                            Spacer()
                            Button("載入更多（已顯示 \(files.count) / \(totalFiles)）") { loadFiles(reset: false) }.disabled(loadingFiles)
                            Spacer()
                        }.padding(10)
                    }
                }.listStyle(.inset)
            }
        }
        .task(id: "\(job.id)-\(filter.rawValue)") {
            loadFiles(reset: true)
            while !Task.isCancelled {
                do { try await Task.sleep(for: .seconds(5)) } catch { return }
                if let current = model.selectedJob, ["scanning", "running", "pausing"].contains(current.status), !loadingFiles, files.count <= 200 {
                    loadFiles(reset: true, clearExisting: false)
                }
            }
        }
        .onChange(of: job.status) { _, _ in loadFiles(reset: true) }
        .onDisappear { fileLoadTask?.cancel() }
    }

    private var statusIcon: String {
        if job.status == "completed" { return "checkmark.circle.fill" }
        if job.failedFiles > 0 || job.status == "failed" { return "exclamationmark.circle" }
        return job.status == "ready" ? "doc.text.magnifyingglass" : "arrow.triangle.2.circlepath"
    }

    @ViewBuilder private var actionButtons: some View {
        HStack {
            if job.needsRescan {
                Button("重新掃描", systemImage: "arrow.clockwise") { Task { await model.perform("retry", job: job) } }
                    .buttonStyle(.borderedProminent)
            }
            if job.canStart {
                Button(job.status == "ready" ? "開始歸檔" : "繼續歸檔", systemImage: "play.fill") {
                    Task { await model.perform("start", job: job) }
                }.buttonStyle(.borderedProminent)
            }
            if job.canPause {
                Button("暫停", systemImage: "pause.fill") { Task { await model.perform("pause", job: job) } }
                    .buttonStyle(.bordered)
            }
            if job.status == "pausing" { Text("當前檔案完成後暫停").font(.caption).foregroundStyle(.secondary) }
            if job.isTerminal && (job.failedFiles > 0 || job.status == "failed") {
                Button("重試失敗檔案", systemImage: "arrow.clockwise") { Task { await model.perform("retry", job: job) } }
            }
        }.disabled(model.isPerformingAction || !model.isConnected)
    }

    private var cleanupResult: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Label(cleanupTitle, systemImage: job.cleanupStatus == "done" ? "checkmark.circle" : "folder.badge.questionmark")
                    .font(.callout)
                Spacer()
                if job.cleanupStatus != "done" {
                    Button("清理空目錄") { Task { await model.perform("cleanup", job: job) } }
                        .disabled(model.isPerformingAction || !model.isConnected)
                }
            }
            if !job.residualPaths.isEmpty {
                DisclosureGroup("查看殘留路徑（\(job.residualPaths.count)）") {
                    ForEach(job.residualPaths, id: \.self) { path in Text(path).font(.caption).foregroundStyle(.secondary).textSelection(.enabled) }
                }
            }
        }.padding(.vertical, 4)
    }
    private var cleanupTitle: String {
        switch job.cleanupStatus {
        case "done": "空目錄已清理"
        case "residual": "目錄內仍有檔案，已保留"
        case "not_requested": "尚未清理空目錄"
        default: "等待清理空目錄"
        }
    }

    private func loadFiles(reset: Bool, clearExisting: Bool = true) {
        fileLoadTask?.cancel(); loadingFiles = true; fileError = nil
        let connectionID = model.selectedConnectionID
        let selectedFilter = filter
        let offset = reset ? 0 : files.count
        if reset && clearExisting { files = []; totalFiles = 0 }
        fileLoadTask = Task {
            do {
                let page = try await model.api().files(jobID: job.id, filter: selectedFilter, offset: offset)
                try Task.checkCancellation()
                guard connectionID == model.selectedConnectionID, selectedFilter == filter else { return }
                if reset { files = page.files } else { files.append(contentsOf: page.files) }
                totalFiles = page.total; loadingFiles = false
            } catch is CancellationError {} catch { guard !Task.isCancelled else { return }; fileError = presentableError(error); loadingFiles = false }
        }
    }
}

struct FileRow: View {
    var file: ArchiveFile
    private var attentionSummary: String {
        if file.status == "unsupported" { return "保留在來源目錄" }
        if file.metadataSource == "mtime" { return "使用檔案修改日期" }
        if file.warning.localizedCaseInsensitiveContains("suffix") { return "同名檔案會另存" }
        return "歸檔資訊需要確認"
    }

    var body: some View {
        DisclosureGroup {
            VStack(alignment: .leading, spacing: 9) {
                LabeledContent("來源", value: file.sourcePath)
                if !file.targetPath.isEmpty { LabeledContent("歸檔", value: file.targetPath) }
                if !file.dateTime.isEmpty { LabeledContent("日期", value: file.dateTime) }
                if !file.metadataSource.isEmpty { LabeledContent("日期來源", value: file.metadataSource) }
                if !file.warning.isEmpty { Label(file.warning, systemImage: "exclamationmark.circle").foregroundStyle(.orange) }
                if !file.error.isEmpty { Label(file.error, systemImage: "xmark.circle").foregroundStyle(.red) }
                if !file.sha256.isEmpty { LabeledContent("SHA-256", value: file.sha256).font(.system(.caption2, design: .monospaced)) }
            }.font(.caption).foregroundStyle(.secondary).textSelection(.enabled).padding(.vertical, 8)
        } label: {
            HStack(spacing: 10) {
                Image(systemName: file.status == "completed" ? "checkmark.circle" : "doc")
                    .foregroundStyle(file.status == "failed" ? Color.red : Color.secondary)
                VStack(alignment: .leading, spacing: 3) {
                    Text(file.name).lineLimit(1)
                    if !file.warning.isEmpty { Text(attentionSummary).font(.caption).foregroundStyle(.secondary).lineLimit(1) }
                    if !file.error.isEmpty { Text(file.error).font(.caption).foregroundStyle(.red).lineLimit(1) }
                }
                Spacer(minLength: 8)
                Text(formattedBytes(file.size)).font(.caption).foregroundStyle(.secondary).monospacedDigit()
                Text(file.statusLabel).font(.caption).foregroundStyle(.secondary).frame(width: 74, alignment: .trailing)
            }.padding(.vertical, 5)
        }
    }
}
