#!/bin/bash
set -euo pipefail

DESKTOP_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUTPUT_DIR="${OUTPUT_DIR:-$DESKTOP_ROOT/dist}"
APP_PATH="$OUTPUT_DIR/NASArchiver.app"
BUILD_CONFIGURATION="${BUILD_CONFIGURATION:-release}"

swift build --package-path "$DESKTOP_ROOT" -c "$BUILD_CONFIGURATION"
BIN_DIR="$(swift build --package-path "$DESKTOP_ROOT" -c "$BUILD_CONFIGURATION" --show-bin-path)"
mkdir -p "$APP_PATH/Contents/MacOS" "$APP_PATH/Contents/Resources"
cp "$BIN_DIR/NASArchiver" "$APP_PATH/Contents/MacOS/NASArchiver"
# Raster source is generated once and committed; packaging needs no network.
ICON_WORK=$(mktemp -d "${TMPDIR:-/tmp}/nas-archiver-icon.XXXXXX")
trap 'rm -rf "$ICON_WORK"' EXIT
mkdir -p "$ICON_WORK/AppIcon.iconset"
for SIZE in 16 32 128 256 512; do
    sips -z "$SIZE" "$SIZE" "$DESKTOP_ROOT/Assets/AppIcon.png" --out "$ICON_WORK/AppIcon.iconset/icon_${SIZE}x${SIZE}.png" >/dev/null
    DOUBLE=$((SIZE * 2))
    sips -z "$DOUBLE" "$DOUBLE" "$DESKTOP_ROOT/Assets/AppIcon.png" --out "$ICON_WORK/AppIcon.iconset/icon_${SIZE}x${SIZE}@2x.png" >/dev/null
done
iconutil -c icns "$ICON_WORK/AppIcon.iconset" -o "$APP_PATH/Contents/Resources/AppIcon.icns"
cat > "$APP_PATH/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
    <key>CFBundleIdentifier</key><string>com.yangyao.nas-media-archiver</string>
    <key>CFBundleName</key><string>NAS Archiver</string>
    <key>CFBundleDisplayName</key><string>NAS Archiver</string>
    <key>CFBundleExecutable</key><string>NASArchiver</string>
    <key>CFBundlePackageType</key><string>APPL</string>
    <key>CFBundleShortVersionString</key><string>0.2.9</string>
    <key>CFBundleVersion</key><string>11</string>
    <key>CFBundleIconFile</key><string>AppIcon</string>
    <key>LSMinimumSystemVersion</key><string>26.0</string>
    <key>LSApplicationCategoryType</key><string>public.app-category.utilities</string>
    <key>NSPrincipalClass</key><string>NSApplication</string>
    <key>NSHighResolutionCapable</key><true/>
    <key>CFBundleDevelopmentRegion</key><string>zh-Hans</string>
    <key>CFBundleLocalizations</key><array><string>zh-Hans</string></array>
    <key>NSLocalNetworkUsageDescription</key><string>连接局域网中的归档服务，浏览目录并管理归档任务。</string>
    <key>NSAppTransportSecurity</key><dict>
        <key>NSAllowsArbitraryLoads</key><true/>
        <key>NSAllowsLocalNetworking</key><true/>
    </dict>
</dict></plist>
PLIST
plutil -lint "$APP_PATH/Contents/Info.plist"
# A stable Apple-issued identity lets macOS retain Local Network permissions
# across development builds. Ad hoc signing is a portable fallback for CI.
SIGN_IDENTITY="${CODE_SIGN_IDENTITY:-}"
if [ -z "$SIGN_IDENTITY" ]; then
    SIGN_IDENTITY=$(security find-identity -v -p codesigning 2>/dev/null | awk '/Apple Development:|Developer ID Application:/ { print $2; exit }')
fi
SIGN_IDENTITY="${SIGN_IDENTITY:--}"
codesign --force --sign "$SIGN_IDENTITY" "$APP_PATH"
codesign --verify --strict "$APP_PATH"
printf '\nBuilt app: %s\n' "$APP_PATH"
