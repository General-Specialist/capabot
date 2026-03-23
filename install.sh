#!/bin/sh
set -e

REPO="General-Specialist/capabot"
BINARY="capabot"
INSTALL_DIR="${CAPABOT_INSTALL_DIR:-$HOME/.local/bin}"

# Detect OS and arch
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

case "$OS" in
  darwin|linux) ;;
  *) echo "Unsupported OS: $OS. Download manually from https://github.com/$REPO/releases"; exit 1 ;;
esac

# Get latest release tag
LATEST=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)
if [ -z "$LATEST" ]; then
  echo "Failed to fetch latest release"
  exit 1
fi

FILENAME="${BINARY}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/$REPO/releases/download/$LATEST/$FILENAME"

echo "Downloading $BINARY $LATEST for $OS/$ARCH..."

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

curl -fsSL "$URL" -o "$TMP/$FILENAME"
tar -xzf "$TMP/$FILENAME" -C "$TMP"

mkdir -p "$INSTALL_DIR"

if [ -w "$INSTALL_DIR" ]; then
  mv "$TMP/$BINARY" "$INSTALL_DIR/$BINARY"
else
  echo "Need sudo to install to $INSTALL_DIR"
  sudo mv "$TMP/$BINARY" "$INSTALL_DIR/$BINARY"
fi

chmod +x "$INSTALL_DIR/$BINARY"

RESET="\033[0m"
BOLD="\033[1m"
GREEN="\033[32m"

printf "\n"
printf "  ${GREEN}${BOLD}✓${RESET} capabot ${LATEST} installed to ${INSTALL_DIR}\n"
printf "\n"
printf "  Run ${BOLD}capabot serve${RESET} to start the server.\n"
printf "  Run ${BOLD}capabot --help${RESET} for all commands.\n"
printf "\n"

# Check if install dir is in PATH
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) printf "  ${BOLD}Note:${RESET} Add ${INSTALL_DIR} to your PATH:\n"
     printf "    export PATH=\"${INSTALL_DIR}:\$PATH\"\n"
     printf "\n" ;;
esac
