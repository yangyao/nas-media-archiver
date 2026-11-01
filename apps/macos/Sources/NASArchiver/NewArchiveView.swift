import SwiftUI

struct NewArchiveView: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.dismiss) private var dismiss
    @State private var listing: DirectoryListing?
    @State private var path = ""
    @State private var selectedPaths: Set<String> = []
    @State private var archiveBase = ""
    @State private var cleanup = true
    @State private var skipDuplicates = true
    @State private var loading = false
    @State private var submitting = false
    @State private var error: String?
    @State private var loadTask: Task<Void, Never>?
    private var connectionID: UUID? { model.selectedConnectionID }

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 12) {
                Image(systemName: "folder.badge.plus").font(.title2).foregroundStyle(.tint)
                Text("新建归档").font(.title2.weight(.semibold))
                Spacer()
            }.padding(22)
            Divider()
            HStack(spacing: 0) {
                VStack(spacing: 0) {
                    HStack(spacing: 10) {
                        Button("来源目录", systemImage: "house") { load("") }
                            .labelStyle(.iconOnly).buttonStyle(.glass)
                            .help("查看服务端允许的来源目录")
                        Button("上一级", systemImage: "arrow.up") { load(listing?.parentPath ?? "") }
                            .labelStyle(.iconOnly).buttonStyle(.glass)
                            .disabled(listing?.path.isEmpty != false || loading)
                        TextField("目录路径", text: $path).textFieldStyle(.roundedBorder).onSubmit { load(path) }
                        Button("前往", systemImage: "arrow.right") { load(path) }
                            .labelStyle(.iconOnly).buttonStyle(.glass)
                    }.padding(14)
                    Divider()
                    if loading {
                        ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
                    } else if let listing, listing.entries.isEmpty {
                        Text(listing.path.isEmpty ? "没有可用的来源目录" : "没有子文件夹")
                            .font(.callout).foregroundStyle(.secondary)
                            .frame(maxWidth: .infinity, maxHeight: .infinity)
                    } else {
                        List(listing?.entries ?? []) { directory in
                            HStack(spacing: 10) {
                                Toggle(isOn: Binding(
                                    get: { selectedPaths.contains(directory.path) },
                                    set: { value in
                                        if value != selectedPaths.contains(directory.path) { toggle(directory.path) }
                                    }
                                )) { Label(directory.name, systemImage: "folder").lineLimit(1) }
                                    .toggleStyle(.checkbox)
                                    .disabled(directory.selectable == false)
                                    .accessibilityLabel("选择 \(directory.name)")
                                    .help(directory.selectable == false ? "请打开子目录选择来源。" : directory.path)
                                Spacer()
                                Button("打开 \(directory.name)", systemImage: "chevron.right") { load(directory.path) }
                                    .labelStyle(.iconOnly).buttonStyle(.plain).foregroundStyle(.secondary)
                            }.padding(.vertical, 6).accessibilityElement(children: .contain)
                        }.listStyle(.inset)
                    }
                    if let listing, !listing.path.isEmpty {
                        Divider()
                        HStack {
                            Spacer()
                            Button(selectedPaths.contains(listing.path) ? "取消选择" : "选择此文件夹") { toggle(listing.path) }
                                .buttonStyle(.glass).controlSize(.small)
                                .disabled(listing.selectable == false || loading)
                        }.padding(12)
                    }
                }
                Divider()
                VStack(alignment: .leading, spacing: 14) {
                    HStack {
                        Text("已选来源").font(.headline)
                        if !selectedPaths.isEmpty { Text("\(selectedPaths.count)").font(.caption).foregroundStyle(.secondary) }
                    }
                    if selectedPaths.isEmpty {
                        Text("在左侧选择文件夹").font(.callout).foregroundStyle(.secondary)
                    }
                    ScrollView {
                        VStack(alignment: .leading, spacing: 12) {
                            ForEach(selectedPaths.sorted(), id: \.self) { selected in
                                HStack(alignment: .top, spacing: 8) {
                                    Image(systemName: "folder").foregroundStyle(.secondary)
                                    Text(displayRemotePath(selected)).font(.caption).lineLimit(3).help(selected)
                                    Spacer(minLength: 0)
                                    Button("移除", systemImage: "xmark.circle.fill") { selectedPaths.remove(selected) }
                                        .labelStyle(.iconOnly).buttonStyle(.plain).foregroundStyle(.secondary)
                                }
                            }
                        }.frame(maxWidth: .infinity, alignment: .leading)
                    }
                    Divider()
                    Text("归档到").font(.caption).foregroundStyle(.secondary)
                    TextField("归档位置", text: $archiveBase).textFieldStyle(.roundedBorder)
                        .onSubmit { load(listing?.path ?? "") }
                    Toggle("跳过重复（保留来源）", isOn: $skipDuplicates)
                        .help("逐字节确认内容相同后跳过，不再复制，来源保留。")
                    Toggle("清理空文件夹", isOn: $cleanup)
                        .help("完成后只移除空文件夹，其他内容保留。")
                }.font(.callout).padding(20).frame(width: 280)
            }
            if let error { StatusBanner(message: error, systemImage: "exclamationmark.triangle", isError: true) }
            Divider()
            HStack {
                Spacer()
                Button("取消") { dismiss() }.buttonStyle(.glass).keyboardShortcut(.cancelAction)
                Button(submitting ? "正在创建…" : "扫描预览") { Task { await create() } }
                    .keyboardShortcut(.defaultAction).buttonStyle(.glassProminent)
                    .help("只扫描并生成预览，不会移动文件。")
                    .disabled(selectedPaths.isEmpty || archiveBase.isEmpty || submitting || !model.isConnected)
            }.padding(18)
        }
        .frame(width: 900, height: 560)
        .onAppear {
            archiveBase = displayRemotePath(model.serverInfo?.archiveBase ?? "")
            load("", enterOnlyRoot: true)
        }
        .onDisappear { loadTask?.cancel() }
        .onChange(of: connectionID) { _, _ in dismiss() }
    }

    private func toggle(_ value: String) {
        if selectedPaths.contains(value) { selectedPaths.remove(value); return }
        selectedPaths = selectedPaths.filter { !$0.hasPrefix(value + "/") }
        guard !selectedPaths.contains(where: { value.hasPrefix($0 + "/") }) else { return }
        selectedPaths.insert(value)
    }
    private func load(_ value: String, enterOnlyRoot: Bool = false) {
        loadTask?.cancel(); loading = true; error = nil
        let destination = archiveBase
        loadTask = Task {
            do {
                let api = try model.api()
                var result = try await api.browseSources(value, archiveBase: destination)
                try Task.checkCancellation()
                if enterOnlyRoot && result.path.isEmpty && result.entries.count == 1 {
                    result = try await api.browseSources(result.entries[0].path, archiveBase: destination)
                    try Task.checkCancellation()
                }
                listing = result; path = displayRemotePath(result.path); loading = false
            } catch is CancellationError {} catch {
                guard !Task.isCancelled else { return }
                self.error = presentableError(error); loading = false
            }
        }
    }
    private func create() async {
        submitting = true; error = nil
        defer { submitting = false }
        do { try await model.createJob(paths: Array(selectedPaths), archiveBase: archiveBase, cleanup: cleanup, skipDuplicates: skipDuplicates); dismiss() }
        catch { self.error = presentableError(error) }
    }
}
