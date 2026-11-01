import AppKit
import SwiftUI

struct FolderContentsView: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.dismiss) private var dismiss
    var roots: [String]
    @State private var contents: RemoteContents?
    @State private var currentPath: String
    @State private var loading = false
    @State private var error: String?

    init(roots: [String], initialPath: String? = nil) {
        self.roots = roots
        _currentPath = State(initialValue: initialPath ?? roots.first ?? "")
    }

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Text("服务端内容").font(.title3.weight(.semibold))
                Spacer()
                Button("完成") { dismiss() }.buttonStyle(.glass).keyboardShortcut(.cancelAction)
            }.padding(20)
            HStack(spacing: 12) {
                Button("上一层", systemImage: "arrow.up") {
                    if let parent = contents?.parentPath { currentPath = parent }
                }.labelStyle(.iconOnly).buttonStyle(.glass)
                    .disabled(contents?.parentPath == nil || loading)
                Text(currentPath).font(.caption).lineLimit(2).textSelection(.enabled)
                Spacer(minLength: 0)
                Button("复制路径", systemImage: "document.on.document") { copyRemotePath(currentPath) }
                    .labelStyle(.iconOnly).buttonStyle(.glass)
            }.padding(.horizontal, 20).padding(.bottom, 14)
            if roots.count > 1 {
                Picker("来源", selection: Binding(get: { roots.first { currentPath == $0 || currentPath.hasPrefix($0 + "/") } ?? roots[0] }, set: { currentPath = $0 })) {
                    ForEach(roots, id: \.self) { Text($0).tag($0) }
                }.padding(.horizontal, 20).padding(.bottom, 10)
            }
            Divider()
            if loading {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if let error {
                VStack(spacing: 14) {
                    Text(error).font(.callout).foregroundStyle(.secondary).textSelection(.enabled)
                    Button("重试") { Task { await load() } }.buttonStyle(.glass)
                }.padding(24).frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if contents?.entries.isEmpty == true {
                Text("目录是空的").foregroundStyle(.secondary).frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List(contents?.entries ?? []) { entry in
                    HStack(spacing: 12) {
                        Image(systemName: entry.isDirectory ? "folder" : "doc").foregroundStyle(.secondary)
                        VStack(alignment: .leading, spacing: 4) {
                            Text(entry.name).lineLimit(1)
                            Text(entry.note).font(.caption).foregroundStyle(.secondary)
                        }
                        Spacer()
                        if !entry.isDirectory { Text(formattedBytes(entry.size)).font(.caption).foregroundStyle(.secondary) }
                        if entry.canOpen {
                            Button("开启", systemImage: "chevron.right") { currentPath = entry.path }
                                .labelStyle(.iconOnly).buttonStyle(.plain)
                        }
                        Button("复制路径", systemImage: "document.on.document") { copyRemotePath(entry.path) }
                            .labelStyle(.iconOnly).buttonStyle(.plain).foregroundStyle(.secondary)
                    }.padding(.vertical, 6)
                }.listStyle(.inset)
            }
        }.frame(width: 740, height: 500)
            .task(id: currentPath) { await load() }
    }
    private func load() async {
        let requestedPath = currentPath
        loading = true; error = nil
        defer { if requestedPath == currentPath { loading = false } }
        do {
            let result = try await model.api().contents(requestedPath)
            try Task.checkCancellation()
            guard requestedPath == currentPath else { return }
            contents = result
        } catch is CancellationError {} catch {
            if requestedPath == currentPath { self.error = presentableError(error) }
        }
    }
}

func copyRemotePath(_ path: String) {
    NSPasteboard.general.clearContents()
    NSPasteboard.general.setString(path, forType: .string)
}
