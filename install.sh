# 🦅 Falcon CLI Installer
# This script installs Falcon by either building from source (if Go is present)
# or downloading the pre-compiled binary from GitHub.

REPO_URL="https://github.com/BlueYeoul/Falcon"
# Default installation path
if [ -w "/usr/local/bin" ]; then
    INSTALL_PATH="/usr/local/bin"
elif [ -d "$HOME/.local/bin" ] && [ -w "$HOME/.local/bin" ]; then
    INSTALL_PATH="$HOME/.local/bin"
else
    INSTALL_PATH="/usr/local/bin" # Fallback to sudo-required path
fi

VERSION="v0.0.1-alpha+2"
DIST_DIR="./dist"

set -e

install_binary() {
    local src=$1
    local dest="$INSTALL_PATH/falcon"
    echo "🚚 Installing to $INSTALL_PATH..."
    mkdir -p "$INSTALL_PATH" 2>/dev/null || sudo mkdir -p "$INSTALL_PATH"
    
    if [ -w "$INSTALL_PATH" ]; then
        cp "$src" "$dest"
        ln -sf "$dest" "$INSTALL_PATH/fco" 2>/dev/null || sudo ln -sf "$dest" "$INSTALL_PATH/fco"
        chmod +x "$dest"
    else
        echo "🔐 Password required for system installation:"
        sudo cp "$src" "$dest"
        sudo ln -sf "$dest" "$INSTALL_PATH/fco"
        sudo chmod +x "$dest"
    fi
}

if command -v go >/dev/null 2>&1; then
    echo "🛠️  Go detected. Building Falcon from source..."
    go build -o falcon_bin *.go
    install_binary "falcon_bin"
else
    # Detect OS and Architecture
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)
    if [ "$ARCH" = "x86_64" ]; then ARCH="amd64"; fi
    if [ "$ARCH" = "arm64" ] || [ "$ARCH" = "aarch64" ]; then ARCH="arm64"; fi

    BINARY_NAME="falcon-${OS}-${ARCH}"
    
    if [ -f "$DIST_DIR/$BINARY_NAME" ]; then
        echo "📦 Found local binary in $DIST_DIR. Using it..."
        install_binary "$DIST_DIR/$BINARY_NAME"
    else
        DOWNLOAD_URL="${REPO_URL}/releases/download/v${VERSION}/${BINARY_NAME}"
        echo "🌐 Go not found and no local binary. Downloading $BINARY_NAME from $DOWNLOAD_URL..."
        
        curl -L "$DOWNLOAD_URL" -o falcon_bin || {
            echo "❌ Error: Could not download binary. Please ensure Go is installed or check the repository releases."
            exit 1
        }
        install_binary "falcon_bin"
        rm -f falcon_bin
    fi
fi

echo ""
echo "✨ Falcon has been installed successfully to $INSTALL_PATH!"
echo "🚀 Available commands: falcon, fco"
echo ""

# Check if INSTALL_PATH is in PATH
if [[ ":$PATH:" != *":$INSTALL_PATH:"* ]]; then
    echo "⚠️  Warning: $INSTALL_PATH is not in your PATH."
    echo "Please add it to your shell config (e.g., ~/.zshrc or ~/.bashrc):"
    echo "  export PATH=\"\$PATH:$INSTALL_PATH\""
    echo ""
fi

$INSTALL_PATH/falcon version
