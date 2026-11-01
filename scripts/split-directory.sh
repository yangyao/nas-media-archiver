#!/bin/sh
# Default: read-only preview. Actual movement requires --apply --plan-file FILE.
# Use --resume --apply --plan-file FILE to finish an interrupted operation.
set -eu
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ARCHIVE_BIN="${ARCHIVE_BIN:-$SCRIPT_DIR/../dist/archive}"
exec "$ARCHIVE_BIN" split-directory "$@"
