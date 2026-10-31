# NAS Archiver for macOS

Native SwiftUI client for the QNAP archiver service. Requires macOS 26 or newer and Xcode 26+ / Swift 6. Uses the native split-view toolbar and Liquid Glass controls; no web view or frontend runtime.

```sh
swift test --package-path desktop
desktop/scripts/package-macos.sh
open desktop/dist/NASArchiver.app
```

Copy the resulting `NASArchiver.app` to `/Applications` to install. Packaging produces an ad-hoc signed local build. Set `CODE_SIGN_IDENTITY` to a Developer ID identity for your own distribution; notarization is a separate release step. The default build targets the host architecture.

## First connection

The client connects to the **archive server**, not to a NAS appliance, QTS, or SSH. The server can run on a QNAP NAS; its host device is distinct from the saved service connection.

1. Start/install the QNAP service and obtain its generated bearer token through your SSH/admin connection.
2. Click the connection settings entry at the bottom left.
3. Enter the archive-service URL (for example `http://192.168.2.196:8787`, not the QTS admin URL), and the token.
4. Test the connection and save. Tokens are stored in macOS Keychain; the address is in UserDefaults.
5. Choose **新增歸檔**. The folder picker browses server-side directories through the service API, never the Mac's local filesystem. Select one or more folders, review the scan, then start archiving.

Tasks continue on the NAS when this app closes. Real-time updates use authenticated SSE. If streaming fails, an explicit banner explains polling/reconnection; jobs refresh every three seconds during fallback. The app provides file filtering/pagination, pause/resume, failure retry, residual-folder information, and empty-directory cleanup. Client appearance/notification preferences live in native Settings (`⌘,`). The app presents one archive server; its connection settings and a quiet status indicator live at the bottom of the task sidebar. The two-column window groups active and historical jobs directly in one compact sidebar, with selected-job details on the right.

## Transport and permissions

The app explicitly supports the service's private-LAN HTTP endpoint, including numeric IP addresses. Its bundle declares `NSAllowsArbitraryLoads` and `NSAllowsLocalNetworking`, plus a local-network usage description. HTTP carries the bearer token without transport encryption; use an HTTPS endpoint or a trusted private network. URLs containing embedded credentials or query-string tokens are rejected. The app has no local file access requirement. Notification permission is requested only when completion notifications are enabled in Settings.

Do not embed tokens in this repository or a signed application bundle. There are no default connections, tokens, or demo jobs.
