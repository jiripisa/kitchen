#!/bin/sh
# install.sh — fetch the latest kitchen release and put it on $PATH.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/jiripisa/kitchen/main/scripts/install.sh | sh

set -eu

OWNER="jiripisa"
REPO="kitchen"
BIN="kitchen"

err() { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }
info() { printf '\033[34m::\033[0m %s\n' "$*"; }
ok()   { printf '\033[32m✓\033[0m %s\n' "$*"; }

need() {
    command -v "$1" >/dev/null 2>&1 || err "$1 is required but not installed"
}
need curl
need uname
need tar
need mktemp

detect_os() {
    case "$(uname -s)" in
        Linux)  echo linux  ;;
        Darwin) echo darwin ;;
        *) err "unsupported OS: $(uname -s)" ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64)  echo amd64 ;;
        aarch64|arm64) echo arm64 ;;
        *) err "unsupported architecture: $(uname -m)" ;;
    esac
}

OS="$(detect_os)"
ARCH="$(detect_arch)"

info "fetching latest release metadata…"
LATEST_URL="https://api.github.com/repos/${OWNER}/${REPO}/releases/latest"
LATEST_JSON="$(curl -fsSL -H 'Accept: application/vnd.github+json' "$LATEST_URL")" \
    || err "could not query GitHub releases"

# Extract the matching tarball URL and checksum URL using grep/sed — keeps
# the dependency footprint to just curl + tar.
TAG="$(printf '%s' "$LATEST_JSON" | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
[ -n "$TAG" ] || err "could not parse release tag"
info "latest release: ${TAG}"

ASSET_PATTERN="${OS}_${ARCH}.tar.gz"
ASSET_URL="$(printf '%s' "$LATEST_JSON" \
    | grep '"browser_download_url"' \
    | grep -- "$ASSET_PATTERN" \
    | head -n1 \
    | sed -E 's/.*"browser_download_url": *"([^"]+)".*/\1/')"
[ -n "$ASSET_URL" ] || err "no asset matching ${ASSET_PATTERN} in release ${TAG}"

CHECKSUMS_URL="$(printf '%s' "$LATEST_JSON" \
    | grep '"browser_download_url"' \
    | grep 'checksums.txt' \
    | head -n1 \
    | sed -E 's/.*"browser_download_url": *"([^"]+)".*/\1/' || true)"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

ASSET_NAME="$(basename "$ASSET_URL")"
info "downloading ${ASSET_NAME}…"
curl -fsSL -o "${TMP_DIR}/${ASSET_NAME}" "$ASSET_URL"

if [ -n "$CHECKSUMS_URL" ]; then
    info "verifying checksum…"
    curl -fsSL -o "${TMP_DIR}/checksums.txt" "$CHECKSUMS_URL"
    if command -v sha256sum >/dev/null 2>&1; then
        ACTUAL="$(sha256sum "${TMP_DIR}/${ASSET_NAME}" | awk '{print $1}')"
    elif command -v shasum >/dev/null 2>&1; then
        ACTUAL="$(shasum -a 256 "${TMP_DIR}/${ASSET_NAME}" | awk '{print $1}')"
    else
        err "neither sha256sum nor shasum is available"
    fi
    EXPECTED="$(grep " ${ASSET_NAME}$" "${TMP_DIR}/checksums.txt" | awk '{print $1}')"
    [ -n "$EXPECTED" ] || err "no checksum entry for ${ASSET_NAME}"
    [ "$ACTUAL" = "$EXPECTED" ] || err "checksum mismatch (got ${ACTUAL}, want ${EXPECTED})"
    ok "checksum verified"
fi

info "extracting…"
tar -xzf "${TMP_DIR}/${ASSET_NAME}" -C "$TMP_DIR"
[ -f "${TMP_DIR}/${BIN}" ] || err "${BIN} not found in archive"
chmod +x "${TMP_DIR}/${BIN}"

# Pick install dir: /usr/local/bin if writable (or with sudo), else $HOME/.local/bin.
INSTALL_DIR="/usr/local/bin"
USE_SUDO=""
if [ ! -w "$INSTALL_DIR" ]; then
    if [ ! -d "$INSTALL_DIR" ] || ! command -v sudo >/dev/null 2>&1; then
        INSTALL_DIR="${HOME}/.local/bin"
        mkdir -p "$INSTALL_DIR"
    else
        USE_SUDO="sudo"
    fi
fi

info "installing to ${INSTALL_DIR}/${BIN}${USE_SUDO:+ (with sudo)}"
$USE_SUDO install -m 0755 "${TMP_DIR}/${BIN}" "${INSTALL_DIR}/${BIN}"

ok "installed kitchen ${TAG} to ${INSTALL_DIR}/${BIN}"

case ":$PATH:" in
    *":${INSTALL_DIR}:"*) ;;
    *)
        printf '\n\033[33mhint:\033[0m %s is not on your PATH. Add it with:\n' "$INSTALL_DIR"
        printf '  export PATH="%s:$PATH"\n\n' "$INSTALL_DIR"
        ;;
esac

printf '\nRun \033[36mkitchen log\033[0m to stream Kubernetes logs.\n'
