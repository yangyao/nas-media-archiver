#!/bin/sh
# Build only: uploads to an ordinary NAS-home workspace; never installs QDK or
# the resulting application, and never writes /etc/config or .qpkg.
set -eu
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)
QDK_COMMIT="b7b5f4c86ebe95b10a62d64725515e7cdcf4bb35"
[ "$#" -ge 1 ] || { echo "Usage: $0 user@nas [--binary FILE] [--output DIRECTORY] [--work-dir NAS-PATH]" >&2; exit 2; }
HOST=$1
shift
case "$HOST" in -*|*[!A-Za-z0-9_@.:-]*) echo "Invalid SSH host." >&2; exit 2 ;; esac
BINARY="$REPO_DIR/dist/archive-linux-arm64"
OUTPUT_DIR="$REPO_DIR/dist/qnap"
REMOTE_DIR=""
while [ "$#" -gt 0 ]; do
    case "$1" in
        --binary) [ "$#" -ge 2 ] || exit 2; BINARY=$2; shift 2 ;;
        --output) [ "$#" -ge 2 ] || exit 2; OUTPUT_DIR=$2; shift 2 ;;
        --work-dir) [ "$#" -ge 2 ] || exit 2; REMOTE_DIR=$2; shift 2 ;;
        *) echo "Unknown argument: $1" >&2; exit 2 ;;
    esac
done
if [ -z "$REMOTE_DIR" ]; then
    REMOTE_HOME=$(ssh -o BatchMode=yes "$HOST" 'printf "%s\n" "$HOME"')
    REMOTE_DIR="$REMOTE_HOME/nas-media-archiver-qdk-build"
fi
# The remote shell command contains only this restricted build path. Reject
# whitespace, shell metacharacters, root, and system/application directories.
case "$REMOTE_DIR" in
    /share/homes/*|/share/CACHEDEV*_DATA/homes/*) ;;
    *) echo "Build workspace must be inside a NAS home directory." >&2; exit 2 ;;
esac
case "$REMOTE_DIR" in
    *[!A-Za-z0-9_./-]*|*/../*|*/..|*/.qpkg/*)
        echo "Unsupported build workspace path." >&2; exit 2 ;;
esac
for TOOL in ssh scp curl tar; do
    command -v "$TOOL" >/dev/null 2>&1 || { echo "Missing local tool: $TOOL" >&2; exit 1; }
done
LOCAL_DIR=$(mktemp -d "${TMPDIR:-/tmp}/nas-media-archiver-remote-build.XXXXXX")
trap 'rm -rf "$LOCAL_DIR"' 0
trap 'exit 1' 1 2 3 15
mkdir -p "$LOCAL_DIR/QDK" "$LOCAL_DIR/stage" "$OUTPUT_DIR"
"$SCRIPT_DIR/build.sh" --stage-only --binary "$BINARY" --output "$LOCAL_DIR/stage"
curl --fail --location --silent --show-error \
    "https://codeload.github.com/qnap-dev/QDK/tar.gz/$QDK_COMMIT" \
    -o "$LOCAL_DIR/qdk-source.tar.gz"
tar -xzf "$LOCAL_DIR/qdk-source.tar.gz" --strip-components=1 -C "$LOCAL_DIR/QDK"
COPYFILE_DISABLE=1 tar --format=ustar -czf "$LOCAL_DIR/qdk-tools.tar.gz" \
    -C "$LOCAL_DIR/QDK/shared" bin scripts template doc
cat > "$LOCAL_DIR/build-remote.sh" <<'REMOTE_SCRIPT'
#!/bin/sh
set -eu
PATH="/opt/bin:/opt/sbin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
export PATH
WORK=$1
cd "$WORK"
WORK=$(pwd -P)
case "$WORK" in
    /share/homes/*|/share/CACHEDEV*_DATA/homes/*) ;;
    *) echo "Resolved build workspace is outside NAS homes." >&2; exit 1 ;;
esac
for CHILD in qdk source output; do
    [ ! -L "$WORK/$CHILD" ] || { echo "Build directory must not be a symlink: $CHILD" >&2; exit 1; }
done
# Reusing a workspace must not retain files removed from a newer package.
rm -rf "$WORK/source" "$WORK/qdk"
mkdir -p qdk source output
tar -xzf qdk-tools.tar.gz -C qdk
tar -xzf qpkg-source.tar.gz -C source
printf 'QDK_VERSION=2.5.3\nQDK_PATH=%s/qdk\n' "$WORK" > qdk.conf
# qbuild on a NAS initially looks for system registration; this local qdk.conf
# supplies its path without installing/registering QDK or modifying its source.
if [ -x /opt/bin/bash ]; then BASH_BIN=/opt/bin/bash; else BASH_BIN=/bin/bash; fi
for TOOL in rsync md5sum gzip awk sed tar hexdump; do
    command -v "$TOOL" >/dev/null 2>&1 || { echo "Missing NAS build tool: $TOOL" >&2; exit 1; }
done
"$BASH_BIN" qdk/bin/qbuild --root "$WORK/source" --build-arch arm_64 --build-dir "$WORK/output"
for PACKAGE in "$WORK"/output/NASMediaArchiver_*.qpkg; do
    [ -f "$PACKAGE" ] || continue
    "$BASH_BIN" qdk/bin/qbuild --query info "$PACKAGE"
    mkdir -p "$WORK/inspect/data"
    "$BASH_BIN" qdk/bin/qbuild --extract "$PACKAGE" "$WORK/inspect"
    if tar -tzf "$WORK/inspect/data.tar.gz" | grep -E '(^|/)\._' >/dev/null; then
        echo "Unexpected macOS metadata in QPKG payload." >&2
        exit 1
    fi
    tar -xzf "$WORK/inspect/data.tar.gz" -C "$WORK/inspect/data"
    cmp "$WORK/source/arm_64/bin/archive" "$WORK/inspect/data/bin/archive"
    cmp "$WORK/source/shared/NASMediaArchiver.sh" "$WORK/inspect/data/NASMediaArchiver.sh"
done
REMOTE_SCRIPT
ssh -o BatchMode=yes "$HOST" "mkdir -p '$REMOTE_DIR'"
scp "$LOCAL_DIR/qdk-tools.tar.gz" "$LOCAL_DIR/build-remote.sh" "$HOST:$REMOTE_DIR/"
for SOURCE in "$LOCAL_DIR"/stage/*_qdk-source.tar.gz; do
    scp "$SOURCE" "$HOST:$REMOTE_DIR/qpkg-source.tar.gz"
done
ssh -o BatchMode=yes "$HOST" "sh '$REMOTE_DIR/build-remote.sh' '$REMOTE_DIR'"
scp "$HOST:$REMOTE_DIR/output/NASMediaArchiver_*.qpkg" "$OUTPUT_DIR/"

    (
        cd "$OUTPUT_DIR"
        if command -v sha256sum >/dev/null 2>&1; then
            sha256sum ./*.qpkg > SHA256SUMS.tmp
        else
            shasum -a 256 ./*.qpkg > SHA256SUMS.tmp
        fi
        mv SHA256SUMS.tmp SHA256SUMS
    )
echo "Built QPKG copied to: $OUTPUT_DIR"
echo "Build workspace retained for inspection: $HOST:$REMOTE_DIR"
