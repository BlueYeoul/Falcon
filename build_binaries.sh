#!/bin/bash

# 🦅 Falcon Binary Builder
# This script cross-compiles Falcon for different platforms.

set -e

VERSION="v0.0.1-alpha"
OUTPUT_DIR="dist"

echo "📂 Creating $OUTPUT_DIR directory..."
mkdir -p $OUTPUT_DIR

# Platforms to build for: OS/ARCH
platforms=(
    "darwin/amd64"
    "darwin/arm64"
    "linux/amd64"
    "linux/arm64"
    "linux/386"
    "windows/amd64"
    "windows/386"
)

echo "🛠️  Starting cross-compilation..."

for platform in "${platforms[@]}"
do
    platform_split=(${platform//\// })
    GOOS=${platform_split[0]}
    GOARCH=${platform_split[1]}
    
    output_name="falcon-${GOOS}-${GOARCH}"
    if [ "$GOOS" = "windows" ]; then
        output_name+=".exe"
    fi
    
    echo "📦 Building for $GOOS/$GOARCH -> $OUTPUT_DIR/$output_name"
    
    GOOS=$GOOS GOARCH=$GOARCH go build -o "$OUTPUT_DIR/$output_name" *.go
done

echo ""
echo "✨ All binaries have been created in the '$OUTPUT_DIR' folder!"
ls -lh $OUTPUT_DIR
