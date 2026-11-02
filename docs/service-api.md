# Archiver service API v1

Implementation contract for the native macOS client and QNAP service. JSON uses camelCase; timestamps are RFC3339 strings; all byte counts are integers. Base path `/api/v1`. Every API request requires `Authorization: Bearer <token>` (including SSE). Error response: `{"error":{"code":"...","message":"..."}}`.

## Endpoints

- `GET /info` -> Info.
- `GET /browse?path=<absolute NAS path>` -> DirectoryListing. Empty path returns allowed roots. Only directories, sorted by name. Resolve symlinks and enforce configured allowed roots; exclude NAS system thumbnail/recycle dirs.
- `GET /jobs` -> array of Job, newest first.
- `POST /jobs` body `{"sourcePaths":["/share/ssd/upload/batch"],"archiveBase":"/share/archive","cleanupEmptyDirectories":true}` -> Job, HTTP 202. Scanning/metadata preparation runs asynchronously. An aggregate job may contain multiple selected sources. Reject overlapping sources, source/target nesting, paths outside allowed roots, and concurrent jobs claiming overlapping sources.
- `GET /jobs/{id}` -> Job.
- `GET /jobs/{id}/files?filter=all|attention|failed&offset=0&limit=200` -> `{"files":[File],"total":123}`.
- `POST /jobs/{id}/start` -> Job, HTTP 202. Start a ready or paused job. Revalidate inputs before moving. Only one active moving job per server; others queue.
- `POST /jobs/{id}/pause` -> Job. Pause at a file boundary; `pausing` while the current file finishes. Queued jobs can be paused.
- `POST /jobs/{id}/retry` -> Job. Replan failed files and return ready (does not immediately move files).
- `POST /jobs/{id}/cleanup` -> Job. Remove empty directories within selected source roots only. Never recursively delete files. Callable after a terminal job; list residual paths if not empty.
- `GET /events` -> SSE; event name `job`, data is a full Job snapshot (no files). Send initial snapshots and subsequent progress; heartbeat comments. Client reconnects and can refresh GET /jobs. Stream failures must be visible to the client.

## Info

`{"name":"My QNAP","version":"0.2.2","archiveBase":"/share/archive","roots":["/share/ssd/upload","/share/archive"],"freeBytes":12345,"tools":{"exiftool":true,"ffprobe":false,"ffmpeg":true}}`

## DirectoryListing

`{"path":"/share/ssd/upload","parentPath":null,"entries":[{"name":"batch","path":"/share/ssd/upload/batch"}]}`

## Job

Fields (always emit zero values/empty arrays where relevant):
- `id`, `name`, `sourcePaths` (array of strings), `archiveBase`, `cleanupEmptyDirectories` (boolean)
- `status`: `scanning`, `ready`, `queued`, `running`, `pausing`, `paused`, `completed`, `completed_with_errors`, `failed`, `interrupted`
- `createdAt`, `updatedAt`
- `unsupportedFiles`, `unsupportedBytes` (integer; inventory retained at source, excluded from the actionable progress denominator)
- `scanComplete` (boolean; partial interrupted scans must be retried to rebuild preview before starting)
- `totalFiles`, `completedFiles`, `failedFiles`, `attentionFiles`, `totalBytes`, `completedBytes` (integer)
- `currentFile` (string; empty if none), `currentFileBytes` (integer, bytes copied within current file, separate from completedBytes)
- `dateRangeStart`, `dateRangeEnd` (RFC3339 strings, empty if unavailable)
- `error` (string, empty if none)
- `cleanupStatus`: `pending`, `done`, `residual`, `not_requested`
- `residualPaths`: array of strings

`completedBytes` counts fully finished files. `totalFiles` and `totalBytes` include unsupported inventory. UI progress may use `(completedBytes + currentFileBytes) / (totalBytes - unsupportedBytes)`, clamped, or completedFiles / (totalFiles - unsupportedFiles). A successfully completed task with only unsupported inventory has no remaining actionable work. Scanning totals grow; do not show a determinate progress percentage until scan finishes. Attention includes mtime fallback, collisions, unsupported files; these should be visible but don't all block execution. Unsupported files stay at source and produce residual cleanup rather than successful deletion claims.

## File

`{"id":"file-000001","sourcePath":"...","targetPath":"...","size":123,"status":"planned","metadataSource":"native-exif","dateTime":"2026-07-01T12:00:00Z","warning":"","error":"","sha256":""}`

Status: `discovered`, `planned`, `checking`, `copying`, `completed`, `skipped`, `failed`, `unsupported`. Metadata source: `filename`, `native-exif`, `exiftool`, `ffprobe`, `mtime` (or empty before metadata extraction). `warning` explains fallback or renamed collisions. Never overwrite existing files; use suffixes for distinct content, or for all collisions when duplicate avoidance is disabled. Live state is in memory and is checkpointed asynchronously every two seconds, at most 500 records per transaction by a single writer. Repeated updates coalesce. A crash can lose unflushed progress (and more than two seconds when a backlog exists). File safety is independent of checkpoint freshness: sync and SHA-256 verify the destination, verify the source immediately before removal, and never overwrite a destination. Recovery must verify destination contents before treating a missing source as completed; an incomplete checkpoint can require a rescan and may leave duplicates or staging files. Exclude `.@__thumb`, `@eaDir`, `@Recycle`, `.stfolder`, `.stversions`.

## Lifecycle and packaging

`archive serve --listen 127.0.0.1:8787 --state-dir ... --archive-base ... --roots /share/ssd/upload,/share/archive --token-file ... --name "My QNAP"`.

No default credentials. Generate a random token if the supplied token file is absent; mode 0600. Default loopback listen; QPKG explicitly configures LAN binding with bearer auth. Minimal setup/status Web page at `/`, no unauthenticated filesystem/credential disclosure. GET `/healthz` returns `{"status":"ok"}`.

Existing CLI commands must keep working. State uses SQLite (pure Go driver compatible with the toolchain), stored outside installed binaries so upgrades preserve state. On restart, interrupted scanning/moving jobs recover to a safe paused/interrupted state, retry reuses saved capture metadata only when source size and modification time are unchanged, and replans destination names. Shutdown pauses at safe file boundaries and drains pending checkpoints; a final write failure is returned. Normal runtime never bypasses the fixed writer cadence.

### Persistence diagnostics (0.2.1)

Authenticated `GET /api/v1/info` also includes `persistence`: `pendingRecords`, `flushIntervalSeconds`, `batchLimit`, `lastFlush`, `lastBatchRecords`, `lastFlushMilliseconds`, and `error`. A failed batch is retained, subsequent changes supersede stale versions, and the next tick retries. Progress reads and SSE use memory; they do not wait for SQLite I/O. Job summaries follow their pending file records, while an initial job record permits partial scans to be loaded. Shutdown may drain multiple capped transactions without waiting for ticks.

Metadata parsing uses a service-wide pool of four readers with lazy persistent ExifTool processes. External tool requests have a 30-second timeout per invocation; video fallback can try multiple tools. Destination suffixes are assigned in discovery order.

### Duplicate handling (0.2.2)

`POST /api/v1/jobs` accepts `skipDuplicates` (default false for API compatibility).
The Mac client defaults it to true. Existing ready/paused/interrupted tasks can
set it with `POST /api/v1/jobs/{id}/skip-duplicates`, body `{"enabled":true}`.
Changes during an active transfer are rejected.

When enabled, only archive files named with the same capture timestamp, size and
extension (including suffixed variants) are candidates. Destination directories
are indexed lazily once per run; files archived earlier in the same run are added
to that index. Candidates must have equal size and byte-for-byte equal contents;
comparison stops on the first difference and never computes a full-library hash.
Source identity, size and mtime are checked against the preview and again after
comparison; target identity/size/mtime are also checked. Symlinks are not accepted
as matching archive files. Different or absent candidates follow the existing
verified-copy and suffix-preserving transfer path.

A confirmed duplicate has status `skipped` and `duplicateOf` naming the existing
archive file. **The source is retained.** Job summaries expose `skippedFiles` and
`skippedBytes` separately from actual archived counts. Progress includes both.
Retry/resume skips already skipped records. Disabling the option does not revisit
records already skipped; a new task can process retained source files.

This is candidate-based duplicate avoidance, not global content deduplication:
archives with unrelated names or different recorded capture times/extensions can
be missed. Equality requires reading both candidate files in full; it saves CPU
hashing and new archive writes, not all disk reads. Preview remains metadata-only.

### Inspection and review (0.2.3)

- `GET /api/v1/browse?path=...&includeFiles=true` lists actual files and directories,
  including residual Syncthing/QNAP housekeeping content. Entries include
  `isDirectory`, `size`, `system`, `canOpen`, and `symlink`. This read-only path
  resolves the directory within configured roots, rejects escaping symlinks,
  and never relaxes the excluded-directory policy used by mutating operations.
  The default directory picker remains unchanged.
- `POST /api/v1/jobs/{job}/files/{file}/acknowledge` clears an informational warning
  after the user reviews it. Unsupported and failed records cannot be dismissed
  this way. File bytes and archive paths are unchanged.
- `POST /api/v1/jobs/{job}/files/{file}/date` accepts
  `{"dateTime":"2020-06-01T12:30:00+08:00"}` before that file is archived, while
  the task is ready/paused/interrupted and its scan is complete. It validates
  source path policy, size and mtime against the preview, reserves a new target,
  and marks the metadata source as `manual`. It does not modify media contents.
  A manual date survives retry when the source is unchanged. Completed files
  cannot be relocated through this endpoint.
- `filter=skipped` is accepted on the file-list endpoint. Attention counters now
  include errors consistently with the attention filter.

### GIF and repeated runs (0.2.4)

GIF files use the photo pipeline and `photos/YYYY/MM` destinations. They are
copied byte-for-byte without frame extraction or transcoding, and use the same
candidate comparison, checksum verification and source-removal safeguards as
other media. Capture dates prefer filenames, then ExifTool, then mtime fallback.
Bare `YYYYMMDDHHMMSS` / `YYYYMMDD_HHMMSS` names with extension or underscore/hyphen
suffixes are supported, with calendar validation.

Re-scanning retained files creates a separate job and preserves the previous
job's historical results. Names remain the source-folder basename. The Mac app
uses separate `#1`, `#2` badges for same-named jobs, ordered by creation time;
compact file counts and status distinguish their scopes. Creation time is in a
hover hint, not appended to the name. Actual job IDs identify API operations.

### Refreshing an unstarted preview (0.2.5)

`POST /api/v1/jobs/{id}/rescan` refreshes an existing ready preview only if archive
execution has never started. It retains the job ID/name/creation time, rebuilds
the current file list (including additions and removals), and reuses metadata
only for unchanged sources. The action immediately clears `scanComplete` and
returns the scanning state; it does not transfer media. The `started` summary
field distinguishes an untouched preview from a retried transfer. Older records
are migrated conservatively using persisted statuses and transfer journals.

Empty or unsupported-only jobs cannot start, even through the API. Their primary
Mac action is “重新掃描”; nonempty unstarted previews offer both rescan and start.
Already-started/completed tasks retain their journals and require a new task for
later batches, preserving history and recovery data.

### Source picker and Simplified Chinese client (0.2.6)

`GET /api/v1/browse?purpose=source` presents the source-picker view of `ROOTS`.
It excludes the configured archive destination, service state directory and their
descendants, including aliases resolved through symlinks. An optional
`archiveBase` query additionally excludes a per-job destination after validating
it against the same access roots. Ancestors of excluded directories remain
navigable but have `selectable:false`; the virtual root is also not selectable.
Entry and listing `selectable` fields are additive. Default browsing and
read-only residual inspection keep their existing behavior and access bounds.

The Mac source picker auto-opens a single available source root, without selecting
it automatically. Multiple source roots are listed for explicit choice. A local
filter provides compatibility with older services that ignore `purpose=source`.
There is no new source-directory step during connection and no automatic widening
of access to every QNAP share. `ROOTS` is an administrator-configured access list,
not just a source preference; it must still include the archive write location.

App strings, menus and bundle localization now use Simplified Chinese. User-owned
folder names, job names and diagnostic paths are not converted.

### 服务端归档设置（0.2.7）

`GET /api/v1/settings` 返回 `configured`、`sourceRoots`、`archiveBase`。
`PUT /api/v1/settings` 接受相同结构（`configured` 由服务端设为 true）。
需要现有 Bearer Token。来源和目标必须是管理员 `ROOTS` 范围内现有目录，
来源互不重叠，且不与目标或服务数据目录重叠；归档位置必须可写。
扫描、排队或归档期间返回 409。保存为 state-dir/settings.json（0600，原子替换），
重启后优先读取；不修改管理员的 ROOTS，也不修改已有任务路径。

`GET /info` 新增 `configured`。新安装默认未配置，仍可连接和浏览允许的目录，
创建任务需先完成设置。旧安装的非空 archive-base 作为已有配置保留。
QNAP 新安装的 ROOTS 默认 /share，archive-base 留空；管理员可缩小 ROOTS。
Mac 连接只需地址与 Token，首次配置及后续修改均在客户端完成。

### 拆分目录工具（0.2.8）

- `POST /api/v1/tools/splits/preview`：`sourcePath`，可选 `prefix`（默认沿用来源目录名）与 `maxBytes`
  （默认 8,000,000,000 字节）。只读预览，返回 ID、文件/字节总量、批次目录列表。
- `POST /api/v1/tools/splits/{id}/start`：显式执行或继续失败/中断的任务。
- `GET /api/v1/tools/splits/{id}`：状态与进度。
- `GET /api/v1/tools/splits`：已执行的任务记录，用于重新打开工具窗口。
- `GET /info` 的 `capabilities.directorySplit` 标识是否支持此工具。

批次是原目录的平级目录，命名 `{prefix}_batch_01`；不足上限也生成一个批次。
名称已占用时顺延编号；预览后出现同名路径或来源文件变化时拒绝开始，需重新预览。
完整文件按相对路径顺序装入批次，保留内部路径，单文件超限时独立一批并提示。
空目录不生成批次；点开头的文件/目录、符号链接及特殊文件留在原处，源根目录保留。
仅处理预览中存在的文件，后来新增的文件保留在来源。隐藏目录计数按项目而非递归文件。

来源与批次必须都在管理员授权和配置的来源范围内，不能与归档/服务数据目录重叠。
拆分与归档任务互斥占用相关目录；关闭 Mac 不会取消执行。持久化计划保存于
`state-dir/splits/*.json`（0600），单任务单执行者，仅开始/结束写状态；逐文件进度在内存。
同一文件系统使用 no-overwrite 硬链接发布，目录 fsync 后移除来源文件名，保留文件元数据，
不读取整个文件或计算哈希。重启后显式继续，通过原文件 inode/大小/mtime 恢复。
跨文件系统不回退到复制，遇到错误会停止并保留文件。

### 删除归档任务（0.2.10）

`DELETE /api/v1/jobs/{id}` 只删除任务与文件条目记录，不访问、移动或删除任何媒体文件。
成功返回 `200 {"id":"…","status":"deleted"}`，等待现有单写入者在固定 tick 中提交后再确认。
未开始的 ready/interrupted/failed，以及 completed/completed_with_errors 可删除；
正在扫描、执行、排队、暂停处理中或已开始但尚未完成的任务返回 409。
仍有未完成发布/临时文件恢复记录的任务也不能删除，需先重试处理。
任务不存在返回 404；所有请求仍需 Bearer Token。

任务快照新增 `deletable`，`info.capabilities.jobDeletion` 标识支持此功能。
已连接的 SSE 客户端收到原任务的 `job` 事件，`status` 为 `deleted`；此事件只表示移除记录。
客户端应忽略之后可能到达的旧快照，避免已删除任务重新出现。
