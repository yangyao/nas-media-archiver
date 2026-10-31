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

`{"name":"My QNAP","version":"0.2.0","archiveBase":"/share/archive","roots":["/share/ssd/upload","/share/archive"],"freeBytes":12345,"tools":{"exiftool":true,"ffprobe":false,"ffmpeg":true}}`

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

Status: `discovered`, `planned`, `copying`, `completed`, `failed`, `unsupported`. Metadata source: `filename`, `native-exif`, `exiftool`, `ffprobe`, `mtime` (or empty before metadata extraction). `warning` explains fallback or renamed collisions. Never overwrite existing files; preserve duplicates with suffixes. SHA-256 journal must be durable before source deletion, and recovery must verify destination contents before treating a missing source as completed. Exclude `.@__thumb`, `@eaDir`, `@Recycle`, `.stfolder`, `.stversions`.

## Lifecycle and packaging

`archive serve --listen 127.0.0.1:8787 --state-dir ... --archive-base ... --roots /share/ssd/upload,/share/archive --token-file ... --name "My QNAP"`.

No default credentials. Generate a random token if the supplied token file is absent; mode 0600. Default loopback listen; QPKG explicitly configures LAN binding with bearer auth. Minimal setup/status Web page at `/`, no unauthenticated filesystem/credential disclosure. GET `/healthz` returns `{"status":"ok"}`.

Existing CLI commands must keep working. State uses SQLite (pure Go driver compatible with the toolchain), stored outside installed binaries so upgrades preserve state. On restart, interrupted scanning/moving jobs recover to a safe paused/interrupted state, never silently discard state. Shutdown pauses at safe file boundaries.
