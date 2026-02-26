# 🦅 Falcon CLI Installer
# This script installs Falcon by either building from source (if Go is present)
# or downloading the pre-compiled binary from GitHub.

REPO_URL="https://github.com/BlueYeoul/Falcon"
INSTALL_PATH="/usr/local/bin"
VERSION="v0.0.1-alpha"

set -e

install_binary() {
    local src=$1
    local dest="$INSTALL_PATH/falcon"
    echo "🚚 Installing to $INSTALL_PATH..."
    if [ -w "$INSTALL_PATH" ]; then
        mv "$src" "$dest"
        ln -sf "$dest" "$INSTALL_PATH/fco"
        chmod +x "$dest"
    else
        echo "🔐 Password required for system installation:"
        sudo mv "$src" "$dest"
        sudo ln -sf "$dest" "$INSTALL_PATH/fco"
        sudo chmod +x "$dest"
    fi
}

if command -v go >/dev/null 2>&1; then
    echo "🛠️  Go detected. Building Falcon from source..."
    go build -o falcon_bin *.go
    install_binary "falcon_bin"
else
    echo "🌐 Go not found. Downloading pre-compiled binary..."
    
    # Detect OS and Architecture
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)
    if [ "$ARCH" = "x86_64" ]; then ARCH="amd64"; fi
    if [ "$ARCH" = "arm64" ] || [ "$ARCH" = "aarch64" ]; then ARCH="arm64"; fi

    BINARY_NAME="falcon-${OS}-${ARCH}"
    DOWNLOAD_URL="${REPO_URL}/releases/download/v${VERSION}/${BINARY_NAME}"
    
    echo "📥 Downloading $BINARY_NAME from $DOWNLOAD_URL..."
    curl -L "$DOWNLOAD_URL" -o falcon_bin || {
        echo "❌ Error: Could not download binary. Please ensure Go is installed or check the repository releases."
        exit 1
    }
    install_binary "falcon_bin"
fi

echo ""
echo "✨ Falcon has been installed successfully!"
echo "🚀 Available commands: falcon, fco"
echo ""
fco version
