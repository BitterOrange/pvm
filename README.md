# pvm

English | [简体中文](README.zh-CN.md)

Personal QEMU dev VMs in a single binary: create an isolated Linux development machine with one command and route all of its traffic through your own VPS.

## Highlights

- **A dev box in one command.** Docker, common tools, Firefox and a desktop come preinstalled. After a one-time image bake, a new VM is ready in about 30 seconds.
- **Traffic leaves only through your exit.** Route a VM through your VPS over WireGuard, Cloudflare WARP, or an imported Clash/Mihomo/Surge node. If the tunnel drops, the VM goes offline instead of using your host's connection.
- **Split routing.** Send chosen domains or networks direct or through another exit; rules apply at once.
- **Checks you can run.** `pvm check dev1` measures the real exit IP on the VM's path, and `pvm route require-check` keeps a VM offline until a check passes.
- **VM programs as host windows.** `pvm app dev1 -- gedit` opens one program as its own window on your desktop; its cookies, fingerprint and traffic stay in the VM.
- **Your project folders in the VM.** Mount a folder, keep two synced copies, or upload once and download the result.
- **Made for scripts and agents.** `--json` output, `pvm capabilities`, `pvm job` and a read-only `pvm dashboard`.
- **Windows without administrator rights.** One installer brings pvm, the PVM Desktop app and the Xpra client; PVM Desktop or `pvm qemu install` then sets up QEMU.

## Installation

pvm runs on macOS on arm64, Linux on amd64, and Windows on amd64.

### Windows

1. Download `pvm_windows_amd64_setup.exe` or `pvm_windows_amd64_setup.msi` from [Releases](../../releases). To verify it first: `gh attestation verify pvm_windows_amd64_setup.exe --repo BitterOrange/pvm`.
2. Run it. No administrator rights are needed.
3. Open **PVM Desktop** from the Start menu and set up QEMU there, or open a new terminal and run `pvm qemu install`.

Both installers put `pvm.exe` and `pvm-desktop.exe` in `%LOCALAPPDATA%\pvm`, add that folder to the user `PATH`, add a Start menu shortcut and install the Xpra client. They carry no QEMU: see [QEMU](#qemu) for the two sources. Your pvm data folder is `%USERPROFILE%\.pvm`, or `PVM_HOME`.

- Stay with the installer type you started with; each refuses to run over the other. To switch, uninstall in Settings > Apps first.
- `pvm upgrade` updates only `pvm.exe`. Run the new installer to update PVM Desktop.
- Uninstalling keeps your VMs, keys, configuration and QEMU builds under `~/.pvm`. The builds are in `~/.pvm\runtime\qemu\builds`; delete them yourself if you no longer need them.
- Upgrading from v1.4.0 leaves its QEMU in place; pvm takes it over as a project build when the daemon next starts. If you upgrade from the v1.4.0 MSI without running pvm in between, that QEMU is removed with the old version and PVM Desktop asks you to set QEMU up again.

<details>
<summary>More about the Windows installers</summary>

- Files in use cannot be replaced. Interactive runs offer to close PVM Desktop and stop the daemon, which also stops running VMs; silent runs (`/S`, `msiexec /qn`) refuse, so close PVM Desktop and run `pvm daemon stop` first.
- Silent MSI install: `msiexec /i pvm_windows_amd64_setup.msi /qn`. Pass `PVMHOME=D:\pvm-home` to choose the data folder; upgrades and uninstalls reuse it.
- The last page of the `.exe` installer offers "Open PVM Desktop". The MSI has no such page.
- Uninstalling removes pvm, PVM Desktop with its shortcut and its "open at login" entry, the Xpra client and the `PATH` entry.

Known limitations:

- The prompt that offers to stop a running daemon has not been tested end to end. If it does not appear, cancel, run `pvm daemon stop`, and run setup again.
- Stopping the daemon from setup waits up to about three minutes for VMs to shut down, without showing progress.
- Setup cannot stop VMs left running without a daemon. It lists them and refuses; shut them down first.
- Antivirus software can briefly lock new files. Setup retries for 10 seconds, then stops and keeps the previous version.
- After `pvm upgrade`, Settings > Apps still shows the installer's version.

</details>

Windows has two backends, set with `PVM_BACKEND` or `backend` in `~/.pvm/config.yaml`:

- `native`: Windows QEMU with WHPX. No Windows QEMU has virtio-9p, so `/share` is off; use [workspaces](#workspaces) for host folders.
- `wsl`: commands go to `pvm` inside WSL2, with `/share`, extra mounts and KVM.
- `auto` (default): native when a native daemon runs or a native QEMU is set up, else WSL2 when pvm is installed there, else native.

Acceleration, DNS and WSL2 settings are in [SKILL.md](SKILL.md).

### macOS and Linux

Install GitHub CLI (`gh`), then:

```bash
curl -fsSL https://raw.githubusercontent.com/BitterOrange/pvm/master/scripts/install.sh | sh
```

The script installs `pvm` to `~/.local/bin` (`INSTALL_DIR` changes it). It checks the checksum and the GitHub build attestation before replacing anything, and keeps the previous version. Then it sets up QEMU as `PVM_QEMU` says: `project` (the default) runs `pvm qemu install -y`, `official` runs `pvm qemu use official -y` with the QEMU you installed from Homebrew or your distribution, and `none` only prints those commands. If QEMU setup fails, pvm stays installed and the script exits non-zero. It installs v1.4.0 and later; for an older `TAG`, it prints the URL of that version's own script.

### Quickstart

The daemon starts on demand. The first `create` downloads and bakes an image, which takes about ten minutes once.

```bash
pvm create --image ubuntu:26.04 --name dev1 --user dev
pvm ssh dev1
pvm desktop dev1
pvm map dev1 8080:80
pvm exit add-warp my-warp
pvm route dev1 --via my-warp
```

`pvm list` shows each VM as `stopped`, `starting` (with its stage), `ready` or `failed`. `pvm image default ubuntu:26.04` lets later creates leave out `--image`.

## Features

### Exit routing that fails closed

Register an exit and route the whole VM through it. TCP, UDP and ICMP go through WireGuard over UDP or TCP. If the tunnel goes down, traffic is dropped, never sent through the host.

```bash
pvm exit add my-exit --server vps.example.com:18443 --token YOUR_TOKEN   # your own VPS
pvm exit add-warp my-warp                                                # Cloudflare WARP, nothing to deploy
pvm exit import my-sub --file clash.yaml --select NODE                   # a Clash/Mihomo/Surge node
pvm route dev1 --via my-exit
pvm route default --via my-exit                       # every VM without its own route
pvm route add direct --domain-suffix example.com      # split routing
pvm route ls
```

Only explicit `direct` and `internal` rules bypass the exit. Direct-routed VMs cannot reach the host LAN unless a rule allows it. To run your own exit, see [Deploy an exit on a VPS](#deploy-an-exit-on-a-vps).

### Checks and automation

- `pvm check dev1` samples the public exit IP on both the VM and gateway paths and cross-checks blocked probes against the gateway's counters.
- `pvm route require-check dev1` keeps a VM offline until a check passes, then holds it to a 90-second lease that the daemon renews; if the daemon goes away, the VM goes offline. `pvm exit set my-exit --ip-mode static` cuts an exit whose IP or region changes.
- `pvm route pause` and `resume` cut and restore a VM's external traffic.
- Read-only commands take `--json`; `pvm capabilities` describes the contract offline; `pvm job` follows long operations.
- `pvm dashboard` serves a read-only status page on loopback; `pvm diag` writes a redacted diagnostic zip.
- `pvm audit dev1 --privacy`, `pvm doctor` and `pvm route explain dev1 example.com` inspect the local setup without network probes.

### Apps as host windows

`pvm app` shows single programs or web pages from a VM as host windows through [Xpra](https://xpra.org). The host needs the Xpra 6.5 client, which the Windows installers include.

```bash
pvm app dev1 -- gedit
pvm app dev1 https://mail.example.com --kiosk
pvm app config dev1 --clipboard both --drop on --audio both
pvm app install dev1 claude-code codex claude-desktop
pvm app ls
pvm app stop dev1 --all
```

- **Clipboard and drops** are off until enabled per VM. Host content enters the VM only while one of its windows is in front, and never content that password managers mark as excluded.
- **Input methods**: text from a host input method, such as Microsoft Pinyin or the emoji panel, reaches the app.
- **Sound** plays on the host; `--audio both` also lends the VM the microphone.
- **Speed**: `--render-cpus N` limits the renderers of Chromium and Electron apps (2 by default), and `--scale 1.25|1.5|2` draws fewer pixels in the VM.
- **Keyring**: `--keyring host` encrypts the VM's login keyring with a password kept on the host, so apps such as Claude Desktop stay signed in. The default, `off`, leaves it to the VM.
- **Catalog**: `pvm app catalog` lists Claude Code, Codex CLI, Claude Desktop and Firefox. `pvm app install` runs a built-in script in the VM; downloads leave through the VM's exit.

Changes made with `pvm app config` apply to open windows at once. See [docs/design/app.md](docs/design/app.md).

### Browser desktop

`pvm desktop dev1` opens the VM's XFCE desktop in a browser tab through noVNC, on a loopback URL. `--public` listens on all interfaces and still requires the VNC password.

### Workspaces

A workspace binds a host folder to one VM at `/workspace/<id>`:

- `mount`: one shared copy (9p, or reverse sshfs on native Windows). File watchers need polling, such as `CHOKIDAR_USEPOLLING=1`.
- `sync`: a copy on each side; `push` and `pull` move changes and stop on conflicts.
- `copy`: upload once, then download the result with `clone`.

```bash
cd ~/src/web
pvm workspace create web dev1 --mode sync
pvm ssh 'npm install && npm test'
pvm workspace pull web
```

Uploads skip `.gitignore` matches and `node_modules`; `.pvmignore` adds rules. pvm refuses to share your home folder, a file system root, its own data folder, or folders with credentials such as `.ssh`, `.aws` or `.env` (`pvm mount --allow-sensitive` overrides this). `pvm workspace rm` never deletes host files. See [docs/design/workspace.md](docs/design/workspace.md).

**The VM name can be left out.** Inside a workspace folder, `pvm ssh`, `pvm exec`, `pvm app` and `pvm map` use the workspace's VM and start in the matching folder. Elsewhere they use the VM set with `pvm default-vm dev1`, else the only VM. At a terminal, pvm asks before starting a stopped VM.

### PVM Desktop

A window for the common tasks, on Windows with the native backend. Open it from the Start menu.

- **VMs**: state and startup stage; start, stop, delete, resize; open a terminal, an app or the file manager; install catalog apps.
- **Create**: a new VM with its size, exit, app settings and apps to preinstall.
- **Exits**, **Workspaces**, **QEMU** and **Settings** (default VM, start at login, English or Simplified Chinese).

Every change runs `pvm.exe`, so the app can do nothing the CLI cannot. It needs the Microsoft Edge WebView2 Runtime, which Windows 10 and 11 normally have. It has not yet been tested end to end from the installers. See [docs/design/gui.md](docs/design/gui.md).

### Architecture

The host `pvm` binary is the CLI and the daemon, and also runs as the VPS exit agent. VMs run a separate small guest agent. A managed gateway VM provides DNS, routing, the WireGuard tunnels and the kill switch.

```text
Control: pvm CLI -> pvm daemon -> gateway VM and dev VMs
Data:    dev VM -> gateway VM -> WireGuard tunnel -> exit on VPS, Cloudflare WARP or Mihomo node -> Internet
```

## QEMU

pvm runs QEMU only from the build you select with `pvm qemu`, by absolute path. It never searches `PATH` and never switches builds on its own. There are two sources:

- **Project build**: built from QEMU's signed source by [pvm-qemu](https://github.com/BitterOrange/pvm-qemu), with the complete source in each release. pvm installs it only when its SHA-256 matches a digest pinned in the pvm release. These are not official QEMU releases.
- **Official build**: your platform's own QEMU: Homebrew on macOS, your distribution's packages on Linux (8.2 or newer), or MSYS2 UCRT64 on Windows.

```bash
pvm qemu                    # builds, the default and the recommended pin
pvm qemu install -y         # the recommended project build
pvm qemu use official       # Homebrew, your distribution or MSYS2
pvm qemu check --default    # exit 0 ok, 3 not set up, 1 failing
pvm qemu gc -y              # remove folders left by older versions
```

Every build must pass version, accelerator, device, test-start and firmware checks before first use and after its binary changes; a project build whose files changed is refused. RAM snapshots restore only into the build that wrote them. The daemon runs without QEMU and picks up a new build without a restart.

## Supported images and hosts

- Images: `ubuntu:26.04`, `ubuntu:24.04`, `debian:13`, `debian:12`.
- Hosts: macOS arm64, Linux amd64, Windows amd64 (native WHPX or WSL2).
- VMs use the host's architecture; there is no cross-architecture emulation. Linux arm64 builds are for VPS exits only.

## Deploy an exit on a VPS

The VPS runs `pvm-exit`, a server-only binary. One command on your host installs it over SSH, starts it and adds the exit:

```bash
pvm exit deploy my-exit --ssh root@vps.example.com
```

SSH authenticates the VPS, so the certificate fingerprint comes with a one-time invite and no token is copied by hand. With a sudo user the system service is installed; `--user-mode` installs a systemd user service with userspace NAT and no root at all; it needs lingering for that user (`sudo loginctl enable-linger USER`, once, by an administrator), or the service stops at logout.

The system service runs as the `pvm-exit` user, with `CAP_NET_ADMIN` and `CAP_NET_RAW` for kernel NAT and no capabilities for userspace NAT. Its settings are in `/etc/pvm-exit/config.yaml`. To set up the VPS yourself instead, use the `.deb` from the release (`sudo apt install ./pvm-exit_<version>_amd64.deb`) or `scripts/install-exit.sh` (it checks the release attestation with `gh`, which needs no GitHub login), then create an invite there:

```bash
sudo pvm-exit invite laptop        # prints: pvm exit add my-exit --invite pvmx1:...
```

An invite works once and expires after 15 minutes. It gives the host its own token, which can register peers but not manage the server; `pvm-exit token ls` and `pvm-exit token rm <id>` list and revoke them, and `pvm exit rm` revokes the host's own token once it has no peer left. A VPS running `pvm exit-agent install` moves over with `pvm-exit init --migrate` (or `pvm exit deploy ... --migrate`), keeping its token, fingerprint and peers; `pvm exit-agent` is removed in v1.5.0. [SKILL.md](SKILL.md#standard-flow-deploy-an-exit-over-ssh) has the details.

The VPS firewall must accept TCP 18443 (control), UDP 18443 (WireGuard over UDP) and TCP 18444 (WireGuard over TCP), and must forward traffic from the `pvmexit0` and `pvmexit1` devices. ufw's default `deny (routed)` blocks that forwarding; allow it, with `eth0` replaced by the internet-facing interface:

```bash
ufw route allow in on pvmexit0 out on eth0
ufw route allow in on pvmexit1 out on eth0
```

For a phone or laptop with a standard WireGuard client, `pvm exit client-config my-exit --output phone.conf` exports a profile and shows a QR code. Use one profile per device. It is a plain IPv4 full tunnel, without pvm's DNS or split routing.

## Upgrade

`pvm upgrade` verifies the release, checks that the new version accepts your QEMU, saves a recovery point, replaces the binary, restarts the daemon and starts the VMs that were running. It never changes QEMU or PVM Desktop. `pvm upgrade --rollback` goes back.

Update checks are off by default; set `update_check: true` in `~/.pvm/config.yaml` to turn them on.

## Roadmap

Recent releases (all versions are in [docs/release-notes](docs/release-notes)):

- **v1.4.1**: upstream v1.3.0-1.3.1 merged, bringing Mihomo exits, `pvm check`, `pvm diag`, `pvm dashboard`, `--json`, `pvm job`, `pvm ssh-config`, `pvm-exit` with `pvm exit deploy` and invites, `pvm image default`, VMs restarted after a daemon restart, the memory balloon, DoH failover, and the clipboard bridge on macOS and Linux. A route switch sets the timezone of the exit's public IP. The installers and `install.sh` no longer carry QEMU: PVM Desktop, `pvm qemu install` or `PVM_QEMU` sets it up. The installers, `pvm upgrade` and `pvm exit deploy` verify release attestations with `gh` but need no GitHub login. Tested end to end on Windows (WHPX), Linux (KVM) and an M1 Mac, with the project QEMU build and the official ones.
- **v1.4.0**: `pvm qemu`, with pinned pvm-qemu builds or the platform's official QEMU; the gateway boots on M1 Macs.
- **v1.3.x**: PVM Desktop, the app catalog, the keyring setting, window scale and input methods in `pvm app`.

Next:

- v1.4.2: Mihomo exits from a subscription link, with manual and automatic updates that never switch the selected node
- WHPX: AVX in VMs, and a QEMU chosen per VM
- PVM Desktop: tested from the installers, updating itself, a tray icon, and screens for snapshots, port maps and mounts
- `pvm desktop` as a host window, like `pvm app`
- `pvm app` on WSL2
- Windows CI for the remaining packages, and an installer smoke test
- v1.5.0: `pvm exit-agent` removed in favour of `pvm-exit`

Details and candidates are in [docs/roadmap.md](docs/roadmap.md).

## Documentation

- [SKILL.md](SKILL.md): the full manual for people and agents; `pvm skill` prints it.
- [SECURITY.md](SECURITY.md) ([简体中文](SECURITY.zh-CN.md)): trust boundaries, credentials, images, QEMU and network checks; `pvm security` prints the English version.
- [AGENTS.md](AGENTS.md): repository map, build and test workflow, and development rules.

## Development

Builds use Go 1.27.4 and garble v0.18.0, both pinned in CI.

```bash
go install mvdan.cc/garble@v0.18.0
./script/build.sh
go test ./...
```

- On Windows, install Git for Windows, Go and garble, then run `.\script\build.ps1` from PowerShell; it writes `pvm.exe`.
- `./script/build-desktop.sh` builds `pvm-desktop.exe` and needs Node.js 22. `go build ./...` and `go test ./...` do not.
- Do not run the full `go test ./...` on Windows: the suite is written for Unix and many tests fail there for unrelated reasons. Run targeted tests, such as `go test ./internal/qemu -run Accel`, and the full suite on Linux, macOS or WSL2.
- No build includes QEMU. CI checks the pinned pvm-qemu builds against their releases and attestations.

## License

pvm is released under the [MIT License](LICENSE). Third-party licenses are listed in [THIRD_PARTY_LICENSES](THIRD_PARTY_LICENSES).
