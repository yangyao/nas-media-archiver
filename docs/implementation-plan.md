# Service and native Mac application

## First release

A QNAP-resident service owns all media IO and durable task state. The macOS 26+ SwiftUI application is a remote controller. Quitting the Mac app never terminates an archive task. The existing CLI remains available.

The Mac window uses two columns, with active and historical jobs grouped directly in one native sidebar. It borrows OrbStack's compact list hierarchy and quiet controls without reproducing its additional navigation column. The app presents a single archive server; connection status/settings are a small footer at the bottom left. Global client preferences live in the native Settings window. The client connects to the archive service HTTP endpoint, not QTS or SSH. Source browsing operates on the service's filesystem.

Workflow: select one or more server folders -> asynchronous scan/metadata preview -> review file paths, fallback dates and collisions -> start -> live file and byte progress -> result/retry/empty-directory cleanup. Unsupported media remain in place and are reported. Cleanup uses empty-directory removal only.

## Boundaries

- `archive.go`: existing CLI and metadata/file helpers.
- `server*.go` / `service*.go`: authenticated API, SQLite job store, worker queue, durable copy journal and SSE.
- `desktop/`: Swift package, native views, URLSession API, Keychain tokens and macOS app packaging.
- `packaging/qnap/`: QDK configuration and QPKG service lifecycle.
- `docs/service-api.md`: shared API contract.

Go embeds the lightweight setup/status page. QPKG serves this page from its Open action; it is not a second full client. Bearer authentication protects all API operations. The server resolves allowed roots and source/target paths and prevents overlapping jobs from claiming the same files. Sensitive credentials are never committed or stored in the client preferences.

## Validation

Backend tests use temporary source/archive roots and cover authentication, path containment, preview, execution, pause/retry, safe cleanup and restart recovery. Run Go race tests and Linux ARM64 cross-build. The native client is built with Xcode's Swift toolchain, tested for API decoding and reviewed by launching an app bundle. An independent HTTP smoke test exercises a real running server against temporary media fixtures. QPKG scripts are syntax checked, and package availability/install validation is recorded accurately.

No automatic monitoring or deduplication deletion in this release. Files with matching content remain separate archive entries. No changes to existing historical archives are needed for developing or testing the service.
