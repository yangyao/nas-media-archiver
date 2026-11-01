import SwiftUI

struct ArchiveSettings: Codable, Sendable {
    var configured: Bool
    var sourceRoots: [String]
    var archiveBase: String
}

struct ArchiveSettingsView: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.dismiss) private var dismiss
    @State private var sources: [String] = []
    @State private var archive = ""
    @State private var choosingSource: Bool?
    @State private var listing: DirectoryListing?
    @State private var path = ""
    @State private var error: String?
    @State private var busy = false
    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            Text("归档设置").font(.title2.weight(.semibold))
            if let selecting = choosingSource {
                HStack {
                    Button("根目录", systemImage: "house") { Task { await browse("") } }.labelStyle(.iconOnly)
                    TextField("服务端路径", text: $path).onSubmit { Task { await browse(path) } }
                    Button("前往") { Task { await browse(path) } }
                }
                List(listing?.entries ?? []) { entry in
                    Button { Task { await browse(entry.path) } } label: {
                        HStack { Label(entry.name, systemImage: "folder"); Spacer(); Image(systemName: "chevron.right") }
                    }.buttonStyle(.plain)
                }
                HStack {
                    Button("返回") { choosingSource = nil }
                    Spacer()
                    Button(selecting ? "添加来源" : "选择归档位置") {
                        guard let p = listing?.path, !p.isEmpty else { return }
                        if selecting { if !sources.contains(p) { sources.append(p) } } else { archive = p }
                        choosingSource = nil
                    }.disabled(listing?.path.isEmpty != false || busy)
                }
            } else {
                Text("来源目录").font(.headline)
                List {
                    ForEach(sources, id: \.self) { source in
                        HStack {
                            Text(displayRemotePath(source)).textSelection(.enabled)
                            Spacer()
                            Button("移除", systemImage: "minus.circle") { sources.removeAll { $0 == source } }.labelStyle(.iconOnly).buttonStyle(.plain)
                        }
                    }
                }.frame(minHeight: 80)
                Button("添加来源…", systemImage: "plus") { choose(true) }
                Text("归档位置").font(.headline)
                HStack {
                    Text(archive.isEmpty ? "尚未选择" : displayRemotePath(archive)).foregroundStyle(archive.isEmpty ? .secondary : .primary).textSelection(.enabled)
                    Spacer()
                    Button("选择…") { choose(false) }
                }
                Text("设置保存在服务端，对所有客户端生效。").font(.caption).foregroundStyle(.secondary)
                Spacer()
                HStack {
                    Button("取消") { dismiss() }.buttonStyle(.glass).keyboardShortcut(.cancelAction)
                    Spacer()
                    Button(busy ? "正在保存…" : "保存") { Task { await save() } }
                        .buttonStyle(.glassProminent).disabled(busy || sources.isEmpty || archive.isEmpty)
                }
            }
            if let error { Text(error).foregroundStyle(.red).font(.callout) }
        }.padding(24).frame(width: 640, height: 480)
        .interactiveDismissDisabled(busy)
        .task {
            busy = true
            defer { busy = false }
            do {
                let value = try await model.api().archiveSettings()
                sources = value.configured ? value.sourceRoots : []
                archive = value.archiveBase
            } catch { self.error = presentableError(error) }
        }
    }
    private func choose(_ source: Bool) {
        choosingSource = source
        Task { await browse("") }
    }
    private func browse(_ value: String) async {
        busy = true; error = nil
        defer { busy = false }
        do { listing = try await model.api().browse(value); path = listing?.path ?? "" }
        catch { self.error = presentableError(error) }
    }
    private func save() async {
        busy = true; error = nil
        defer { busy = false }
        do {
            _ = try await model.api().saveArchiveSettings(.init(configured: true, sourceRoots: sources, archiveBase: archive))
            await model.refresh()
            dismiss()
        } catch { self.error = presentableError(error) }
    }
}
