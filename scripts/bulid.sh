#!/bin/bash

set -e

########################################
# Config
########################################

APP_NAME="tianlong-agent"

VERSION=$(git describe --tags --abbrev=0 2>/dev/null || echo "v1.2.3")
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")

BUILD_DATE=$(date "+%Y%m%d")

DIST_ROOT="dist"
DIST_DIR="${DIST_ROOT}/${BUILD_DATE}"

mkdir -p "$DIST_DIR"

########################################
# Build Info
########################################

FILE_PREFIX="${APP_NAME}-${VERSION}-${BUILD_DATE}"

LDFLAGS="-s -w \
-X main.Version=${VERSION} \
-X main.Commit=${COMMIT} \
-X main.BuildTime=${BUILD_DATE}"

echo ""
echo "========================================="
echo "App Name   : $APP_NAME"
echo "Version    : $VERSION"
echo "Date       : $BUILD_DATE"
echo "Commit     : $COMMIT"
echo "Output Dir : $DIST_DIR"
echo "========================================="
echo ""

########################################
# Build
########################################

build() {
    GOOS=$1
    GOARCH=$2
    OUTPUT=$3

    echo "▶ Building $OUTPUT"

    GOOS=$GOOS GOARCH=$GOARCH \
    go build \
    -trimpath \
    -ldflags="$LDFLAGS" \
    -o "$OUTPUT"
}

build darwin arm64 \
"${DIST_DIR}/${FILE_PREFIX}-darwin-arm64"

build darwin amd64 \
"${DIST_DIR}/${FILE_PREFIX}-darwin-amd64"

build linux amd64 \
"${DIST_DIR}/${FILE_PREFIX}-linux-amd64"

build windows amd64 \
"${DIST_DIR}/${FILE_PREFIX}-windows-amd64.exe"

########################################
# Package
########################################

echo ""
echo "▶ Packaging..."
echo ""

cd "$DIST_DIR"

tar -czf \
"${FILE_PREFIX}-darwin-arm64.tar.gz" \
"${FILE_PREFIX}-darwin-arm64"

tar -czf \
"${FILE_PREFIX}-darwin-amd64.tar.gz" \
"${FILE_PREFIX}-darwin-amd64"

tar -czf \
"${FILE_PREFIX}-linux-amd64.tar.gz" \
"${FILE_PREFIX}-linux-amd64"

zip -q \
"${FILE_PREFIX}-windows-amd64.zip" \
"${FILE_PREFIX}-windows-amd64.exe"

########################################
# SHA256
########################################

if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 \
    *.tar.gz \
    *.zip \
    > SHA256SUMS
elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum \
    *.tar.gz \
    *.zip \
    > SHA256SUMS
fi

cd - >/dev/null

########################################
# Result
########################################

echo ""
echo "✅ Build Success"
echo ""
echo "Artifacts:"
echo ""

find "$DIST_DIR" -type f | sort

echo ""
echo "Location:"
echo "$DIST_DIR"
echo ""