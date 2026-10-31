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
            HStack(spacing: 13) {
                Image(systemName: "folder.badge.plus").font(.title2).foregroundStyle(.tint)
                VStack(alignment: .leading, spacing: 5) {
                    Text("新增歸檔").font(.title2.weight(.semibold))
                    Text("選擇資料夾，先掃描預覽。")
                        .foregroundStyle(.secondary)
                }
                Spacer()
            }.padding(24)
            Divider()
            HStack(spacing: 0) {
                VStack(alignment: .leading, spacing: 0) {
                    HStack {
                        Button("根目錄", systemImage: "house") { load("") }.labelStyle(.iconOnly)
                        Button("上一級", systemImage: "arrow.up") { load(listing?.parentPath ?? "") }
                            .labelStyle(.iconOnly).disabled(listing?.path.isEmpty != false || loading)
                        TextField("服務端路徑", text: $path).textFieldStyle(.roundedBorder).onSubmit { load(path) }
                        Button("前往", systemImage: "arrow.right") { load(path) }.labelStyle(.iconOnly)
                    }.padding(14)
                    Divider()
                    if loading {
                        Spacer(); HStack { Spacer(); ProgressView("讀取目錄…"); Spacer() }; Spacer()
                    } else if let listing, listing.entries.isEmpty {
                        ContentUnavailableView("沒有子資料夾", systemImage: "folder", description: Text("可以選擇當前資料夾，或返回上一級。"))
                    } else {
                        List(listing?.entries ?? []) { directory in
                            HStack(spacing: 10) {
                                Toggle(isOn: Binding(
                                    get: { selectedPaths.contains(directory.path) },
                                    set: { value in
                                        if value != selectedPaths.contains(directory.path) { toggle(directory.path) }
                                    }
                                )) {
                                    Label(directory.name, systemImage: "folder.fill").lineLimit(1)
                                }
                                .toggleStyle(.checkbox)
                                .accessibilityLabel("選擇 \(directory.name)")
                                Spacer()
                                Button("開啟 \(directory.name)", systemImage: "chevron.right") { load(directory.path) }
                                    .labelStyle(.iconOnly)
                                    .help("開啟 \(directory.name)")
                            }
                            .padding(.vertical, 5)
                            .accessibilityElement(children: .contain)
                        }.listStyle(.inset)
                    }
                    Divider()
                    HStack {
                        Text(listing?.path.isEmpty == false ? listing!.path : "允許存取的 服務端目錄")
                            .font(.caption).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                        Spacer()
                        if let listing, !listing.path.isEmpty {
                            Button(selectedPaths.contains(listing.path) ? "取消選擇當前目錄" : "選擇當前目錄") { toggle(listing.path) }
                        }
                    }.padding(12)
                }
                Divider()
                VStack(alignment: .leading, spacing: 12) {
                    Text("已選擇 \(selectedPaths.count) 個資料夾").font(.headline)
                    if selectedPaths.isEmpty {
                        Text("可選擇多個批次。嵌套的資料夾會合併爲最上層目錄。")
                            .font(.callout).foregroundStyle(.secondary)
                    }
                    ScrollView {
                        VStack(alignment: .leading, spacing: 12) {
                            ForEach(selectedPaths.sorted(), id: \.self) { selected in
                                HStack(alignment: .top) {
                                    Image(systemName: "folder")
                                    Text(selected).font(.caption).textSelection(.enabled)
                                    Spacer(minLength: 0)
                                    Button("移除", systemImage: "xmark.circle.fill") { selectedPaths.remove(selected) }
                                        .labelStyle(.iconOnly).buttonStyle(.plain).foregroundStyle(.secondary)
                                }
                            }
                        }
                    }
                    Divider()
                    Text("歸檔位置").font(.headline)
                    TextField("歸檔根目錄", text: $archiveBase).textFieldStyle(.roundedBorder)
                    VStack(alignment: .leading, spacing: 5) {
                        Label("照片 → photos / 年 / 月", systemImage: "photo")
                        Label("PNG → pngs / 年 / 月", systemImage: "photo.on.rectangle")
                        Label("影片 → videos / 年 / 月", systemImage: "film")
                    }.font(.caption).foregroundStyle(.secondary)
                    Toggle("略過重複檔案", isOn: $skipDuplicates).font(.callout)
                    Text("內容相同時保留來源，不再複製。").font(.caption).foregroundStyle(.secondary)
                    Toggle("完成後清理空目錄", isOn: $cleanup).font(.callout)
                    Text("只移除空目錄；不支援或失敗的檔案會保留。")
                        .font(.caption).foregroundStyle(.secondary)
                }.padding(20).frame(width: 280)
            }
            if let error { StatusBanner(message: error, systemImage: "exclamationmark.triangle", isError: true) }
            Divider()
            HStack {
                Label("掃描不會移動檔案", systemImage: "checkmark.shield").font(.caption).foregroundStyle(.secondary)
                Spacer()
                Button("取消") { dismiss() }.keyboardShortcut(.cancelAction)
                Button(submitting ? "正在建立…" : "掃描預覽") { Task { await create() } }
                    .keyboardShortcut(.defaultAction).buttonStyle(.glassProminent)
                    .disabled(selectedPaths.isEmpty || archiveBase.isEmpty || submitting || !model.isConnected)
            }.padding(20)
        }
        .frame(width: 920, height: 650)
        .onAppear { archiveBase = model.serverInfo?.archiveBase ?? ""; load("") }
        .onDisappear { loadTask?.cancel() }
        .onChange(of: connectionID) { _, _ in dismiss() }
    }

    private func toggle(_ value: String) {
        if selectedPaths.contains(value) { selectedPaths.remove(value); return }
        selectedPaths = selectedPaths.filter { !$0.hasPrefix(value + "/") }
        guard !selectedPaths.contains(where: { value.hasPrefix($0 + "/") }) else { return }
        selectedPaths.insert(value)
    }
    private func load(_ value: String) {
        loadTask?.cancel(); loading = true; error = nil
        loadTask = Task {
            do {
                let result = try await model.api().browse(value)
                try Task.checkCancellation()
                listing = result; path = result.path; loading = false
            } catch is CancellationError {} catch { guard !Task.isCancelled else { return }; self.error = presentableError(error); loading = false }
        }
    }
    private func create() async {
        submitting = true; error = nil
        defer { submitting = false }
        do { try await model.createJob(paths: Array(selectedPaths), archiveBase: archiveBase, cleanup: cleanup, skipDuplicates: skipDuplicates); dismiss() }
        catch { self.error = presentableError(error) }
    }
}
