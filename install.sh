#!/bin/sh
set -e

# Repository info
OWNER="CodeWithYagnesh"
REPO="sctl"
BINARY_NAME="sctl"

# Setup colors if output is terminal
if [ -t 1 ]; then
  RED='\033[0;31m'
  GREEN='\033[0;32m'
  YELLOW='\033[0;33m'
  BLUE='\033[0;34m'
  PURPLE='\033[0;35m'
  CYAN='\033[0;36m'
  BOLD='\033[1m'
  NC='\033[0m' # No Color
else
  RED=''
  GREEN=''
  YELLOW=''
  BLUE=''
  PURPLE=''
  CYAN=''
  BOLD=''
  NC=''
fi

# Print beautiful banner
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
  printf "         ░                         \n"
  printf "${NC}"
  printf "   ${BOLD}Script Controller CLI${NC}\n"
  printf "   ${PURPLE}By CodeWithYagnesh${NC}\n"
  printf "========================================\n\n"
}

info() {
  printf "${CYAN}${BOLD}[INFO]${NC} %s\n" "$1"
}

success() {
  printf "${GREEN}${BOLD}[SUCCESS]${NC} %s\n" "$1"
}

warn() {
  printf "${YELLOW}${BOLD}[WARNING]${NC} %s\n" "$1"
}

error() {
  printf "${RED}${BOLD}[ERROR]${NC} %s\n" "$1" >&2
}

# Run banner
print_banner

# Detect OS
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "${OS}" in
  linux*)   OS="linux" ;;
  darwin*)  OS="darwin" ;;
  msys*|mingw*|cygwin*) OS="windows" ;;
  *)        error "Unsupported OS: ${OS}"; exit 1 ;;
esac

# Detect Architecture
ARCH="$(uname -m)"
case "${ARCH}" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *)            error "Unsupported architecture: ${ARCH}"; exit 1 ;;
esac

# Get latest release version
info "Fetching latest release version from GitHub..."
VERSION=""
if command -v curl >/dev/null 2>&1; then
  VERSION=$(curl -s "https://api.github.com/repos/${OWNER}/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
elif command -v wget >/dev/null 2>&1; then
  VERSION=$(wget -qO- "https://api.github.com/repos/${OWNER}/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
fi

if [ -z "${VERSION}" ]; then
  # Fallback version if rate-limited or offline
  VERSION="v1.0.4"
  warn "Could not fetch latest release via GitHub API. Falling back to default: ${VERSION}"
else
  success "Latest version found: ${VERSION}"
fi

# Define archive name and download URL
if [ "${OS}" = "windows" ]; then
  ARCHIVE="${BINARY_NAME}-${VERSION}-${OS}-${ARCH}.zip"
else
  ARCHIVE="${BINARY_NAME}-${VERSION}-${OS}-${ARCH}.tar.gz"
fi

URL="https://github.com/${OWNER}/${REPO}/releases/download/${VERSION}/${ARCHIVE}"

# Create a temporary directory for downloading
TMP_DIR=$(mktemp -d)
clean_up() {
  rm -rf "${TMP_DIR}"
}
trap clean_up EXIT

info "Downloading release asset..."
if command -v curl >/dev/null 2>&1; then
  curl -L -# -o "${TMP_DIR}/${ARCHIVE}" "${URL}"
elif command -v wget >/dev/null 2>&1; then
  wget -q --show-progress -O "${TMP_DIR}/${ARCHIVE}" "${URL}"
else
  error "Neither curl nor wget found. Please install one of them."
  exit 1
fi

# Determine installation directory
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

info "Installing to ${BINDIR}..."

# Extract the binary
if [ "${OS}" = "windows" ]; then
  if command -v unzip >/dev/null 2>&1; then
    unzip -q "${TMP_DIR}/${ARCHIVE}" -d "${TMP_DIR}"
  else
    error "unzip command not found. Cannot extract ZIP file."
    exit 1
  fi
  BINARY_FILE="${BINARY_NAME}.exe"
else
  tar -xzf "${TMP_DIR}/${ARCHIVE}" -C "${TMP_DIR}"
  BINARY_FILE="${BINARY_NAME}"
fi

# Move binary to target directory
if [ "${USE_SUDO}" = "true" ]; then
  info "Elevation required (sudo) to move binary to ${BINDIR}..."
  sudo mv "${TMP_DIR}/${BINARY_FILE}" "${BINDIR}/${BINARY_FILE}"
else
  mv "${TMP_DIR}/${BINARY_FILE}" "${BINDIR}/${BINARY_FILE}"
fi

success "Successfully installed ${BINARY_NAME} to ${BINDIR}/${BINARY_FILE}!"
if [ "${BINDIR}" = "${HOME}/.local/bin" ]; then
  warn "Please make sure ${BINDIR} is in your PATH."
fi

# Create default configuration directory and file
CONFIG_DIR="${HOME}/.config/sctl"
CONFIG_FILE="${CONFIG_DIR}/config.yaml"

info "Configuring default settings at ${CONFIG_FILE}..."
mkdir -p "${CONFIG_DIR}"

if [ ! -f "${CONFIG_FILE}" ]; then
  cat <<EOF > "${CONFIG_FILE}"
scripts:
  - name_alias: hello_world
    description: Print a friendly greeting
    command: echo "Hello, World!"
    output_folder_path: ./output
EOF
  success "Default configuration file created."
else
  info "Configuration file already exists at ${CONFIG_FILE} (skipping overwrite)."
fi

# Add SCTL_CONFIG environment variable to shell profile if not already set
SHELL_PROFILES=""
if [ -f "${HOME}/.bashrc" ]; then
  SHELL_PROFILES="${SHELL_PROFILES} ${HOME}/.bashrc"
fi
if [ -f "${HOME}/.zshrc" ]; then
  SHELL_PROFILES="${SHELL_PROFILES} ${HOME}/.zshrc"
fi
if [ -f "${HOME}/.profile" ]; then
  SHELL_PROFILES="${SHELL_PROFILES} ${HOME}/.profile"
fi

ENV_ADDED="false"
for PROFILE in ${SHELL_PROFILES}; do
  if ! grep -q "SCTL_CONFIG" "${PROFILE}"; then
    echo "" >> "${PROFILE}"
    echo "# sctl (Script Controller) configuration path" >> "${PROFILE}"
    echo "export SCTL_CONFIG=\"\${HOME}/.config/sctl/config.yaml\"" >> "${PROFILE}"
    ENV_ADDED="true"
    info "Added SCTL_CONFIG environment variable to ${PROFILE}"
  fi
done

if [ "${ENV_ADDED}" = "true" ]; then
  success "Installation complete! Please restart your terminal or run:"
  printf "    ${BOLD}source ~/.bashrc${NC} or ${BOLD}source ~/.zshrc${NC}\n"
else
  sctl --help
  success "Installation complete! sctl is ready to use."
fi
