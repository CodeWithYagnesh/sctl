#!/bin/sh
# install.sh — sctl installer
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/CodeWithYagnesh/sctl/main/install.sh | sh
#   curl -fsSL ...install.sh | VERSION=v1.0.8 sh          # specific version via env
#   ./install.sh                                            # latest
#   ./install.sh v1.0.8                                    # specific version via arg
#
set -e

OWNER="CodeWithYagnesh"
REPO="sctl"
BINARY_NAME="sctl"

# ── Colours ──────────────────────────────────────────────────────────────────
if [ -t 1 ]; then
  RED='\033[0;31m'
  GREEN='\033[0;32m'
  YELLOW='\033[0;33m'
  CYAN='\033[0;36m'
  PURPLE='\033[0;35m'
  BOLD='\033[1m'
  NC='\033[0m'
else
  RED='' GREEN='' YELLOW='' CYAN='' PURPLE='' BOLD='' NC=''
fi

info()    { printf "${CYAN}${BOLD}[INFO]${NC}    %s\n" "$1"; }
success() { printf "${GREEN}${BOLD}[OK]${NC}      %s\n" "$1"; }
warn()    { printf "${YELLOW}${BOLD}[WARN]${NC}    %s\n" "$1"; }
error()   { printf "${RED}${BOLD}[ERROR]${NC}   %s\n" "$1" >&2; }
step()    { printf "\n${BOLD}──── %s${NC}\n" "$1"; }

# ── Banner ───────────────────────────────────────────────────────────────────
print_banner() {
  printf "${CYAN}${BOLD}"
  printf "    ____    ____  ______  __    \n"
  printf "  ██████  ▄████▄  ▄▄▄█████▓ ██▓    \n"
  printf "▒██    ▒ ▒██▀ ▀█  ▓  ██▒ ▓▒▓██▒    \n"
  printf "░ ▓██▄   ▒▓█    ▄ ▒ ▓██░ ▒░▒██░    \n"
  printf "  ▒   ██▒▒▓▓▄ ▄██▒░ ▓██▓ ░ ▒██░    \n"
  printf "▒██████▒▒▒ ▓███▀ ░  ▒██▒ ░ ░██████▒\n"
  printf "▒ ▒▓▒ ▒ ░░ ░▒ ▒  ░  ▒ ░░   ░ ▒░▓  ░\n"
  printf "░ ░▒  ░ ░  ░  ▒       ░    ░ ░ ▒  ░\n"
  printf "░  ░  ░  ░          ░        ░ ░   \n"
  printf "      ░  ░ ░                   ░  ░\n"
  printf "${NC}"
  printf "   ${BOLD}Script Controller CLI${NC}\n"
  printf "   ${PURPLE}By CodeWithYagnesh${NC}\n"
  printf "========================================\n\n"
}

print_banner

# ── Resolve requested version ─────────────────────────────────────────────────
# Priority: CLI arg > VERSION env var > fetch latest from GitHub API
step "Resolving version"

REQUESTED_VERSION="${1:-${VERSION:-}}"

if [ -n "${REQUESTED_VERSION}" ]; then
  # Normalise: ensure it starts with 'v'
  case "${REQUESTED_VERSION}" in
    v*) VERSION="${REQUESTED_VERSION}" ;;
    *)  VERSION="v${REQUESTED_VERSION}" ;;
  esac
  info "Requested version: ${BOLD}${VERSION}${NC}"
else
  info "No version specified — fetching latest from GitHub..."
  if command -v curl >/dev/null 2>&1; then
    VERSION=$(curl -fsSL "https://api.github.com/repos/${OWNER}/${REPO}/releases/latest" \
      | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
  elif command -v wget >/dev/null 2>&1; then
    VERSION=$(wget -qO- "https://api.github.com/repos/${OWNER}/${REPO}/releases/latest" \
      | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
  fi

  if [ -z "${VERSION}" ]; then
    warn "Could not determine latest version via GitHub API."
    error "Please specify a version manually: ./install.sh v1.0.9"
    exit 1
  fi
  success "Latest version: ${BOLD}${VERSION}${NC}"
fi

# ── Detect OS & Arch ──────────────────────────────────────────────────────────
step "Detecting platform"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "${OS}" in
  linux*)             OS="linux"   ;;
  darwin*)            OS="darwin"  ;;
  msys*|mingw*|cygwin*) OS="windows" ;;
  *)  error "Unsupported OS: ${OS}"; exit 1 ;;
esac

ARCH="$(uname -m)"
case "${ARCH}" in
  x86_64|amd64)  ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  i386|i686)     ARCH="386"   ;;
  *)  error "Unsupported architecture: ${ARCH}"; exit 1 ;;
esac

info "Platform: ${BOLD}${OS}/${ARCH}${NC}"

# ── Build asset name & URL ────────────────────────────────────────────────────
# Release asset format: sctl-<version>-<os>-<arch>.tar.gz  (or .zip on Windows)
step "Preparing download"

if [ "${OS}" = "windows" ]; then
  ARCHIVE="${BINARY_NAME}-${VERSION}-${OS}-${ARCH}.zip"
else
  ARCHIVE="${BINARY_NAME}-${VERSION}-${OS}-${ARCH}.tar.gz"
fi

URL="https://github.com/${OWNER}/${REPO}/releases/download/${VERSION}/${ARCHIVE}"
info "Asset  : ${BOLD}${ARCHIVE}${NC}"
info "URL    : ${CYAN}${URL}${NC}"

# ── Download ──────────────────────────────────────────────────────────────────
step "Downloading"

TMP_DIR="$(mktemp -d)"
cleanup() { rm -rf "${TMP_DIR}"; }
trap cleanup EXIT

if command -v curl >/dev/null 2>&1; then
  curl -L --fail --progress-bar -o "${TMP_DIR}/${ARCHIVE}" "${URL}" || {
    error "Download failed. Check that version ${VERSION} exists:"
    error "https://github.com/${OWNER}/${REPO}/releases"
    exit 1
  }
elif command -v wget >/dev/null 2>&1; then
  wget -q --show-progress -O "${TMP_DIR}/${ARCHIVE}" "${URL}" || {
    error "Download failed. Check that version ${VERSION} exists:"
    error "https://github.com/${OWNER}/${REPO}/releases"
    exit 1
  }
else
  error "Neither curl nor wget found. Please install one of them."
  exit 1
fi

success "Downloaded ${ARCHIVE}"

# ── Extract ───────────────────────────────────────────────────────────────────
step "Extracting"

if [ "${OS}" = "windows" ]; then
  command -v unzip >/dev/null 2>&1 || { error "unzip not found."; exit 1; }
  unzip -q "${TMP_DIR}/${ARCHIVE}" -d "${TMP_DIR}"
  BINARY_FILE="${BINARY_NAME}.exe"
else
  tar -xzf "${TMP_DIR}/${ARCHIVE}" -C "${TMP_DIR}"
  BINARY_FILE="${BINARY_NAME}"
fi

[ -f "${TMP_DIR}/${BINARY_FILE}" ] || {
  error "Binary '${BINARY_FILE}' not found inside archive. Contents:"
  ls "${TMP_DIR}/"
  exit 1
}

# ── Install ───────────────────────────────────────────────────────────────────
step "Installing"

if [ -z "${BINDIR}" ]; then
  if [ -w "/usr/local/bin" ]; then
    BINDIR="/usr/local/bin"
    USE_SUDO="false"
  elif command -v sudo >/dev/null 2>&1 && [ "$(id -u)" -ne 0 ]; then
    BINDIR="/usr/local/bin"
    USE_SUDO="true"
  else
    BINDIR="${HOME}/.local/bin"
    USE_SUDO="false"
    mkdir -p "${BINDIR}"
  fi
else
  USE_SUDO="false"
fi

info "Installing to ${BOLD}${BINDIR}${NC}"
chmod +x "${TMP_DIR}/${BINARY_FILE}"

if [ "${USE_SUDO}" = "true" ]; then
  warn "Elevation required — prompting for sudo..."
  sudo mv "${TMP_DIR}/${BINARY_FILE}" "${BINDIR}/${BINARY_FILE}"
else
  mv "${TMP_DIR}/${BINARY_FILE}" "${BINDIR}/${BINARY_FILE}"
fi

success "Installed: ${BOLD}${BINDIR}/${BINARY_FILE}${NC}"

if [ "${BINDIR}" = "${HOME}/.local/bin" ]; then
  warn "${BINDIR} is not in PATH on all systems."
  warn "Add this to your shell profile if needed:"
  printf "    export PATH=\"\$HOME/.local/bin:\$PATH\"\n"
fi

# ── Default config ─────────────────────────────────────────────────────────────
step "Setting up default config"

CONFIG_DIR="${HOME}/.config/sctl"
CONFIG_FILE="${CONFIG_DIR}/config.yaml"
mkdir -p "${CONFIG_DIR}"

if [ ! -f "${CONFIG_FILE}" ]; then
  cat >"${CONFIG_FILE}" <<'EOF'
scripts:
  - name_alias: hello_world
    description: Print a friendly greeting
    command: echo "Hello, World! Welcome to sctl."
    output_folder_path: ./output/hello_world
EOF
  success "Default config created: ${CONFIG_FILE}"
else
  info "Config already exists — skipping overwrite: ${CONFIG_FILE}"
fi

# ── Shell profile: SCTL_CONFIG ─────────────────────────────────────────────────
step "Configuring shell environment"

for PROFILE in "${HOME}/.bashrc" "${HOME}/.zshrc" "${HOME}/.profile"; do
  [ -f "${PROFILE}" ] || continue
  if ! grep -q "SCTL_CONFIG" "${PROFILE}"; then
    printf "\n# sctl — Script Controller\nexport SCTL_CONFIG=\"\${HOME}/.config/sctl/config.yaml\"\n" \
      >>"${PROFILE}"
    info "Added SCTL_CONFIG to ${PROFILE}"
  fi
done

# ── Done ───────────────────────────────────────────────────────────────────────
printf "\n"
printf "${GREEN}${BOLD}════════════════════════════════════════${NC}\n"
printf "${GREEN}${BOLD}  sctl ${VERSION} installed successfully!${NC}\n"
printf "${GREEN}${BOLD}════════════════════════════════════════${NC}\n"
printf "\n"
printf "  Restart your terminal, then run:\n"
printf "    ${BOLD}sctl --help${NC}\n"
printf "\n"
printf "  To upgrade later:\n"
printf "    ${BOLD}sctl upgrade${NC}            # latest\n"
printf "    ${BOLD}./install.sh v1.0.8${NC}     # specific version\n"
printf "\n"
