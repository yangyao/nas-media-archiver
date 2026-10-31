# nas-media-archiver

`nas-media-archiver` archives photos and videos into a date-based directory structure.
It includes a Go CLI and NAS service, a native macOS 26+ SwiftUI client, and QNAP
QPKG packaging. The NAS owns all file operations; closing the Mac app does not
stop an archive task.

It is designed for NAS workflows first, especially QNAP-style environments, but
it can also run on a regular Linux host.

## Repository layout

```text
cmd/archive/          Go CLI + HTTP service (single NAS executable)
  web/                Embedded service status page
apps/macos/           Native SwiftUI client, tests, icon and app packaging
packaging/qnap/       QPKG lifecycle scripts and installer builds
scripts/              Service integration checks
docs/                API, architecture, usage and validation
```

See [repository architecture](docs/repository-layout.md) for module boundaries
and the future `apps/web/` integration path. Go dependencies stay in the root
module; the Mac app has its own Swift package.

## Features

- Archives files into `photos/YYYY/MM`, `videos/YYYY/MM`, and `pngs/YYYY/MM`
- Uses `YYYYMMDD_HHMMSS_size.ext` naming
- Optionally skips byte-identical archive candidates while retaining the source; distinct collisions use suffixes
- Supports `scan`, `run`, `status`, `watch`, `files`, `retry`, and `export`
- Supports `--dry-run` before a real archive run (the same job can then be run for real)
- Excludes NAS thumbnail directories (`.@__thumb`, `@eaDir`)
- CLI stores per-job state as `job.json` and `events.jsonl`; the service uses in-memory state and bounded SQLite checkpoints
- Handles cross-filesystem moves with copy-then-rename fallback and SHA-256 verification before source deletion

## Supported file types

- Photos: `jpg`, `jpeg`, `heic`, `webp`, `gif`
- Videos: `mp4`, `mov`, `3gp`
- Images: `png` (ExifTool metadata when available, otherwise mtime)

## NAS service and Mac app

The desktop workflow is: connect to an archive server, select server-side folders, scan and review the
archive plan, start, monitor progress, then clean empty source directories.
The Mac client defaults to skipping confirmed duplicates while retaining their sources; distinct collisions use suffixes. Unsupported files stay at the source. Empty previews can be rescanned in the same unstarted task after adding files.

Build the service and run it with explicit allowed paths:

```bash
make build
./dist/archive serve \
  --listen 127.0.0.1:8787 \
  --state-dir /path/to/persistent-state \
  --archive-base /path/to/archive \
  --roots /path/to/input,/path/to/archive \
  --token-file /path/to/persistent-state/token \
  --name "My NAS"
```

The service generates a private random token file when it does not exist. Enter
that token and the server URL in the Mac app's **連線設定** panel (the field is labelled **連線密碼**). Tokens are stored
in a private app-owned local file, without Keychain prompts. Default service binding is loopback; LAN use requires an
explicit listen address, and the token is required for API access. For an
untrusted network, terminate TLS in front of the service rather than sending
bearer tokens over plaintext HTTP.

```bash
make macos                 # Build the native .app bundle on a Mac with Xcode 26+
make test                  # Go race tests
make smoke                 # Live HTTP integration test with disposable media
```

The two-column Mac window lists active and historical jobs in one compact sidebar.
Connection settings for the single server live at the bottom left; client
preferences use the native Settings window. Task execution survives client disconnection. The service checkpoints up to 500 dirty records every two seconds; abrupt restarts can lose unflushed progress. Interrupted tasks require review/resume or rescan, reusing saved metadata for unchanged sources.

See [QNAP installation](docs/qnap-install.md), [Mac client](apps/macos/README.md),
[API contract](docs/service-api.md), [implementation plan](docs/implementation-plan.md),
and [validation results](docs/validation.md).

## Build

```bash
go build -o archive ./cmd/archive
make build-linux-arm64
make build-linux-arm7
```

## Quick start

1. Scan an input directory.

```bash
./archive scan \
  --path /path/to/input \
  --state-dir /path/to/archive-state
```

2. Run a dry run first.

```bash
./archive run \
  --job <job-id> \
  --archive-base /path/to/archive \
  --state-dir /path/to/archive-state \
  --dry-run \
  --workers 8 \
  --snapshot-every 100
```

3. Run the real archive.

```bash
./archive run \
  --job <job-id> \
  --archive-base /path/to/archive \
  --state-dir /path/to/archive-state \
  --workers 8 \
  --snapshot-every 100
```

## QNAP notes

QNAP ACL behavior differs from a standard Linux host. In practice:

- run a dry run first
- use `sudo -u admin` for real writes into shared archive directories
- ensure `exiftool` is available if you want HEIC fallback support
- ensure `ffprobe` or `ffmpeg` is available for video metadata fallback

Example:

```bash
sudo -u admin ./archive run \
  --job <job-id> \
  --archive-base /path/to/archive \
  --state-dir /path/to/archive-state
```

## Recommended CLI runtime settings

Based on current ARM QNAP benchmarks:

- `--workers 8`
- `--snapshot-every 100`

## Project docs

- [Usage guide](./docs/usage.md)
- [Design](./docs/design.md)
- [Benchmarks](./docs/benchmarks.md)
- [QNAP notes](./docs/qnap-notes.md)

## License

MIT
