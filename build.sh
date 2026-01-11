#!/bin/bash
set -e

APP_NAME="ScoreScrape Bridge"
APP_ID="io.scorescrape.bridge"
VERSION=$(cat VERSION 2>/dev/null || echo "1.0.0")

# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

echo "Building ${APP_NAME} for macOS..."

# Check for fyne
if ! command -v fyne &> /dev/null; then
    if ! go env GOPATH &>/dev/null; then
         echo "Error: GOPATH not set"
         exit 1
    fi
    FYNE_CMD="$(go env GOPATH)/bin/fyne"
    if [ ! -f "$FYNE_CMD" ]; then
        echo "Installing fyne CLI..."
        go install fyne.io/tools/cmd/fyne@latest
    fi
else
    FYNE_CMD="fyne"
fi

# Remove existing app bundle
rm -rf "${APP_NAME}.app"

# Build the macOS app
$FYNE_CMD package --target darwin \
    --app-id "${APP_ID}" \
    --name "${APP_NAME}" \
    --app-version "${VERSION}" \
    --icon "${SCRIPT_DIR}/pkg/ui/Icon.png" \
    --src cmd/bridge \
    --tags gui \
    --release

echo "✓ macOS app built: ${APP_NAME}.app"
