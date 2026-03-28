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

