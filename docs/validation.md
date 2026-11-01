# Version 0.2.0 validation

Validated on 2026-10-31 using Go 1.20.5 and Xcode 27.1 / Swift 6.4 on an Apple Silicon Mac. The QNAP target was a TS-216 running Linux aarch64.

## Automated checks

- `go test -race ./...`: CLI preview-to-run regression, authentication, allowed-root/symlink containment, duplicate preservation, unsupported residuals, state-directory locking, incomplete scan recovery, published-file corruption/recovery, retry journal preservation, job claim checks, queued pause/resume, and active pause at a verified file boundary.
- `go vet ./...` and `git diff --check`: passed.
- `make smoke`: a real local HTTP service exercised auth, directory browsing, async preview, SSE, archive, SHA-256 records, no-overwrite collisions, unsupported-file preservation and empty-directory cleanup.
- `swift test --package-path apps/macos`: thirteen native-client tests passed, covering URL/auth/error handling, progress, unsupported files, partial scans, fragmented UTF-8/CRLF SSE framing and a connected stream without job events.
- `packaging/qnap/test.sh`: shell syntax, architecture metadata and install/upgrade data-preservation guards passed.

## Runtime checks

The packaged SwiftUI app was launched and operated through its native UI. A local fixture service verified connection testing and app-local password persistence across quit/relaunch, remote folder browsing, preview, start, live completion updates, history, and residual-folder reporting. The final visible connection UI uses **服務端** and distinguishes archive-service URLs from QTS and SSH. Test connection credentials were removed afterward.

The Linux ARM64 binary ran directly on the QNAP. A disposable fixture was copied from the SSD volume to the home/archive volume, verified by SHA-256, removed from the source, and its empty source folder cleaned. The SQLite job journal reported completion. This test service was then stopped; existing photo archives were not part of these development tests.

A real QPKG was built on the QNAP using a pinned official QDK checkout in a build-only home directory. QDK query/extract validation passed; the extracted binary and init script matched their inputs. The final extracted binary's MD5 was `e00125cbfe91c371f7742a88ea103525` (also matched locally). This checksum comparison is a build-payload consistency check; the service's media verification uses SHA-256.

## Artifacts and release limits

- `apps/macos/dist/NASArchiver.app`: macOS 26+, host architecture ARM64, ad-hoc signed and signature verified.
- `dist/NASArchiver_0.2.0_macos-arm64.zip`: ZIP of the app bundle.
- `dist/qnap/NASMediaArchiver_0.2.0_arm_64.qpkg`: real QDK-generated development installer.
- `dist/qnap/SHA256SUMS`: current QPKG checksum.

The QPKG has not been installed/registered in App Center as part of this validation; its extracted service binary was tested on the target NAS. Fresh package installation is configured disabled until an administrator reviews allowed paths and enables it. App Center installation/lifecycle integration remains an installation acceptance check. The Mac build is not Developer ID signed/notarized, and the QPKG is not QNAP signed. Public distribution signing is a separate release step.

## Connection simplification

The client no longer calls Keychain APIs. Server tokens are presented as connection passwords and are stored in an app-owned Application Support directory with `0700` directory / `0600` file permissions. Tests cover persistence after reopening, update/removal isolation, permissions, and invalid local data. Existing Keychain entries are deliberately not accessed during migration; users re-enter their server token once.

First-run connection is a required sheet: no cancel action or interactive dismissal until authenticated connection and initial job retrieval succeed. Previously configured servers reconnect automatically; a failed startup connection opens the editor for correction.

Connection-flow tests additionally cover wrong-password rejection, authenticated connection plus initial task loading before completion, and initial task-load failures. These checks passed. The first-run mandatory-sheet changes compile, but UI capture for that final iteration was unavailable (`cgWindowNotFound`); Escape/dismissal behavior has not been visually rechecked in that iteration.

## Empty-server archive onboarding

After authenticated connection and initial job loading, a server without any active or historical jobs opens the archive-folder sheet automatically. Connection-sheet dismissal is sequenced before the folder sheet. Initial folder setup has a Cancel action and allows interactive dismissal, including during a temporary network outage. The automatic offer is limited to once per connection per app session; refreshing and reconnecting after cancellation do not reopen the dialog. Submitting a valid scan job ends the step. Archive execution still requires the explicit Start action. Existing tasks skip onboarding. Sixteen Swift tests pass, including empty-server presentation, existing active/history bypass, and connection-sheet sequencing.

The connection-to-folder transition was checked against the real NAS without submitting a production scan/archive task. The cancellation regression test now additionally covers refresh and reconnection after canceling the folder picker.

The dismissible-folder update was checked in the native app against the real NAS: Cancel returned to the main window, the footer opened connection settings, closing those settings did not reopen onboarding, and Escape closed a manually opened archive sheet. No source directory was selected and no archive job was created. The suite also checks that a cached disconnected indicator does not block a valid authenticated HTTP client; SSE interruption alone no longer marks the service unavailable.

## Service 0.2.1 optimization

- `go test -race ./...`: passed, including fixed-tick batch limits, coalescing,
  failed-flush retries/reset ordering, incremental counters, persistent ExifTool,
  partial-scan metadata reuse, source-change invalidation and stale-checkpoint
  recovery after source removal.
- `go vet ./...`, real-process smoke test and QNAP packaging checks: passed.
- Linux ARM64 build and real QDK package: version 0.2.1.
- Real QNAP full scan: 7,711 included files in approximately 84 seconds, zero
  failures. No media were moved or deleted. See `benchmarks.md` for methodology,
  cache caveats and durability semantics.
- The previous incomplete preview was recovered using saved metadata; its final
  records match a complete scan using an empty state directory. Both database
  reload and preview consistency were verified.
- QDK on this firmware returned installer exit 10 despite completing the
  upgrade. Registry version, running API version and installed binary digest
  were checked independently before accepting the upgrade.

## Version 0.2.2 duplicate avoidance

The native client offers “略過重複檔案” during creation and on ready/paused task
previews. Identical content is skipped with the source retained, as requested.
Tests cover identical base names, identical suffixed candidates, same-size but
different contents, disabled behavior, duplicates created earlier in the same
job, changed sources, symlinks and option updates at safe boundaries. Swift tests
verify that skipped files complete progress without inflating archived counts,
and that older responses without skip fields still decode.

Deployed 0.2.2 on the real QNAP, verified the installed binary digest against the
local Linux ARM64 build, and enabled duplicate skipping on the existing ready
7,711-file task. A disposable test used a hard link to a real 61,422-byte JPEG as
its source and an isolated archive copy as its candidate: exactly one file was
skipped, zero files archived, the source remained, and the original file's
inode/size/mtime were unchanged. The disposable source/archive directories were
removed and the isolated service stopped. The actual 7,711-file task was not
started. A native URLSession/APIClient probe using saved local credentials also
successfully decoded the running 0.2.2 server and enabled option.

Final client tests: 17 passed. The 0.2.2 app bundle and ZIP are available under
`apps/macos/dist` and `dist`; the real QDK installer is under `dist/qnap-0.2.2` and
was uploaded to the NAS home installers directory.

## Version 0.2.3 task UI and review

The native task header groups capture-date range and source/archive paths below
the title, keeps file/byte progress in one line, and removes redundant status,
zero-failure and attention banners. Attention is accessed through one filter.
Start/resume/pause and secondary actions use native macOS 26 glass button styles.
Completed tasks expose a file/directory browser instead of only residual path
text. The browser shows actual unsupported files and Syncthing marker contents,
with recommendations and copy-path actions.

Attention disclosures explain unsupported formats, mtime fallback, collisions
and failures. Informational warnings can be acknowledged, pending files can have
capture dates corrected, and failed tasks can be retried. QA did not acknowledge
or alter the real task's seven attention records.

- Go race tests and vet passed, including root containment for read-only
  inspection, housekeeping visibility, manual-date replanning/persistence,
  preservation across retry, and rejection of unsafe review states.
- Swift tests: 18 passed. Real-process smoke checks passed for residual listing,
  manual date HTTP requests, warning acknowledgement and unchanged media bytes.
- The QNAP's existing task had naturally finished (7,707 archived, zero failed)
  before upgrading the idle server to 0.2.3. Installed binary digest and API
  version were verified. Actual source leftovers are four GIF files and the
  `.stfolder` marker directory, all visible in the native browser.
- Native UI screenshots verified the final compact header, attention filter,
  explanatory file disclosures and actual residual/system-folder navigation.
- Built-in image_gen created `apps/macos/Assets/AppIcon.png`; its exact prompt is
  stored beside it. Packaging builds all ten ICNS renditions and registers
  `CFBundleIconFile`. No image-generation service is needed during packaging.
- macOS logs showed `Local network prohibited` after replacing an ad hoc build.
  The package script now prefers an available Apple-issued signing identity;
  `CODE_SIGN_IDENTITY` still overrides it and CI can fall back to ad hoc signing.
  The Apple Development-signed build was successfully connected to the real NAS.
  Apple's guidance: https://developer.apple.com/documentation/technotes/tn3179-understanding-local-network-privacy

## Version 0.2.4 GIF support and app identity

GIFs now follow the photo archive pipeline. Tests verify filename capture dates
(including all four retained real GIF naming patterns), invalid-date rejection,
unchanged GIF bytes, checksum-backed source removal and duplicate source
retention. Go race/vet and real-process smoke checks passed.

The real QNAP was upgraded while idle, with a pre-upgrade state backup. A new
four-file GIF preview was created with zero unsupported/attention records, using
2020/2021 capture dates from filenames. At the user's request this new task was
started: all four archived successfully, with zero skips or failures. Each
published target's SHA-256 was independently checked against the task record,
and each completed source was absent. The original 7,711-file task history was
preserved. The source folder still retains its Syncthing marker intentionally.

The app explicitly loads its bundled ICNS into `NSApplication.applicationIconImage`
at launch, avoiding stale runtime Dock icons after development builds. Launch
Services registration was refreshed for the local bundle. Native About-panel
screenshots verified the generated blue photo/archive icon and version 0.2.4.

Repeated folder names keep their original names and receive separate short run
badges (`#1`, `#2`); sidebar counts/status and creation-time hover hints distinguish
runs without long dates in titles. Swift tests cover ordinal ordering independent
of list order and existing nanosecond timestamp parsing (19 tests passed).

## Version 0.2.5 empty previews and rescan

Added same-job rescan for unstarted ready previews, with current-file enumeration,
unchanged-source metadata reuse and immediate scan-completion invalidation. Empty
previews cannot queue an archive operation and show a prominent rescan action.
Started or completed jobs cannot discard transfer journals through rescan.

Tests cover empty preview → add file → rescan without transfer, removal of stale
preview/SQLite records after deleting a file and rescanning, state reload, and
rejection after archiving starts. Swift tests cover empty-task button eligibility
and preserving rescan only before execution (20 tests). The real-process smoke
workflow verifies the public rescan endpoint and empty-start rejection.

Version 0.2.5 was installed on the idle QNAP after a state backup and verified
against the local ARM64 binary digest. The real empty `iphonexs` task remained
ready/unstarted with zero files. Native UI inspection showed only the prominent
rescan action (no archive-start action), and clicking it refreshed the same job
without any transfer. Empty task status is presented as “等待檔案”.

## Repository layout consolidation

Go sources and tests moved to `cmd/archive/` under the existing root module;
the independent Swift package moved to `apps/macos/`. Build targets, ignore
rules and current documentation use the new locations. The service landing page
is now an embedded HTML file under `cmd/archive/web/`, with unchanged behavior.
The NAS executable name, service API, configuration and state paths are unchanged.

Validation after the move: Go race tests/vet, local/Linux ARM64/Linux ARMv7 builds,
real-process HTTP smoke checks, all 20 Swift tests, Mac app packaging/signature
checks, and QNAP lifecycle tests/staging. No production NAS restart is needed for
this source-layout change.

## Version 0.2.6 Simplified Chinese and focused source selection

Converted all native App strings and bundle localization to Simplified Chinese,
including settings, connection prompts, task actions and notifications. The new
archive sheet removes its subtitle, repeated path footer, classification guide
and explanatory paragraphs. It retains source selection, archive destination and
two concise options, with details in help hints. The destination displays the
friendly QNAP share path rather than a physical CACHEDEV path.

The source-purpose browse endpoint hides archive/state directories and aliases,
marks their ancestors as non-selectable, supports per-job destinations and
preserves existing generic browse/inspection permissions. The client filters old
responses as a compatibility fallback and auto-opens a single source root. No
ROOTS configuration or filesystem access scope is expanded.

Go race tests/vet passed, including source/target separation, symlink aliases,
ancestor navigation and custom destinations. Swift tests: 21 passed, including
source filtering against canonical/share aliases and older response decoding.

Deployed 0.2.6 on the idle QNAP after a state backup; verified the registry/API
version and installed executable digest. The live source catalog contains only
`upload`, with its batch folders visible, while the generic access roots remain
unchanged. Native screenshots verified Simplified Chinese menus and controls,
the reduced 900×560 archive dialog, automatic entry into `/share/ssd/upload`, the
friendly `/share/archive` destination, and absence of `archive` in the home
source list. Manually browsing the archive destination produced a Chinese
rejection message, and the picker was restored to upload without creating or
starting any task.

## 0.2.7 — 客户端配置服务端归档目录

Go race tests、vet、HTTP smoke 及 QNAP 打包检查通过。新增测试覆盖首次未配置、
目录越界/重叠、任务执行时拒绝修改、0600 配置持久化和重启恢复。
Mac 22 项测试通过，覆盖未配置时先显示设置、不与连接弹窗竞争、取消后刷新不重开。
已在空闲 NAS 安装 0.2.7，核对 API/注册版本及 ARM64 二进制摘要；
保留旧来源和归档目录。通过真实 Mac 界面读取并保存相同配置，核对服务端返回值；
未创建或运行归档任务。升级前备份 state，现有三个任务状态保持不变。

## 0.2.8 — 平级目录拆分工具

新增共享 Go 拆分引擎、CLI/脚本、受 Token 保护的预览/执行/进度接口，以及 Mac“工具 → 拆分目录”。
Go race tests、vet、HTTP smoke、QNAP 打包检查通过，Mac 23 项测试通过。
临时文件测试覆盖不足上限仍生成平级批次、大小边界、超大单文件、路径和命名冲突、
文件变化拒绝、文件元数据、隐藏同步项目保留、来源根目录保留、归档任务互斥，
以及硬链接发布后中断、服务重启后的显式恢复。所有移动测试均使用临时目录。

已在空闲 NAS 安装 0.2.8 并核对版本和 ARM64 二进制摘要。
真实 iphone15 只读预览为 1,877 个普通文件、6,986,915,574 字节，一个 iPhone 15 batch 01
平级目录；两个隐藏同步目录保留。未调用这批数据的 start，未创建批次目录、未移动任何文件。
Mac 菜单入口、来源选择界面通过原生 UI 检查，预览结果通过真实 API 验证。真实执行留给用户测试。
