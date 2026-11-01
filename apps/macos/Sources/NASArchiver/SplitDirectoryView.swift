import SwiftUI

struct SplitBatch: Codable, Identifiable, Sendable {
    var path: String
    var files: Int
    var bytes: Int64
    var oversized: Bool
    var id: String { path }
}
struct DirectorySplitJob: Codable, Identifiable, Sendable {
    var id: String
    var status: String
    var sourcePath: String
    var prefix: String
    var maxBytes: Int64
    var totalFiles: Int
    var totalBytes: Int64
    var completedFiles: Int
    var completedBytes: Int64
    var retainedEntries: Int
    var batches: [SplitBatch]
    var error: String
    var isRunning: Bool { status == "running" }
    var canStart: Bool { ["preview", "failed", "interrupted"].contains(status) && totalFiles > 0 }
}

struct SplitDirectoryView: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.dismiss) private var dismiss
    @State private var source = ""
    @State private var prefix = ""
    @State private var job: DirectorySplitJob?
    @State private var history: [DirectorySplitJob] = []
    @State private var choosing = false
    @State private var listing: DirectoryListing?
    @State private var path = ""
    @State private var busy = false
    @State private var error: String?
    @State private var notice: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            HStack {
                Label("拆分目录", systemImage: "folder.badge.gearshape").font(.title2.weight(.semibold))
                Spacer()
                if !history.isEmpty {
                    Menu("最近拆分") {
                        ForEach(history) { item in
                            Button("\(item.prefix) · \(item.completedFiles)/\(item.totalFiles)") { select(item) }
                        }
                    }.disabled(busy || job?.isRunning == true)
                }
            }
            if choosing { browser } else { details }
            if let error { Text(error).font(.callout).foregroundStyle(.red).textSelection(.enabled) }
            if let notice { Text(notice).font(.caption).foregroundStyle(.secondary) }
            Spacer(minLength: 0)
            Divider()
            HStack {
                Button(choosing ? "返回" : "关闭") {
                    if choosing { choosing = false } else { dismiss() }
                }.buttonStyle(.glass).keyboardShortcut(.cancelAction).disabled(busy)
                Spacer()
                if choosing {
                    Button("选择此目录") { chooseCurrent() }
                        .buttonStyle(.glassProminent).disabled(listing?.selectable == false || listing?.path.isEmpty != false || busy)
                } else if let job, job.isRunning {
                    ProgressView().controlSize(.small)
                    Text("正在拆分").foregroundStyle(.secondary)
                } else if let job, job.canStart {
                    if job.status == "preview" {
                        Button("重新预览") { Task { await preview() } }.buttonStyle(.glass).disabled(busy)
                    }
                    Button(job.status == "preview" ? "开始拆分" : "继续拆分") { Task { await start() } }
                        .buttonStyle(.glassProminent).disabled(busy)
                } else if job?.status == "completed" {
                    Button("新建拆分") { self.job = nil; source = ""; prefix = ""; error = nil; notice = nil }
                        .buttonStyle(.glassProminent)
                } else {
                    Button(busy ? "正在预览…" : "预览") { Task { await preview() } }
                        .buttonStyle(.glassProminent).disabled(source.isEmpty || busy)
                }
            }
        }.padding(24).frame(width: 760, height: 560)
        .interactiveDismissDisabled(busy)
        .task { await loadHistory() }
        .task(id: job?.isRunning == true ? job?.id : nil) {
            guard let id = job?.id, job?.isRunning == true else { return }
            while !Task.isCancelled {
                do {
                    try await Task.sleep(for: .seconds(1))
                    let value = try await model.api().splitJob(id)
                    try Task.checkCancellation()
                    job = value; error = value.error.isEmpty ? nil : value.error
                    notice = nil
                    if !value.isRunning { await loadHistory(selectActive: false); return }
                } catch is CancellationError { return }
                catch { notice = "连接暂时中断，正在重试；拆分由服务端继续执行。" }
            }
        }
        .onChange(of: model.selectedConnectionID) { _, _ in dismiss() }
    }

    private var details: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack {
                Text("来源").foregroundStyle(.secondary).frame(width: 56, alignment: .leading)
                Text(source.isEmpty ? "选择要拆分的目录" : displayRemotePath(source)).lineLimit(1).truncationMode(.middle).help(source)
                Spacer()
                Button("选择…") { choosing = true; Task { await browse("") } }.disabled(busy || job?.isRunning == true || (job != nil && job?.status != "preview"))
            }
            HStack {
                Text("批次名称").foregroundStyle(.secondary)
                TextField("例如 iPhone 15", text: $prefix)
                    .onChange(of: prefix) { _, value in if job?.status == "preview", value != job?.prefix { job = nil } }
                    .disabled(job != nil && job?.status != "preview")
                Text("每批 8 GB").foregroundStyle(.secondary)
            }
            if let job {
                if job.status == "preview" {
                    Text("\(job.totalFiles.formatted()) 个文件 · \(splitBytes(job.totalBytes)) · \(job.batches.count) 个批次")
                } else {
                    HStack {
                        Text(job.status == "completed" ? "拆分完成" : "\(job.completedFiles.formatted()) / \(job.totalFiles.formatted()) 个 · \(splitBytes(job.completedBytes)) / \(splitBytes(job.totalBytes))")
                        Spacer()
                        Button("刷新", systemImage: "arrow.clockwise") { Task { await reload() } }.labelStyle(.iconOnly).buttonStyle(.borderless)
                    }
                    if job.isRunning { ProgressView(value: Double(job.completedFiles), total: Double(max(1, job.totalFiles))) }
                }
                List(job.batches) { batch in
                    HStack {
                        Label((batch.path as NSString).lastPathComponent, systemImage: "folder")
                        Spacer()
                        if batch.oversized { Image(systemName: "exclamationmark.triangle").foregroundStyle(.orange).help("单个文件超过 8 GB，将独立保留，文件不会切碎。") }
                        Text("\(batch.files.formatted()) 个 · \(splitBytes(batch.bytes))").foregroundStyle(.secondary)
                    }.help(displayRemotePath(batch.path))
                }
                if job.totalFiles == 0 { Text("没有可拆分的文件。").foregroundStyle(.secondary) }
                if job.retainedEntries > 0 {
                    Text("\(job.retainedEntries) 个隐藏或特殊项目留在原目录。").font(.caption).foregroundStyle(.secondary)
                }
            } else {
                Text("文件移到平级批次目录，原目录保留。不足 8 GB 也会生成一个批次。")
                    .font(.callout).foregroundStyle(.secondary)
            }
        }
    }
    private var browser: some View {
        VStack(spacing: 12) {
            HStack {
                Button("来源目录", systemImage: "house") { Task { await browse("") } }.labelStyle(.iconOnly)
                Button("上一级", systemImage: "arrow.up") { Task { await browse(listing?.parentPath ?? "") } }.labelStyle(.iconOnly).disabled(listing?.path.isEmpty != false)
                TextField("服务端路径", text: $path).onSubmit { Task { await browse(path) } }
                Button("前往") { Task { await browse(path) } }
            }.disabled(busy)
            List((listing?.entries ?? []).filter { !$0.name.hasPrefix(".") }) { entry in
                Button { Task { await browse(entry.path) } } label: {
                    HStack { Label(entry.name, systemImage: "folder"); Spacer(); Image(systemName: "chevron.right").foregroundStyle(.secondary) }.contentShape(Rectangle())
                }.buttonStyle(.plain).disabled(busy)
            }
        }
    }
    private func splitBytes(_ value: Int64) -> String { ByteCountFormatter.string(fromByteCount: value, countStyle: .decimal) }
    private func chooseCurrent() {
        guard let p = listing?.path, !p.isEmpty else { return }
        source = p; job = nil; error = nil; choosing = false
        let name = (displayRemotePath(p) as NSString).lastPathComponent
        if name.lowercased().hasPrefix("iphone"), let number = Int(name.dropFirst(6)) { prefix = "iPhone \(number)" }
        else { prefix = name }
    }
    private func browse(_ value: String) async {
        busy = true; error = nil; listing = nil
        defer { busy = false }
        do {
            let result = try await model.api().browseSources(value, archiveBase: model.serverInfo?.archiveBase ?? "")
            listing = result; path = result.path
            if value.isEmpty, result.entries.count == 1 { listing = try await model.api().browseSources(result.entries[0].path, archiveBase: model.serverInfo?.archiveBase ?? ""); path = listing?.path ?? "" }
        } catch { self.error = presentableError(error) }
    }
    private func preview() async {
        busy = true; error = nil
        defer { busy = false }
        do { job = try await model.api().previewSplit(source: source, prefix: prefix) }
        catch { self.error = presentableError(error) }
    }
    private func start() async {
        guard let id = job?.id else { return }
        busy = true; error = nil
        defer { busy = false }
        do { job = try await model.api().startSplit(id); await loadHistory(selectActive: false) }
        catch { self.error = presentableError(error) }
    }
    private func select(_ value: DirectorySplitJob) { job = value; source = value.sourcePath; prefix = value.prefix; error = value.error.isEmpty ? nil : value.error }
    private func reload() async {
        guard let id = job?.id else { return }
        do { select(try await model.api().splitJob(id)) } catch { self.error = presentableError(error) }
    }
    private func loadHistory(selectActive: Bool = true) async {
        do {
            history = try await model.api().splitJobs()
            if selectActive, job == nil, let active = history.first(where: { $0.status != "completed" }) { select(active) }
        } catch { self.error = presentableError(error) }
    }
}
