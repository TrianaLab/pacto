#!/usr/bin/env bash
set -e

: ${BINARY_NAME:="pacto"}
: ${USE_SUDO:="true"}
: ${DEBUG:="false"}
: ${PACTO_INSTALL_DIR:="/usr/local/bin"}
: ${REPO:="TrianaLab/pacto"}
: ${API_URL:="https://api.github.com/repos/$REPO/releases"}

HAS_CURL="$(type curl >/dev/null 2>&1 && echo true || echo false)"
HAS_WGET="$(type wget >/dev/null 2>&1 && echo true || echo false)"

initArch() {
  ARCH=$(uname -m)
  case $ARCH in
    x86_64|amd64) ARCH="amd64";;
    aarch64|arm64) ARCH="arm64";;
    *) echo "Unsupported architecture: $ARCH" >&2; exit 1;;
  esac
}

initOS() {
  OS=$(uname | tr '[:upper:]' '[:lower:]')
  case "$OS" in
    linux|darwin) ;;
    mingw*|msys*|cygwin*) OS="windows";;
    *) echo "Unsupported OS: $OS" >&2; exit 1;;
  esac
}

runAsRoot() {
  if [ "$USE_SUDO" = "true" ] && [ "$(id -u)" -ne 0 ]; then
    sudo "$@"
  else
    "$@"
  fi
}

verifySupported() {
  supported="linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 windows-amd64 windows-arm64"
  if ! echo "$supported" | grep -qw "$OS-$ARCH"; then
    echo "No prebuilt binary for $OS-$ARCH" >&2
    exit 1
  fi
  if [ "$HAS_CURL" != "true" ] && [ "$HAS_WGET" != "true" ]; then
    echo "curl or wget is required" >&2
    exit 1
  fi
}

checkDesiredVersion() {
  if [ -z "$DESIRED_VERSION" ]; then
    if [ "$HAS_CURL" = "true" ]; then
      TAG=$(curl -sSL "$API_URL/latest" | grep -E '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    else
      TAG=$(wget -qO- "$API_URL/latest" | grep -E '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    fi
    if [ -z "$TAG" ]; then
      echo "Failed to fetch latest version" >&2
      exit 1
    fi
  else
    TAG="$DESIRED_VERSION"
    status_code=0
    if [ "$HAS_CURL" = "true" ]; then
      status_code=$(curl -sSL -o /dev/null -w "%{http_code}" "$API_URL/tags/$TAG")
    else
      status_code=$(wget --server-response --spider -q "$API_URL/tags/$TAG" 2>&1 | awk '/HTTP\//{print $2}')
    fi
    if [ "$status_code" != "200" ]; then
      echo "Version $TAG not found in $REPO releases" >&2
      exit 1
    fi
  fi
}

checkInstalledVersion() {
  if [ -f "$PACTO_INSTALL_DIR/$BINARY_NAME$EXT" ]; then
    INSTALLED=$("$PACTO_INSTALL_DIR/$BINARY_NAME$EXT" version 2>/dev/null || true)
    if echo "$INSTALLED" | grep -q "$TAG"; then
      echo "$BINARY_NAME $TAG is already installed"
      exit 0
    fi
  fi
}

downloadFile() {
  filename="${BINARY_NAME}_${OS}_${ARCH}${EXT}"
  url="https://github.com/$REPO/releases/download/$TAG/$filename"
  tmp="$(mktemp -d)"
  target="$tmp/$filename"
  if [ "$HAS_CURL" = "true" ]; then
    curl -fsSL "$url" -o "$target"
  else
    wget -qO "$target" "$url"
  fi
  chmod +x "$target"
  mv "$target" "$tmp/$BINARY_NAME$EXT"
  DOWNLOAD_DIR="$tmp"
}

installFile() {
  runAsRoot mv "$DOWNLOAD_DIR/$BINARY_NAME$EXT" "$PACTO_INSTALL_DIR/"
  echo "$BINARY_NAME installed to $PACTO_INSTALL_DIR/$BINARY_NAME$EXT"
}

help() {
  echo "Usage: get-pacto.sh [--version <version>] [--no-sudo] [--help]"
  echo "  --version, -v specify version (e.g. v1.2.3)"
  echo "  --no-sudo     disable sudo for installation"
  echo "  --help, -h    show help"
}

cleanup() {
  [ -n "$DOWNLOAD_DIR" ] && rm -rf "$DOWNLOAD_DIR"
}

trap cleanup EXIT

while [ $# -gt 0 ]; do
  case $1 in
    --version|-v)
      shift
      if [ -n "$1" ]; then
        DESIRED_VERSION="$1"
      else
        echo "Expected version after $1" >&2
        exit 1
      fi
      ;;
    --no-sudo)
      USE_SUDO="false"
      ;;
    --help|-h)
      help
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      help
      exit 1
      ;;
  esac
  shift
done

initArch
initOS
verifySupported
checkDesiredVersion

EXT=""
if [ "$OS" = "windows" ]; then
  EXT=".exe"
fi

checkInstalledVersion
downloadFile
installFile
