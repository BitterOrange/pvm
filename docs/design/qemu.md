# QEMU builds: official channels and pvm-qemu

Status: proposed, nothing implemented. The four directions under [Decisions](#decisions) were chosen on 2026-11-25; the items under [Open decisions](#open-decisions) are not settled. The code references describe v1.3.6.

## Motivation

Every pvm release ships a QEMU runtime per host, and the Windows installers embed it:

- **Size.** QEMU is most of the Windows installers. In v1.3.6, `pvm-qemu-runtime-windows-x86_64.tar.gz` is 64.6 MB of the 112.8 MB `_setup.exe`; the MSI is 177.7 MB.
- **Provenance.** The runtimes are repacks of other people's binaries: the Homebrew bottle on macOS (re-signed with the hypervisor entitlement), Ubuntu 24.04's apt QEMU 8.2.2 on Linux, and a weilnetz master snapshot on Windows (`VERSION=20260811`, reporting `11.1.0 (v11.1.0-12130-ge470268ff4)`). Nothing is pinned. `script/qemu/build-qemu.sh` exists but is unused.
- **Fixes we need are not in any build we ship.** The WHPX XSAVE fixes (upstream 3e985595ce6f, b63d6420ad98, 6275714a72b2) are only on master and in 11.2.0-rc1, so WHPX guests have no AVX (see the WHPX item in [roadmap.md](../roadmap.md)).
- **GPL.** QEMU is GPLv2. Whoever distributes a binary must offer the complete corresponding source. The repacks shipped without source, and the weilnetz commit cannot be found in the public repository (see [Past releases](#past-releases-and-the-gpl)).
- **Discovery searches PATH.** `qemu.Find` (`internal/qemu/find.go`) tries the bundled slot, then PATH, Program Files and Homebrew paths, and trusts the slot by location: `Toolchain.Bundled` skips the darwin HVF probe and exempts the build from the `BrokenLiveRestore` refusal.

The goal is that installers stop carrying QEMU, and each user picks either a QEMU that this project builds from signed upstream source, or the QEMU of their platform's official channel.

## Today

- **Discovery.** `qemu.Find` is called in five places: `internal/cli/daemon.go` (daemon start), `internal/cli/vm.go` twice (the golden bake, which runs in the CLI, and the Windows create-time 9p decision), and `internal/cli/wsl.go` twice (the `auto` backend and doctor). The not-found error says `brew install qemu` on every OS.
- **Snapshots.** `snapshot.json` matches `qemu_version` as an `X.Y.Z` string only, so it cannot tell a stock 11.1.3 from a patched one.
- **WHPX arguments** are fixed in `Accel.Args()`: `whpx,kernel-irqchip=off`, `vmport=off`, `-cpu host,-xsave` (or `qemu64` while kernel args are pending), and `noxsave` in the guest.
- **Install and upgrade.** `scripts/install.sh` and `pvm upgrade` download, attest and swap the runtime (`internal/cli/upgrade_runtime.go`), with `--rollback` restoring the previous slot. `PVM_SKIP_QEMU=1` skips it.
- **Windows installers.** NSIS and MSI embed the runtime. `pvm-running.ps1` finds running VMs by the QEMU path.

## Decisions

1. **Two sources.** Installers stop bundling QEMU. A user chooses a project build or an official build.
2. **Project build ("self-compiled").** The CI of a new public repository, BitterOrange/pvm-qemu, builds QEMU from the signed upstream tarball, optionally with patches that are already upstream. Releases carry build-provenance attestations, `SHA256SUMS` and the complete corresponding source. Users install it with `pvm qemu install`, or from PVM Desktop on Windows. This repository's `/releases/latest`, which `install.sh`, `pvm upgrade` and the update check use, is untouched.
3. **Official build.** Each platform's own channel: Homebrew on macOS, the distribution's packages on Linux, and on Windows the builds qemu.org links to (weilnetz or MSYS2 UCRT64). pvm detects, guides and checks them; it never downloads, repackages or redistributes them.
4. **Per-VM QEMU** is planned now and delivered in phases. Phase 1: several builds side by side with one global default, with build identity and snapshot binding in their final form from the start. Phase 2: a build per VM, CPU and irqchip arguments derived from each build's capabilities, and the WHPX AVX switch.

## Releases

Every step leaves every platform with a way to get a working QEMU.

| Step | Release | What users get |
|---|---|---|
| 0 | pvm-qemu `11.1.3-pvm1` | The first stock build for all three hosts, attested, with source. Validated under unchanged pvm v1.3.6 on real HVF, KVM and WHPX hosts before any pvm code changes. |
| 1a | pvm v1.4.0 (rc prereleases first) | `pvm qemu`, builds side by side, official builds, a daemon that runs without QEMU, the Desktop QEMU page. `pvm upgrade` stops touching QEMU. The Windows installers still carry the pinned project build. |
| 1b | new `install.sh` once v1.4.0 is Latest; v1.4.1 | Installers without QEMU; QEMU is set up on first run. |
| 2a | pvm-qemu `11.2.0-pvm1` | The WHPX XSAVE fixes. If 11.2.0 slips, `11.1.3-pvm2` with the three commits backported. |
| 2b | pvm v1.5.0 | A build per VM, CPU arguments from capabilities, WHPX AVX opt-in per VM with a global kill switch. |
| 2c | v1.5.x, then v1.6.0 | AVX becomes the default for new VMs after a soak on both CPU vendors. v1.6.0 deletes the transitional code. |

Tags with a `-` are published as GitHub prereleases, so v1.3.6 clients can test `pvm upgrade --to v1.4.0-rc1` without moving `/releases/latest`.

`install.sh` on master changes only after v1.4.0 is Latest. README pipes master's script, which installs `/releases/latest`; a new script on master during the rc phase would install v1.3.6 with a script that expects v1.4. The old script plus the bridge assets installs v1.4.0 correctly.

## Build identity

Each project archive carries `<arch>/pvm-qemu.json` (schema 1):

- `build` (the tag), `qemu_version`, `host`, `target`;
- `upstream` {tarball, sha256, signing_key};
- `patches[]` {file, sha256, upstream_commit};
- `configure[]`;
- `system` (the wrapper or exe) and `system_exec` (the real executable: `bin/qemu-system-x86_64.real` behind the Linux wrapper);
- `img` and `firmware_dir`;
- `declares` {virtio_9p, slirp, accels[]}, informational and required to agree with the probes;
- `build_env` {runner_image, container_digest, run_id, commit};
- `source_assets[]`.

`internal/qemu/identity.go` defines `BuildIdentity {id, source, channel, version, system_sha256}`:

- `id` is the pvm-qemu tag, or `official`;
- `system_sha256` hashes the real executable: the `.real` ELF on Linux, or the `EvalSymlinks` target of an official binary. A content hash tells stock from patched builds, and also catches a `brew upgrade` under the same path;
- the channel's package version is recorded for diagnostics.

It renders as `11.1.3-pvm1@<sha[:12]>`.

A project build is accepted only when its `build` equals the directory name, its `host` matches the running host, the parsed `--version` equals `qemu_version`, every file matches `SHA256SUMS` with no unlisted file, the tag is not revoked, and `declares` agrees with the probes. Capabilities that change guest-visible CPU behaviour (`whpx_xsave`, `whpx_kernel_irqchip`) are never read from the archive; they come only from pvm's pin table.

## State and layout

- **`<PVM_HOME>/qemu.json`** (schema 1, mode 0600) holds `default` and `builds[]`. It is written only with `fsutil.AtomicWrite` under `run/qemu.lock`, decoded with `DisallowUnknownFields`, and a corrupt file is an error, never reset. On Windows the final rename is retried for up to 2 s, because Go opens files without `FILE_SHARE_DELETE` and a lock-free reader can make it fail. Each entry records the identity, `dir` for project builds, absolute `system`, `system_exec`, `img` and `firmware_dir` paths, `archive_sha256`, `caps` {accels[], virtio_9p}, `version_line`, and `checked_by`/`checked_at`. Per-file stats are kept in memory, not on disk.
- **Project builds** live in `runtime/qemu/builds/<tag>`. An install downloads to `builds/.stage-<tag>.part` (resumable), extracts and verifies in `builds/.stage-<rand>/`, then takes the lock, renames into place and writes `qemu.json` last. Removal renames to `builds/.trash-<rand>`, writes `qemu.json`, then deletes; if the write fails the rename is reverted. The resolver ignores dot-directories, and `gc` cleans them.
- **Official builds** stay where their channel put them.
- **No selection anywhere else.** No `config.yaml` key, environment variable, symlink or junction selects a build. `qemu.json` is machine state read by absolute path, so the launchd, systemd or scheduled-task environment cannot change the result. Phase 2 adds an optional `qemu_build` to `vm.json`.
- **Transitional adoption sources**, until v1.6.0: `runtime/qemu/<arch>` on every OS, and on Windows `runtime/qemu/msi-payload/x86_64`, which only the v1.4.0 MSI writes.

## Resolution

`internal/qemu/resolve.go` replaces `Find`. There is no PATH search anywhere.

- **`Resolve(p)`** reads `qemu.json` without the lock, takes the default, refuses a revoked build and checks identity: for a project build it stats every file, with a full verification once per process and after any stat change; for an official build it re-hashes when the stat changed. It re-probes in memory when `checked_by` is stale. It returns a `Toolchain` with `Build` and `Caps`. With no default it returns `ErrQEMUNotSetUp`, or `ErrAdoptionPending` when a candidate can be adopted. It never migrates.
- **`cli.resolveQEMU(p)`** wraps it for the bake (including the auto-bake inside `create`), the Windows create-time 9p decision (now `caps.virtio_9p`) and `pvm qemu`. On `ErrAdoptionPending` it migrates when no daemon holds `DaemonRunLock`, reports what blocks adoption when a v1.4+ daemon runs, and says `restart the daemon to finish the upgrade: pvm daemon restart` when an older daemon runs.
- **`Ready(p)` and `Inspect(p)`** never change anything. They serve the `auto` backend, Desktop and doctor.
- **The daemon** replaces `Server.TC` and `Server.Accel` with a selector, `s.qemuFor(spec)`. It re-reads `qemu.json` when its stat changes, caches per build id with the accelerator detected once per build, and checks identity on every call. A changed official binary goes through the gates, probes and smoke start again; the result is used in memory and persisted best effort. A changed project build is refused. VM start, gateway start and watchdog restart go through it; create, clone, compact, resize and mounts use the effective build; snapshots use `s.qemuForRunning`, the build recorded in `run.json`. Running VMs keep the binary they started with.
- **The bake stays in the CLI.** With one state file and per-start resolution in the daemon, both agree at the time of each operation. `golden.json` records the `qemu_build` that baked the image.

## Gates

`Toolchain.Bundled`, the darwin shortcut in `accel.go` and `BrokenLiveRestore` are removed. No build is exempt by location. Every build, project or official, passes these before it is registered and after its binary changes:

- **Minimum version per host:** darwin/arm64 11.1.0, windows/amd64 11.1.0, linux/amd64 8.2.0. An unparseable `--version` fails. A single 11.1.0 floor would exclude every LTS distribution's QEMU.
- **Probes**, each with a 10 s timeout: `-accel help` (the native accelerator and tcg), `-machine help` (virt or q35), `-netdev help` (user and stream), `-device help` (the virtio devices pvm uses, `pcie-root-port`, and `virtio-9p-pci-non-transitional`, required on darwin and linux and recorded on Windows), `-object help` (`rng-builtin` on Windows, `rng-random` elsewhere), `qemu-img --version`, and the firmware file. The firmware check replaces today's silent `fw = ""`.
- **Smoke start.** Help output cannot show the argv features pvm relies on, so pvm runs its own: in a temp dir under `run/`, create a 64 MiB qcow2 with this build's `qemu-img`, run `qemu-img convert -B` (the snapshot form) and the `-b` form compaction uses, then start pvm's real argv from `BuildArgs` for a synthetic VM under TCG with `-S`: smbios, a chardev socket with logfile, user netdev with hostfwd and `ipv6=off`, a stream netdev to a temp listener, the virtio devices, and a 9p fsdev where supported. The process must be alive after 3 s and accept `hostfwd_add` over QMP. This runs at install, `use`, `check` and after a binary change, never per start.
- **darwin:** `codesign --verify --strict`, and `com.apple.security.hypervisor` in the entitlements. A missing entitlement is a hard error, never a silent TCG run. The check moves here from `upgrade_runtime.go`.
- **Project builds** are verified file by file against `SHA256SUMS` at install, migration, `pvm qemu check` and the daemon's first use. At every start the size and mtime of every listed file are compared, and any difference triggers a full verification. On Linux the executed code is the shell wrapper, the bundled `ld.so` and `.real`, so hashing only `qemu-system` would miss a changed loader or library.

An unavailable host accelerator stays a visible TCG fallback, and `tcgHint` gains a darwin message. `PVM_QEMU_ACCEL` applies only to a build that passed its gates; it is recorded in `run.json` and the snapshot's `qemu_args`, and shown in status as `accel_forced`.

These checks catch accidental or partial changes. The host user is trusted (SECURITY.md), so they are not a boundary against that user.

## Official channels

`qemu.DetectOfficial(goos)` is read-only and checks fixed locations only, each with a check that the channel owns the files. A binary that merely sits on PATH is never labelled official.

- **macOS:** `/opt/homebrew/bin/qemu-system-aarch64` and `qemu-img`, resolving into `/opt/homebrew/Cellar/qemu/<ver>/bin`, with firmware in `/opt/homebrew/share/qemu`. The stable bin path is registered, so a `brew upgrade` is noticed. Guide: `brew install qemu`, then `pvm qemu use official`; snapshot users `brew pin qemu`.
- **Linux:** `/usr/bin/qemu-system-x86_64` and `qemu-img`, owned per `dpkg-query -S`, `rpm -qf` or `pacman -Qo`. Supported: Debian, Ubuntu, Fedora and Arch. Guides by `/etc/os-release`: `sudo apt install qemu-system-x86 qemu-utils` (snapshot users: `sudo apt-mark hold qemu-system-x86 qemu-utils`), `sudo dnf install qemu-system-x86-core qemu-img`, `sudo pacman -S qemu-base`. RHEL-family `/usr/libexec/qemu-kvm`, NixOS, Alpine and Gentoo, and anything below 8.2.0, get an explicit `not a supported official channel; use pvm qemu install`.
- **Windows:**
  - weilnetz: the directory in `HKLM\Software\qemu64` `Install_Dir` (64-bit view, which also records a custom `/D=` directory), with the QEMU uninstall entry and its `DisplayVersion`; firmware in `share\` next to the exe. Installing needs administrator rights.
  - MSYS2: the root from the MSYS2 uninstall entry's `InstallLocation`, else `C:\msys64`; it requires `ucrt64\bin\qemu-system-x86_64.exe` and the pacman local-db entry `mingw-w64-ucrt-x86_64-qemu-*`; firmware in `ucrt64\share\qemu`. Guide: `pacman -S mingw-w64-ucrt-x86_64-qemu` in a UCRT64 shell.
  - Both guides link https://www.qemu.org/download/#windows. With both present, `use official` needs `--channel`.

pvm prints the install step and never runs a package manager or installer: they need sudo or UAC, and decision 3 forbids redistribution. The UI names the channel ("QEMU from Homebrew", "your distribution's QEMU", "QEMU for Windows linked from qemu.org"), never "Official QEMU", since none of these channels is the QEMU project itself. `official` stays only as the command keyword.

The v1.4.0 notes say that QEMU on PATH, in `/usr/local` or in `%LocalAppData%\Programs\qemu`, all of which v1.3 `Find` used, is no longer used, and name the replacement command.

## Installing project builds

### Pins and attestation

`internal/qemu/pvmqemu.json` (schema 1), embedded in pvm:

- `repo` and `workflow`;
- `min_version{host}` and `recommended{host}`;
- `builds{<tag>: {hosts{<host>: {asset, sha256, size, tree_sha256, system_sha256, source_assets[]}}, declares{whpx_xsave, whpx_kernel_irqchip}}}`, where `tree_sha256` is the SHA-256 of the archive's `SHA256SUMS`;
- `revoked{<tag>: reason}`;
- `official_validated[]`, the official channel versions pvm has validated for CPU capabilities (phase 2).

`script/ci/fetch-qemu-builds.sh` checks every pin with `gh attestation verify` against pvm-qemu's `release.yml` and the tag, then compares size, digest, tree and `system_sha256`. It runs on PRs that touch `pvmqemu.json`, weekly, and in the tag package job. Pins enter pvm only through reviewed PRs.

At run time, pvm accepts an archive in exactly one of two ways, which never fall back to each other:

- its SHA-256 equals this pvm's pin, whether downloaded or given with `--file`. No `gh` is needed, which matters because Windows Desktop users usually have none. Trust still reduces to pvm-qemu's workflow, passed on through pvm's own attested release; the precedent is the pinned Xpra client digest in `script/windows/fetch-xpra-client.sh`;
- for a tag this pvm does not pin, `--unpinned` plus `gh attestation verify` of both the archive and `SHA256SUMS`, bound to the repo, the signer workflow, the tag ref, hosted runners and github.com. Missing `gh` is an error.

`revoked` makes `Resolve` refuse that build as default and for new starts, with `install the recommended build: pvm qemu install --use`. Running VMs keep running. Revocations and new pins reach users only through a pvm release. Proposed cadence: re-pin within 14 days of an upstream stable point release or a QEMU security advisory that affects the shipped configuration, in a pvm patch release.

### Downloads and offline install

From v1.4.1 the first-run download is the only way a fresh Windows install gets QEMU, and today's client (30 s total timeout, no resume, no system proxy) fails any 70 MB file below about 2.3 MB/s. A shared download client, also used by `pvm upgrade`:

- no total timeout; a 30 s response-header timeout and a 60 s idle timeout per read;
- a resumable `.part` file with HTTP Range, three retries with backoff, and a body cap at the pinned size;
- `HTTPS_PROXY`/`HTTP_PROXY`; on Windows, when those are unset, the static HKCU Internet Settings `ProxyServer` when `ProxyEnable=1`. A PAC-only setup fails with an error that names `HTTPS_PROXY` and `--file`; full PAC support would need a JavaScript engine.

`pvm qemu install --file <archive>` matches the archive's SHA-256 against this host's pins and works offline. Desktop offers "Install from file".

## RAM snapshots

`SnapshotManifest` drops `qemu_version` and gains:

- `qemu_build`, the `BuildIdentity` of the running process from `run.json` (`RunState` gains `qemu_build`, written at start);
- `qemu_args`, the CPU-relevant part of the argv the VM actually ran with: the values of `-accel`, `-machine` and `-cpu` and the presence of `-no-reboot`. It is taken from `RunState.Args`, so an early TCG fallback is recorded correctly.

**Creating** a snapshot uses the build recorded in `run.json`. It is refused when that build is no longer registered, or when the file on disk no longer has the recorded `system_sha256` (a `brew upgrade` or apt replaced it under the VM). The disk is captured with that build's `qemu-img`.

**Restoring** first substitutes the accelerator as today, then requires the same build id and `system_sha256` as the build this start uses (phase 1: the default; phase 2: the VM's build), and the same `qemu_args`. The error names both builds and the ways out: `pvm qemu use <tag>`, `pvm qemu install <tag>`, or `pvm snapshot rm dev1`. `pvm qemu ls` and doctor list snapshots bound to an official build that has since changed.

**Snapshots written before v1.4** are refused. Which binary wrote them cannot be known, because the bridge or installer swaps the repack for a pvm-qemu build before the new pvm runs, and binding by version would allow a restore across builds. The error offers `pvm upgrade --rollback -y` (start the VM there, which consumes the snapshot, then upgrade again), `pvm snapshot rm dev1`, or the existing restore-overlay guidance. The first v1.4 command prints a one-time notice listing affected VMs. Consumed snapshots are never checked. A v1.3.x reader sees an empty `qemu_version` and refuses; the release notes say so.

## The daemon without QEMU

Today readiness is computed once, and `daemonRun` exits when `Find` fails. Without QEMU every daemon-backed command, including `pvm ls`, `pvm rm` and `pvm snapshot rm`, would wait 60 s and fail, and under systemd's `Restart=on-failure` the exit becomes a restart loop.

- **Startup.** `daemonRun` no longer resolves QEMU and never exits because of it. `daemon.Run` takes `DaemonRunLock`, runs the migration, builds the selector and serves.
- **Recovery.** An `ensureCluster` error matching `qemu.ErrUnusable` (no default, default missing, gate failure, modified project build, revoked build, adoption pending) marks that cluster `blocked: <reason>`. A blocked cluster's relays, forwards and agents are not recovered, so nothing forwards traffic, and a VM still running in it has no gateway path: fail-closed. A blocked cluster does not count against daemon readiness; every other error keeps today's "not ready".
- **Watcher.** A 5 s loop, next to the workspace mounts loop, re-stats `qemu.json` and the adoption candidates. On a change it retries a pending adoption and the blocked clusters, then recomputes readiness. `pvm qemu use|install` therefore takes effect at the next start without a daemon restart.
- **Errors.** Start, create, clone and snapshot endpoints return 409 with the typed error, which names the next command, and the CLI prints it as is.
- **Status.** `/v1/health` keeps `ready`. `/v1/status` drops `qemu` and `bundled`, and adds `qemu_build` {default, source, channel, version, path, accel}, `qemu_changed`, `qemu_problems[]`, `blocked_clusters[]` and `accel_forced`. The new names make a CLI and daemon of different versions fail visibly. Daemon, cluster and VM readiness stay separate (Hard Constraint 7).
- **`Ensure`** keeps its gating with no QEMU preflight, so `pvm ls`, `rm`, `snapshot rm`, `exit` and `route` work without QEMU. It gains a child watch: if the daemon exits before it becomes healthy, it reports `the daemon exited while starting: <last daemon.log line>; full log: <path>`.

`pvm daemon status` prints a line such as `qemu:    11.1.3-pvm1 (project build, QEMU 11.1.3) <path>`, with notes such as `dev2 still runs on official until it restarts` or `cluster 1 is blocked: no QEMU yet; pvm qemu install`. Against a daemon older than v1.4.0 it says to restart it.

Doctor gains read-only, redacted checks that never start the daemon or use the network: `qemu`, `qemu_builds`, `qemu_update` (a newer pin, or a revoked default), `qemu_leftovers` (gc candidates, pre-1.4 snapshots, snapshots bound to a changed official build) and, on Windows, `qemu_desktop` (an older `pvm-desktop.exe`). Under the WSL backend the native checks are labelled `native (not used: backend is wsl)`.

## Locking

`fsutil` offers only an exclusive non-blocking lock, and a long-held lock would fail gateway, watchdog and VM starts. So `run/qemu.lock` covers only short critical sections: the final rename plus the `qemu.json` write, migration renames, and the removal rename plus write. It is taken with `fsutil.TryLock`, retried every 100 ms for up to 30 s, then fails with `another pvm is changing QEMU builds; retry`. Downloads, verification, extraction and probes run unlocked in `builds/.stage-*`. Reads are lock-free, and VM starts never take the lock.

## Commands

```
pvm qemu [ls [--json]]
pvm qemu install [<tag>] [--file <archive>] [--use] [--unpinned] [-y]
pvm qemu use <tag>|official [--channel weilnetz|msys2] [-y]
pvm qemu check [<tag>|official|--default] [--read-only]
pvm qemu rm <tag>|official|--all [-y]
pvm qemu gc [-y]
```

- **`ls`** lists the builds (default marker, id, source and channel, QEMU version, accelerator, virtio-9p, gate status, size, running VMs, bound snapshots), an official install that is detected but not registered, the recommended pin and revocations, adoption candidates and why each is or is not adoptable, and leftovers.
- **`install`** without a tag installs the recommended pin, and says `already installed` when that build is intact. It becomes the default only when none is set or with `--use`. Steps: a free-space check (3x the size), the download with progress, verification, extraction, gates, probes, the smoke start, then the lock, rename and registry write. It asks at a terminal and needs `-y` otherwise.
- **`use`** runs the checks, writes the default, and lists VMs that keep another build until they restart and snapshots that need another build.
- **`check`** writes nothing; `--read-only` also skips migration. Exit codes: 0 ok, 3 not set up, 1 failing, 2 usage. `pvm upgrade` relies on them.
- **`rm`** runs inside a healthy daemon (`DELETE /v1/qemu/builds/{id}`), which serialises it against starts with a per-build reference count, or under `DaemonRunLock` when no daemon runs. It refuses the default, builds that running VMs use, and (phase 2) pinned builds, and asks before removing builds that unconsumed snapshots are bound to. For `official` it only unregisters.
- **`gc`** lists staging and trash directories, non-adoptable candidates, old runtime backups and pack leftovers, with sizes, and deletes only after confirmation or `-y`. It refuses anything a live QEMU runs from, and keeps the backup the current upgrade recovery point references and every symlink target of a kept entry, saying why.

`qemu` joins the commands without the update notice. It follows the effective backend like every VM command, so under the WSL backend it is delegated to WSL. `PVM_SKIP_QEMU` is removed. A daily local note appears when the default is not the recommended pin or is revoked.

Phase 2 adds `pvm qemu use <tag>|official|default --vm <vm> [--avx|--no-avx]` and `pvm create --qemu <tag>|official [--avx]`.

## Error messages

Each names the next command, and none falls back to another build. Examples:

- No default: `pvm: no QEMU yet; pvm needs one to run VMs. Choose one (once): pvm qemu install (QEMU 11.1.3 built by the pvm-qemu project, about 70 MB) | pvm qemu use official`, with a second line per OS (`brew install qemu first`; `sudo apt install qemu-system-x86 qemu-utils (8.2 or newer) first`; on Windows the qemu.org link with the administrator note, or the MSYS2 command). It replaces the `brew install qemu` error and the 60 s wait.
- Adoption blocked: `pvm: runtime/qemu/x86_64 holds pvm-qemu 11.1.3-pvm1 but dev1 still runs from it; stop it (pvm stop dev1) and pvm adopts it`.
- Mismatched tree: `pvm: runtime\qemu\msi-payload\x86_64 differs from pinned 11.1.3-pvm1 in libglib-2.0-0.dll and 2 more; install it: pvm qemu install --use`.
- Default gone: `pvm: the default QEMU (QEMU from Homebrew, /opt/homebrew/bin/qemu-system-aarch64) is no longer there; reinstall it (brew install qemu) or pick another: pvm qemu ls`.
- Changed and failing: `pvm: QEMU from Homebrew changed on disk (11.1.3 -> 11.0.0) and failed its check: older than 11.1.0; run brew upgrade qemu, or pvm qemu use 11.1.3-pvm1`.
- Project build changed: `pvm: 11.1.3-pvm1 failed its check: bin/libglib-2.0-0.dll differs from SHA256SUMS; reinstall: pvm qemu rm 11.1.3-pvm1 -y && pvm qemu install 11.1.3-pvm1 --use`.
- Revoked: `pvm: 11.1.3-pvm1 was withdrawn by this pvm release (<reason>); install the recommended build: pvm qemu install --use`.
- No entitlement: `pvm: <path> lacks the com.apple.security.hypervisor entitlement, so macOS would refuse HVF; use brew install qemu or pvm qemu install`.
- Too old: `pvm: your distribution's QEMU 6.2.0 (/usr/bin) is older than 8.2.0, the oldest pvm supports on Linux; use pvm qemu install`.
- Unpinned without gh: `pvm: 11.2.0-pvm1 is not pinned by this pvm; --unpinned needs the GitHub CLI (gh) to verify its attestation; install gh, or install the pinned build: pvm qemu install`.
- PAC proxy: `pvm: Windows uses a proxy script (PAC) that pvm cannot read; set HTTPS_PROXY, or download pvm-qemu-11.1.3-pvm1-windows-amd64.tar.gz and run pvm qemu install --file <path>`.
- Snapshot mismatch: `pvm: dev1 has a RAM snapshot taken with 11.1.3-pvm1@1a2b3c4d5e6f and -cpu host,-xsave; it restores only into that build and those args: pvm qemu use 11.1.3-pvm1, or drop it: pvm snapshot rm dev1`.
- Pre-1.4 snapshot: `pvm: the snapshot of dev1 was written before pvm v1.4.0 and cannot be bound to a QEMU build; restore it under the version that wrote it (pvm upgrade --rollback -y, then pvm start dev1, then pvm upgrade) or drop it: pvm snapshot rm dev1`.
- Upgrade guards: `pvm: pvm v1.4+ cannot install v1.3.x with --to; use pvm upgrade --rollback -y, or that version's installer`, and `pvm v1.4.2 does not accept the current default QEMU 11.1.3-pvm1: <reason>; install a build it accepts first, then upgrade`.
- 9p on Windows: `this QEMU build has no virtio-9p (no QEMU for Windows has it); using --share none (set PVM_BACKEND=wsl for host shares)`.

## PVM Desktop

- **Backend.** `State` gains `qemu` {ready, pending_adoption, blocked_reason, default, recommended {id, size_mb, installed}, official {found, channel, label, version, problem}, problem}, read from `qemu.Ready`, `Inspect` and a cached `DetectOfficial` that "Check again" clears. New methods run `pvm.exe` as tasks with `PVM_BACKEND=native` in the subprocess: `InstallQEMU()` (`pvm qemu install --use -y`), `InstallQEMUFromFile(path)`, `UseOfficialQEMU(channel)`, `UseQEMU(id)`, `RemoveQEMU(id, confirmed)` and `QEMUBuilds()`. They work from the WSL page too. The daemon picks up `qemu.json` through its watcher.
- **QEMU page.** Shown when `qemu.ready` is false, even with a healthy daemon, which keeps auto-starting because it no longer needs QEMU. Two cards: "QEMU built by pvm (recommended)", built from the signed upstream source by the pvm-qemu project, checked against this pvm release, about 70 MB, no administrator rights, with Install and Install from file; and "QEMU for Windows linked from qemu.org", with the detection result or failed check, the weilnetz administrator note, the MSYS2 command, "Open qemu.org downloads" and "Check again". Errors use the task callout with the last line and Try again; a PAC error offers Install from file.
- **WSL page** gains "Set up QEMU for Windows", which says that this moves the app to the native backend and that WSL VMs stay reachable with `wsl -- pvm`.
- **Elsewhere.** A QEMU panel in Settings; About shows the build and the pvm-qemu notice (unaffiliated, trademark). English and Chinese strings; mock scenes `noqemu`, `pending`, `blocked` and `installing`.
- **By release.** The v1.4.0 installers still place QEMU, so the page appears only without it. In v1.4.1 it is the first-run path.

## Windows installers and backends

**v1.4.0**

- `pack-setup.sh` is fed the extracted pinned tree, in the `bin/share` layout it requires.
- NSIS stages it and swaps it into the old slot as today, so the result is byte-identical to the pin.
- The MSI installs it into `[PVMHOME]\runtime\qemu\msi-payload\x86_64` with component GUIDs from a new heat prefix, `msi-payload/x86_64/`. Component GUIDs come from the path only, `RemoveExistingProducts` runs late, and same-or-higher-version DLLs are costed as already present, so an upgrade at the old path would probably leave a mixed weilnetz and pvm-qemu tree that cannot be adopted. At a new path, the old MSI's slot files are removed as files it placed, and the new files are fresh installs. Migration adopts the payload. Phase 0 measures this; the fallback is `REINSTALLMODE=amus` at the old path.
- `pvm-running.ps1` takes `-PvmHome` instead of `-RuntimeDir`. It matches `pvm.exe` and `pvm-desktop.exe` by path under the install dir, and `qemu-system-x86_64.exe` when its command line, with backslashes and without case, contains `<PvmHome>\vms\`. VMs then count wherever their QEMU lives; bake and gateway VMs also live under `vms\`. NSIS writes a `PvmHome` value under its uninstall key, and its Uninstall section reads it (an empty value aborts with a message). The MSI passes `[PVMHOME]`.

**v1.4.1** drops QEMU from NSIS, the MSI (`RuntimeFiles`, `RUNTIMEDIR`, `SetRuntimeDir`) and `pack-setup.sh`, whose arguments become `<pvm.exe> <pvm-desktop.exe> <xpra-dir> [out]`. NSIS removes its `RuntimeRoot` value once. Expected MSI outcomes, to be confirmed: v1.4.0 to v1.4.1 removes nothing that migration already moved; if pvm never ran in between, the payload goes and the QEMU page appears; v1.3.6 to v1.4.1 removes the old repack. `builds\` has no MSI components. A v1.3.x `uninstall.exe` kept by `pvm upgrade` still checks `-RuntimeDir`, so the notes tell Windows users to rerun the installer.

**Auto backend.** Without bundled QEMU, today's rule would send fresh installs on machines with pvm in WSL to a dead end. `auto` chooses native when a native daemon is healthy or `qemu.Ready` holds (a default is set, or an adoption is pending), otherwise wsl when WSL has pvm, otherwise native. A detected but unregistered official QEMU does not count. Native setup under wsl is explicit: `PVM_BACKEND=native pvm qemu install`, or Desktop's route.

**Uninstall** never removes `runtime\qemu\builds` or official installs; kept VMs and snapshots are bound to them, and from v1.4.1 the installer did not place them. The message names `pvm qemu rm --all -y`, to run before uninstalling.

## pvm upgrade and install.sh

**`pvm upgrade` from v1.4.0**

- The QEMU staging, activation and validation go. The darwin entitlement check moves into `internal/qemu`. The 30 s client is replaced by the shared download client.
- `--to` below v1.4.0 is refused before any download.
- After the staged binary is validated, when the effective backend is native, it runs `PVM_BACKEND=native <staged> qemu check --default --read-only` with a 60 s timeout. Exit 0 continues; exit 3 prints the setup hint and continues; any other code, a signal or a timeout aborts before the recovery point is saved, with the staged output. A new pvm that raises a gate therefore changes nothing.
- On Windows, when `pvm-desktop.exe` sits next to `pvm.exe`, it downloads the new `pvm-desktop_windows_amd64.tar.gz` asset, verifies it like pvm, saves `pvm-desktop.exe.previous-<sha>` in the recovery point and replaces it after `pvm.exe`, using the rename-aside that works while the app runs; it then tells the user to reopen PVM Desktop. Rollback restores it. The old `install.sh` insists on a single entry in the pvm archive, so Desktop needs its own asset. This reverses the packaging decision in [gui.md](gui.md).
- Afterwards it prints `pvm <tag> recommends QEMU <id>: pvm qemu install --use` when the recommended pin differs. It never switches builds.
- Kept until v1.6.0: the recovery point's QEMU fields and `restoreQEMUUpgrade`, so `--rollback` to v1.3.x restores the old slot. It works after migration too.

**`install.sh`, merged once v1.4.0 is Latest**

- The QEMU variables, staging, swap and runtime rollback hint go, along with the Git Bash `PVM_HOME` block that exists only for the runtime root.
- `TAG` below v1.4.0 is refused with the URL of that tag's own script.
- `PVM_QEMU=project|official|none` (default `project`), validated before any download. After the binary is in place, `project` runs `pvm qemu install -y` (downloads only when the recommended pin is missing, never switches an existing default), `official` runs `pvm qemu use official -y`, and `none` prints both commands. On failure it exits non-zero with `pvm is installed; QEMU setup failed: retry pvm qemu install`, and the binary stays.
- `gh` stays required for the pvm binary's own attestation.
- A CI job runs master's `install.sh` against the current Latest, weekly and on PRs that touch it.

## Old clients: bridge assets and rollback

Every v1.3.x `pvm upgrade` downloads and validates `pvm-qemu-runtime-*.tar.gz` from the target tag before it saves the recovery point. A release without them gets a 404; the failure is safe but blocks the upgrade. So, until v1.6.0, every release attaches:

- the pinned archives, byte-identical, as `pvm-qemu-runtime-{aarch64,linux-x86_64,windows-x86_64}.tar.gz`;
- `pvm-qemu-<tag>-source.tar.gz` and each matching deps-source asset;

all fetched and checked by `fetch-qemu-builds.sh --bridge` and attested by pvm's `build.yml`. The archive format keeps today's runtime layout, so the same bytes pass the v1.3.6 `install.sh` and upgrader checks (attestation, `SHA256SUMS`, layout, `VERSION`, firmware, `--version`, entitlement), and one archive serves as the phase 0 test input, the bridge asset and the v1.4.0 installer payload.

A v1.3.x binary installed by `--to` would silently fall back to PATH or a system QEMU once the slot has been migrated, which is why `--to` below v1.4.0 is refused. v1.6.0 removes the bridge and the restore code; its notes tell v1.3.x users to run `PVM_SKIP_QEMU=1 pvm upgrade`, then `pvm qemu install`.

## Migration

`internal/qemu/migrate.go` exists from v1.4.0 through v1.5.x and is idempotent. Under the two-source rule a repack is neither source, and it must not be deleted unasked (Hard Constraint 3); the `install.sh` rollback commands and the macOS symlink depend on it.

**Who runs it.** Only a process holding `DaemonRunLock`: the v1.4+ daemon at start and on its watcher tick while an adoption is pending, or a CLI process when no daemon runs. A v1.3.x daemon left running by `--no-restart` or `install.sh` still launches from the slot, and blocks migration until `pvm daemon restart`.

**Per candidate** (`runtime/qemu/<arch>`, and on Windows `runtime/qemu/msi-payload/x86_64`):

1. If it is a symlink (macOS `install.sh`), resolve it and require the target inside `runtime/qemu`.
2. Hash its `SHA256SUMS` and compare with the pinned `tree_sha256` values. This is cheap, so a repack costs nothing on repeated checks.
3. On a match, verify every file and reject unlisted files. Then rename it into `builds/<tag>`, register it and set it as default if none is set; or, if `builds/<tag>` already holds the same identity, remove the duplicate. Refuse while a live `run.json` argv points into it. Windows renames retry for 10 s.
4. On a digest match with mismatching files, leave it and name the files in `pvm qemu ls`, doctor and the setup error.
5. With no match (a repack), leave it for `gc`.
6. Never touch `<arch>.previous-*`, `release-*`, `.upgrade-qemu-*` or `.x86_64.new/.old`.
7. Log every action, and on the first run print the notice about pre-1.4 snapshots.

Results: a v1.3.x `pvm upgrade` leaves an adoptable slot through the bridge, and the restarted v1.4 daemon adopts it; the old `install.sh` with v1.4.0 does the same, on macOS through a symlink; the v1.4.0 NSIS fills the slot and the MSI the payload directory; `PVM_SKIP_QEMU=1` upgraders get the explicit "no QEMU yet" error.

## Failure and rollback

| Case | Result |
|---|---|
| Crash during install | `.part` resumes on the next install; `.stage-*` is cleaned by the next install or `gc`; an unregistered `builds/<tag>` is verified and registered by the next install. |
| Removal | In the daemon with a reference count, or under `DaemonRunLock`. The `.trash` rename fails atomically on Windows while files are in use. |
| Two writers | The short lock with bounded retry. |
| Corrupt `qemu.json` | Explicit error, never reset. |
| Binary changed | Official: checked again and logged. Project: refused. |
| Snapshot into another build, or under a changed binary | Refused. |
| Revoked build | Refused, with the install command. |
| New pvm raises a gate | The staged check aborts the upgrade before anything is replaced. |
| No usable QEMU | Daemon up and ready, blocked clusters reported, Desktop shows its QEMU page. |
| Old daemon still running | No migration until it restarts. |
| v1.4.x to v1.3.x | `pvm upgrade --rollback` restores binary, Desktop and slot. v1.3.x ignores `builds/` and `qemu.json`, and refuses v1.4 snapshots. `--to` below v1.4.0 is refused; installers: run the old installer. |
| Between builds | `pvm qemu use <previous>`. |
| v1.5 to v1.4 | Refused while `whpx_avx` is set in `config.yaml`. |
| Maintainer side | Each phase is a revertible commit set; a bad pvm-qemu tag is revoked in the next pvm release. |

## The pvm-qemu repository

### Bootstrap and protections

The maintainer creates the repository by hand; no agent creates repositories or changes their settings. It is public from day one: artifact attestations on private repositories need GitHub Enterprise, and the source must be public anyway.

- Actions on, with a read-only default `GITHUB_TOKEN`; only the publish job gets `contents`, `id-token` and `attestations` write.
- A tag ruleset for `*-pvm*`: maintainers only, no update or delete.
- Protected main requiring PRs, with the solo maintainer's merge policy written down; CODEOWNERS on `.github/`.
- A `release` environment with the maintainer as required reviewer and self-review allowed.
- Immutable releases if available, and private vulnerability reporting as the security contact.
- Actions pinned by SHA, GitHub-hosted runners only, no self-hosted runner.
- Only `.github/workflows/release.yml` publishes, on tags matching `^[0-9]+\.[0-9]+\.[0-9]+(-rc[0-9]+)?-pvm[0-9]+$`, draft-first: create a draft, upload, write `SHA256SUMS`, attest, then publish. rc tags are prereleases and are never pinned. `ci.yml` builds PRs, runs the tamper tests and publishes nothing.

### Layout and naming

- `releases/<tag>.env` (`QEMU_VERSION`, `QEMU_SHA256`, `PATCHES`), committed before tagging; `keys/qemu-release.asc` and `FINGERPRINT`; `patches/<tag>/` (`git format-patch` with an `Upstream: <sha>` trailer).
- `script/`: `build.sh` (from `build-qemu.sh`, with its silent pvg edit replaced by `--disable-pvg`), the three pack scripts moved from this repository, a new `probe-linux-runtime.sh`, `check-closure.sh`, `hv-entitlements.plist`, `verify-upstream.sh`, `collect-dep-sources.sh`, and a copy of `lib/native.sh`. pvm then deletes `script/qemu/`.
- `scripts/validate.sh` and `validate.ps1`, run by hand on real hosts.
- README, SECURITY, AGENTS and NOTICE. NOTICE and README say the project is unaffiliated with QEMU and that QEMU is a trademark of Fabrice Bellard.
- Tags `<qemu-version>-pvm<N>`; N increments on any change of patches, flags or dependency set. Release title: `QEMU 11.1.3 for pvm, build 1 (unofficial)`. Binaries keep upstream names.
- Assets: `pvm-qemu-<tag>-{darwin-arm64,linux-amd64,windows-amd64}.tar.gz`, `pvm-qemu-<tag>-source.tar.gz`, `pvm-qemu-<tag>-<os>-<arch>-deps-source.tar` and `SHA256SUMS`.
- Each archive has one top-level `<qemu-arch>/` directory of regular files and directories only: `bin/`, `lib/` (macOS and Linux), `share/qemu` (macOS and Linux) or `bin/share` (Windows), `VERSION` (the tag), `pvm-qemu.json`, `doc/` and `SHA256SUMS` covering every other file, with executables at mode 0755 in the tar headers. `.tar.zst` or zip would be rejected by old clients, and so would a Windows `share/qemu` layout.

### Source verification and patches

`verify-upstream.sh` fetches `qemu-<v>.tar.xz` and its `.sig` from download.qemu.org over HTTPS only. All of these are required: `gpg --show-keys` of the committed key yields exactly the pinned fingerprint, `gpgv --status-fd 1` emits `VALIDSIG` for it, and the SHA-256 equals the env file. Patches apply with `patch -p1 --forward --fuzz=0 --batch`, each patch's SHA-256 goes into the manifest, and only commits already merged upstream are allowed. `ci.yml` proves the checks fail on a flipped tarball byte, a substituted `.sig` and a corrupted patch context.

Patch sets: none for `11.1.3-pvm1` and `11.2.0-pvm1`; the three WHPX commits, which touch only `target/i386/whpx`, for `11.1.3-pvm2`, built only if 11.2.0 has not shipped when v1.5.0 is otherwise ready. A larger `FD_SETSIZE` for Windows (roadmap: port maps) would get its own tag.

### Builds per OS

A `source` job (ubuntu-24.04) verifies, patches and uploads the tree.

- **darwin-arm64** on macos-15 with Homebrew build dependencies: `--target-list=aarch64-softmmu --enable-hvf --enable-slirp --enable-virtfs` and the usual disables (cocoa, gtk, sdl, docs, guest agent, user, gnutls, curl, libssh, pvg). `pack-runtime.sh` takes a `QEMU_PREFIX` instead of the Cellar path and a required `DEST`, rewrites the dylib closure with `install_name_tool`, then signs. `check-closure.sh` fails on any `otool -L` path outside `@executable_path`, `@loader_path`, `/usr/lib` or `/System`, since a hosted Mac cannot hide `/opt/homebrew`. A TCG smoke runs from a copy at another path with `DYLD_*` cleared. `minos` is recorded.
- **linux-amd64** in a digest-pinned ubuntu:24.04 container: `--enable-kvm --enable-slirp --enable-virtfs --enable-attr --enable-cap-ng --disable-modules` plus the disables. `pack-linux-runtime.sh` takes its inputs from the install prefix and BIOS/iPXE from QEMU's own `pc-bios`. The isolated startup probe moves to `probe-linux-runtime.sh`, which runs on the runner host with sudo (a default container lacks `CAP_SYS_ADMIN` for `unshare --mount`), hides the distribution QEMU directories with tmpfs, runs from a copy at another path and fails when not root. Extra TCG smokes run in debian:12 and fedora containers.
- **windows-cross** on ubuntu-24.04: a container built from the verified tarball's own `tests/docker/dockerfiles/fedora-win64-cross.docker` (digest and `rpm -qa` go into the manifest), `configure --cross-prefix=x86_64-w64-mingw32- --target-list=x86_64-softmmu --enable-whpx --enable-slirp` plus the disables, then `make installer`, whose `scripts/nsis.py` collects the DLL closure, then `pack-windows-runtime.sh` with a local installer (the weilnetz scraper goes, and `VERSION` comes from the tag). `check-closure.sh` fails on a DLL import that is neither shipped nor a Windows system DLL. This is upstream's own cross build path and runs on Linux runners. If whpx is missing from the cross build, the fallback is a native MSYS2 UCRT64 build on windows-2025.
- **windows-smoke** on windows-2025: `--version`, whpx and tcg in `-accel help`, user and stream in `-netdev help`, the devices, virtio-9p recorded as absent, a 3 s TCG start, and pvm's argv smoke from a pinned pvm checkout. No Authenticode, like `pvm.exe`.

Every OS job writes `pvm-qemu.json`, `doc/` (COPYING, COPYING.LIB, LICENSE, THIRD-PARTY.txt, SOURCE.txt), `VERSION` and `SHA256SUMS`; checks the archive rules and the `declares`-versus-probe agreement; runs a 3 s TCG start; and has a 120 minute timeout. Build times are not measured yet.

### macOS signing

After the `install_name_tool` rewrites, every dylib and `qemu-img` is signed ad hoc, then `qemu-system-aarch64` last with `codesign --force -s - --entitlements hv-entitlements.plist`, which holds only `com.apple.security.hypervisor`. There is no identity path that depends on a secret, since an optional identity would repeat the silent fallback the pvm release job once had; this matches upstream meson and Homebrew bottles. CI asserts `codesign --verify --strict` and the entitlement, and that a deliberately unsigned copy fails. pvm writes files with Go HTTP, which sets no quarantine xattr (to be verified), and extracts `--file` archives itself, so the xattr stays on the archive. A hand-extracted tree is never adopted.

### GPL source

- Per tag, `pvm-qemu-<tag>-source.tar.gz` holds the exact upstream tarball and `.sig`, the patches, a `git archive` of the repository at the tag and `BUILD.md`.
- Per OS, a deps-source asset from `collect-dep-sources.sh`: `apt-get source --download-only` after `dpkg -S` on Linux, the Homebrew formulae plus `brew fetch --build-from-source` on macOS, and `dnf download --source` after `rpm -qf` for the mingw closure.
- In each binary archive, `doc/THIRD-PARTY.txt` lists component, version, license and source file, and CI fails if a shipped library is unlisted. `doc/SOURCE.txt` names the source assets of the same release.
- Every pvm release that carries any QEMU binary (the bridge assets, the v1.4.0 NSIS and MSI payload) attaches byte-identical copies of the matching source assets, listed in `SHA256SUMS` and attested. GPLv2 section 3 counts network access as an offer only when the source is available "from the same place", so a link to the pvm-qemu release is not enough.
- Releases are never deleted. Source assets stay while any binary of the tag is offered or pinned, and for at least three years after.

## Phase 2: per-VM QEMU and WHPX AVX

### Per-VM selection

- `vm.json` gains `qemu_build` (empty follows the default) and `whpx_avx`, both `omitempty`, with no migration.
- `pvm create --qemu <tag>|official [--avx]` is checked in the CLI (the 9p decision uses that build's caps) and again in the daemon; clone copies both fields.
- `pvm qemu use <tag>|official|default --vm <vm> [--avx|--no-avx]` goes through `POST /v1/vms/{name}/qemu`, which requires a stopped VM, refuses while an unconsumed snapshot is bound to another build, and writes `vm.json` under `qemu.lock`.
- The daemon resolves `spec.QEMUBuild` or the default with no fallback. A pinned build that is missing, revoked or failing is a typed error, and a gateway in that state marks its cluster blocked. Gateways can be pinned the same way and follow the default with AVX off. The bake uses the default build and the safe profile. `rm` refuses pinned builds. Status adds `qemu_builds[]`.
- Desktop shows each VM's build, a "Change QEMU" dialog with the AVX toggle, and a select on Create.
- v1.4's `vm.json` decoding is not strict and would silently drop the pins, so `pvm upgrade --rollback` from v1.5 to a v1.4 recovery point refuses while `config.yaml` sets `whpx_avx` (which v1.4 cannot load) and lists the pinned VMs. Snapshot binding still catches RAM restores.

### CPU profiles and AVX

`internal/qemu/profile.go` replaces `Accel.Args`, `ArgsForGuest` and `GuestKernelArgs`.

A VM is eligible for AVX when its accelerator is whpx, its build has `whpx_xsave` in pvm's pin table (a pinned project build, or an official channel and version in `official_validated`), `spec.WHPXAVX` is set, and `config.yaml` `whpx_avx` (a new strict key, `auto|off`, default `auto`) is not `off`. `whpx_xsave` is never inferred from the version.

`DesiredKernelArgs` returns `noxsave` on WHPX unless the VM is eligible. `CPUArgs` on WHPX: `qemu64` while kernel args are pending; `host,-xsave` with verified `noxsave`; `host,-cet-ss,-cet-ibt` (or the profile the soak selects) without `noxsave` and eligible; otherwise `qemu64`, and the reconciler adds `noxsave`. Every WHPX profile keeps `whpx,kernel-irqchip=off`, `vmport=off` and `-no-reboot` until a build declares `whpx_kernel_irqchip`. HVF and KVM stay on `host` and TCG on `max`, byte-identical to today.

### Kernel arguments

`reconcileKernelArgs` replaces `applyPendingKernelArgs` and `ensureGatewayKernelArgs`, and works in both directions. When the desired arguments differ from `kernel_args`, it writes or deletes `/etc/default/grub.d/99-pvm-kernel-args.cfg` over SSH, runs `update-grub`, verifies the presence and absence of each managed argument in `grub.cfg`, records `kernel_args` with `pending=false`, cold-boots and verifies `/proc/cmdline`. On a mismatch it sets `pending=true` and stops the VM. `vm.json` keeps `kernel_args` and `kernel_args_pending`, so there is no format change.

- Removing `noxsave`: the VM boots `host,-xsave` first, then the AVX profile.
- Back to an unfixed build, or the kill switch: `qemu64`, then `noxsave` is added, then `host,-xsave`.
- A golden image's kernel args seed its clones, and each clone reconciles at first start.
- Snapshots are invalidated through `qemu_args`.

AVX is opt-in in v1.5.0. It becomes the default for new VMs on capable builds in v1.5.x, after at least 8 hours per CPU vendor (10 vCPUs, Claude Desktop, `stress-ng --verify`) with no xsave errors. Existing VMs are unchanged.

## Phases and exit criteria

### Phase 0: pvm-qemu `11.1.3-pvm1`

No pvm code changes. Steps: bootstrap the repository; move and generalise the scripts; upstream verification with negative tests; the build jobs with relocation proof; source assets and the draft-first release; then, on an HVF Mac, a KVM host and the WHPX host, stop the daemon, replace `~/.pvm/runtime/qemu/<arch>` with the extracted archive and run the full cycle under unchanged v1.3.6 (v1.3.6 takes any working binary in the slot): create with a bake, start and readiness, PTY ssh, 9p on macOS and Linux, a map, an exit route with tunnel loss, snapshot create and restore (proving `qemu-img convert -B` on 11.1.3), compact, `pvm app` on Windows, and several hours of normal WHPX use compared with weilnetz 11.1.0. Finally restore the original slot.

The MSI experiment runs on clean Windows VM snapshots: from a v1.3.6 MSI install, upgrade to (a) a test MSI with the payload at `msi-payload\x86_64` and the new GUID prefix, (b) one at the old path with `REINSTALLMODE=amus`, and (c) one without QEMU, each with and without a simulated migration, and check for self-heal. If (a) fails, v1.4.0 uses (b); if both fail, the MSI design is revisited.

Exit: the tag has three archives, source and deps-source assets, `SHA256SUMS` and attestations that verify under the pvm policy (and fail with another `--source-ref`); the Linux probe is proven isolated (it fails when `/usr/share/qemu` is the only source of `bios-256k.bin`); the closure checks pass; all three archives passed the v1.3.6 run on real hosts with no TCG fallback; the MSI experiment chose the layout.

### Phase 1a: pvm v1.4.0

Steps: the core (identity, state, gates, probes, smoke start, resolve, pins); official detection; archive install, downloads and `--file`; snapshot binding; one-time adoption; the daemon selector and the lifecycle without QEMU; `pvm qemu` and its consumers; `pvm upgrade` decoupled and replacing Desktop; the Desktop QEMU page; installers, detection and release CI (bridge, source and Desktop assets, the prerelease flag, the pins job, `upgrade-compat.yml`, `install-latest.yml`, and `e2e-kvm.yml` if hosted runners expose `/dev/kvm`); docs, rule changes and English release notes.

Exit, on real HVF, KVM and WHPX hosts: a fresh install from the installers reaches a ready VM; upgrades from v1.3.6 through `pvm upgrade`, `--no-restart` plus restart, the old `install.sh`, NSIS and the MSI all end with a working default, and rollback to v1.3.6 works; `pvm qemu use official` works with Homebrew, Ubuntu apt, weilnetz and MSYS2; the darwin gate rejects an unsigned copy; tunnel loss cuts VM traffic with one project and one official build per OS; the daemon without QEMU stays usable; pre-1.4 snapshots are refused with recovery steps; `upgrade-compat.yml` is green; `go test ./...` and `go vet ./...` pass in Linux and macOS CI.

### Phase 1b: new install.sh, then v1.4.1

The new `install.sh` merges right after v1.4.0 is Latest. v1.4.1 removes QEMU from NSIS, the MSI and `pack-setup.sh`, documents first-run setup, proxies, `--file`, the auto backend with pvm in WSL and that uninstall keeps builds, and adds the Windows tag smoke from the roadmap (silent install, `pvm qemu install -y`, `pvm qemu check --default`, then uninstall with builds kept).

Exit: a fresh Windows 11 VM without `gh` reaches a running WHPX VM through Desktop alone and through the CLI alone, also behind an explicit proxy and offline with `--file`; the NSIS and MSI upgrade and uninstall matrices pass; master's `install.sh` installs Latest in CI; the installer size drop is measured.

### Phase 2a: pvm-qemu with the WHPX fixes

`11.2.0-pvm1`, or `11.1.3-pvm2` as above. An rc prerelease from 11.2.0-rc may serve early soak runs and is never pinned. v1.4.x users can install it with `--unpinned`, and it runs on the safe profile until v1.5.0 pins it and declares `whpx_xsave`. Exit: published with attestations and sources, and a multi-hour WHPX soak on Intel and AMD with `-cpu host,-cet-ss,-cet-ibt` and no `noxsave` shows AVX and AVX2, passes `stress-ng --verify`, and logs no xsave errors.

### Phase 2b: pvm v1.5.0

The per-VM pin, the profiles and kill switch, the reconciler, Desktop's per-VM QEMU and AVX, the rollback guard, the new pin, docs. Exit: VMs on different builds run side by side; the AVX opt-in and its reversal both work with guest cmdline verification, also for a gateway and a golden clone; the kill switch reverts every VM at its next start; an injected `update-grub` failure leaves the VM stopped and pending, booting `qemu64` next; snapshots refuse a restore across a build or profile change; rollback to v1.4 is guarded; HVF, KVM and TCG argv are byte-identical to v1.4.

### Phase 2c: v1.5.x and v1.6.0

v1.5.x gives new VMs on capable builds `whpx_avx=true` unless the kill switch is set, with the soak results in the notes. v1.6.0 deletes `migrate.go`, the payload adoption, the bridge and source copies, the QEMU rollback fields and the transitional rule sentences. Exit: no code reads the old slot or the payload directory except `gc`, and releases carry no QEMU binaries.

## Rule changes

### AGENTS.md

Hard Constraint 5 is replaced in v1.4.0 with:

> 5. QEMU 只有两种来源。
> (a) 项目构建：独立仓库 pvm-qemu 的发布 workflow 从上游发布源码包构建（源码包必须同时通过固定指纹的 GPG 签名校验和固定 SHA256 校验，只叠加该仓库公开的上游补丁），并在同一 release 公开完整对应源码。pvm 只在以下情况把它安装到 `~/.pvm/runtime/qemu/builds/<tag>`：归档 SHA256 与本版本 `internal/qemu/pvmqemu.json` 的固定值一致（本仓库 CI 已用 `gh attestation verify` 对 pvm-qemu 仓库、发布 workflow 和 tag 核对）；或对未固定的 tag 显式使用 `--unpinned` 并通过同样绑定的 `gh attestation verify`。`pvmqemu.json` 撤回的构建不得使用。
> (b) 官方构建：macOS 的 Homebrew、Linux 发行版软件包、Windows 上 qemu.org 链接的 weilnetz 安装版或 MSYS2 UCRT64，由用户通过该渠道安装。pvm 只在固定位置检测并核对渠道归属、给出安装指引和检查，不下载、不重新打包、不分发。
> pvm 只按 `~/.pvm/qemu.json` 中选定构建的绝对路径运行 QEMU，不在 PATH 或其他位置查找。任何构建在登记前和二进制变化后，都必须通过按宿主的最低版本门槛、加速器与设备能力探测、pvm 参数冒烟启动和固件检查；macOS 上 qemu-system 还必须通过 `codesign --verify --strict` 并带 `com.apple.security.hypervisor` entitlement。这些检查不得绕过，也不得按安装位置豁免。项目构建在安装、迁移、`pvm qemu check` 和 daemon 首次使用时逐文件核对 SHA256SUMS，之后每次启动核对每个文件的大小和修改时间，有变化即重新逐文件核对，不符即拒绝使用。所选构建缺失、被改动、被撤回或不合格时显式报错并给出下一步命令，绝不静默换用其他构建。RAM 快照只恢复到写入它的同一构建（构建 id 加 qemu-system SHA256）和同一 CPU 参数。影响客户机 CPU 行为的能力声明只来自 `pvmqemu.json`。本仓库不构建、不重新打包 QEMU。
> v1.6.0 之前的过渡例外：旧版 `pvm upgrade` 使用的 `pvm-qemu-runtime-*` 资产和 v1.4.0 Windows 安装器中的 QEMU，只能是 `pvmqemu.json` 固定、与 pvm-qemu 发布资产逐字节相同的归档，并在同一 release 附带其完整对应源码；迁移只能把逐文件核对通过的固定构建，从 `runtime/qemu/<arch>` 和 Windows 的 `runtime/qemu/msi-payload` 收编为项目构建，且只能由持有 daemon 运行锁的进程执行。
> VPS 的 nft/iptables 规则按实例命名空间精确创建和清理，绝不 flush 全局规则。

v1.6.0 deletes the paragraph that begins "v1.6.0 之前的过渡例外".

The research principle on legacy compatibility gets, in v1.4.0, a time-boxed exception, deleted in v1.6.0:

> 例外：QEMU 过渡代码（旧位置与 `msi-payload` 的收编迁移、`pvm-qemu-runtime-*` 桥接资产及其源码副本、回滚到 v1.3.x 所需的 QEMU 恢复字段）按 Hard Constraint 5 的过渡例外保留，到 v1.6.0 统一删除；期间不得扩展，也不得提前删除。

Hard Constraint 1 gets an addendum, an open decision:

> 上游项目公开发布的签名公钥指纹，以及公开发布产物的 SHA256（如 `script/windows/fetch-xpra-client.sh` 和 `internal/qemu/pvmqemu.json` 中固定的值、SECURITY.md 中的 QEMU 发布密钥指纹），是公开校验值，不属于上述禁止项；新增或修改时须在评审中逐项核对来源。

Other lines: the no-binaries rule says "QEMU 构建及其归档" instead of "QEMU runtime"; the `internal/qemu` map entry covers the build registry, identity, gates, probes, install and official detection; the `script/` entry says this repository does not build or pack QEMU, pins pvm-qemu in `internal/qemu/pvmqemu.json` and checks the pins with `script/ci/fetch-qemu-builds.sh`; the build entry drops `PVM_PACK_QEMU`; and Build & Test gains: changes to QEMU selection, gates, probes, CPU arguments or snapshot binding are verified with at least one project and one official build, covering create, start, readiness, snapshot create and restore, and tunnel loss cutting the VM off.

### SECURITY.md

The trust boundary names "the QEMU build selected with `pvm qemu`" instead of "QEMU". The kill-switch test is repeated after changing the build and after a package manager upgrades an official build. The install paragraph says pvm does not build or repackage QEMU and describes the transitional copies. The advice to keep QEMU current names `pvm qemu ls`, and says that a package manager upgrade of an official build makes RAM snapshots of the previous build unrestorable (`brew pin qemu`, `apt-mark hold`, or a project build). A new section:

> **QEMU builds.** pvm runs QEMU only from the build selected with `pvm qemu` and never looks for it on PATH. There are two sources.
>
> A project build is compiled by the release workflow of the separate pvm-qemu repository from the upstream release tarball, which must carry a valid signature from QEMU's release key and match a pinned SHA-256, plus only upstream patches published there. Every release publishes the complete corresponding source, including the sources of bundled libraries. `pvm qemu install` accepts an archive, downloaded or given with `--file`, only when its SHA-256 equals a digest pinned in this pvm release, which this repository's CI checked with `gh attestation verify` against the pvm-qemu repository, its release workflow and the tag. A tag this release does not pin needs `--unpinned` and the same check with `gh` on your machine. A pvm release can withdraw a build; pvm then refuses to start new VMs with it.
>
> An official build is the QEMU your platform's channel installs: Homebrew on macOS, your distribution's packages on Linux, and on Windows the weilnetz installer or MSYS2 UCRT64 that qemu.org links to. pvm does not download, repackage or vouch for it, and relies on that channel's own checks. The weilnetz installers are master-branch snapshots, verified only by HTTPS and a SHA-512 file on the same host, and their code-signing certificate has expired.
>
> For either source, pvm enforces a minimum version per host, accelerator and device probes, a test start with pvm's own arguments, the firmware, and on macOS a valid signature carrying com.apple.security.hypervisor, before first use and whenever the binary changes. A project build is checked file by file and then by size and modification time at every start; one whose files changed is refused. These checks catch accidental or partial changes; they do not protect against the host user, who is trusted. An official build that its package manager replaced is checked again, and the change is logged and shown by `pvm daemon status`. pvm never switches to another build on its own.
>
> A RAM snapshot restores only into the build (id and qemu-system SHA-256) and CPU arguments that wrote it, and pvm refuses to snapshot a VM whose QEMU changed on disk since it started.

v1.5.0 adds: capabilities that change guest CPU behaviour, such as XSAVE under WHPX, are enabled only for builds this pvm release declares after validation, and `whpx_avx: off` in `config.yaml` turns them off for every VM. Whether the key's fingerprint appears in the section depends on the Hard Constraint 1 decision.

### SKILL.md, README and others

- SKILL.md: never run QEMU directly, only `pvm qemu`; build artifacts no longer contain QEMU; no QEMU for Windows has virtio-9p; `PVM_QEMU_ACCEL` applies only after the gates; the new `auto` rule and `PVM_BACKEND=native` for native setup under WSL; `pvm upgrade` never changes QEMU and on Windows also replaces PVM Desktop; a new "QEMU builds" section with the commands, `--file`, `HTTPS_PROXY`, the two sources, the errors, the daemon without QEMU and snapshot binding (phase 2: `--vm`, `--qemu`, `--avx`, `whpx_avx: off`).
- README.md and README.zh-CN.md together: installers and `pvm upgrade`, uninstall keeping builds, virtio-9p per build, the `auto` rule, the Windows installer limitations, and a new "QEMU" section. The install paragraph changes only with the new `install.sh`.
- [gui.md](gui.md): the packaging line and a "First run: QEMU page" decision. [roadmap.md](../roadmap.md): the WHPX item split, the Windows CI tag smoke, the Desktop update item ticked, no self-hosted runner for this work.
- `cmd/pvm/main.go` usage, the user-visible strings that mention bundled QEMU or `brew install qemu`, and the installer comments.
- Release notes (English): v1.4.0 covers the two sources and the migration, repacks left for `gc`, restoring RAM snapshots before upgrading, the QEMU version change per host, PATH and `/usr/local` no longer used, `PVM_SKIP_QEMU` removed, the bridge and source copies, `--to` below v1.4.0 refused, rerunning the Windows installer after a v1.3.x `pvm upgrade`, `pvm daemon restart` after `--no-restart`, and a correction of the v1.3.x "replaces pvm.exe only" lines.

Every release compares the built `pvm skill` and `pvm security` with the files and greps for personal paths, hosts and tokens.

## Verification

Hosted CI cannot run HVF or WHPX (no nested virtualization), and KVM on hosted runners is unverified. There is no self-hosted runner for this work: a runner for a public repository on the maintainer's daily host, which holds `~/.pvm` keys and tokens, is a risk that `workflow_dispatch` alone does not remove. Real HVF, KVM and WHPX runs are manual, from the documented scripts, on a disposable VM or a named host, with logs attached to the release PR. Unverified items go into the release notes.

- **Resources.** macOS: a contributor's Mac on current macOS, or the maintainer's. WHPX: the maintainer's host. A second CPU vendor for WHPX: a contributor. Clean Windows: a fresh Windows 11 VM with nested virtualization, restored from a snapshot for each run; test MSIs are built with wixl on Linux, WSL or CI. KVM: `e2e-kvm.yml`, else a contributor's Linux host.
- **Per OS**, with at least one project and one official build: create with a bake, start and readiness, PTY ssh, a 9p mount (macOS, Linux), a map, an exit route with a tunnel-loss kill-switch check, snapshot create and restore, compact, and `pvm app` on Windows.
- **Transitions:** the MSI v1.3.6 to v1.4.0 adoption; an old Desktop with the new CLI, with and without pvm in WSL; a daemon with no default, a broken default, a blocked migration with an orphan VM, and `use` taking effect without a restart; master's `install.sh` against Latest; a throttled link, an explicit proxy, a PAC-only proxy and `--file`; an install while another `use` runs; `rm` racing a start; the v1.5 to v1.4 rollback guard.
- **Unit tests:** the registry (round trip, strict decode, corrupt file, Windows rename retry), the lock retry, pins and revocation, the download client (resume, idle timeout, size cap, proxy selection), `--file` matching, the migration matrix, `gc` keeping symlink targets, gates per OS, probe parsing from captured fixtures (Homebrew 11.1.3, Ubuntu 8.2.2, Debian 13, Fedora, weilnetz 11.1.0, MSYS2 11.1.3, 11.1.3-pvm1), the smoke start with a fake QEMU, entitlement parsing, install tamper cases, per-start stat detection, blocked clusters and readiness, `rm` refusals and reference counts, and in phase 2 the reconciler and profiles. Existing tests that use `Bundled`, `BrokenLiveRestore`, `has9p` or a `Server` built with a toolchain move to the new types and a fake selector. On Windows only targeted tests run; the full suite runs in Linux and macOS CI.

## Risks

- **Every Windows user moves from the weilnetz 11.1.0 snapshot to a mingw-built 11.1.3**, which may behave differently on WHPX. Phase 0 requires a multi-hour WHPX run, phase 1 keeps the CPU arguments, `pvm qemu use official` stays open, and a bad build can be revoked.
- **The first-run download is the only path after v1.4.1.** The resumable client, proxies, the explicit PAC error, `--file`, and v1.4.1 exit criteria that include throttled, proxied and offline runs.
- **Linux users jump from 8.2.2 to 11.1.3, macOS users to the pvm-qemu build**, which makes pre-1.4 RAM snapshots unrestorable; a restore overlay can also block `snapshot rm`. The refusal with recovery steps, the one-time notice, and release notes that say to restore snapshots before upgrading.
- **Changed readiness semantics could hide a real recovery problem.** Only typed `qemu.ErrUnusable` errors block a cluster, a blocked cluster gets no relays, forwards or agents, and status and doctor list the reason.
- **Supply chain of pvm-qemu.** Protected tags and main, a reviewed release environment, SHA-pinned actions, hosted runners only, GPG plus a pinned digest with tamper tests, pins only by reviewed PRs, and revocation.
- **An official binary changes under pvm** (`brew upgrade`, unattended upgrades). Per-start checks, re-gating, a log entry and a status counter; snapshots of VMs on the old binary are refused; docs give `brew pin` and `apt-mark hold`.
- **Relocated binaries fail on some hosts** (macOS `minos` from macos-15 dependencies, older Windows). The install-time gate and smoke start fail clearly, the minimum host versions are documented, and the official channel remains.
- **Old Desktop and uninstaller after a v1.3.x `pvm upgrade`.** The old Desktop's compiled-in `Find` loses the migrated slot and can show its WSL page. From v1.4.0 `pvm upgrade` replaces Desktop; a one-time notice and the notes tell v1.3.x upgraders to rerun the installer.
- **Disk use** grows with builds side by side and v1.3 leftovers (about 179 MB each on Windows). `pvm qemu ls` shows sizes; `gc` removes leftovers on request.
- **The phase 2 transition leaves a guest on an unsafe CPU and kernel-args combination.** CPU arguments follow the verified grub state, pending means `qemu64`, the VM stops in a safe state on failure, and the kill switch and opt-in stay until the soak passes.

## Unverified assumptions

- The v1.3.6 to v1.4.0 MSI upgrade with the payload at a new path and new GUIDs leaves byte-identical files, and `REINSTALLMODE=amus` works with wixl as a fallback. rc MSIs share ProductVersion 1.4.0 with the final, because `pack-setup.sh` strips the suffix, so they are tested only on fresh VM snapshots. (Phase 0 experiment.)
- Fedora's mingw64 headers provide `winhvplatform.h`, and 11.1.3-pvm1 on Windows is as stable on WHPX as weilnetz 11.1.0. (windows-smoke, then the multi-hour run.)
- A source-built, relocated, ad-hoc-signed macOS binary from macos-15 gets HVF on current macOS, and its `minos`. (A Mac.)
- Ubuntu 24.04's QEMU 8.2.2 passes the argv smoke start: stream netdev, balloon free-page reporting, `virtio-9p-pci-non-transitional`, HMP `hostfwd_add`, rng and `qemu-img convert -B`. (e2e-kvm and a real host.)
- `qemu-img convert` still accepts `-B` in 11.x: the snapshot code uses it, while the compaction code assumes it was renamed. (The smoke start and a phase 0 snapshot.)
- The v1.3.6 `install.sh` and upgrader accept the pvm-qemu archive: `VERSION` holding a tag, the extra `pvm-qemu.json` and `doc/`, the Linux wrapper, exec bits from the headers. (`upgrade-compat.yml` and `TAG=v1.4.0-rc1 sh install.sh`.)
- WHPX works inside a nested-virtualization Windows 11 VM, which decides whether clean-machine runs are possible. (The maintainer's host; otherwise a separate physical machine.)
- Hosted ubuntu-24.04 runners expose a usable `/dev/kvm`; build times fit the 120 minute timeout. (The first runs.)
- weilnetz is detectable through `HKLM\Software\qemu64` `Install_Dir` and its uninstall entry, also with `/D=`; the MSYS2 QEMU has WHPX and runs from `ucrt64\bin` outside an MSYS2 shell.
- `pvm-running.ps1` can read the command line of 64-bit QEMU from the 32-bit NSIS process, and pvm's argv always contains `<PvmHome>\vms\` with backslashes.
- `-accel/-device/-netdev help` output parses stably from 8.2 to 11.x. (Fixtures from Ubuntu 24.04, Debian 13 and Fedora.)
- Files written by Go HTTP escape Gatekeeper quarantine and Mark-of-the-Web; Defender or Smart App Control do not flag unsigned pvm-qemu DLLs or hold freshly extracted files beyond the 10 s rename retry. (Clean macOS and Windows 11 VMs.)
- QEMU release tarballs contain the `roms/` sources; the tarball's `fedora-win64-cross.docker` builds standalone; the three WHPX commits apply to v11.1.3 with zero fuzz.
- A lock-free reader of `qemu.json` on Windows can make the rename fail, and a 2 s retry is enough. (A concurrent reader test.)
- The HKCU `ProxyServer` formats seen in practice parse correctly, and PAC-only setups are rare enough.
- `gh attestation verify` works without `gh auth login` for public repositories (affects only `--unpinned` and CI); immutable releases, tag rulesets and draft-first publishing work for the new repository.
- The version of `pvm-desktop.exe` can be read from its Go build info, for the old-Desktop notice.
- A restore overlay plus an unconsumed pre-1.4 snapshot, which blocks both start and `snapshot rm`, does not occur on real hosts. (Inspect VM dirs before v1.4.0.)

## Open decisions

Each has the plan's recommendation; some list a simpler alternative.

1. **Repository.** BitterOrange/pvm-qemu, created by hand as a public repository, with the maintainer as required reviewer of the `release` environment and self-review allowed. Recommended: yes.
2. **Transitional carriage.** Until v1.6.0, re-publish the pinned archives under the old names with their source, and let the v1.4.0 installers carry the Windows build. Recommended: yes; without it every v1.3.x `pvm upgrade` gets a 404, and v1.3.6 MSI users lose QEMU on upgrade. Simpler alternative: carry the bridge only in v1.4.x, so v1.3.x users must upgrade through v1.4 and the transitional code goes in v1.5.0.
3. **MSI layout for v1.4.0.** Recommended: the payload in `msi-payload` with new GUIDs; the phase 0 experiment decides between that and `REINSTALLMODE=amus` at the old path.
4. **`pvm upgrade` replaces PVM Desktop** from v1.4.0, which reverses the packaging decision in gui.md. Recommended: yes. Simpler alternative: a separate roadmap item; then the one-time notice and the notes must tell v1.3.x upgraders to rerun the installer, since the old Desktop loses QEMU after migration.
5. **`install.sh` timing.** Master unchanged until v1.4.0 is Latest. Recommended: yes; the alternative is rc development on a release branch.
6. **Installers drop QEMU** in v1.4.1, after the clean-VM, proxy, offline and MSI criteria. Recommended: yes.
7. **Default source for fresh installs** (`PVM_QEMU` default and Desktop's recommended card). Recommended: the project build on all hosts.
8. **Minimum versions** darwin 11.1.0, windows 11.1.0, linux 8.2.0. Recommended: yes, confirmed by the smoke start on 8.2.2.
9. **Pre-1.4 RAM snapshots.** Recommended: refuse, with the rollback route, rather than bind by version.
10. **Old repacks after migration.** Recommended: leave them for `pvm qemu gc`.
11. **Installing a pinned digest without `gh`.** Recommended: yes; CI checked every pin against its attestation.
12. **Hard Constraint 1 addendum** for public key fingerprints and published digests. Recommended: yes.
13. **Windows build.** Recommended: Fedora mingw cross build, with native MSYS2 UCRT64 as the fallback.
14. **Official Windows channels.** Recommended: both weilnetz and MSYS2, with MSYS2 preferred in the docs. Simpler alternative: MSYS2 UCRT64 only at first; it has 11.1.3 stable, builds from signed source and needs no administrator rights, while weilnetz offers master snapshots with an expired certificate.
15. **Package managers.** Recommended: pvm never runs brew, apt, dnf, pacman or an installer.
16. **`pvm qemu` under the WSL backend.** Recommended: delegated like other commands, with native setup through `PVM_BACKEND=native` or Desktop.
17. **Windows proxies.** Recommended: environment variables plus the static WinINet proxy, an explicit PAC error, and `--file`.
18. **macOS.** Recommended: ad-hoc signing with only the hypervisor entitlement, and macOS 15 as the stated minimum unless dependencies are built from source.
19. **Authenticode** for pvm-qemu binaries. Recommended: none for now; revisit on Smart App Control or Defender reports.
20. **What v1.4.0 pins** if 11.2.0 ships first. Recommended: the newest stock tag that passed the phase 0 run on all three hosts, with the phase 1 WHPX arguments unchanged either way.
21. **Update cadence and revocation.** Recommended: re-pin within 14 days of an upstream stable or security release that affects the shipped configuration; revoke builds with a known security or data-loss bug.
22. **Phase 2 build.** Recommended: `11.2.0-pvm1`, with `11.1.3-pvm2` only if 11.2.0 slips past v1.5.0 readiness.
23. **AVX policy and test machines.** Recommended: opt-in in v1.5.0, default after at least 8 hours per vendor. Who provides the second-vendor WHPX host and the Mac is open.
24. **No self-hosted runner** for this work. Recommended: yes; if one is wanted later, an ephemeral just-in-time runner in a disposable VM with fork-PR approval.
25. **Uninstall keeps builds** and documents `pvm qemu rm --all -y`. Recommended: yes.
26. **The GPL gap in past releases**, below. Recommended: a licensing review before v1.4.0.
27. **When the transitional code goes.** Recommended: v1.6.0, announced in the v1.4.0 and v1.5.0 notes (or v1.5.0 with alternative 2).

## Past releases and the GPL

v1.1.x to v1.3.6 shipped repackaged QEMU runtimes without the corresponding source, and the commit of the weilnetz build cannot be found in the public repository. The plan recommends a licensing review before v1.4.0, upstream source pointers in those releases' notes, and a decision on whether to remove their standalone runtime assets; removing them breaks `--to` downgrades to those versions, which v1.4+ refuses anyway. This document is not a legal opinion.
