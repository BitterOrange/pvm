#!/bin/sh
# Install pvm-exit on a Linux VPS and start it.
#   as root:      system service run by the pvm-exit user (kernel NAT allowed)
#   as any user:  systemd user service with userspace NAT, no root at all
# Arguments are passed to `pvm-exit init`, e.g. --public-endpoint vps.example.com
# or --migrate to take over an existing `pvm exit-agent` service.
set -eu
umask 077

OWNER="BitterOrange"
REPO="pvm"
API="https://api.github.com/repos/${OWNER}/${REPO}/releases/latest"
BASE="https://github.com/${OWNER}/${REPO}/releases/download"
command -v gh >/dev/null 2>&1 || { echo "GitHub CLI (gh) with attestation support is required; install it before running this installer" >&2; exit 1; }
attest() {
  gh attestation verify "$1" --repo "$OWNER/$REPO" --signer-workflow "$OWNER/$REPO/.github/workflows/build.yml" --source-ref "refs/tags/$TAG" --deny-self-hosted-runners --hostname github.com || { echo "release attestation verification failed; nothing was installed" >&2; exit 1; }
}

if [ -z "${TAG:-}" ]; then
  TAG=$(curl -fsSL "$API" | grep '"tag_name"' | head -1 | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')
fi
case "$TAG" in
  ''|*[!a-zA-Z0-9.-]*|.*|-*) echo "invalid release tag" >&2; exit 1 ;;
esac
[ "$(uname -s)" = Linux ] || { echo "pvm-exit runs on Linux" >&2; exit 1; }
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64; FORMAT='*ELF*64-bit*x86-64*' ;;
  aarch64|arm64) ARCH=arm64; FORMAT='*ELF*64-bit*ARM*aarch64*' ;;
  *) echo "unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac
ASSET="pvm-exit_linux_${ARCH}"

if [ "$(id -u)" = 0 ]; then
  DEST=/usr/local/bin
  INIT="init"
else
  DEST="$HOME/.local/bin"
  INIT="init --user"
fi

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
cd "$TMP"
verify() {
  awk -v name="$1" '$2 == name {print $1 "  " file; n++} END {if (n != 1) exit 1}' file="$2" SHA256SUMS > want.sum || { echo "missing or duplicate checksum entry: $1" >&2; exit 1; }
  sha256sum -c want.sum >/dev/null
}

echo "downloading ${ASSET}.tar.gz (${TAG})..."
curl -fsSL "$BASE/$TAG/${ASSET}.tar.gz" -o asset.tar.gz
curl -fsSL "$BASE/$TAG/SHA256SUMS" -o SHA256SUMS
attest SHA256SUMS
attest asset.tar.gz
verify "${ASSET}.tar.gz" asset.tar.gz
# Extract only the executable, never arbitrary archive paths.
[ "$(tar -tzf asset.tar.gz)" = pvm-exit ] || { echo "unexpected archive entries" >&2; exit 1; }
tar -tvzf asset.tar.gz | awk 'substr($0,1,1) != "-" {bad=1} END {exit bad}' || { echo "binary must be a regular file" >&2; exit 1; }
tar -xzf asset.tar.gz pvm-exit
command -v file >/dev/null 2>&1 || { echo "file utility is required to validate the release platform" >&2; exit 1; }
# shellcheck disable=SC2254
case "$(file -b pvm-exit)" in
  $FORMAT) ;;
  *) echo "release platform mismatch" >&2; exit 1 ;;
esac
chmod 755 pvm-exit
[ "$(./pvm-exit version)" = "$TAG" ] || { echo "release version mismatch" >&2; exit 1; }

mkdir -p "$DEST"
install -m 0755 pvm-exit "$DEST/pvm-exit.new"
mv -f "$DEST/pvm-exit.new" "$DEST/pvm-exit"
echo "installed $DEST/pvm-exit ($TAG)"
cd /
# shellcheck disable=SC2086
exec "$DEST/pvm-exit" $INIT "$@"
