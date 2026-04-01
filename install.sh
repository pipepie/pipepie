#!/bin/sh
# pipepie installer
# Usage: curl -sSL https://raw.githubusercontent.com/pipepie/pipepie/main/install.sh | sh
set -e

REPO="pipepie/pipepie"
BINARY="pie"
INSTALL_DIR="/usr/local/bin"

# Detect OS and arch
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

# Get latest version
VERSION=$(curl -sSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)
if [ -z "$VERSION" ]; then
  echo "Could not determine latest version"
  exit 1
fi

FILENAME="pie_${OS}_${ARCH}"
if [ "$OS" = "windows" ]; then
  FILENAME="${FILENAME}.zip"
else
  FILENAME="${FILENAME}.tar.gz"
fi

URL="https://github.com/$REPO/releases/download/$VERSION/$FILENAME"

echo ""
echo "  Installing pipepie $VERSION ($OS/$ARCH)"
echo ""

# Download
TMP=$(mktemp -d)
echo "  Downloading $URL..."
curl -sSL "$URL" -o "$TMP/$FILENAME"

# Extract
cd "$TMP"
if [ "$OS" = "windows" ]; then
  unzip -q "$FILENAME"
else
  tar xzf "$FILENAME"
fi

# Install
if [ -w "$INSTALL_DIR" ]; then
  mv "$BINARY" "$INSTALL_DIR/$BINARY"
else
  echo "  Need sudo to install to $INSTALL_DIR"
  sudo mv "$BINARY" "$INSTALL_DIR/$BINARY"
fi
chmod +x "$INSTALL_DIR/$BINARY"

# Cleanup
rm -rf "$TMP"

echo "  ✓ Installed $BINARY to $INSTALL_DIR/$BINARY"
echo ""
echo "  Get started:"
echo "    pie setup     (on your server)"
echo "    pie login     (on your dev machine)"
echo "    pie connect 3000"
echo ""
