import SwiftUI
import UserNotifications

struct ConnectionEditorView: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.dismiss) private var dismiss
    var connection: NASConnection?
    @State private var name = "我的歸檔服務"
    @State private var address = "http://"
    @State private var token = ""
    @State private var error: String?
    @State private var confirmRemoval = false
    @State private var testing = false
    @State private var testResult: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Image(systemName: "server.rack").font(.title2).foregroundStyle(.tint)
                Text("連線設定").font(.title2.weight(.semibold))
                Spacer()
            }.padding(24)
            Form {
                Section("連線") {
                    TextField("服務地址", text: $address, prompt: Text("http://192.168.2.196:8787"))
                        .autocorrectionDisabled()
                    SecureField("連線權杖", text: $token)
                    Text("請填寫歸檔服務端的 HTTP / HTTPS 地址，不是 QTS 或 SSH 登入地址。服務端可安裝在 NAS 上，權杖保存在 macOS 鑰匙串中。")
                        .font(.caption).foregroundStyle(.secondary)
                    HStack {
                        Button(testing ? "正在測試…" : "測試連線") { Task { await testConnection() } }.disabled(testing)
                        if let testResult { Text(testResult).font(.caption).foregroundStyle(.green) }
                    }
                }
                if connection?.id == model.selectedConnectionID, let info = model.serverInfo {
                    Section("服務資訊") {
                        LabeledContent("歸檔根目錄", value: info.archiveBase).textSelection(.enabled)
                        LabeledContent("分類規則", value: "照片 / PNG / 影片 · 年 / 月")
                        LabeledContent("服務版本", value: info.version)
                        LabeledContent("可用空間", value: formattedBytes(info.freeBytes))
                        Text("歸檔規則由服務端管理；新建任務時可選擇歸檔位置。")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                }
                if let error { Text(error).foregroundStyle(.red).font(.callout).textSelection(.enabled) }
            }.formStyle(.grouped)
            Divider()
            HStack {
                if connection != nil {
                    Button("移除連線…", role: .destructive) { confirmRemoval = true }
                }
                Spacer()
                Button("取消") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("保存並連線") { save() }.keyboardShortcut(.defaultAction)
                    .disabled(name.trimmingCharacters(in: .whitespaces).isEmpty || token.isEmpty || testing)
            }.padding(20)
        }
        .frame(width: 580, height: connection == nil ? 460 : 620)
        .onAppear {
            if let connection {
                name = connection.name; address = connection.address
                do { token = try TokenStore.token(for: connection.id) } catch { self.error = presentableError(error) }
            }
        }
        .confirmationDialog("移除這個服務端連線？", isPresented: $confirmRemoval) {
            Button("移除連線", role: .destructive) {
                guard let connection else { return }
                do { try model.removeConnection(connection); dismiss() } catch { self.error = presentableError(error) }
            }
        } message: { Text("只移除 Mac 上保存的連線和權杖，服務端上的任務繼續運行。") }
    }

    private func save() {
        do {
            let value = NASConnection(id: connection?.id ?? UUID(), name: name.trimmingCharacters(in: .whitespacesAndNewlines), address: address.trimmingCharacters(in: .whitespacesAndNewlines))
            try model.saveConnection(value, token: token); dismiss()
        } catch { self.error = presentableError(error) }
    }
    private func testConnection() async {
        testing = true; error = nil; testResult = nil
        defer { testing = false }
        do {
            let api = try APIClient(address: address, token: token)
            let info = try await api.info()
            testResult = "已連線到 \(info.name) · \(info.version)"
        } catch { self.error = presentableError(error) }
    }
}

struct ClientSettingsView: View {
    @EnvironmentObject private var model: AppModel
    @AppStorage("appearance") private var appearance = "system"
    @AppStorage("completionNotifications") private var completionNotifications = false
    @State private var notificationError: String?
    var body: some View {
        TabView {
            Form {
                Picker("外觀", selection: $appearance) {
                    Text("跟隨系統").tag("system")
                    Text("淺色").tag("light")
                    Text("深色").tag("dark")
                }
                Toggle("歸檔完成時發送通知", isOn: $completionNotifications)
                    .onChange(of: completionNotifications) { _, enabled in
                        guard enabled else { return }
                        Task {
                            do {
                                let granted = try await UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound])
                                if !granted { completionNotifications = false; notificationError = "請在系統設定中允許 NAS Archiver 發送通知。" }
                            } catch { completionNotifications = false; notificationError = presentableError(error) }
                        }
                    }
                if let notificationError { Text(notificationError).font(.caption).foregroundStyle(.orange) }
                Text("退出 Mac 應用後，服務端上的歸檔任務仍會繼續。")
                    .font(.caption).foregroundStyle(.secondary)
            }.formStyle(.grouped).tabItem { Label("通用", systemImage: "gearshape") }
            VStack(alignment: .leading, spacing: 16) {
                Text("服務端連線").font(.headline)
                Text("點擊主視窗左下角的設定，即可修改歸檔服務的連線地址與權杖。")
                    .foregroundStyle(.secondary)
                if let connection = model.selectedConnection {
                    LabeledContent("服務地址", value: connection.address).font(.callout)
                    LabeledContent("狀態", value: model.connectionLabel).font(.callout)
                } else {
                    Text("尚未設定連線").font(.callout).foregroundStyle(.secondary)
                }
                Spacer()
            }.padding(24).tabItem { Label("連線", systemImage: "network") }
        }
        .frame(width: 560, height: 300)
        .padding(12)
    }
}
