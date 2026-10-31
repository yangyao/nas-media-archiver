# NAS Archiver for macOS

Native SwiftUI client for the QNAP archiver service. Requires macOS 26 or newer and Xcode 26+ / Swift 6. Uses the native split-view toolbar and Liquid Glass controls; no web view or frontend runtime.

```sh
swift test --package-path apps/macos
apps/macos/scripts/package-macos.sh
open apps/macos/dist/NASArchiver.app
```

Copy the resulting `NASArchiver.app` to `/Applications` to install. Packaging prefers an available Apple-issued signing identity, with ad-hoc signing as a CI fallback. Set `CODE_SIGN_IDENTITY` to choose a specific identity for distribution; notarization is a separate release step. The default build targets the host architecture.

## First connection

The client connects to the **archive server**, not to a NAS appliance, QTS, or SSH. The server can run on a QNAP NAS; its host device is distinct from the saved service connection.

1. Start/install the QNAP service and obtain its generated bearer token through your SSH/admin connection.
2. On first launch the connection window opens automatically. It stays open until the service connection succeeds; there is no Cancel action. Later launches reconnect using the saved address/password. If automatic startup connection fails, the connection window reopens for correction.
3. Enter the archive-service URL (for example `http://192.168.2.196:8787`, not the QTS admin URL), and the server token in **連線密碼**.
4. Click **連線**. The window closes only after authentication and the initial task load succeed. The password is saved in the app's local credentials directory; the address is in UserDefaults. The app never accesses Keychain.
5. If the server has no tasks, **新增歸檔** opens automatically after the connection window finishes closing (or immediately after automatic reconnection). The window can always be canceled or dismissed with Escape, including when the service is unreachable. Automatic onboarding is offered once per server per app session, so canceling it does not cause polling, refresh or reconnection to reopen it. To continue, select one or more server-side folders and submit **掃描預覽**. The scan task then appears in the main window; starting the actual archive remains a separate action. If any active or historical tasks already exist, onboarding is skipped and future archive dialogs can be canceled. The folder picker browses through the service API, never the Mac's local filesystem.

Tasks continue on the NAS when this app closes. Real-time updates use authenticated SSE. If streaming fails, an explicit banner explains polling/reconnection; jobs refresh every three seconds during fallback. The app provides file filtering/pagination, pause/resume, failure retry, residual-folder information, and empty-directory cleanup. Client appearance/notification preferences live in native Settings (`⌘,`). The app presents one archive server; its connection settings and a quiet status indicator live at the bottom of the task sidebar. The two-column window groups active and historical jobs directly in one compact sidebar, with selected-job details on the right.

## Transport and permissions

The app explicitly supports the service's private-LAN HTTP endpoint, including numeric IP addresses. Its bundle declares `NSAllowsArbitraryLoads` and `NSAllowsLocalNetworking`, plus a local-network usage description. HTTP carries the bearer token without transport encryption; use an HTTPS endpoint or a trusted private network. URLs containing embedded credentials or query-string tokens are rejected. The app has no local file access requirement. Notification permission is requested only when completion notifications are enabled in Settings.

Do not embed tokens in this repository or a signed application bundle. There are no default connections, tokens, or demo jobs.

## Local connection password

The server token remains the HTTP bearer credential; **連線密碼** is its user-facing label, not a Mac login password or a QTS account password. It is saved as plain text under `~/Library/Application Support/NASArchiver/credentials/<connection-id>.token`, in a directory with mode `0700` and a file with mode `0600`. Writes are atomic. This path is stable across local development builds and signing changes. Removing a saved connection removes its local token file.

Previously saved Keychain entries are not read, migrated automatically, or deleted, so upgrading cannot trigger a Keychain authorization prompt. An existing connection needs its server token entered once in the new local store. Credentials are not stored in UserDefaults, build output, source code, or logs.
