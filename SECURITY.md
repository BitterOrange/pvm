---
name: pvm-security
description: Review pvm's host, guest, image, credential, and network security boundaries, and verify the configuration of a running VM.
---

# pvm security: boundaries and verification

`pvm security` prints this guide. A Chinese translation is in SECURITY.zh-CN.md in the repository.

pvm provides local QEMU development VMs and configurable network routing. It does not guarantee anonymity, an undetectable VM, a new third-party application identity, or any service's account or risk-control outcome.

## At a glance

- **Trusted**: the host user and OS, the pvm daemon, the selected QEMU build, the guest kernel, the gateway, image publishers and the exit provider.
- **Not a sandbox between VMs**: VMs in one cluster share a layer-2 network. Do not mix mutually untrusted guests.
- **Fail closed**: with an exit selected, a dropped tunnel leaves the VM offline. Explicit `direct` and `internal` rules are the only exceptions.
- **Nothing personal enters a VM by itself**: host `~/.ssh`, `~/.gitconfig` and personal files are never copied in. Mounts, `/share` and your own commands can still expose them.
- **Secrets stay private**: the pvm data directory holds keys, tokens and passwords. Keep it private and review anything before sharing it.
- **Test your own deployment**, including tunnel loss, before relying on it for sensitive traffic.

## Threat model

The host user, host OS, pvm daemon, the QEMU build selected with `pvm qemu`, guest kernel, gateway, image publishers, and selected exit provider are trusted components. Host administrators can read VM disks, memory and credentials. A compromised gateway or exit can observe network destinations and unencrypted traffic; application TLS remains necessary.

VMs in one cluster share a gateway and a layer-2 network. The gateway validates configured source IP/MAC pairs, but these are not cryptographic identities or per-port source bindings. A guest administrator can imitate another registered VM's pair. Do not mix mutually untrusted guests or rely on these checks to contain a hostile guest with root access. Use separate host environments and independently controlled networks for that threat model.

## Host and guest privileges

### The host binary and daemon

The host `pvm` binary exposes the complete pvm control plane and acts with the host user's filesystem and process permissions. It can manage all local VMs, cluster routes, host-path mounts, exit registrations, and stored credentials. Access to the host account or pvm data directory is therefore not limited to one guest.

On Windows, the daemon uses loopback TCP instead of a Unix socket and requires the bearer token in `~/.pvm/run/admin.token` for every API except `/v1/health`. This token is a local control-plane credential and must remain private.

### PVM Desktop

PVM Desktop (`pvm-desktop.exe`, Windows) is another client of the same daemon and CLI, run as the host user with the same rights as the host `pvm` binary. It adds no privileges and no commands of its own.

- It opens no network listener: the window's pages are served from memory inside the process.
- Every change runs `pvm.exe` from the app's own folder with an argument array, never through a shell, after the names have passed the CLI's own checks.
- The daemon's admin token, exit tokens and private keys stay in the app's Go backend and are never sent to the window. A token typed into the "add exit" form is written to a private file under the pvm run directory, passed to `pvm exit add --token-file`, and deleted afterwards, also on failure; a file left behind because the app was killed during the command is removed when the app next starts. It is never on a command line, and output lines containing it are masked.
- A VM made with the Create form is created stopped and started only after its exit is set, so it never boots on another exit; if setting the exit fails, the VM stays stopped.
- Text from VMs and the daemon API is shown as text and never interpreted as HTML.
- Opening the app at login is a per-user `Run` registry value that the app writes on request and the uninstaller removes.

### The guest binary and channels

- The Linux guest uses a separate binary with only `agent`, `map`, and `skill`. It has no host lifecycle, route, mount, exit, or `security` commands.
- Every local guest user can use `map`; requests carry no VM name. The daemon derives the VM identity from that guest's virtio-serial channel and limits list, add, and remove operations to maps owned by that VM. These maps still create host loopback listeners into the current VM.
- Each VM has a `pvm.stream` virtio-serial port for display, measurement and TCP port-map streams. Only the host daemon opens streams on it, by naming a target from a fixed allowlist in the guest agent (`novnc`, `xpra:<uid>`, clipboard targets, `tcp:<port>` to guest loopback). The guest cannot open a stream, connect to host addresses, or reach the network through it, so the VM's egress path and kill-switch are unchanged. TCP port maps carried over `pvm.stream` reach only `127.0.0.1:<port>` inside that VM and consume no gateway slirp socket. The daemon exposes these streams only through its own authenticated API.
- This reduced command surface is not an OS privilege sandbox. The guest agent service runs as root and accepts exec requests from the host, so the host retains full control of the guest. `pvm security` is a host command and is intentionally absent from the guest binary.

### The dashboard

`pvm dashboard` opens a separate loopback HTTP listener that serves only `GET`: a single-file HTML page and a redacted `/api/snapshot` guarded by a random 128-bit token carried in the URL fragment (sent as a Bearer header). It rejects non-loopback `Host` headers (DNS rebinding), sets a strict `default-src 'none'` CSP, and exposes no mutation endpoints.

### Apps as host windows

`pvm app` runs an Xpra server as the VM user, listening only on a unix socket in its runtime directory with a per-VM password; the host client reaches it through a daemon loopback listener over `pvm.stream`. Window titles end in the VM name, so a VM window cannot pass as a host window by its title. Clipboard and file transfer are off by default and set per VM with `pvm app config`:

- **Clipboard**: the Xpra client sends the host clipboard even while none of its windows has focus, so pvm never lets Xpra carry host content into the VM. Host content goes in only through pvm's bridge, only when one of the VM's windows is in front, and never content marked `ExcludeClipboardContentFromMonitorProcessing` by password managers. The daemon refuses clipboard streams unless the VM's policy allows them.
- **Drops**: with drops on, files dropped on a VM window upload into the VM and are never opened there; a file the VM sends to the host needs the user's confirmation in a host dialog. With drops off, the client refuses file transfer in both directions.

The Secret Service keyring of the app display is set per VM with `pvm app config <vm> --keyring off|host`:

- `off`, the default for new VMs: pvm leaves the keyring alone. The display has no login to unlock it, so apps that keep secrets there may ask for its password, and how those secrets are protected is up to the keyring the user sets up.
- `host`: the VM's `login` keyring is encrypted with a random password that pvm keeps on the host, in `keyring.secret` in the VM's app directory under `~/.pvm`, and sends over SSH stdin to unlock the keyring before any app starts. The secrets in the VM, such as Claude Desktop's sign-in, are encrypted at rest. Anyone who can read the host's `~/.pvm` can read the password. While the keyring is unlocked, the VM user and root in the VM can read its secrets. pvm never replaces the password, and never changes a keyring that has a password pvm did not set: `pvm app` then refuses to start apps.
- Before this setting, pvm kept an unencrypted `login` keyring with an empty password. A VM with no setting that still has it gets the `host` password the next time an app starts, which keeps its secrets.

`pvm app install` runs an install script as root inside the VM through the guest agent:

- The scripts are built into the pvm binary, one per app in the catalog that `pvm app catalog` lists. The daemon accepts only catalog ids and takes no script, path or URL from the caller.
- Every download happens inside the VM and leaves through the VM's route and exit, under the same kill-switch as its other traffic; the host daemon downloads nothing.
- Claude Code and Claude Desktop come from Anthropic's APT repositories, and the script refuses a signing key whose fingerprint differs from the pinned one. Codex has no signed repository: its installer is fetched from the vendor over HTTPS and run as the VM user, so that install rests on TLS and the installer's own checksum check.

## Shared files and cache consistency

What is shared by default:

- New user VMs get a private read-write `/share` backed by their own VM directory. `--share none` disables that export; `--share global` explicitly uses the common pvm share directory.
- Existing VMs keep their original share mode; an upgrade does not silently move user files.
- A clone receives an empty private share; host share contents are not copied with its disk.
- Bake and gateway VMs export no default host share.

Extra mounts:

- They use `security_model=none` and `access=any`, with the guest uid aligned to the host uid, and expose host files with the host user's privileges.
- Global shares and shared extra mounts can transmit secrets, executable code, and identifiers between VMs.
- `pvm mount --read-only` enforces a read-only QEMU backend as well as the guest mount option; adding or removing one requires a stopped VM.

What pvm refuses to share:

- Both `pvm mount` and `pvm workspace` refuse the host home directory, a filesystem root, pvm's data directory, and folders containing known credential files or directories: `.ssh`, `.aws`, `.gitconfig`, `.npmrc`, `.env` files other than template suffixes, `*.pem`/`*.p12`/`*.pfx`, and SSH private key names.
- `pvm mount --allow-sensitive` explicitly overrides this check with a warning.

Neither a separate disk nor a fresh hostname makes shared content safe, and read-write shares still let the guest modify `.git/hooks` and `.git/config`. Mount only data that the guest is allowed to read and change; do not place host SSH keys, git configuration, credentials, or personal files in shared directories.

Mount profiles:

- `coherent` uses `cache=none` and preserves the bidirectional-visibility baseline for files actively read and written on both sides. It does not provide multi-writer transaction consistency.
- `readahead`: if the host rewrites or replaces a file while an old guest file descriptor remains open, even a newly opened guest descriptor can see old contents until the old descriptor is closed. Do not use this profile when that visibility delay is unacceptable.

## Network policy and its limits

Exit routing:

- With an exit selected, unmatched IPv4 traffic is intended to use that tunnel. A failed tunnel must leave that traffic offline rather than switch it to direct egress. A successful WireGuard handshake alone does not prove DNS, TCP, UDP, or ICMP forwarding works.
- Explicit `direct`, `internal`, and bypass rules are exceptions. They may send traffic and DNS through the host uplink even when the VM has a default exit. Inspect the complete rule set before treating a VM as exit-only.
- Direct egress, both L3-forwarded direct VMs and fake-ip TCP/UDP/ICMP relays on direct rules, drops private (RFC1918), loopback, link-local, CGNAT, benchmark and multicast/reserved destinations, including slirp host loopback (`10.0.2.2`), unless an explicit `internal` domain rule or `direct --cidr` rule permits them. Plain direct-domain rules that resolve to private addresses do not implicitly allow LAN access.
- Exit-bound traffic gets the same drop at the gateway, so it does not depend on the exit: link-local/metadata and the other non-public ranges never enter an exit tunnel, except the exit-agent's own in-tunnel address (probes) and CIDRs that an explicit `pvm route add proxy --cidr <subnet> --via <exit>` rule sends there, which the exit-agent must also allow with `--domain-allow-cidr`.

Gates and pauses:

- `pvm route pause` places VMs in the nft `paused4` set and blocks relay dials, cutting external egress while preserving cluster-local links and host port maps.
- `pvm route require-check` additionally requires a passing `pvm-check/1` report and a 90-second root-owned lease file (`/run/pvm/grants.json`) on the gateway; unverified VMs sit in `unverified4`, and lease expiry removes access and flushes conntrack.

Mihomo exits:

- Mihomo exits (`pvm exit import`) run as an independent GPL-3.0 process inside the gateway VM under a locked-down systemd unit (`DynamicUser=yes`, `ProtectSystem=strict`, `NoNewPrivileges=yes`) with a root-only config (`/etc/pvm/mihomo/<name>.yaml`, `0600`) and a loopback-only SOCKS5 listener.
- Subscription credentials are stored `0600` on the host and pushed only to the gateway, never into dev VMs.
- If the Mihomo listener is unreachable or the exit violates its `--ip-mode` drift baseline (or resolves to mainland China), `MarkForExit` fails closed and live relay connections are reset.

DNS and IPv6:

- Default-exit domains resolve through the exit to real IPv4 addresses. Explicit proxy-domain rules use fake-IP mappings. Supported exit DNS queries must not silently fall back to the host resolver.
- The managed cluster is IPv4-only, drops forwarded IPv6, and suppresses AAAA answers on fake-ip, direct and internal DNS paths. Exported standard WireGuard client configurations cover IPv4 only; the client must separately disable or contain IPv6.
- Domain rules affect only resolution through the gateway DNS. Applications with hardcoded DoH can bypass those rules. Blocking known public DoH/DoQ resolver addresses cannot block every encrypted DNS service, and custom HTTPS resolvers may still be reachable through the selected route. pvm does not decrypt HTTPS to classify it.

Other limits:

- SFTP-backed workspaces (native Windows reverse sshfs) reject guest writes to `.git`, `.hg`, `.jj` and `.sl` path components; 9p read-write shares cannot filter per-path on the host, so guests with a read-write 9p share can still modify `.git/hooks` and `.git/config`.
- Loopback host port maps are accessible to other processes running on the host.
- The desktop session has no separate XFCE login and screen locking is disabled. The VNC password is carried in the noVNC URL fragment, so it is omitted from the HTTP request, but remains a secret in terminal output and browser history. `pvm desktop --public` explicitly exposes the desktop to the network, and anyone who can reach that port can attempt the password. Its HTTP/noVNC connection is not TLS-protected by pvm; use a trusted network or an authenticated encrypted tunnel. Desktop URLs contain secrets and must not be shared.

Routing policy is a security goal with regression coverage, not a proof that every host/kernel/QEMU configuration is leak-free. Test the actual deployment, including tunnel loss, before relying on it for sensitive traffic.

## QEMU builds

pvm runs QEMU only from the build selected with `pvm qemu` and never looks for it on PATH. There are two sources.

- **Project build**: compiled by the release workflow of the separate pvm-qemu repository from the upstream release tarball, which must carry a valid signature from QEMU's release key and match a pinned SHA-256, plus only upstream patches published there. Every release publishes the complete corresponding source, including the sources of bundled libraries. `pvm qemu install` accepts an archive, downloaded or given with `--file`, only when its SHA-256 equals a digest pinned in this pvm release, which this repository's CI checked with `gh attestation verify` against the pvm-qemu repository, its release workflow and the tag. A tag this release does not pin needs `--unpinned` and the same check with `gh` on your machine. A pvm release can withdraw a build; pvm then refuses to start new VMs with it.
- **Official build**: the QEMU your platform's channel installs: Homebrew on macOS, your distribution's packages on Linux, and on Windows MSYS2 UCRT64, which qemu.org links to. pvm does not download, repackage or vouch for it, and relies on that channel's own checks.

Checks for either source:

- Before first use and whenever the binary changes: a minimum version per host, accelerator and device probes, a test start with pvm's own arguments, the firmware, and on macOS a valid signature carrying com.apple.security.hypervisor.
- A project build is checked file by file and then by size and modification time at every start; one whose files changed is refused.
- These checks catch accidental or partial changes; they do not protect against the host user, who is trusted.
- An official build that its package manager replaced is checked again, and the change is logged and shown by `pvm daemon status`. pvm never switches to another build on its own.

Snapshots and missing builds:

- A RAM snapshot restores only into the build (id and qemu-system SHA-256) and CPU arguments that wrote it, and pvm refuses to snapshot a VM whose QEMU changed on disk since it started. Native Windows (WHPX) has no RAM snapshots with any build.
- Without a usable build the daemon still runs, but it starts no VM, and a cluster it cannot recover for that reason is reported as blocked: pvm restores none of its port relays, forwards or agents, so a VM still running in it has no network path.

## Images and bake

Base images:

- Downloads are checked against checksums fetched from the distribution over HTTPS. This detects corrupt or inconsistent downloads; pvm does not independently verify a distribution signing key, and trusts the HTTPS endpoint and host trust store.
- Golden images additionally contain packages installed from the base image's configured repositories. Bake does not turn an untrusted base image into a trusted one.
- For independent image signature verification, pass `pvm image fetch --keyring /trusted/distribution.gpg <image>`. This requires `gpgv` and a keyring whose origin and contents you have independently verified; every key in it is trusted. Missing or invalid signatures fail without falling back, including for cached images. No publisher key is downloaded automatically, and signatures do not guarantee image freshness.

pvm releases:

- The release installer and self-upgrader require `gh attestation verify`, binding the archive to this repository, the release workflow, and the selected tag, in addition to checking `SHA256SUMS`. This trusts GitHub and the authorized workflow; it does not protect against an authorized malicious source change. Downloads without a matching proof are rejected. The installers (`scripts/install.sh`, `scripts/install-exit.sh`), `pvm upgrade` and `pvm exit deploy` fetch the public attestation from the GitHub API without a login and verify it offline with `gh attestation verify --bundle`, so `gh` must be installed but needs no GitHub credentials; when the API cannot be reached, refuses the request or has no attestation for the file, nothing is installed.
- Installation stages all assets first and retains earlier files. pvm does not build or repackage QEMU, and `pvm upgrade` replaces only the pvm binary, never QEMU (see QEMU builds above).
- In v1.4.x only, the releases also carry byte-identical copies of the pinned pvm-qemu builds under the older `pvm-qemu-runtime-*` names, with their complete corresponding source, so that older clients can upgrade. The install script and the Windows installers replace only pvm and its own files; QEMU is then set up with `pvm qemu`, under the checks above, and a failed setup leaves pvm installed and is reported.

Bake:

- A build VM uses host/slirp egress and may use explicitly configured host proxy environment variables. It does not inherit the final VM's selected exit route.
- Proxy credentials must not be printed by shell tracing, and temporary proxy configuration is removed before a successful image is sealed. Failed builds kept with `--keep-vm` can retain credentials and provisioning logs; treat them as sensitive. Removing a file inside qcow2 is not forensic erasure of all backing blocks.
- A successful bake resets SSH host keys, machine-id and cloud-init state and removes the build user. It does not reset arbitrary application enrollment, revoke credentials, or certify every file in the image.
- Interrupted build VMs can contain sensitive state and are preserved for explicit inspection and removal rather than automatically deleted based on a name prefix.
- Golden versions are retained as backing images. Do not edit, replace, or remove a golden image while any VM or snapshot depends on it. A forced guest shutdown must not be treated as successful image sealing.

## Credentials and identity

SSH:

- New VMs use separate management SSH keys under the pvm data directory. Existing VMs retain their original key; those using the original shared pvm key expose a stable public-key correlation identifier inside each guest.
- Clone uses the source management key only to bootstrap its first identity reset, then replaces that authorized pvm key with its own while preserving independently added keys.
- pvm does not automatically copy the host's `~/.ssh` or `~/.gitconfig` into guests. Explicit mounts, files placed in `/share`, SSH options, and commands run by the user can still expose host data.
- Managed SSH, SCP and gateway hops ignore personal SSH configuration, offer only the pvm key, and disable host-agent use and forwarding. Manually invoked SSH can use different options; review its actual command and environment.

Exits:

- The exit control plane uses HTTPS, a bearer token, and certificate fingerprint pinning; the data plane uses WireGuard. Verify the initial fingerprint through a trusted channel: trust-on-first-use alone does not authenticate the first connection. An invite from `pvm-exit invite` carries the fingerprint, so it authenticates the server as far as the way it travelled does: `pvm exit deploy` fetches it over SSH, which has already authenticated the VPS; an invite pasted from a chat is only as trustworthy as that chat.
- When `pvm exit deploy` needs a sudo password, pvm reads it from the host's terminal without echo and sends it once through the SSH connection. On the VPS it lives only in a variable of the setup shell and reaches `sudo -S` on stdin: never in a command line, the environment or a file, and the commands run under sudo get no stdin. It is not stored anywhere.
- Exit tokens have roles. The admin token file and admin tokens manage the server; enroll tokens, which invites hand out, register peers and see and remove only their own, up to a peer limit (64 by default). The server keeps only SHA-256 hashes of issued tokens and invites (`auth.json`, mode `0600`). An invite works once and expires, after 15 minutes by default and 24 hours at most. `pvm-exit token rm` revokes a token and removes its peers, so a lost host loses its tunnels too. `pvm exit rm` revokes the host's own enroll token once its last peer is gone, so no unused token stays valid on the server.
- `pvm-exit` runs as the unprivileged `pvm-exit` user with `NoNewPrivileges`, `ProtectSystem=full` and `ProtectHome`. Kernel NAT gets only `CAP_NET_ADMIN` and `CAP_NET_RAW`, which still allow changing the VPS's network configuration, so do not share that user; userspace NAT gets no capabilities, and `pvm-exit init --user` runs with no root at all. The legacy `pvm exit-agent install` service runs as root until it is migrated with `pvm-exit init --migrate`.
- Prefer `--token-file` with an owner-only regular file to avoid bearer tokens in process arguments and shell history. During upgrades, preserve the existing service unit's token and fingerprint, and investigate a certificate fingerprint mismatch rather than blindly pinning the new value.
- WARP registration stores the private key and device token in `~/.pvm/exits/<name>.json` with mode `0600`. Only the referenced exit's WireGuard data-plane parameters are pushed to the managed gateway; the Cloudflare API token is not. If registration fails and remote rollback also fails, pvm preserves recovery credentials in a mode-`0600` `~/.pvm/exits/<name>.warp-pending` file and refuses to register that name again until the device is restored or revoked. Treat both files as secrets and follow the recovery instruction instead of deleting the pending state or reusing the name.
- Each exported standard WireGuard client profile and its QR code contain a private key. Use a separate profile for each device and never share one profile across devices. Treat the exported file, terminal scrollback, and QR code as secrets.

Secrets in general: exit certificates, WireGuard keys, API tokens, desktop passwords, and snapshots are secrets. Keep the pvm data directory private, back it up securely, and never publish raw configuration files, `wg` private-key output, or full diagnostic dumps. Existing credentials must not be silently replaced when unreadable or inconsistent; investigate and restore the expected files.

Identity:

- A fresh VM initializes OS machine identifiers. Clone preserves the source's effective default exit before any boot and regenerates its hostname, machine-id, management key, and SSH host keys on first boot, but copies user files and may preserve application-specific identifiers such as an AI client's random userID, tokens, browser profiles, and cache state. A snapshot restores the same identity and secrets.
- To avoid copying an existing client enrollment, create a fresh VM or clone before installing the client, then use the application's supported enrollment and credential-management process. Do not assume clone or a MAC/hostname change provides a new application identity. pvm cannot prevent account correlation through OAuth, billing, IP reputation, shared files, or application telemetry.
- Timezone and locale are convenience settings, not evidence of geography or anonymity. Keep application configuration consistent with its supported deployment and the service's terms. pvm cannot guarantee a service's trust score, telemetry behavior, paid features, account separation, or ban outcome.

## Local diagnostics and outbound metadata

- **Update checks** are disabled by default. `update_check: true` in the host config or `PVM_UPDATE_CHECK=1` enables GitHub requests using host egress; GitHub can see that source address. `PVM_NO_UPDATE_CHECK=1` overrides both. Successes and failures are cached for 24 hours. An exit server's timezone suggestion uses its system settings or its configured `timezone`, without contacting an IP geolocation service. When a VM's route switches to an exit, the gateway looks up the exit's public IP through that exit (ipwho.is, then geojs.io and ipapi.co), the same lookup `pvm check` makes, and the VM takes that zone unless the exit has `--tz`; these services see the exit's address, never the host's.
- **Logs**: successful domain-connect requests are not logged by default. The gateway maintains a root-only fake-IP mapping file for explicit `pvm dns ls` inspection; the command's output contains browsing destinations and must remain private. Gateway configuration containing exit credentials is root-only.
- **Local checks**: `pvm audit --privacy` and `pvm doctor` report configuration and permission issues with identifying values withheld; they do not verify a running guest's traffic.
- **Diagnostic bundles**: `pvm diag [output.zip] [--offline]` writes an atomic, whitelist-only ZIP containing a manifest, projected host/VM/exit metadata (no raw key files, tokens or host mount sources), bounded tails of daemon and VM console/QEMU logs, and, when the daemon is running, live status and gateway `journalctl` output. Registered tokens, private keys, desktop passwords and exit endpoints are redacted along with URL credentials, PEM blocks, user home paths and public IPv4 addresses; private and documentation ranges remain readable for routing diagnosis. Review any diagnostic bundle before sharing it.
- **Log rotation**: daemon logs rotate at 8 MiB (two rotated generations) and redact log messages on write; VM `console.log` (8 MiB) and `console.log.qemu` (2 MiB) rotate before each QEMU start (one rotated generation).
- **The VPS exit** blocks private, link-local, metadata, multicast and local-host destinations by default across its managed data paths, except its own tunnel probe/control services. `--domain-allow-cidr` explicitly permits matching destinations across those paths, so use narrow entries. Local interface addresses are captured at exit startup; restart the exit after changing its interface addresses. These controls do not isolate mutually hostile peers from all VPS administration risks.

## Verify a running environment

Use mock names below with the actual deployment's names. Keep real addresses, hostnames, tokens and command output private.

```bash
pvm daemon status
pvm list
pvm route ls
pvm exit ls
pvm mount ls dev1
pvm audit dev1
pvm audit dev1 --privacy
pvm doctor
pvm route explain dev1 example.com
pvm exit test my-exit
```

- Check the route's default, per-VM override, explicit domain/CIDR exceptions, registered exit, and mount source paths. `pvm audit` checks several network protocols, but is a point-in-time test and does not simulate all outages. WARP testing has narrower coverage than a pvm VPS exit.
- For an offline guest, `pvm exec dev1 <cmd...>` uses the agent channel and requires no guest networking. Readiness states differ: a running QEMU process is not proof that cloud-init, SSH, the exec agent, or the cluster gateway is ready.
- For tunnel-loss testing, use a disposable environment and an exit you control. Observe IPv4 traffic and DNS at the host uplink while the exit is unreachable; check that exit-bound flows fail and no direct fallback occurs. Test IPv6, non-A/AAAA DNS, hardcoded resolver requests, split rules, and long-lived connections. Do not stop a shared production exit as an audit shortcut. Restore the exit rather than switching a sensitive VM to `direct`. Repeat the test after changing the QEMU build with `pvm qemu use` or `pvm qemu install --use`, and after a package manager upgrades an official build.
- Review guest enrollment and secrets before sharing a clone or snapshot. Review `/share` and every extra mount before running untrusted code.
- Keep QEMU, base images, guest packages, and pvm current; `pvm qemu ls` shows the builds, the default and the build this pvm release recommends. A package manager upgrade of an official build makes RAM snapshots taken with the previous build unrestorable; if you rely on snapshots, hold the package (`brew pin qemu`, `sudo apt-mark hold qemu-system-x86 qemu-utils`) or use a project build, and change builds deliberately. Rebuild golden images to update future VMs, and update existing guests separately.
