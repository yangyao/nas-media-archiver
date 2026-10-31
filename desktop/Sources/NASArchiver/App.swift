import AppKit
import SwiftUI

@main
struct NASArchiverApp: App {
    @StateObject private var model = AppModel()
    @AppStorage("appearance") private var appearance = "system"
    var body: some Scene {
        WindowGroup {
            ContentView()
                .environmentObject(model)
                .preferredColorScheme(appearance == "light" ? .light : (appearance == "dark" ? .dark : nil))
                .frame(minWidth: 1000, minHeight: 680)
        }
        .windowStyle(.titleBar)
        .windowToolbarStyle(.unified)
        .defaultSize(width: 1220, height: 800)
        .commands {
            CommandGroup(after: .newItem) {
                Button("新增歸檔…") { model.showNewArchive = true }
                    .keyboardShortcut("n", modifiers: .command)
                    .disabled(!model.isConnected)
                Button("刷新") { Task { await model.refresh() } }
                    .keyboardShortcut("r", modifiers: .command)
            }
        }
        Settings { ClientSettingsView().environmentObject(model) }
    }
}
