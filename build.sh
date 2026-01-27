#!/bin/bash
set -e

APP_NAME="ScoreScrape Bridge"
APP_ID="io.scorescrape.bridge"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

VERSION=$(cat VERSION 2>/dev/null || echo "1.0.0")

# sync VERSION into pkg/bridge for go:embed
cp VERSION pkg/bridge/VERSION

echo "Building ${APP_NAME} v${VERSION} for macOS..."

# ensure fyne CLI is available
if ! command -v fyne &> /dev/null; then
    FYNE_CMD="$(go env GOPATH)/bin/fyne"
    if [ ! -f "$FYNE_CMD" ]; then
        echo "Installing fyne CLI..."
        go install fyne.io/tools/cmd/fyne@latest
    fi
else
    FYNE_CMD="fyne"
fi

rm -rf "${APP_NAME}.app"

$FYNE_CMD package --target darwin \
    --app-id "${APP_ID}" \
    --name "${APP_NAME}" \
    --app-version "${VERSION}" \
    --icon "${SCRIPT_DIR}/assets/Icon.png" \
    --src cmd/bridge \
    --tags gui \
    --release

echo "Built: ${APP_NAME}.app"
