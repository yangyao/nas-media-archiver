#!/bin/sh
set -eu
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)
MODE="qpkg"
BINARY="$REPO_DIR/dist/archive-linux-arm64"
OUTPUT_DIR="$REPO_DIR/dist/qnap"
while [ "$#" -gt 0 ]; do
    case "$1" in
        --stage-only) MODE="stage"; shift ;;
        --binary) [ "$#" -ge 2 ] || exit 2; BINARY=$2; shift 2 ;;
        --output) [ "$#" -ge 2 ] || exit 2; OUTPUT_DIR=$2; shift 2 ;;
        -h|--help)
            echo "Usage: $0 [--stage-only] [--binary FILE] [--output DIRECTORY]"
            exit 0 ;;
        *) echo "Unknown argument: $1" >&2; exit 2 ;;
    esac
done
[ -f "$BINARY" ] || { echo "Build the Linux ARM64 service first: make build-linux-arm64" >&2; exit 1; }
if [ "$MODE" = "qpkg" ] && ! command -v qbuild >/dev/null 2>&1; then
    echo "qbuild is unavailable. Install official QNAP QDK on a build host, or use --stage-only to create a build-input archive (not an installable QPKG)." >&2
    exit 1
fi
# Prevent accidentally packaging a macOS binary or a different Linux CPU.
if command -v od >/dev/null 2>&1; then
    ELF_HEADER=$(od -An -tx1 -N20 "$BINARY" | tr -d ' \n')
    case "$ELF_HEADER" in
        7f454c46020101??????????????????????b700) ;;
        *) echo "Expected a little-endian Linux ARM64 ELF binary: $BINARY" >&2; exit 1 ;;
    esac
fi
mkdir -p "$OUTPUT_DIR"
OUTPUT_DIR=$(CDPATH= cd -- "$OUTPUT_DIR" && pwd)
WORK_DIR=$(mktemp -d "${TMPDIR:-/tmp}/nas-media-archiver-qdk.XXXXXX")
trap 'rm -rf "$WORK_DIR"' 0
trap 'exit 1' 1 2 3 15
mkdir -p "$WORK_DIR/shared" "$WORK_DIR/arm_64/bin"
cp "$SCRIPT_DIR/qpkg.cfg" "$SCRIPT_DIR/package_routines" "$WORK_DIR/"
cp "$SCRIPT_DIR/shared/NASMediaArchiver.sh" "$SCRIPT_DIR/shared/service.conf.example" "$WORK_DIR/shared/"
cp "$BINARY" "$WORK_DIR/arm_64/bin/archive"
chmod 755 "$WORK_DIR/shared/NASMediaArchiver.sh" "$WORK_DIR/arm_64/bin/archive"
# qpkg.cfg contains fixed metadata only, maintained in this repository.
. "$SCRIPT_DIR/qpkg.cfg"
if [ "$MODE" = "stage" ]; then
    ARCHIVE="$OUTPUT_DIR/${QPKG_NAME}_${QPKG_VER}_arm_64_qdk-source.tar.gz"
    # USTAR avoids leaking macOS extended attributes/AppleDouble into QDK inputs.
    COPYFILE_DISABLE=1 tar --format=ustar -czf "$ARCHIVE" -C "$WORK_DIR" .
    echo "QDK build inputs (not installable): $ARCHIVE"
else
    qbuild --root "$WORK_DIR" --build-arch arm_64 --build-dir "$OUTPUT_DIR"
    FOUND=false
    for PACKAGE in "$OUTPUT_DIR"/"${QPKG_NAME}"*.qpkg; do
        [ -f "$PACKAGE" ] || continue
        FOUND=true
        echo "Installable QPKG: $PACKAGE"
    done
    [ "$FOUND" = true ] || { echo "qbuild did not produce a QPKG." >&2; exit 1; }
    (
        cd "$OUTPUT_DIR"
        if command -v sha256sum >/dev/null 2>&1; then
            sha256sum ./*.qpkg > SHA256SUMS.tmp
        else
            shasum -a 256 ./*.qpkg > SHA256SUMS.tmp
        fi
        mv SHA256SUMS.tmp SHA256SUMS
    )

fi
