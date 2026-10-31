# Version 0.2.0 validation

Validated on 2026-10-31 using Go 1.20.5 and Xcode 27.1 / Swift 6.4 on an Apple Silicon Mac. The QNAP target was a TS-216 running Linux aarch64.

## Automated checks

- `go test -race ./...`: CLI preview-to-run regression, authentication, allowed-root/symlink containment, duplicate preservation, unsupported residuals, state-directory locking, incomplete scan recovery, published-file corruption/recovery, retry journal preservation, job claim checks, queued pause/resume, and active pause at a verified file boundary.
- `go vet ./...` and `git diff --check`: passed.
- `make smoke`: a real local HTTP service exercised auth, directory browsing, async preview, SSE, archive, SHA-256 records, no-overwrite collisions, unsupported-file preservation and empty-directory cleanup.
- `swift test --package-path desktop`: thirteen native-client tests passed, covering URL/auth/error handling, progress, unsupported files, partial scans, fragmented UTF-8/CRLF SSE framing and a connected stream without job events.
- `packaging/qnap/test.sh`: shell syntax, architecture metadata and install/upgrade data-preservation guards passed.

## Runtime checks

The packaged SwiftUI app was launched and operated through its native UI. A local fixture service verified connection testing and app-local password persistence across quit/relaunch, remote folder browsing, preview, start, live completion updates, history, and residual-folder reporting. The final visible connection UI uses **服務端** and distinguishes archive-service URLs from QTS and SSH. Test connection credentials were removed afterward.

The Linux ARM64 binary ran directly on the QNAP. A disposable fixture was copied from the SSD volume to the home/archive volume, verified by SHA-256, removed from the source, and its empty source folder cleaned. The SQLite job journal reported completion. This test service was then stopped; existing photo archives were not part of these development tests.

A real QPKG was built on the QNAP using a pinned official QDK checkout in a build-only home directory. QDK query/extract validation passed; the extracted binary and init script matched their inputs. The final extracted binary's MD5 was `e00125cbfe91c371f7742a88ea103525` (also matched locally). This checksum comparison is a build-payload consistency check; the service's media verification uses SHA-256.

## Artifacts and release limits

- `desktop/dist/NASArchiver.app`: macOS 26+, host architecture ARM64, ad-hoc signed and signature verified.
- `dist/NASArchiver_0.2.0_macos-arm64.zip`: ZIP of the app bundle.
- `dist/qnap/NASMediaArchiver_0.2.0_arm_64.qpkg`: real QDK-generated development installer.
- `dist/qnap/SHA256SUMS`: current QPKG checksum.

The QPKG has not been installed/registered in App Center as part of this validation; its extracted service binary was tested on the target NAS. Fresh package installation is configured disabled until an administrator reviews allowed paths and enables it. App Center installation/lifecycle integration remains an installation acceptance check. The Mac build is not Developer ID signed/notarized, and the QPKG is not QNAP signed. Public distribution signing is a separate release step.

## Connection simplification

The client no longer calls Keychain APIs. Server tokens are presented as connection passwords and are stored in an app-owned Application Support directory with `0700` directory / `0600` file permissions. Tests cover persistence after reopening, update/removal isolation, permissions, and invalid local data. Existing Keychain entries are deliberately not accessed during migration; users re-enter their server token once.

First-run connection is a required sheet: no cancel action or interactive dismissal until authenticated connection and initial job retrieval succeed. Previously configured servers reconnect automatically; a failed startup connection opens the editor for correction.

Connection-flow tests additionally cover wrong-password rejection, authenticated connection plus initial task loading before completion, and initial task-load failures. These checks passed. The first-run mandatory-sheet changes compile, but UI capture for that final iteration was unavailable (`cgWindowNotFound`); Escape/dismissal behavior has not been visually rechecked in that iteration.
