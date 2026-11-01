import SwiftUI

struct JobDetailView: View {
    @EnvironmentObject private var model: AppModel
    var job: ArchiveJob
    @State private var filter: FileFilter = .all
    @State private var files: [ArchiveFile] = []
    @State private var totalFiles = 0
    @State private var loadingFiles = false
    @State private var fileError: String?
    @State private var fileLoadTask: Task<Void, Never>?
    @State private var folderTarget: FolderTarget?

    var body: some View {
        VStack(spacing: 0) {
            VStack(alignment: .leading, spacing: 18) {
                HStack(alignment: .top, spacing: 16) {
                    HStack(spacing: 8) {
                        Text(job.name).font(.title2.weight(.semibold)).textSelection(.enabled)
                        if let ordinal = taskOrdinal(job, among: model.jobs) {
                            TaskRunBadge(ordinal: ordinal).help("建立于 \(job.creationDescription)")
                        }
                    }
                    Spacer(minLength: 12)
                    actionButtons
                }
                metadata
                progress
                if job.isTerminal { completionActions }
                if !job.error.isEmpty {
                    StatusBanner(message: job.error, systemImage: "exclamationmark.triangle", isError: true)
                }
            }.padding(22)
            Divider()
            fileToolbar
            if let fileError { StatusBanner(message: fileError, systemImage: "exclamationmark.triangle", isError: true) }
            if files.isEmpty && !loadingFiles {
                Text(filter == .all ? "" : "没有这类文件")
                    .font(.callout).foregroundStyle(.secondary).frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List {
                    ForEach(files) { file in
                        FileRow(file: file, job: job, onBrowse: { path in folderTarget = FolderTarget(path: path) }, onChanged: { loadFiles(reset: true) })
                    }
                    if files.count < totalFiles {
                        HStack {
                            Spacer()
                            Button("加载更多") { loadFiles(reset: false) }.buttonStyle(.glass).disabled(loadingFiles)
                            Spacer()
                        }.padding(10)
                    }
                }.listStyle(.inset)
            }
        }
        .sheet(item: $folderTarget) { target in
            FolderContentsView(roots: job.sourcePaths, initialPath: target.path).environmentObject(model)
        }
        .task(id: "\(job.id)-\(filter.rawValue)") {
            loadFiles(reset: true)
            while !Task.isCancelled {
                do { try await Task.sleep(for: .seconds(5)) } catch { return }
                if ["scanning", "running", "pausing"].contains(model.selectedJob?.status ?? ""), !loadingFiles, files.count <= 200 {
                    loadFiles(reset: true, clearExisting: false)
                }
            }
        }
        .onChange(of: job.status) { _, _ in loadFiles(reset: true) }
        .onDisappear { fileLoadTask?.cancel() }
    }

    private var metadata: some View {
        HStack(spacing: 12) {
            if let range = job.dateRangeLabel { Text(range).fixedSize() }
            if !job.sourcePaths.isEmpty {
                Image(systemName: "folder")
                Text(job.sourcePaths.map(displayRemotePath).joined(separator: ", ")).lineLimit(1).truncationMode(.middle)
                    .help(job.sourcePaths.joined(separator: "\n"))
                    .contextMenu { Button("复制来源路径") { copyRemotePath(job.sourcePaths.joined(separator: "\n")) } }
            }
            Image(systemName: "arrow.right").font(.caption2)
            Text(displayRemotePath(job.archiveBase)).lineLimit(1).truncationMode(.middle).help(job.archiveBase)
                .contextMenu { Button("复制归档路径") { copyRemotePath(job.archiveBase) } }
        }.font(.caption).foregroundStyle(.secondary).textSelection(.enabled)
    }

    private var progress: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 8) {
                if job.status == "scanning" {
                    Text("扫描 · \(job.totalFiles) 个 · \(formattedBytes(job.totalBytes))")
                } else if job.status == "ready" {
                    Text(job.supportedFiles == 0 ? "没有可归档的文件" : "\(job.supportedFiles) 个文件 · \(formattedBytes(job.supportedBytes))")
                } else if job.isTerminal {
                    Text("已归档 \(job.completedFiles) 个 · \(formattedBytes(job.completedBytes))")
                    if (job.skippedFiles ?? 0) > 0 { Text("· 跳过 \(job.skippedFiles ?? 0) 个") }
                } else {
                    Text("\(job.processedFiles) / \(job.supportedFiles) 个 · \(formattedBytes(min(job.supportedBytes, job.processedBytes + job.currentFileBytes))) / \(formattedBytes(job.supportedBytes))")
                }
                Spacer()
                if ["paused", "pausing", "queued", "interrupted"].contains(job.status) {
                    Text(job.statusLabel).foregroundStyle(.secondary)
                }
            }.font(.callout).monospacedDigit()
            if job.status == "scanning" {
                ProgressView().progressViewStyle(.linear)
            } else if ["running", "pausing", "paused"].contains(job.status) {
                ProgressView(value: job.progress)
            }
            if !job.currentFile.isEmpty {
                Text((job.currentFile as NSString).lastPathComponent)
                    .font(.caption).foregroundStyle(.tertiary).lineLimit(1).help(job.currentFile)
            }
        }
    }

    private var fileToolbar: some View {
        HStack {
            Text("文件").font(.headline)
            Spacer()
            Menu {
                Picker("显示文件", selection: $filter) {
                    ForEach(FileFilter.allCases) { value in Text(filterLabel(value)).tag(value) }
                }.pickerStyle(.inline)
            } label: {
                Label(filterLabel(filter), systemImage: "line.3.horizontal.decrease").font(.callout)
            }.menuStyle(.borderlessButton).fixedSize()
            if loadingFiles { ProgressView().controlSize(.small) }
            Button("重新整理", systemImage: "arrow.clockwise") { loadFiles(reset: true) }
                .labelStyle(.iconOnly).buttonStyle(.plain).disabled(loadingFiles)
        }.padding(.horizontal, 22).padding(.vertical, 12)
    }
    private func filterLabel(_ value: FileFilter) -> String {
        let count: Int
        switch value {
        case .all: count = job.totalFiles
        case .attention: count = job.attentionFiles
        case .failed: count = job.failedFiles
        case .skipped: count = job.skippedFiles ?? 0
        }
        return "\(value.title) · \(count)"
    }

    private var actionButtons: some View {
        HStack(spacing: 10) {
            if job.canStart {
                Menu {
                    Toggle("跳过重复（保留来源）", isOn: Binding(
                        get: { job.skipDuplicates ?? false },
                        set: { enabled in Task { await model.setSkipDuplicates(enabled, job: job) } }
                    ))
                } label: { Image(systemName: "ellipsis") }
                    .menuStyle(.button).buttonStyle(.glass).menuIndicator(.hidden).help("归档选项")
            }
            if job.needsRescan {
                Button("重新扫描", systemImage: "arrow.clockwise") { Task { await model.perform("retry", job: job) } }
                    .buttonStyle(.glassProminent)
            }
            if job.canRescan {
                if job.supportedFiles == 0 {
                    Button("重新扫描", systemImage: "arrow.clockwise") { Task { await model.perform("rescan", job: job) } }.buttonStyle(.glassProminent)
                } else {
                    Button("重新扫描", systemImage: "arrow.clockwise") { Task { await model.perform("rescan", job: job) } }.buttonStyle(.glass)
                }
            }
            if job.canStart {
                Button(job.status == "ready" ? "开始归档" : "继续", systemImage: "play.fill") {
                    Task { await model.perform("start", job: job) }
                }.buttonStyle(.glassProminent)
            }
            if job.canPause {
                Button("暂停", systemImage: "pause.fill") { Task { await model.perform("pause", job: job) } }.buttonStyle(.glass)
            }
            if job.isTerminal && (job.failedFiles > 0 || job.status == "failed") {
                Button("重试失败文件", systemImage: "arrow.clockwise") { Task { await model.perform("retry", job: job) } }.buttonStyle(.glass)
            }
        }.buttonBorderShape(.capsule).controlSize(.regular)
            .disabled(model.isPerformingAction || !model.isConnected)
    }

    private var completionActions: some View {
        HStack(spacing: 12) {
            if job.cleanupStatus == "done" {
                Label("空目录已清理", systemImage: "checkmark.circle").font(.caption).foregroundStyle(.secondary)
            } else {
                Button("查看来源内容", systemImage: "folder") {
                    folderTarget = FolderTarget(path: job.residualPaths.first ?? job.sourcePaths.first ?? "")
                }.buttonStyle(.glass)
                Button("清理空目录", systemImage: "folder.badge.minus") { Task { await model.perform("cleanup", job: job) } }
                    .buttonStyle(.glass).disabled(model.isPerformingAction || !model.isConnected)
            }
        }.buttonBorderShape(.capsule).controlSize(.small)
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

private struct FolderTarget: Identifiable { var path: String; var id: String { path } }

struct FileRow: View {
    @EnvironmentObject private var model: AppModel
    var file: ArchiveFile
    var job: ArchiveJob
    var onBrowse: (String) -> Void
    var onChanged: () -> Void
    @State private var editDate = false
    @State private var isExpanded = false
    @State private var reviewError: String?
    private var canReview: Bool { job.canStart || job.isTerminal }
    private var canEditDate: Bool { job.canStart && ["planned", "failed"].contains(file.status) && file.sha256.isEmpty }

    private var hasReviewActions: Bool {
        ["unsupported", "skipped", "failed"].contains(file.status)
            || (canReview && !file.warning.isEmpty)
    }

    private func pathRow(_ title: String, path: String) -> some View {
        GridRow(alignment: .firstTextBaseline) {
            Text(title).foregroundStyle(.secondary)
            HStack(spacing: 6) {
                Text(displayRemotePath(path))
                    .lineLimit(1).truncationMode(.middle)
                    .help(path)
                Button { copyRemotePath(path) } label: {
                    Image(systemName: "doc.on.doc")
                }
                .buttonStyle(.borderless)
                .foregroundStyle(.secondary)
                .help("复制\(title)路径")
                .accessibilityLabel("复制\(title)路径")
                .textSelection(.disabled)
                Spacer(minLength: 0)
            }
        }
    }

    var body: some View {
        DisclosureGroup(isExpanded: $isExpanded) {
            VStack(alignment: .leading, spacing: 12) {
                Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 8) {
                    pathRow("来源", path: file.sourcePath)
                    if !file.targetPath.isEmpty { pathRow("归档", path: file.targetPath) }
                    if !file.dateTime.isEmpty {
                        GridRow(alignment: .firstTextBaseline) {
                            Text("日期").foregroundStyle(.secondary)
                            HStack(spacing: 8) {
                                Text(file.dateTime.prefix(19).replacingOccurrences(of: "T", with: " "))
                                    .help(file.dateTime)
                                if !file.metadataSource.isEmpty {
                                    Text("· \(file.metadataLabel)").foregroundStyle(.secondary)
                                        .help(file.needsDateReview ? "未找到拍摄日期，已使用文件修改日期。" : file.metadataLabel)
                                }
                                if canEditDate && file.needsDateReview {
                                    Button("修改…") { editDate = true }.buttonStyle(.link)
                                }
                                Spacer(minLength: 0)
                            }
                        }
                    }
                }
                if !file.needsDateReview, let advice = file.reviewAdvice {
                    Text(advice).foregroundStyle(.secondary)
                }
                if !file.error.isEmpty { Text(file.error).foregroundStyle(.red) }
                if hasReviewActions {
                HStack(spacing: 10) {
                    if file.status == "unsupported" || file.status == "skipped" || file.status == "failed" {
                        Button("查看来源") { onBrowse((file.sourcePath as NSString).deletingLastPathComponent) }.buttonStyle(.glass)
                    }
                    if canReview && !file.warning.isEmpty && file.status != "unsupported" && file.status != "failed" {
                        Button("确认信息", systemImage: "checkmark") {
                            Task {
                                do { try await model.reviewFile(file, job: job); onChanged() }
                                catch { reviewError = presentableError(error) }
                            }
                        }.buttonStyle(.glass).disabled(model.isPerformingAction)
                    }
                    if job.isTerminal && file.status == "failed" {
                        Button("重试失败文件") { Task { await model.perform("retry", job: job) } }.buttonStyle(.glass)
                    }
                }.controlSize(.small).textSelection(.disabled)
                }
                if let reviewError { Text(reviewError).foregroundStyle(.red) }
            }.font(.caption).textSelection(.enabled).padding(.vertical, 10)
        } label: {
            HStack(alignment: .firstTextBaseline, spacing: 10) {
                Image(systemName: file.status == "completed" ? "checkmark.circle" : (file.reviewTitle == nil ? "doc" : "exclamationmark.circle"))
                    .font(.system(size: 15))
                    .foregroundStyle(file.status == "failed" ? Color.red : Color.secondary)
                    .frame(width: 20)
                Text(file.name).lineLimit(1).truncationMode(.middle).layoutPriority(1)
                if !isExpanded, let title = file.reviewTitle {
                    Text(title).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer(minLength: 8)
                Text(formattedBytes(file.size)).font(.caption).foregroundStyle(.secondary).monospacedDigit()
                Text(file.statusLabel).font(.caption).foregroundStyle(.secondary).frame(width: 74, alignment: .trailing)
            }.padding(.vertical, 5)
        }
        .disclosureGroupStyle(FileDisclosureStyle())
        .sheet(isPresented: $editDate) {
            CaptureDateView(file: file, job: job, onChanged: onChanged).environmentObject(model)
        }
    }
}

// Keep disclosure indicators, file icons and filenames on the same baseline.
private struct FileDisclosureStyle: DisclosureGroupStyle {
    func makeBody(configuration: Configuration) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            Button {
                configuration.isExpanded.toggle()
            } label: {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Image(systemName: configuration.isExpanded ? "chevron.down" : "chevron.right")
                        .font(.system(size: 10, weight: .semibold))
                        .foregroundStyle(.secondary)
                        .frame(width: 12)
                    configuration.label
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityValue(configuration.isExpanded ? "已展开" : "已折叠")
            .accessibilityHint("显示或隐藏文件详情")
            if configuration.isExpanded {
                configuration.content.padding(.leading, 20)
            }
        }
    }
}

private struct CaptureDateView: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.dismiss) private var dismiss
    var file: ArchiveFile
    var job: ArchiveJob
    var onChanged: () -> Void
    @State private var date: Date
    @State private var saving = false
    @State private var error: String?
    init(file: ArchiveFile, job: ArchiveJob, onChanged: @escaping () -> Void) {
        self.file = file; self.job = job; self.onChanged = onChanged
        _date = State(initialValue: ISO8601DateFormatter().date(from: file.dateTime) ?? Date())
    }
    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            Text("设置拍摄日期").font(.title3.weight(.semibold))
            Text(file.name).font(.callout).foregroundStyle(.secondary)
            DatePicker("拍摄日期", selection: $date, displayedComponents: [.date, .hourAndMinute])
            Text("更新归档日期与目标位置，不会修改原始文件。").font(.caption).foregroundStyle(.secondary)
            if let error { Text(error).font(.caption).foregroundStyle(.red) }
            HStack {
                Spacer()
                Button("取消") { dismiss() }.buttonStyle(.glass).keyboardShortcut(.cancelAction)
                Button("储存") {
                    saving = true
                    Task {
                        defer { saving = false }
                        do {
                            let formatter = ISO8601DateFormatter(); formatter.timeZone = .current
                            try await model.reviewFile(file, job: job, dateTime: formatter.string(from: date))
                            onChanged(); dismiss()
                        } catch { self.error = presentableError(error) }
                    }
                }.buttonStyle(.glassProminent).disabled(saving)
            }
        }.padding(24).frame(width: 440)
    }
}
