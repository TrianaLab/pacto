#!/usr/bin/env bash
set -e

: ${BINARY_NAME:="pacto"}
: ${USE_SUDO:="true"}
: ${DEBUG:="false"}
: ${PACTO_INSTALL_DIR:="/usr/local/bin"}
: ${REPO:="TrianaLab/pacto"}
: ${API_URL:="https://api.github.com/repos/$REPO/releases"}
: ${PLUGINS_REPO:="TrianaLab/pacto-plugins"}
: ${PLUGINS_API_URL:="https://api.github.com/repos/$PLUGINS_REPO/releases"}
PLUGINS="pacto-plugin-schema-infer pacto-plugin-openapi-infer"

HAS_CURL="$(type curl >/dev/null 2>&1 && echo true || echo false)"
HAS_WGET="$(type wget >/dev/null 2>&1 && echo true || echo false)"

# Authenticate api.github.com calls when a token is available. Anonymous API
# access is limited to 60 requests/hour per IP and rate-limits on shared CI
# runners, which surfaces as a spurious "version not found" during tag lookup.
# With a token the limit is 5000/hour. Download URLs (the release CDN) are not
# rate-limited and intentionally left unauthenticated.
GH_API_TOKEN="${GITHUB_TOKEN:-${GH_TOKEN:-}}"
CURL_AUTH=()
WGET_AUTH=()
if [ -n "$GH_API_TOKEN" ]; then
  CURL_AUTH=(-H "Authorization: Bearer $GH_API_TOKEN")
  WGET_AUTH=(--header="Authorization: Bearer $GH_API_TOKEN")
fi

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
      TAG=$(curl -sSL "${CURL_AUTH[@]}" "$API_URL/latest" | grep -E '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    else
      TAG=$(wget "${WGET_AUTH[@]}" -qO- "$API_URL/latest" | grep -E '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    fi
    if [ -z "$TAG" ]; then
      echo "Failed to fetch latest version" >&2
      exit 1
    fi
  else
    TAG="$DESIRED_VERSION"
    status_code=0
    if [ "$HAS_CURL" = "true" ]; then
      status_code=$(curl -sSL "${CURL_AUTH[@]}" -o /dev/null -w "%{http_code}" "$API_URL/tags/$TAG")
    else
      status_code=$(wget "${WGET_AUTH[@]}" --server-response --spider -q "$API_URL/tags/$TAG" 2>&1 | awk '/HTTP\//{print $2}')
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

verifyChecksum() {
  local file="$1"
  local checksums_url="$2"
  local filename="$3"

  tmp_checksums="$(mktemp)"
  if [ "$HAS_CURL" = "true" ]; then
    curl -fsSL "$checksums_url" -o "$tmp_checksums" 2>/dev/null || true
  else
    wget -qO "$tmp_checksums" "$checksums_url" 2>/dev/null || true
  fi

  if [ ! -s "$tmp_checksums" ]; then
    rm -f "$tmp_checksums"
    echo "Warning: checksums file not available, skipping verification" >&2
    return 0
  fi

  expected=$(grep "$filename" "$tmp_checksums" | awk '{print $1}')
  rm -f "$tmp_checksums"

  if [ -z "$expected" ]; then
    echo "Warning: no checksum found for $filename, skipping verification" >&2
    return 0
  fi

  if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "$file" | awk '{print $1}')
  elif command -v shasum >/dev/null 2>&1; then
    actual=$(shasum -a 256 "$file" | awk '{print $1}')
  else
    echo "Warning: sha256sum/shasum not found, skipping verification" >&2
    return 0
  fi

  if [ "$expected" != "$actual" ]; then
    echo "Checksum verification failed for $filename" >&2
    echo "  expected: $expected" >&2
    echo "  actual:   $actual" >&2
    return 1
  fi
}

downloadFile() {
  filename="${BINARY_NAME}_${OS}_${ARCH}${EXT}"
  url="https://github.com/$REPO/releases/download/$TAG/$filename"
  checksums_url="https://github.com/$REPO/releases/download/$TAG/checksums.txt"
  tmp="$(mktemp -d)"
  target="$tmp/$filename"
  if [ "$HAS_CURL" = "true" ]; then
    curl -fsSL "$url" -o "$target"
  else
    wget -qO "$target" "$url"
  fi
  verifyChecksum "$target" "$checksums_url" "$filename"
  chmod +x "$target"
  mv "$target" "$tmp/$BINARY_NAME$EXT"
  DOWNLOAD_DIR="$tmp"
}

installFile() {
  runAsRoot mv "$DOWNLOAD_DIR/$BINARY_NAME$EXT" "$PACTO_INSTALL_DIR/"
  echo "$BINARY_NAME installed to $PACTO_INSTALL_DIR/$BINARY_NAME$EXT"
}

checkPluginsVersion() {
  if [ "$HAS_CURL" = "true" ]; then
    PLUGINS_TAG=$(curl -sSL "${CURL_AUTH[@]}" "$PLUGINS_API_URL/latest" | grep -E '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
  else
    PLUGINS_TAG=$(wget "${WGET_AUTH[@]}" -qO- "$PLUGINS_API_URL/latest" | grep -E '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
  fi
  if [ -z "$PLUGINS_TAG" ]; then
    echo "Warning: failed to fetch latest plugins version, skipping plugin installation" >&2
    echo "  $BINARY_NAME itself is installed. Re-run this script to retry, or download" >&2
    echo "  the plugins from https://github.com/$PLUGINS_REPO/releases" >&2
    return 1
  fi
}

downloadPlugin() {
  plugin_name="$1"
  filename="${plugin_name}_${OS}_${ARCH}${EXT}"
  url="https://github.com/$PLUGINS_REPO/releases/download/$PLUGINS_TAG/$filename"
  tmp="$(mktemp -d)"
  target="$tmp/$filename"
  if [ "$HAS_CURL" = "true" ]; then
    curl -fsSL "$url" -o "$target"
  else
    wget -qO "$target" "$url"
  fi
  chmod +x "$target"
  mv "$target" "$tmp/$plugin_name$EXT"
  DOWNLOAD_DIR="$tmp"
}

installPlugin() {
  plugin_name="$1"
  runAsRoot mv "$DOWNLOAD_DIR/$plugin_name$EXT" "$PACTO_INSTALL_DIR/"
  echo "$plugin_name installed to $PACTO_INSTALL_DIR/$plugin_name$EXT"
}

installPlugins() {
  if ! checkPluginsVersion; then
    return
  fi
  echo "Installing official plugins ($PLUGINS_TAG)..."
  for plugin in $PLUGINS; do
    downloadPlugin "$plugin"
    installPlugin "$plugin"
  done
}

help() {
  echo "Usage: get-pacto.sh [--version <version>] [--no-sudo] [--help]"
  echo "  --version, -v specify version (e.g. v3.2.1); default: latest release"
  echo "  --no-sudo     disable sudo for installation"
  echo "  --help, -h    show help"
  echo ""
  echo "Environment:"
  echo "  PACTO_INSTALL_DIR  install directory (default: /usr/local/bin); must exist"
  echo "  GH_TOKEN           GitHub token for version lookup, to avoid the"
  echo "                     anonymous API rate limit (60 requests/hour per IP)"
}

# Must end on a successful command: bash takes the exit status of the last
# command in an EXIT trap as the script's status, so a bare failing test here
# turned `--help` and "already installed" (both `exit 0`) into exit 1.
cleanup() {
  if [ -n "$DOWNLOAD_DIR" ]; then
    rm -rf "$DOWNLOAD_DIR"
  fi
  return 0
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

echo "Installing $BINARY_NAME $TAG..."
checkInstalledVersion
downloadFile
installFile
installPlugins
