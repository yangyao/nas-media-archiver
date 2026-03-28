# QNAP Notes

This document captures the operational differences that matter when running
`nas-media-archiver` on QNAP NAS devices.

## Why QNAP is special

Compared with a generic Linux host, QNAP commonly introduces:

- shared-folder ACL behavior that can override manual permission changes
- restricted `admin` login behavior
- older bundled media tools
- Entware-installed tools that may live outside the default `PATH`

Those differences mostly affect real archive writes and metadata fallback tools.

## Recommended workflow

1. Upload the binary to a stable path on the NAS.
2. Run `scan`.
3. Run `run --dry-run`.
4. Inspect `status`, `files`, and failures.
5. Run the real archive with `sudo -u admin`.

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

## Permissions

If archive writes fail on QNAP, check:

1. whether the process has shared-folder write permission
2. whether ACL behavior is undoing manual chmod or chown changes
3. whether you are writing into a protected share without admin context

In practice, real writes into shared archive directories often need
`sudo -u admin`.

## Tool availability

Typical built-in tools:

- `find`
- `stat`
- `md5sum`
- `date`
- `ffmpeg`

Common Entware additions:

- `exiftool`
- `perl`

Notes:

- `exiftool` may live under `/opt/bin`
- `ffprobe` may not exist even if `ffmpeg` does
- QNAP-shipped `ffmpeg` versions may be old

## Metadata implications

On QNAP systems:

- JPEG usually works well with native EXIF parsing
- HEIC may rely on `exiftool`
- video fallback may rely on `ffprobe` or `ffmpeg`
- PNG usually falls back to filename parsing or `mtime`

## Filesystem layout

Some QNAP setups place the input share and the archive share on different
filesystems. In that case, plain `rename` fails with a cross-device error.

The tool already handles this by using copy-then-rename fallback.

## Operational advice

- prefer dry runs before real writes
- keep a dedicated state directory outside transient temp paths
- use stable binary deployment paths on the NAS
- validate `exiftool` and video tooling before large archive jobs

