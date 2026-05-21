#!/usr/bin/env bash
# install.sh — build and install awsso into PREFIX/bin.
#
# Usage:
#   ./install.sh                  # installs to /usr/local/bin (sudo if needed)
#   PREFIX=$HOME/.local ./install.sh
#   ./install.sh --prefix /opt/local
#
# Designed to be sourced from a broader devops bootstrap. Idempotent.

set -euo pipefail

PREFIX="${PREFIX:-/usr/local}"
REPO_URL="${REPO_URL:-https://github.com/dutraph/awsso-tui.git}"
REF="${REF:-main}"

while [ $# -gt 0 ]; do
    case "$1" in
        --prefix) PREFIX="$2"; shift 2 ;;
        --prefix=*) PREFIX="${1#*=}"; shift ;;
        --repo) REPO_URL="$2"; shift 2 ;;
        --ref) REF="$2"; shift 2 ;;
        -h|--help)
            sed -n '2,12p' "$0"
            exit 0
            ;;
        *)
            echo "unknown option: $1" >&2
            exit 2
            ;;
    esac
done

log() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m!!\033[0m %s\n' "$*" >&2; exit 1; }

command -v go >/dev/null 2>&1 || die "go (>= 1.22) not found on PATH. Install Go first."
command -v aws >/dev/null 2>&1 || log "warning: 'aws' CLI not found — awsso uses it for EKS kubeconfig + exec auth."

# Resolve install dir. Use sudo only if writing to a system path the user
# doesn't already own.
INSTALL_DIR="$PREFIX/bin"
SUDO=""
mkdir -p "$INSTALL_DIR" 2>/dev/null || SUDO="sudo"
if [ -n "$SUDO" ]; then
    log "creating $INSTALL_DIR (requires sudo)"
    sudo mkdir -p "$INSTALL_DIR"
fi
if [ ! -w "$INSTALL_DIR" ]; then SUDO="sudo"; fi

# Build from the current directory if it already looks like the repo,
# otherwise clone to a temp directory.
src_dir=""
if [ -f "./go.mod" ] && grep -q "^module github.com/dutraph/awsso-tui" ./go.mod 2>/dev/null; then
    log "building from current directory"
    src_dir="$(pwd)"
else
    tmp="$(mktemp -d)"
    trap 'rm -rf "$tmp"' EXIT
    log "cloning $REPO_URL ($REF) into $tmp"
    git clone --depth 1 --branch "$REF" "$REPO_URL" "$tmp/awsso"
    src_dir="$tmp/awsso"
fi

log "compiling"
( cd "$src_dir" && go build -trimpath -ldflags "-s -w" -o ./bin/awsso . )

log "installing to $INSTALL_DIR/awsso"
$SUDO install -m 0755 "$src_dir/bin/awsso" "$INSTALL_DIR/awsso"

log "done. run: awsso configure"
