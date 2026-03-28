# Design

This document is the source of truth for the project architecture and runtime
behavior.

Related docs:

- Usage and deployment: `docs/usage.md`
- Benchmark notes: `docs/benchmarks.md`
- QNAP-specific notes: `docs/qnap-notes.md`

## Goals

The project is a single-binary Go CLI that archives photos and videos from an
input directory into a date-based archive tree.

It is optimized for NAS-first workflows:

- no web frontend
- no background server
- no database required for the initial version
- all file operations happen locally on the machine that runs the binary

## Core principles

1. Correctness before speed.
2. File-level state tracking.
3. Keep expensive work local.
4. Remove fixed per-file overhead before adding more concurrency.
5. Read aggressively, write conservatively.

## Archive model

Supported archive groups:

- `photos`
- `videos`
- `pngs`

Target path format:

```text
<archive-base>/<type>/YYYY/MM/YYYYMMDD_HHMMSS_SIZE.ext
```

Examples:

```text
/archive/photos/2024/01/20240115_143052_1234567.jpg
/archive/videos/2024/01/20240115_143052_987654321.mov
```

## Metadata strategy

The runtime uses a cheap-to-expensive detection order.

### Date source order

By confidence:

1. photo EXIF `DateTimeOriginal`
2. photo EXIF `CreateDate`
3. video `creation_time`
4. filename parsing
5. file `mtime`

By runtime cost:

1. filename parsing
2. native Go metadata parsing
3. external-tool fallback
4. `mtime`

### Per-type behavior

JPEG:

```text
filename parse
  -> native EXIF
  -> mtime fallback
```

HEIC:

```text
filename parse
  -> exiftool fallback
  -> mtime
```

Video:

```text
filename parse
  -> ffprobe / ffmpeg fallback
  -> mtime
```

PNG:

```text
filename parse
  -> mtime
```

### External tools

External tools are fallback paths, not the default path.

- `exiftool` is used only when native support is missing or weak.
- `ffprobe` or `ffmpeg` is used only when video metadata cannot be obtained by
  cheaper means.

Constraints:

- no double `exiftool` invocation for a single file
- no shell pipelines like `ffmpeg | grep`
- no unconditional external tool per file

## Job and task model

The smallest unit of state is a file.

Each task tracks:

- source path
- size
- mtime
- current status
- extracted datetime
- metadata source
- optional md5
- final target path

### Task lifecycle

```text
discovered
  -> queued
  -> metadata_running
  -> metadata_done
  -> planning_done
  -> md5_pending
  -> md5_running
  -> move_queued
  -> moving
  -> completed

Any stage may also end in:
  -> failed
  -> skipped
```

`skipped` is used for dry-run completion and for files that disappear during a
run before they can be safely archived.

## Persistence model

Per job, the tool stores:

- `job.json`
- `events.jsonl`

`job.json` is the latest snapshot.  
`events.jsonl` is an append-only event log.

This supports:

- progress inspection
- failure review
- retries
- post-run auditing

## Runtime pipeline

High-level flow:

```text
scan
  -> build job snapshot
  -> run
  -> metadata detection
  -> target planning
  -> duplicate resolution
  -> single-writer archive move
  -> snapshot/event persistence
```

Concurrency model:

- metadata and planning can run concurrently
- final writes remain single-writer
- md5 is computed only on target collisions

## Duplicate handling

If the target path does not exist:

- write directly

If the target path already exists:

1. compute source md5
2. compute destination md5
3. if identical, append `_1`, `_2`, ...
4. if different, append an 8-character hash suffix

This preserves all variants instead of dropping content silently.

## Cross-filesystem safety

The tool first tries `rename`.

If the source and destination are on different filesystems, it falls back to:

1. copy to a temp file in the destination directory
2. flush the temp file
3. rename temp file into place
4. remove the source file

This is required for many NAS layouts where input and archive shares are not on
the same filesystem.

## CLI surface

Supported commands:

- `archive scan`
- `archive run`
- `archive status`
- `archive watch`
- `archive files`
- `archive retry`
- `archive export`
- `archive jobs`

The CLI is job-oriented. It does not expose a server API and does not require a
daemon process.

