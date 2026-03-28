# nas-media-archiver

`nas-media-archiver` is a single-binary Go CLI that archives photos and videos
from an input directory into a date-based directory structure.

It is designed for NAS workflows first, especially QNAP-style environments, but
it can also run on a regular Linux host.

## Features

- Archives files into `photos/YYYY/MM`, `videos/YYYY/MM`, and `pngs/YYYY/MM`
- Uses `YYYYMMDD_HHMMSS_size.ext` naming
- Preserves duplicate files safely
- Supports `scan`, `run`, `status`, `watch`, `files`, `retry`, and `export`
- Supports `--dry-run` before a real archive run
- Stores per-job state as `job.json` and `events.jsonl`
- Handles cross-filesystem moves safely with copy-then-rename fallback

## Supported file types

- Photos: `jpg`, `jpeg`, `heic`
- Videos: `mp4`, `mov`, `3gp`
- Images via mtime fallback: `png`

Current unsupported examples:

- `gif`
- `webp`

## Build

```bash
go build -o archive .
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
  --archive-base /share/archive \
  --state-dir /path/to/archive-state
```

## Recommended runtime settings

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
