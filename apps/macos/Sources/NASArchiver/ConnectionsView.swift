import SwiftUI
import UserNotifications

struct ConnectionEditorView: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.dismiss) private var dismiss
    var connection: NASConnection?
    @State private var name = "我的归档服务"
    @State private var address = "http://"
    @State private var token = ""
    @State private var error: String?
    @State private var confirmRemoval = false
    @State private var testing = false
    @State private var saving = false
    @State private var testResult: String?
    @State private var showArchiveSettings = false

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Image(systemName: "server.rack").font(.title2).foregroundStyle(.tint)
                Text("连接设置").font(.title2.weight(.semibold))
                Spacer()
            }.padding(24)
            Form {
                Section("连接") {
                    TextField("服务地址", text: $address, prompt: Text("http://192.168.2.196:8787"))
                        .autocorrectionDisabled()
                    SecureField("连接密码", text: $token, prompt: Text("服务端提供的 Token"))
                        .help("填写归档服务的 Token，密码保存在本机。")
                    HStack {
                        Button(testing ? "正在测试…" : "测试连接") { Task { await testConnection() } }.disabled(testing || saving)
                        if let testResult { Text(testResult).font(.caption).foregroundStyle(.green) }
                    }
                }
                if connection?.id == model.selectedConnectionID, let info = model.serverInfo {
                    Section("服务信息") {
                        LabeledContent("归档根目录", value: displayRemotePath(info.archiveBase)).textSelection(.enabled)
                        LabeledContent("分类规则", value: "照片 / PNG / 视频 · 年 / 月")
                        LabeledContent("服务版本", value: info.version)
                        LabeledContent("可用空间", value: formattedBytes(info.freeBytes))
                        Button("归档目录设置…") { showArchiveSettings = true }
                    }
                }
                if let error { Text(error).foregroundStyle(.red).font(.callout).textSelection(.enabled) }
            }.formStyle(.grouped)
            Divider()
            HStack {
                if connection != nil && model.isConnected {
                    Button("移除连接…", role: .destructive) { confirmRemoval = true }
                        .disabled(saving)
                }
                Spacer()
                if model.isConnected {
                    Button("取消") { dismiss() }.buttonStyle(.glass).keyboardShortcut(.cancelAction).disabled(saving)
                }
                Button(saving ? "正在连接…" : "连接") { Task { await save() } }
                    .buttonStyle(.glassProminent).keyboardShortcut(.defaultAction)
                    .disabled(name.trimmingCharacters(in: .whitespaces).isEmpty || token.isEmpty || testing || saving)
            }.padding(20)
        }
        .frame(width: 580, height: connection == nil ? 460 : 620)
        .sheet(isPresented: $showArchiveSettings) { ArchiveSettingsView().environmentObject(model) }
        .interactiveDismissDisabled(!model.isConnected || saving)
        .onAppear {
            if let connection {
                name = connection.name; address = connection.address
                do { token = try TokenStore.token(for: connection.id) } catch { self.error = presentableError(error) }
            }
        }
        .confirmationDialog("移除这个服务端连接？", isPresented: $confirmRemoval) {
            Button("移除连接", role: .destructive) {
                guard let connection else { return }
                do { try model.removeConnection(connection); dismiss() } catch { self.error = presentableError(error) }
            }
        } message: { Text("只移除 Mac 上保存的连接和密码，服务端上的任务继续运行。") }
    }

    private func save() async {
        guard !saving else { return }
        saving = true; error = nil
        defer { saving = false }
        do {
            let value = NASConnection(id: connection?.id ?? model.selectedConnectionID ?? UUID(), name: name.trimmingCharacters(in: .whitespacesAndNewlines), address: address.trimmingCharacters(in: .whitespacesAndNewlines))
            try await model.saveConnection(value, token: token)
            dismiss()
        } catch { self.error = presentableError(error) }
    }
    private func testConnection() async {
        testing = true; error = nil; testResult = nil
        defer { testing = false }
        do {
            let api = try APIClient(address: address, token: token)
            let info = try await api.info()
            testResult = "已连接到 \(info.name) · \(info.version)"
        } catch { self.error = presentableError(error) }
    }
}

struct ClientSettingsView: View {
    @EnvironmentObject private var model: AppModel
    @AppStorage("appearance") private var appearance = "system"
    @AppStorage("completionNotifications") private var completionNotifications = false
    @State private var notificationError: String?
    @State private var showArchiveSettings = false
    var body: some View {
        TabView {
            Form {
                Picker("外观", selection: $appearance) {
                    Text("跟随系统").tag("system")
                    Text("浅色").tag("light")
                    Text("深色").tag("dark")
                }
                Toggle("归档完成时发送通知", isOn: $completionNotifications)
                    .onChange(of: completionNotifications) { _, enabled in
                        guard enabled else { return }
                        Task {
                            do {
                                let granted = try await UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound])
                                if !granted { completionNotifications = false; notificationError = "请在系统设置中允许 NAS Archiver 发送通知。" }
                            } catch { completionNotifications = false; notificationError = presentableError(error) }
                        }
                    }
                if let notificationError { Text(notificationError).font(.caption).foregroundStyle(.orange) }
                Button("归档目录设置…") { showArchiveSettings = true }.disabled(!model.isConnected)
                Text("退出 Mac 应用后，服务端上的归档任务仍会继续。")
                    .font(.caption).foregroundStyle(.secondary)
            }.formStyle(.grouped).tabItem { Label("通用", systemImage: "gearshape") }
            VStack(alignment: .leading, spacing: 16) {
                Text("服务端连接").font(.headline)
                Text("点击主窗口左下角的设置，即可修改归档服务的连接地址与密码。")
                    .foregroundStyle(.secondary)
                if let connection = model.selectedConnection {
                    LabeledContent("服务地址", value: connection.address).font(.callout)
                    LabeledContent("状态", value: model.connectionLabel).font(.callout)
                } else {
                    Text("尚未设置连接").font(.callout).foregroundStyle(.secondary)
                }
                Spacer()
            }.padding(24).tabItem { Label("连接", systemImage: "network") }
        }
        .sheet(isPresented: $showArchiveSettings) { ArchiveSettingsView().environmentObject(model) }
        .frame(width: 560, height: 340)
        .padding(12)
    }
}
