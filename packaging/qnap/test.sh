#!/bin/sh
# Installer-hook regression: configuration/state survive repeated installation.
set -eu
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
WORK=$(mktemp -d "${TMPDIR:-/tmp}/nas-media-archiver-package-test.XXXXXX")
trap 'rm -rf "$WORK"' 0
trap 'exit 1' 1 2 3 15
for FILE in "$SCRIPT_DIR"/*.sh "$SCRIPT_DIR/qpkg.cfg" \
    "$SCRIPT_DIR/package_routines" "$SCRIPT_DIR"/shared/*.sh \
    "$SCRIPT_DIR"/shared/*.example; do
    sh -n "$FILE"
done
. "$SCRIPT_DIR/qpkg.cfg"
[ "$QPKG_NAME" = "NASMediaArchiver" ]
[ "$QPKG_SERVICE_PORT" = "$QPKG_WEB_PORT" ]
[ "$QPKG_SERVICE_PROGRAM" = "NASMediaArchiver.sh" ]
[ -f "$SCRIPT_DIR/shared/$QPKG_SERVICE_PROGRAM" ]
SYS_QPKG_DIR="$WORK/volume/.qpkg/$QPKG_NAME"
mkdir -p "$SYS_QPKG_DIR/bin"
cp "$SCRIPT_DIR/shared/service.conf.example" "$SYS_QPKG_DIR/"
cp "$SCRIPT_DIR/shared/NASMediaArchiver.sh" "$SYS_QPKG_DIR/"
printf '#!/bin/sh\nexit 0\n' > "$SYS_QPKG_DIR/bin/archive"
err_log() { echo "$*" >&2; exit 1; }
log() { :; }
. "$SCRIPT_DIR/package_routines"
pkg_post_install
DATA_DIR="$WORK/volume/.nas-media-archiver"
test -f "$DATA_DIR/service.conf"
test -d "$DATA_DIR/state"
test -x "$SYS_QPKG_DIR/bin/archive"
printf '\nNAME="Customized NAS"\n' >> "$DATA_DIR/service.conf"
printf 'persistent state\n' > "$DATA_DIR/state/fixture"
cp "$DATA_DIR/service.conf" "$WORK/expected.conf"
pkg_post_install
cmp "$WORK/expected.conf" "$DATA_DIR/service.conf"
test "$(cat "$DATA_DIR/state/fixture")" = 'persistent state'
printf '#!/bin/sh\nexit 0\n' > "$SYS_QPKG_DIR/NASMediaArchiver.sh"
if (pkg_pre_install) >/dev/null 2>&1; then
    echo 'Upgrade did not reject a still-running service.' >&2
    exit 1
fi
printf '#!/bin/sh\nexit 3\n' > "$SYS_QPKG_DIR/NASMediaArchiver.sh"
pkg_pre_install
echo 'QNAP package syntax, metadata and upgrade preservation checks passed.'
