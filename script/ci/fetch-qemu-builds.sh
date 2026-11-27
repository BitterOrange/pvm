#!/usr/bin/env bash
# Checks every pvm-qemu build pinned in internal/qemu/pvmqemu.json against
# its GitHub release: the build-provenance attestation of pvm-qemu's
# release workflow for that tag, the size, the archive digest, the digest
# of the tree's SHA256SUMS and the digest of the emulator.
#
#   fetch-qemu-builds.sh                 check every pin
#   fetch-qemu-builds.sh --bridge DIR    also put the recommended archives in
#                                        DIR under the names pvm v1.3.x
#                                        upgrades from, with their source
#                                        assets, and extract the Windows tree
#                                        to DIR/windows-tree for the installers
#
# The bridge exists in v1.4.x only and is removed in v1.5.0.
# Needs gh (authenticated), python3, tar and sha256sum.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
pins="$root/internal/qemu/pvmqemu.json"
bridge=""
case "${1:-}" in
  --bridge) bridge=${2:?--bridge needs a directory}; mkdir -p "$bridge"; bridge=$(cd "$bridge" && pwd) ;;
  "") ;;
  *) echo "usage: $0 [--bridge DIR]" >&2; exit 2 ;;
esac

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# repo, workflow, then one line per pin: tag host asset sha256 size tree system recommended(0|1)
python3 - "$pins" > "$work/pins.txt" <<'PY'
import json, sys
p = json.load(open(sys.argv[1]))
print(p["repo"])
print(p["workflow"])
rec = p.get("recommended", {})
for tag, b in sorted(p["builds"].items()):
    for host, h in sorted(b["hosts"].items()):
        print(tag, host, h["asset"], h["sha256"], h["size"], h["tree_sha256"], h["system_sha256"], int(rec.get(host) == tag))
PY
# A Windows python3 ends lines with CR.
tr -d '\r' < "$work/pins.txt" > "$work/pins.lf" && mv "$work/pins.lf" "$work/pins.txt"
repo=$(sed -n 1p "$work/pins.txt")
workflow=$(sed -n 2p "$work/pins.txt")

attest() { # file tag
  gh attestation verify "$1" --repo "$repo" --signer-workflow "$workflow" \
    --source-ref "refs/tags/$2" --deny-self-hosted-runners --hostname github.com >/dev/null
}

fetch() { # tag asset dir
  gh release download "$1" --repo "$repo" --pattern "$2" --dir "$3" --clobber
  attest "$3/$2" "$1"
}

sha() { sha256sum "$1" | cut -d' ' -f1; }

# The runtime folder name pvm v1.3.x unpacks, by host.
legacy_name() {
  case "$1" in
    darwin-arm64) echo aarch64 ;;
    linux-amd64) echo linux-x86_64 ;;
    windows-amd64) echo windows-x86_64 ;;
    *) return 1 ;;
  esac
}

arch_of() {
  case "$1" in
    *-arm64) echo aarch64 ;;
    *-amd64) echo x86_64 ;;
  esac
}

checked=0
while read -r tag host asset want_sha want_size want_tree want_sys recommended; do
  dir="$work/$tag/$host"
  mkdir -p "$dir/tree"
  fetch "$tag" "$asset" "$dir"
  file="$dir/$asset"
  size=$(wc -c < "$file" | tr -d ' ')
  [ "$size" = "$want_size" ] || { echo "$asset: size $size, pinned $want_size" >&2; exit 1; }
  [ "$(sha "$file")" = "$want_sha" ] || { echo "$asset: sha256 differs from the pin" >&2; exit 1; }
  tar -xzf "$file" -C "$dir/tree"
  arch=$(arch_of "$host")
  [ "$(ls "$dir/tree")" = "$arch" ] || { echo "$asset does not hold exactly $arch/" >&2; exit 1; }
  tree="$dir/tree/$arch"
  [ "$(sha "$tree/SHA256SUMS")" = "$want_tree" ] || { echo "$asset: SHA256SUMS differs from the pin" >&2; exit 1; }
  (cd "$tree" && sha256sum --quiet -c SHA256SUMS) || { echo "$asset: files differ from SHA256SUMS" >&2; exit 1; }
  [ "$(tr -d '\r\n' < "$tree/VERSION")" = "$tag" ] || { echo "$asset: VERSION is not $tag" >&2; exit 1; }
  sys=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["system_exec"])' "$tree/pvm-qemu.json")
  [ "$(sha "$tree/$sys")" = "$want_sys" ] || { echo "$asset: the emulator differs from the pin" >&2; exit 1; }
  echo "ok: $tag $host"
  checked=$((checked + 1))

  if [ -n "$bridge" ] && [ "$recommended" = 1 ]; then
    cp "$file" "$bridge/pvm-qemu-runtime-$(legacy_name "$host").tar.gz"
    fetch "$tag" "pvm-qemu-$tag-$host-deps-source.tar" "$bridge"
    if [ ! -f "$bridge/pvm-qemu-$tag-source.tar.gz" ]; then
      fetch "$tag" "pvm-qemu-$tag-source.tar.gz" "$bridge"
    fi
    if [ "$host" = windows-amd64 ]; then
      rm -rf "$bridge/windows-tree"
      cp -R "$tree" "$bridge/windows-tree"
    fi
  fi
done < <(tail -n +3 "$work/pins.txt")

echo "checked $checked pinned archive(s)"
if [ -n "$bridge" ]; then
  for h in darwin-arm64 linux-amd64 windows-amd64; do
    [ -f "$bridge/pvm-qemu-runtime-$(legacy_name "$h").tar.gz" ] || { echo "no recommended build pinned for $h; a v1.4.x release needs one per host" >&2; exit 1; }
  done
fi
