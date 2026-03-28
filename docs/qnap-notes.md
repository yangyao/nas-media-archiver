# QNAP Notes

This document covers the QNAP-specific operational details that matter when
running `nas-media-archiver`.

## Why QNAP needs special handling

QNAP behaves differently from a generic Linux host in a few important ways:

- shared-folder ACL behavior can override manual permission changes
- the `admin` account may not be available for direct SSH login
- tool availability varies by firmware and Entware packages

These differences affect how you run real archive writes.

## Recommended execution model

Use a normal SSH user to upload or invoke the binary, then use `sudo -u admin`
for the real archive run when writing into shared folders.

Example:

```bash
./archive scan \
  --path /path/to/input \
  --state-dir /path/to/archive-state

./archive run \
  --job <job-id> \
  --archive-base /path/to/archive \
  --state-dir /path/to/archive-state \
  --dry-run

sudo -u admin ./archive run \
  --job <job-id> \
  --archive-base /path/to/archive \
  --state-dir /path/to/archive-state
```

## Tooling notes

Typical QNAP environments may provide:

- `find`
- `stat`
- `md5sum`
- `date`
- `ffmpeg`

If Entware is installed, you may also have:

- `exiftool`
- `perl`

Important:

- `exiftool` may live under `/opt/bin`
- `ffprobe` may be unavailable even if `ffmpeg` exists
- older QNAP systems often ship older `ffmpeg`

## Metadata implications

In practice:

- JPEG usually works well with native EXIF parsing
- HEIC often benefits from `exiftool` fallback
- video metadata fallback may depend on `ffprobe` or `ffmpeg`
- PNG often falls back to filename parsing or `mtime`

## Permission model

When archive writes fail unexpectedly on QNAP, check:

1. whether the process is running with sufficient shared-folder privileges
2. whether ACL rules are overriding manual chmod or chown changes
3. whether the target archive path is on a different filesystem

The program already supports cross-filesystem safe moves via copy-then-rename
fallback, so the remaining issue is usually permissions rather than rename
semantics.

## Recommended workflow

1. Upload the binary to a stable path on the NAS.
2. Run `scan`.
3. Run `run --dry-run`.
4. Inspect `status`, `files`, and any failures.
5. Run the real archive with `sudo -u admin`.

