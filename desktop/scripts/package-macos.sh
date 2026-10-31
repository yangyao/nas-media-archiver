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
cat > "$APP_PATH/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
    <key>CFBundleIdentifier</key><string>com.yangyao.nas-media-archiver</string>
    <key>CFBundleName</key><string>NAS Archiver</string>
    <key>CFBundleDisplayName</key><string>NAS Archiver</string>
    <key>CFBundleExecutable</key><string>NASArchiver</string>
    <key>CFBundlePackageType</key><string>APPL</string>
    <key>CFBundleShortVersionString</key><string>0.2.0</string>
    <key>CFBundleVersion</key><string>1</string>
    <key>LSMinimumSystemVersion</key><string>26.0</string>
    <key>LSApplicationCategoryType</key><string>public.app-category.utilities</string>
    <key>NSPrincipalClass</key><string>NSApplication</string>
    <key>NSHighResolutionCapable</key><true/>
    <key>CFBundleDevelopmentRegion</key><string>zh-Hant</string>
    <key>CFBundleLocalizations</key><array><string>zh-Hant</string></array>
    <key>NSLocalNetworkUsageDescription</key><string>連線到區域網路上的歸檔服務端，瀏覽資料夾並管理歸檔任務。服務端可執行在 NAS 上。</string>
    <key>NSAppTransportSecurity</key><dict>
        <key>NSAllowsArbitraryLoads</key><true/>
        <key>NSAllowsLocalNetworking</key><true/>
    </dict>
</dict></plist>
PLIST
plutil -lint "$APP_PATH/Contents/Info.plist"
codesign --force --sign "${CODE_SIGN_IDENTITY:--}" "$APP_PATH"
codesign --verify --strict "$APP_PATH"
printf '\nBuilt app: %s\n' "$APP_PATH"
