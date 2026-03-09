#!/bin/bash
set -e

echo "Installing cleancode..."

# Check prerequisites
if ! command -v go &> /dev/null; then
    echo "Error: Go is not installed. Install it from https://go.dev/dl/"
    exit 1
fi

if ! command -v claude &> /dev/null; then
    echo "Warning: Claude Code CLI not found. Install it from https://docs.anthropic.com/en/docs/claude-code"
    echo "         cleancode index/search/callers/stats will work, but review/explain require claude."
fi

if ! command -v gcc &> /dev/null && ! command -v clang &> /dev/null; then
    echo "Error: C compiler not found. Install Xcode CLI tools (macOS) or gcc (Linux)."
    echo "  macOS: xcode-select --install"
    echo "  Linux: sudo apt install gcc"
    exit 1
fi

# Clone and build
TMPDIR=$(mktemp -d)
echo "Cloning..."
git clone --depth 1 https://github.com/angus-lau/cleancode.git "$TMPDIR/cleancode" 2>/dev/null

echo "Building..."
cd "$TMPDIR/cleancode"
CGO_ENABLED=1 go build -o cleancode ./cmd/cleancode/

echo "Installing to /usr/local/bin (requires sudo)..."
sudo mv cleancode /usr/local/bin/cleancode

# Cleanup
rm -rf "$TMPDIR"

echo ""
echo "cleancode installed successfully!"
echo ""
echo "Quick start:"
echo "  cd your-project"
echo "  cleancode init"
echo "  cleancode index"
echo "  cleancode review"
