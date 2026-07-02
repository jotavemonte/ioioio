#!/usr/bin/env bash
#
# Builds the ioioio-mac binary, wraps it in a macOS .app bundle, and packages
# that into a distributable .dmg (with a drag-to-Applications shortcut).
#
# Usage: packaging/build-dmg.sh [version]
#   version defaults to "dev".
#
# Output: dist/ioioio.app and dist/ioioio-<version>.dmg
set -euo pipefail

VERSION="${1:-dev}"
APP_NAME="ioioio"
BUNDLE_ID="com.jotavemonte.ioioio"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/dist"
APP="$DIST/$APP_NAME.app"
DMG="$DIST/$APP_NAME-$VERSION.dmg"

echo "==> Building binary"
rm -rf "$DIST"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
CGO_ENABLED=1 go build -o "$APP/Contents/MacOS/$APP_NAME" "$ROOT/cmd/ioioio-mac"

echo "==> Writing Info.plist"
cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key>
	<string>$APP_NAME</string>
	<key>CFBundleDisplayName</key>
	<string>ioioio</string>
	<key>CFBundleExecutable</key>
	<string>$APP_NAME</string>
	<key>CFBundleIdentifier</key>
	<string>$BUNDLE_ID</string>
	<key>CFBundleVersion</key>
	<string>$VERSION</string>
	<key>CFBundleShortVersionString</key>
	<string>$VERSION</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>LSMinimumSystemVersion</key>
	<string>11.0</string>
	<key>NSHighResolutionCapable</key>
	<true/>
	<key>LSApplicationCategoryType</key>
	<string>public.app-category.developer-tools</string>
</dict>
</plist>
PLIST

# Optional icon: if packaging/icon.png exists, render it into an .icns.
if [[ -f "$ROOT/packaging/icon.png" ]]; then
	echo "==> Generating app icon"
	ICONSET="$DIST/$APP_NAME.iconset"
	mkdir -p "$ICONSET"
	for size in 16 32 128 256 512; do
		sips -z $size $size "$ROOT/packaging/icon.png" --out "$ICONSET/icon_${size}x${size}.png" >/dev/null
		sips -z $((size*2)) $((size*2)) "$ROOT/packaging/icon.png" --out "$ICONSET/icon_${size}x${size}@2x.png" >/dev/null
	done
	iconutil -c icns "$ICONSET" -o "$APP/Contents/Resources/$APP_NAME.icns"
	rm -rf "$ICONSET"
	/usr/libexec/PlistBuddy -c "Add :CFBundleIconFile string $APP_NAME" "$APP/Contents/Info.plist" 2>/dev/null || true
fi

# Ad-hoc sign so Gatekeeper lets a locally built app run (no Developer ID needed
# for personal/local distribution).
echo "==> Ad-hoc code signing"
codesign --force --deep --sign - "$APP"

echo "==> Building DMG"
STAGING="$DIST/dmg"
mkdir -p "$STAGING"
cp -R "$APP" "$STAGING/"
ln -s /Applications "$STAGING/Applications"
hdiutil create -volname "$APP_NAME" -srcfolder "$STAGING" -ov -format UDZO "$DMG" >/dev/null
rm -rf "$STAGING"

echo "==> Done"
echo "    App: $APP"
echo "    DMG: $DMG"
