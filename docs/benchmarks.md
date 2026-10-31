# Benchmarks

This document summarizes representative dry-run benchmark results for
`nas-media-archiver` on a 4-core ARM QNAP NAS.

The purpose is to show relative performance changes across optimization stages,
not to preserve one user's exact runtime history.

## Test setup

- Host class: 4-core ARM QNAP NAS
- Run mode: `--dry-run`
- Dataset shape:
  - about 700 media files
  - mostly `.jpg`
  - a smaller number of `.mov` and `.png`
  - a very small number of `.mp4`

## Optimization stages

### Stage A

- single-threaded
- rewrites `job.json` after every file
- opens and closes `events.jsonl` for every event

### Stage B

- single-threaded
- writes `job.json` in batches
- keeps `events.jsonl` open during the run

### Stage C

- keeps Stage B persistence changes
- adds concurrent metadata and planning workers
- still uses a single writer for final archive writes

### Stage D

- splits image work and video fallback work into separate queues
- keeps single-writer semantics

## Dry-run results

### Stage B baseline

- elapsed: `40s`
- throughput: about `18 files/s`
- average: about `0.055s/file`

### Stage C worker comparison

| workers | elapsed | throughput |
|------|------|------|
| `1` | `40s` | `18 files/s` |
| `2` | `25s` | `29 files/s` |
| `4` | `23s` | `31 files/s` |
| `8` | `18s` | `40 files/s` |
| `12` | `20s` | `36 files/s` |
| `16` | `19s` | `38 files/s` |

### Stage D split-queue comparison

| fast/video | elapsed | throughput |
|------|------|------|
| `7/1` | `26s` | `28 files/s` |
| `6/2` | `23s` | `31 files/s` |
| `8/1` | `26s` | `28 files/s` |

## Conclusions

1. Batching snapshot writes and keeping the event log open removes obvious
   persistence overhead.
2. The best dry-run setting observed on this machine is still:
   - `--workers 8`
   - `--snapshot-every 100`
3. On this workload, split video queues did not beat the simpler `--workers 8`
   configuration.
4. Once worker counts go past `8`, returns flatten or regress because scheduler,
   disk, and external-tool contention start to dominate.

## Practical guidance

For a similar ARM NAS setup, start with:

```bash
./archive run \
  --job <job-id> \
  --archive-base /path/to/archive \
  --state-dir /path/to/archive-state \
  --dry-run \
  --workers 8 \
  --snapshot-every 100
```

Then tune only if your workload differs materially, especially if your video
ratio is much higher than your image ratio.


## Service 0.2.1: fixed-cadence checkpoints and persistent metadata readers

Validated on the same four-core ARM64 QNAP on 2026-10-31. The original
service scan was stopped after approximately 44 minutes with 6,440 discovered
records, before completing. Its database and binary were backed up before the
upgrade.

The real source directory contains 7,712 files including a Syncthing marker in
`.stfolder`; the preview correctly contains 7,711 files after exclusions:
7,707 planned media files and four unsupported files. These measurements only
scan and plan; they do not transfer or delete media.

| Run | Observed wall time |
| --- | ---: |
| Resume using metadata from the previous partial checkpoint | approximately 27 seconds |
| Full scan with a separate, initially empty service state directory | 83.5 seconds |

The full scan had no application metadata cache. It could benefit from the OS
filesystem cache populated by earlier scans; this is not a cold-disk benchmark.
Polling adds up to roughly five seconds to the full-run observation. Do not
extrapolate these numbers to other media formats, devices or archive transfers.

Full-scan metadata sources: native JPEG EXIF 5,002; persistent ExifTool 2,377;
ffprobe 306; filename 19; mtime fallback three; unsupported four. There were zero
failed files. Every planned target was unique. All file SHA fields remained
empty and completed-file counts remained zero, confirming no archive transfer
was performed.

SQLite flushes ran every two seconds with a 500-record transaction limit. Sampled
commits took 84–574 ms, with no write errors; the initial backlog drained while
metadata processing continued. Both the resumed preview and full preview matched
for source path, size, mtime, capture date, metadata source, target path and status.
Restarting the isolated validation service loaded all 7,711 records as ready.
The isolated service was then stopped; the production task remains ready.

A local arm64 microbenchmark of statistics updates (one pass of discoveries,
without metadata or SQL) compared the original full-list recalculation with the
new incremental update:

| Records | Original recalculation | Incremental |
| --- | ---: | ---: |
| 1,000 | 17.8 ms | 0.27 ms |
| 8,000 | 1,141 ms | 2.90 ms |

Run `go test ./cmd/archive -run '^$' -bench BenchmarkServiceScanState -benchtime=1x -benchmem`.
These are single-iteration diagnostics of the statistics algorithm, not an
end-to-end NAS speedup claim. Incremental bookkeeping uses additional memory.

### Durability contract

Live progress is authoritative in memory. One writer coalesces dirty records,
then commits at most 500 records each two-second tick. A backlog or failed flush
can lose more than one tick of progress on an abrupt crash. Normal shutdown
waits for workers and drains capped batches; flush errors are returned. Runtime
never launches overlapping writes or bypasses the timer because a threshold was
reached. Authenticated `/api/v1/info` exposes pending records, errors and recent
flush timings.

Checkpoint freshness does not authorize source deletion. The transfer path still
syncs and verifies destination data, publishes without overwriting, syncs the
destination directory, then verifies source contents and identity before removal.
A stale checkpoint may require a rescan, leave a staging file or produce an
additional archive copy. Version 0.2.1 had no content deduplication. Version 0.2.2 adds optional candidate-based byte comparison with source retention (see `service-api.md`). Retrying
an incomplete scan reuses saved capture metadata only for unchanged size/mtime
and assigns destination paths again against the current filesystem.
