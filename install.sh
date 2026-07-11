#!/bin/sh
set -e

# Repository info
OWNER="CodeWithYagnesh"
REPO="sctl"
BINARY_NAME="sctl"

# Detect OS
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "${OS}" in
  linux*)   OS="linux" ;;
  darwin*)  OS="darwin" ;;
  msys*|mingw*|cygwin*) OS="windows" ;;
  *)        echo "Unsupported OS: ${OS}" >&2; exit 1 ;;
esac

# Detect Architecture
ARCH="$(uname -m)"
case "${ARCH}" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *)            echo "Unsupported architecture: ${ARCH}" >&2; exit 1 ;;
esac

# Get latest release version
echo "Fetching latest release version..."
VERSION=""
if command -v curl >/dev/null 2>&1; then
  VERSION=$(curl -s "https://api.github.com/repos/${OWNER}/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
elif command -v wget >/dev/null 2>&1; then
  VERSION=$(wget -qO- "https://api.github.com/repos/${OWNER}/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
fi

if [ -z "${VERSION}" ]; then
  # Fallback version if rate-limited or offline
  VERSION="v1.0.1"
  echo "Could not fetch latest release via GitHub API. Falling back to default: ${VERSION}"
else
  echo "Latest version found: ${VERSION}"
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

echo "Downloading ${URL}..."
if command -v curl >/dev/null 2>&1; then
  curl -L -o "${TMP_DIR}/${ARCHIVE}" "${URL}"
elif command -v wget >/dev/null 2>&1; then
  wget -O "${TMP_DIR}/${ARCHIVE}" "${URL}"
else
  echo "Neither curl nor wget found. Please install one of them." >&2
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

echo "Installing to ${BINDIR}..."

# Extract the binary
if [ "${OS}" = "windows" ]; then
  if command -v unzip >/dev/null 2>&1; then
    unzip -q "${TMP_DIR}/${ARCHIVE}" -d "${TMP_DIR}"
  else
    echo "unzip command not found. Cannot extract ZIP file." >&2
    exit 1
  fi
  BINARY_FILE="${BINARY_NAME}.exe"
else
  tar -xzf "${TMP_DIR}/${ARCHIVE}" -C "${TMP_DIR}"
  BINARY_FILE="${BINARY_NAME}"
fi

# Move binary to target directory
if [ "${USE_SUDO}" = "true" ]; then
  sudo mv "${TMP_DIR}/${BINARY_FILE}" "${BINDIR}/${BINARY_FILE}"
else
  mv "${TMP_DIR}/${BINARY_FILE}" "${BINDIR}/${BINARY_FILE}"
fi

echo "Successfully installed ${BINARY_NAME} to ${BINDIR}/${BINARY_FILE}!"
if [ "${BINDIR}" = "${HOME}/.local/bin" ]; then
  echo "Please make sure ${BINDIR} is in your PATH."
fi

# Create default configuration directory and file
CONFIG_DIR="${HOME}/.config/sctl"
CONFIG_FILE="${CONFIG_DIR}/config.yaml"

echo "Configuring default config file at ${CONFIG_FILE}..."
mkdir -p "${CONFIG_DIR}"

if [ ! -f "${CONFIG_FILE}" ]; then
  cat <<EOF > "${CONFIG_FILE}"
scripts:
  - name_alias: hello_world
    description: Print a friendly greeting
    command: echo "Hello, World!"
    output_folder_path: ./output
EOF
  echo "Default configuration file created."
else
  echo "Configuration file already exists at ${CONFIG_FILE}."
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
    echo "Added SCTL_CONFIG to ${PROFILE}"
  fi
done

if [ "${ENV_ADDED}" = "true" ]; then
  echo "Please restart your terminal or run: source <your-shell-profile> (e.g. source ~/.bashrc or source ~/.zshrc) to apply SCTL_CONFIG change."
fi
