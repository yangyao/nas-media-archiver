import AppKit
import SwiftUI

@main
struct NASArchiverApp: App {
    @NSApplicationDelegateAdaptor(ArchiverApplicationDelegate.self) private var appDelegate
    @StateObject private var model = AppModel()
    @AppStorage("appearance") private var appearance = "system"
    var body: some Scene {
        WindowGroup {
            ContentView()
                .environmentObject(model)
                .environment(\.locale, Locale(identifier: "zh_Hans_CN"))
                .preferredColorScheme(appearance == "light" ? .light : (appearance == "dark" ? .dark : nil))
                .frame(minWidth: 860, minHeight: 620)
        }
        .windowStyle(.titleBar)
        .windowToolbarStyle(.unified)
        .defaultSize(width: 1080, height: 760)
        .commands {
            CommandGroup(after: .newItem) {
                Button("新建归档…") { model.showNewArchive = true }
                    .keyboardShortcut("n", modifiers: .command)
                    .disabled(!model.isConnected)
                Button("刷新") { Task { await model.refresh() } }
                    .keyboardShortcut("r", modifiers: .command)
            }
        }
        Settings { ClientSettingsView().environmentObject(model).environment(\.locale, Locale(identifier: "zh_Hans_CN")) }
    }
}

@MainActor
final class ArchiverApplicationDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        // Load the actual bundle resource rather than a cached Launch Services
        // icon associated with an earlier development build at this path.
        if let url = Bundle.main.url(forResource: "AppIcon", withExtension: "icns"),
           let icon = NSImage(contentsOf: url) {
            NSApplication.shared.applicationIconImage = icon
        }
    }
}
