#!/bin/sh
set -eu
umask 077

OWNER="BitterOrange"
REPO="pvm"
API="https://api.github.com/repos/${OWNER}/${REPO}/releases/latest"
BASE="https://github.com/${OWNER}/${REPO}/releases/download"
ATTEST_API="https://api.github.com/repos/${OWNER}/${REPO}/attestations"
# PVM_QEMU picks how QEMU is set up once pvm is installed: project (the
# build this pvm recommends, from the pvm-qemu project), official (the
# platform's own QEMU, which you install yourself) or none.
PVM_QEMU="${PVM_QEMU:-project}"
case "$PVM_QEMU" in
  project|official|none) ;;
  *) echo "invalid PVM_QEMU=$PVM_QEMU (want project, official or none)" >&2; exit 2 ;;
esac
die() { echo "$1; installed version unchanged" >&2; exit 1; }
header() { tr -d '\r' < att.hdr | awk -v k="$1" -F': *' 'tolower($1) == k {print $2}'; }
# The attestation is public: fetch it anonymously and verify it offline,
# so no GitHub login is needed. gh without --bundle would need one.
attest() {
  if command -v shasum >/dev/null 2>&1; then
    digest=$(shasum -a 256 "$1" | cut -d' ' -f1)
  else
    digest=$(sha256sum "$1" | cut -d' ' -f1)
  fi
  code=$(curl -sS -D att.hdr -o att.json -w '%{http_code}' -H 'Accept: application/vnd.github+json' "$ATTEST_API/sha256:$digest") ||
    die "cannot reach api.github.com for the attestation of $2"
  case "$code" in
    200) ;;
    404) die "$2 has no attestation from $OWNER/$REPO, so it is not a build of its release workflow" ;;
    403|429)
      if [ "$(header x-ratelimit-remaining)" = 0 ]; then
        reset=$(header x-ratelimit-reset)
        die "the GitHub API allows 60 requests an hour without a login and they are used up; retry after $(date -d "@$reset" 2>/dev/null || date -r "$reset" 2>/dev/null || echo "an hour")"
      fi
      die "api.github.com refused the attestation request for $2 (HTTP $code)" ;;
    *) die "api.github.com answered HTTP $code for the attestation of $2" ;;
  esac
  # one bundle per line; a response in another layout leaves this empty and fails
  sed -n 's/^[[:space:]]*"bundle":[[:space:]]*\({.*}\),\{0,1\}[[:space:]]*$/\1/p' att.json > att.jsonl
  [ -s att.jsonl ] || die "the attestation response for $2 holds no bundle"
  gh attestation verify "$1" --bundle att.jsonl --repo "$OWNER/$REPO" --signer-workflow "$OWNER/$REPO/.github/workflows/build.yml" --source-ref "refs/tags/$TAG" --deny-self-hosted-runners --hostname github.com ||
    die "release attestation verification failed for $2"
}

if [ -z "${TAG:-}" ]; then
  TAG=$(curl -fsSL "$API" | grep '"tag_name"' | head -1 | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')
fi
case "$TAG" in
  ''|*[!a-zA-Z0-9.-]*|.*|-*) echo "invalid release tag" >&2; exit 1 ;;
esac
# This script sets QEMU up with `pvm qemu`, which older releases lack; they
# install with the script of their own tag.
major=$(printf %s "$TAG" | sed -n 's/^v\([0-9][0-9]*\)\.\([0-9][0-9]*\)\..*/\1/p')
minor=$(printf %s "$TAG" | sed -n 's/^v\([0-9][0-9]*\)\.\([0-9][0-9]*\)\..*/\2/p')
[ -n "$major" ] && [ -n "$minor" ] || { echo "invalid release tag" >&2; exit 1; }
if [ "$major" -lt 1 ] || { [ "$major" -eq 1 ] && [ "$minor" -lt 4 ]; }; then
  echo "$TAG predates pvm qemu; install it with its own script: curl -fsSL https://raw.githubusercontent.com/$OWNER/$REPO/$TAG/scripts/install.sh | TAG=$TAG sh" >&2
  exit 1
fi
command -v gh >/dev/null 2>&1 || { echo "GitHub CLI (gh) with attestation support is required to check the release; no GitHub login is needed" >&2; exit 1; }
S=$(uname -s)
M=$(uname -m)
BIN_NAME=pvm
case "$S/$M" in
  Darwin/arm64) ASSET="pvm_darwin_arm64" ;;
  Linux/x86_64) ASSET="pvm_linux_amd64" ;;
  MINGW64_NT-*/x86_64|MSYS_NT-*/x86_64)
    ASSET="pvm_windows_amd64"
    BIN_NAME="pvm.exe"
    ;;
  Linux/aarch64|Linux/arm64)
    echo "linux/arm64 host is not supported; for a VPS exit server use scripts/install-exit.sh" >&2; exit 1 ;;
  *) echo "unsupported host: $S/$M (supported: darwin/arm64, linux/amd64, windows/amd64)" >&2; exit 1 ;;
esac

INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
mkdir -p "$INSTALL_DIR"
INSTALL_DIR=$(cd "$INSTALL_DIR" && pwd)
# Staging on the destination filesystem makes the final rename atomic.
LOCK_DIR="$INSTALL_DIR/.pvm-install.lock"
mkdir "$LOCK_DIR" 2>/dev/null || { echo "another installation is active (or interrupted; inspect $LOCK_DIR)" >&2; exit 1; }
TMP=$(mktemp -d "$INSTALL_DIR/.pvm-install.XXXXXX") || { rmdir "$LOCK_DIR"; exit 1; }
BINARY_SWITCHED=0
BINARY_BACKUP=""
COMMITTED=0
cleanup() {
  rc=$?
  trap - EXIT INT TERM
  if [ "$COMMITTED" = 0 ] && [ "$BINARY_SWITCHED" = 1 ]; then
    if [ -n "$BINARY_BACKUP" ]; then
      cp -p "$BINARY_BACKUP" "$INSTALL_DIR/$BIN_NAME"
    else
      rm -f "$INSTALL_DIR/$BIN_NAME"
    fi
  fi
  rm -rf "$TMP"
  rmdir "$LOCK_DIR"
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
cd "$TMP"

verify() {
  name=$1
  file=$2
  awk -v name="$name" '$2 == name {print $1 "  " file; n++} END {if (n != 1) exit 1}' file="$file" SHA256SUMS > want.sum || { echo "missing or duplicate checksum entry: $name" >&2; exit 1; }
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 -c want.sum >/dev/null
  else
    sha256sum -c want.sum >/dev/null
  fi
}

echo "downloading ${ASSET}.tar.gz (${TAG})..."
curl -fsSL "$BASE/$TAG/${ASSET}.tar.gz" -o asset.tar.gz
curl -fsSL "$BASE/$TAG/SHA256SUMS" -o SHA256SUMS
attest SHA256SUMS SHA256SUMS
attest asset.tar.gz "${ASSET}.tar.gz"
verify "${ASSET}.tar.gz" asset.tar.gz
# Extract only the executable, never arbitrary archive paths.
[ "$(tar -tzf asset.tar.gz)" = "$BIN_NAME" ] || { echo "unexpected binary archive entries" >&2; exit 1; }
tar -tvzf asset.tar.gz | awk 'substr($0,1,1) != "-" {bad=1} END {exit bad}' || { echo "binary must be a regular file" >&2; exit 1; }
tar -xzf asset.tar.gz "$BIN_NAME"
[ -f "$BIN_NAME" ] && [ ! -L "$BIN_NAME" ] || { echo "invalid executable archive entry" >&2; exit 1; }
command -v file >/dev/null 2>&1 || { echo "file utility is required to validate the release platform" >&2; exit 1; }
BINARY_FORMAT=$(file -b "$BIN_NAME")
case "$S/$BINARY_FORMAT" in
  Darwin/*Mach-O*arm64*|Linux/*ELF*64-bit*x86-64*|MINGW64_NT-*/PE32+*|MSYS_NT-*/PE32+*) ;;
  *) echo "release platform mismatch" >&2; exit 1 ;;
esac
chmod 755 "$BIN_NAME"
[ "$(./$BIN_NAME version)" = "$TAG" ] || { echo "release version mismatch" >&2; exit 1; }

if [ -e "$INSTALL_DIR/$BIN_NAME" ]; then
  BACKUP=$(mktemp "$INSTALL_DIR/$BIN_NAME.previous.XXXXXX")
  cp -p "$INSTALL_DIR/$BIN_NAME" "$BACKUP"
  BINARY_BACKUP="$BACKUP"
  echo "binary rollback: cp '$BACKUP' '$INSTALL_DIR/.pvm-rollback' && mv -f '$INSTALL_DIR/.pvm-rollback' '$INSTALL_DIR/$BIN_NAME'"
fi
BINARY_SWITCHED=1
mv -f "$TMP/$BIN_NAME" "$INSTALL_DIR/$BIN_NAME"
COMMITTED=1
echo "installed: $INSTALL_DIR/$BIN_NAME"
case ":$PATH:" in
  *:"$INSTALL_DIR":*) ;;
  *) echo "add to PATH: export PATH=\"$INSTALL_DIR:\$PATH\"" ;;
esac

# pvm itself is installed now; a failed QEMU setup leaves it in place.
PVM="$INSTALL_DIR/$BIN_NAME"
case "$S" in
  # This installs the native Windows pvm, so its QEMU is native too, even
  # where pvm in WSL would be the auto backend.
  MINGW64_NT-*|MSYS_NT-*) PVM_BACKEND=native; export PVM_BACKEND ;;
esac
cd "$INSTALL_DIR"
case "$PVM_QEMU" in
  project)
    echo "setting up QEMU: pvm qemu install"
    "$PVM" qemu install -y || { echo "pvm is installed; QEMU setup failed: retry pvm qemu install" >&2; exit 1; } ;;
  official)
    echo "setting up QEMU: pvm qemu use official"
    "$PVM" qemu use official -y || { echo "pvm is installed; QEMU setup failed: install your platform's QEMU and run pvm qemu use official" >&2; exit 1; } ;;
  none)
    echo "QEMU not set up; pvm needs one to run VMs: pvm qemu install (a build from the pvm-qemu project), or pvm qemu use official (your platform's QEMU)" ;;
esac
echo "next: pvm create --image ubuntu:26.04 --name dev1 --user dev  (the first create downloads the image and bakes the golden image once, about ten minutes)"
