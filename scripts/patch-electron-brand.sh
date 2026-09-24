#!/bin/bash
# Patch downloaded Electron app bundle with WES Code branding.
# Run after `node build/lib/electron` downloads Electron.
# Idempotent: safe to run multiple times.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EDITOR_DIR="$(dirname "$SCRIPT_DIR")/editor"
ELECTRON_DIR="$EDITOR_DIR/.build/electron"

OLD_NAME="Code - OSS"
NEW_NAME="WES Code"
NEW_EXE="wescode"
NEW_ID="com.weisyn.wescode"
NEW_ICON="wescode.icns"

if [[ "$OSTYPE" != "darwin"* ]]; then
    echo "patch-electron-brand: macOS only, skipping"
    exit 0
fi

OLD_APP="$ELECTRON_DIR/$OLD_NAME.app"
NEW_APP="$ELECTRON_DIR/$NEW_NAME.app"

# Determine which app exists
if [ -d "$NEW_APP" ]; then
    echo "✓ Already branded as '$NEW_NAME'"
    APP="$NEW_APP"
elif [ -d "$OLD_APP" ]; then
    APP="$OLD_APP"
else
    echo "⚠ No Electron .app found in $ELECTRON_DIR"
    exit 0
fi

echo "=== Patching Electron bundle: $OLD_NAME → $NEW_NAME ==="

# 1. Patch main app Info.plist + rename Contents/MacOS/Electron
# gulp-electron only renames the MacOS binary when opts.darwinExecutable is
# set; this script is the fallback for the .build/electron copy used by
# `make run` / code.sh, so it must do the same rename or Finder keeps
# showing a file literally named "Electron".
PLIST="$APP/Contents/Info.plist"
plutil -replace CFBundleDisplayName -string "$NEW_NAME" "$PLIST"
plutil -replace CFBundleName -string "$NEW_NAME" "$PLIST"
plutil -replace CFBundleIdentifier -string "$NEW_ID" "$PLIST"
plutil -replace CFBundleIconFile -string "$NEW_ICON" "$PLIST"
plutil -replace CFBundleExecutable -string "$NEW_EXE" "$PLIST"
# Accept either stock Electron or a prior "WES Code" rename.
for candidate in Electron "WES Code" "$NEW_EXE"; do
    if [ -f "$APP/Contents/MacOS/$candidate" ] && [ "$candidate" != "$NEW_EXE" ]; then
        mv "$APP/Contents/MacOS/$candidate" "$APP/Contents/MacOS/$NEW_EXE"
        echo "  ✓ MacOS executable: $candidate → $NEW_EXE"
        break
    fi
done
if [ -f "$APP/Contents/MacOS/$NEW_EXE" ]; then
    echo "  ✓ MacOS executable: $NEW_EXE"
else
    echo "  ⚠ MacOS executable not found under Contents/MacOS/"
fi
echo "  ✓ Main Info.plist"

# 2. Copy WES Code icon
ICON_SRC="$EDITOR_DIR/resources/darwin/code.icns"
if [ -f "$ICON_SRC" ]; then
    cp "$ICON_SRC" "$APP/Contents/Resources/$NEW_ICON"
    echo "  ✓ Icon: $NEW_ICON"
fi

# 3. Rename Helper apps + executables + plists
FRAMEWORKS="$APP/Contents/Frameworks"
for suffix in "" " (GPU)" " (Plugin)" " (Renderer)"; do
    OLD_HELPER="$FRAMEWORKS/${OLD_NAME} Helper${suffix}.app"
    NEW_HELPER="$FRAMEWORKS/${NEW_NAME} Helper${suffix}.app"

    if [ -d "$OLD_HELPER" ]; then
        # Rename executable
        OLD_EXE="$OLD_HELPER/Contents/MacOS/${OLD_NAME} Helper${suffix}"
        NEW_EXE="$OLD_HELPER/Contents/MacOS/${NEW_NAME} Helper${suffix}"
        if [ -f "$OLD_EXE" ]; then
            mv "$OLD_EXE" "$NEW_EXE"
        fi

        # Update Helper Info.plist
        HELPER_PLIST="$OLD_HELPER/Contents/Info.plist"
        if [ -f "$HELPER_PLIST" ]; then
            plutil -replace CFBundleName -string "${NEW_NAME} Helper${suffix}" "$HELPER_PLIST"
            plutil -replace CFBundleExecutable -string "${NEW_NAME} Helper${suffix}" "$HELPER_PLIST" 2>/dev/null || true
            if [ -z "$suffix" ]; then
                plutil -replace CFBundleIdentifier -string "${NEW_ID}.helper" "$HELPER_PLIST"
            fi
        fi

        # Rename Helper .app directory
        mv "$OLD_HELPER" "$NEW_HELPER"
        echo "  ✓ Helper${suffix}"
    elif [ -d "$NEW_HELPER" ]; then
        echo "  ✓ Helper${suffix} (already renamed)"
    fi
done

# 4. Rename main .app directory
if [ "$APP" != "$NEW_APP" ]; then
    mv "$APP" "$NEW_APP"
    echo "  ✓ App bundle: $NEW_NAME.app"
fi

# 5. Refresh Launch Services
/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister -f "$NEW_APP" 2>/dev/null || true

echo "=== Done: $NEW_NAME.app ==="
