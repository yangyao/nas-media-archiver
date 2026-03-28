# Usage Guide

`archive` is a single-binary CLI for scanning an input directory, building a
job, and then archiving supported media files into a date-based output
structure.

## Build

```bash
go build -o archive .
make build-linux-arm64
make build-linux-arm7
```

## Basic concepts

- `scan` creates a job from an input directory
- `run` processes a job
- `status` shows job progress
- `files` lists per-file states
- `watch` tails the event log
- `retry` resets failed files so they can run again
- `export` writes filtered results as JSON or CSV

## Deploy

Example deployment to a NAS:

```bash
scp dist/archive-linux-arm64 user@your-nas:/path/to/archive
ssh user@your-nas 'chmod +x /path/to/archive'
```

## Scan

```bash
./archive scan \
  --path /path/to/input \
  --state-dir /path/to/archive-state
```

Example output:

```text
Job: <job-id>
Files: <count>
Status: created
ScannedAt: <timestamp>
```

## List jobs

```bash
./archive jobs \
  --state-dir /path/to/archive-state
```

## Show job status

```bash
./archive status \
  --job <job-id> \
  --state-dir /path/to/archive-state
```

## List files in a job

```bash
./archive files \
  --job <job-id> \
  --state-dir /path/to/archive-state
```

Only failed files:

```bash
./archive files \
  --job <job-id> \
  --state-dir /path/to/archive-state \
  --status failed
```

## Watch events

```bash
./archive watch \
  --job <job-id> \
  --state-dir /path/to/archive-state
```

## Dry run

Always start with a dry run:

```bash
./archive run \
  --job <job-id> \
  --archive-base /path/to/archive \
  --state-dir /path/to/archive-state \
  --dry-run
```

Dry run performs:

- metadata extraction
- target-path planning
- duplicate detection checks
- state and event recording

Dry run does not:

- create destination files
- move source files

## Real archive run

```bash
./archive run \
  --job <job-id> \
  --archive-base /path/to/archive \
  --state-dir /path/to/archive-state
```

On QNAP, prefer:

```bash
sudo -u admin ./archive run \
  --job <job-id> \
  --archive-base /path/to/archive \
  --state-dir /path/to/archive-state
```

## Recommended settings

For a 4-core ARM QNAP NAS, a good starting point is:

```bash
./archive run \
  --job <job-id> \
  --archive-base /path/to/archive \
  --state-dir /path/to/archive-state \
  --dry-run \
  --workers 8 \
  --snapshot-every 100
```

## Retry failed files

```bash
./archive retry \
  --job <job-id> \
  --state-dir /path/to/archive-state
```

Then run the job again.

## Export results

Export failed files as CSV:

```bash
./archive export \
  --job <job-id> \
  --state-dir /path/to/archive-state \
  --status failed \
  --format csv \
  --output failed.csv
```

Export JSON:

```bash
./archive export \
  --job <job-id> \
  --state-dir /path/to/archive-state \
  --status failed \
  --format json \
  --output failed.json
```

## Related docs

- `docs/design.md`
- `docs/benchmarks.md`
- `docs/qnap-notes.md`

