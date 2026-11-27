#!/usr/bin/env bash
# Build pvm-exit_<version>_<arch>.deb from a built pvm-exit binary.
# usage: script/package-exit-deb.sh <pvm-exit binary> <version tag> <amd64|arm64> [out dir]
# The unit comes from the binary itself (`pvm-exit unit`), so the package
# and `pvm-exit init` cannot drift; run this on the target architecture.
set -euo pipefail

bin=${1:?pvm-exit binary}
tag=${2:?version tag}
arch=${3:?amd64 or arm64}
out=${4:-.}
case "$arch" in amd64|arm64) ;; *) echo "unsupported arch $arch" >&2; exit 1 ;; esac
case "$tag" in ''|*[!a-zA-Z0-9.-]*) echo "invalid version tag $tag" >&2; exit 1 ;; esac
# v1.4.1 -> 1.4.1; a prerelease suffix sorts before the release in dpkg.
ver=${tag#v}
ver=${ver/-/\~}

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
chmod 0755 "$stage"
install -d -m 0755 "$stage/DEBIAN" "$stage/usr/bin" "$stage/usr/lib/systemd/system"
install -m 0755 "$bin" "$stage/usr/bin/pvm-exit"
"$stage/usr/bin/pvm-exit" unit --exe /usr/bin/pvm-exit > "$stage/usr/lib/systemd/system/pvm-exit.service"
chmod 0644 "$stage/usr/lib/systemd/system/pvm-exit.service"

cat > "$stage/DEBIAN/control" <<EOF
Package: pvm-exit
Version: $ver
Architecture: $arch
Maintainer: pvm maintainers <noreply@example.com>
Section: net
Priority: optional
Depends: systemd, passwd
Recommends: nftables
Description: pvm exit server
 Exit server for pvm virtual machines: WireGuard over UDP or TCP, NAT,
 scoped tokens and one-time invites. Runs as the unprivileged pvm-exit
 user; kernel NAT gets only CAP_NET_ADMIN and CAP_NET_RAW.
EOF

cat > "$stage/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e
if [ "$1" = configure ]; then
  if pvm-exit init --packaged; then
    echo "pvm-exit is running; create a client invite with: sudo pvm-exit invite <client-name>"
  else
    echo "pvm-exit is installed but not started; fix the error above, then run the command it names (by default: sudo pvm-exit init --packaged)" >&2
  fi
fi
exit 0
EOF

cat > "$stage/DEBIAN/prerm" <<'EOF'
#!/bin/sh
set -e
if [ "$1" = remove ] && [ -d /run/systemd/system ]; then
  systemctl disable --now pvm-exit.service >/dev/null 2>&1 || true
fi
exit 0
EOF

cat > "$stage/DEBIAN/postrm" <<'EOF'
#!/bin/sh
set -e
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload >/dev/null 2>&1 || true
fi
if [ "$1" = purge ]; then
  rm -f /etc/systemd/system/pvm-exit.service.d/10-userspace-nat.conf
  rmdir /etc/systemd/system/pvm-exit.service.d 2>/dev/null || true
  echo "kept /etc/pvm-exit and /var/lib/pvm-exit (admin token, certificate, peers) and the pvm-exit user; delete them by hand if you no longer need them"
fi
exit 0
EOF
chmod 0755 "$stage/DEBIAN/postinst" "$stage/DEBIAN/prerm" "$stage/DEBIAN/postrm"

mkdir -p "$out"
dpkg-deb --root-owner-group --build "$stage" "$out/pvm-exit_${ver}_${arch}.deb" >/dev/null
echo "$out/pvm-exit_${ver}_${arch}.deb"
