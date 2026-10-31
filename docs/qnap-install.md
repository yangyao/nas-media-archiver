# Install the QNAP service

The Mac client connects to a persistent Go service on the NAS. The service is
distributed as `NASMediaArchiver` in QNAP App Center. The first package targets
QNAP's **`arm_64`** platform, which corresponds to Linux `aarch64` / Go `arm64`
(including the TS-216). This is not the 32-bit `arm-x41` platform.

## Build

Build the server binary from the repository root:

```sh
make build-linux-arm64
```

On a QNAP QDK build host, create an installable QPKG:

```sh
packaging/qnap/build.sh
```

The script requires `qbuild` from the official QNAP QDK on `PATH`, stages only
the package's files and the ARM64 binary, then invokes:

```sh
qbuild --root <temporary-build-root> --build-arch arm_64 --build-dir dist/qnap
```

Alternatively, build on the NAS over SSH without installing QDK:

```sh
packaging/qnap/build-on-nas.sh user@nas
```

This helper downloads the official QDK source pinned to commit
`b7b5f4c86ebe95b10a62d64725515e7cdcf4bb35` (QDK 2.5.3), uploads the tool scripts
and staged application into a directory under the SSH user's NAS home, runs
QDK there, queries the result's metadata, and copies the real `.qpkg` back to
`dist/qnap`. It never installs/registers QDK, changes system configuration, or
installs/starts the application. Required NAS tools are Bash, rsync, tar,
gzip, md5sum, awk, sed and hexdump; the TS-216 used for development has these.
SSH key authentication must already work. The build workspace is retained
for inspection and may be deleted afterward. Options are `--binary FILE`,
`--output DIRECTORY`, and `--work-dir NAS-HOME-PATH`.

On a Mac without QDK, prepare the build inputs instead:

```sh
packaging/qnap/build.sh --stage-only
```

This produces `dist/qnap/NASMediaArchiver_0.2.0_arm_64_qdk-source.tar.gz`.
**It is a source/staging archive, not an installable QPKG.** Extract it on a
QDK host and run `qbuild --build-arch arm_64`. The real `.qpkg` produced by QDK
is the file to select in App Center. The build script never renames a tarball
to `.qpkg` and never installs anything onto a NAS.

Optional build-script arguments are `--binary FILE` and `--output DIRECTORY`.
Version and package metadata are maintained in `packaging/qnap/qpkg.cfg`.
QDK's default icons are used until application icons are supplied.

## Install and configure

1. Sign in to QTS as an administrator. Open **App Center → Install Manually**,
   select the `.qpkg`, and complete installation. This development package is
   unsigned; it has not been published or signed by QNAP.
2. Fresh installation leaves the service disabled. Configuration must be
   reviewed before enabling it. Determine the installed package path:

   ```sh
   /sbin/getcfg NASMediaArchiver Install_Path -f /etc/config/qpkg.conf
   ```

3. Configuration is on the same volume as the package, outside its installed
   directory. For an installation at
   `/share/CACHEDEV2_DATA/.qpkg/NASMediaArchiver`, edit:

   ```text
   /share/CACHEDEV2_DATA/.nas-media-archiver/service.conf
   ```

   The template contains:

   ```sh
   LISTEN="0.0.0.0:8787"
   NAME="My Archive Server"
   ARCHIVE_BASE="/share/archive"
   ROOTS="/share/ssd/upload,/share/archive"
   STOP_TIMEOUT="120"
   ```

   Set existing source and destination locations appropriate to the NAS. Each
   source and destination must be beneath one of the comma-separated `ROOTS`.
   Keep the roots narrow. The service resolves symlinks and checks paths at
   request time. Configuration uses shell syntax and is administrator-owned;
   it must not be writable by ordinary users. Do not add passwords or tokens.

4. Enable the application in App Center. Startup creates a cryptographically
   random token with file mode `0600`; there are no default credentials. The
   service runs under QTS's administrative application context, which permits
   writes to protected archive shares. It does not use a stored sudo password.
5. Add the archive server in the Mac app using `http://NAS-LAN-ADDRESS:8787` and the token
   read locally by an administrator from the data directory's `token` file.
   The Mac client saves it in a user-only local credential file, without Keychain access. The Web landing page and logs never
   expose the token.

The default listener is intentionally available on the LAN so the Mac client
can connect. All filesystem/task APIs, including progress streams, require
the bearer token. HTTP does not encrypt that token; use a trusted LAN, an SSH
tunnel, or an HTTPS reverse proxy for other networks. No cloud relay, public
port forwarding, or QTS authentication bypass is configured by this package.
Changing the service port also requires updating App Center's Web_Port entry
or rebuilding the package metadata to keep its Open link correct.

## Metadata tools

The Go service and its SQLite driver do not require a Go toolchain, Python,
Node.js, or an external SQLite executable on the NAS. Metadata fallbacks use:

| File type | Metadata support |
|---|---|
| JPEG | Native Go EXIF reader |
| HEIC / PNG / WebP | ExifTool at `/opt/bin/exiftool` when available |
| Video | `ffprobe`, otherwise `ffmpeg` on the service PATH |
| Missing metadata | Filename/date fallback, then modification time; visible in preview |

ExifTool is commonly installed through Entware and needs Perl. Existing QNAP
`ffmpeg` may be old; verify representative video dates in the scan preview.
Missing metadata tools do not silently become a package installation failure:
the API reports availability, and files using modification time are flagged
for attention. This package does not install Entware or download executable
dependencies at service startup.

The init script extends PATH to include `/opt/bin` and `/opt/sbin`. It does
not assume `nohup`, systemd, or `start-stop-daemon` exists on QNAP firmware.

## Lifecycle and troubleshooting

App Center controls start/stop. Administrators can also run the installed
`NASMediaArchiver.sh` with `start`, `stop`, `restart`, or `status`:

```sh
/share/CACHEDEV2_DATA/.qpkg/NASMediaArchiver/NASMediaArchiver.sh status
```

The script honors App Center's Enable setting on start, checks PID ownership
before signaling a process, ignores parent SIGHUP, and sends SIGTERM to stop.
It waits up to `STOP_TIMEOUT` seconds for a safe file boundary and does not
force-kill an ongoing copy. If a stop times out, wait and check again before
upgrading or uninstalling. Lifecycle calls use a lock under `/var/lock`; a
stale lock after an interrupted shell operation should be removed only after
confirming no start/stop operation is still running.

Files on the selected package volume:

```text
.qpkg/NASMediaArchiver/          # replaceable application files
.nas-media-archiver/
  service.conf                  # persistent administrative settings, 0600
  token                         # persistent API credential, 0600
  service.log                   # startup/runtime diagnostics
  service.log.1                 # previous log if >5 MiB at startup
  state/                        # persistent SQLite state and recovery journal
```

The PID file is `/var/run/nas-media-archiver.pid`. `/healthz` returns only
service liveness; it reveals no credentials, paths, or task data. The App
Center Open button launches the service's lightweight setup/status page.

Upgrades replace `.qpkg/NASMediaArchiver` and preserve `.nas-media-archiver`.
Uninstall also intentionally retains that data directory and all user media.
Back it up while the service is stopped. For a complete reset, an
administrator can explicitly remove it after uninstalling; doing so removes
task history and the pairing token. App migration between volumes is not
enabled in this package; moving application data requires an explicit stopped
service migration.

## References

- [Official QNAP QDK developer guide](https://github.com/qnap-dev/QDK/blob/master/docs/QDK-Developer-Guide.md)
- [Official QDK source and releases](https://github.com/qnap-dev/QDK)
