---
name: pvm
description: Create and manage isolated Linux dev VMs with pvm, and route all of a VM's (or the whole cluster's) traffic through a VPS exit-node. Use when you need a clean/disposable dev machine, separate guest filesystems and controlled egress, an environment that exits only via a specific VPS, a browser desktop, port maps, snapshots, or clones.
---

# pvm: isolated dev VMs and egress control

pvm is a QEMU virtual machine system for the local machine (single Go binary). A daemon runs permanently; the CLI auto-spawns it when needed (exception: after an explicit `pvm daemon stop` it stays down until `pvm daemon start`). Everything goes through the `pvm` command — never run or touch QEMU directly; manage the QEMU pvm runs only with `pvm qemu` (see QEMU builds below).

For security boundaries, credential handling, isolation risks, and verification, run `pvm security`.

## Agent and script contract

- Run `pvm capabilities` first (offline, zero side effects) to inspect the protocol version, supported `--json` commands, platform features and exit codes (`0` success, `1` operation failed, `2` invalid input, `3` user action required, `4` wait timeout).
- Read-only commands (`list`, `daemon status`, `mount ls`, `map ls`, `route ls`, `route explain`, `image list`, `workspace ls`, `workspace status`, `exit ls`, `doctor`, `audit`, `check`, `diag`, `job`, `ssh-config`, `default`) accept `--json`; human progress goes to stderr so stdout is always valid JSON. Errors carry `{code, message, next_action, retryable}`.
- Async provisioning returns a job handle: inspect with `pvm job ls [--vm NAME] --json` or wait with `pvm job <id> --wait [duration]`. On `wait_timeout` (exit 4), query the same job ID — never blindly resubmit a mutation whose result is uncertain.
- `pvm default [<vm>|--clear]` and `pvm image default [<distro:version>|--clear]` manage host defaults in `~/.pvm/defaults.json`. With a default (or a single user VM), `pvm ssh` / `pvm ssh <one-word-cmd>` / `pvm ssh -- <cmd...>` omit the VM name; `pvm create <name> --user dev` omits `--name` and uses the default image. Destructive commands (`rm`, `stop`, `compact`, `snapshot rm`) always require an explicit VM name.
- `pvm dashboard [--open]` serves a local read-only status page (strict Host check, random URL-fragment token, strict CSP, no mutation endpoints).
- `pvm ssh-config <vm>` prints an OpenSSH config entry (with a gateway `ProxyCommand`) for VS Code Remote-SSH or rsync; it never writes to `~/.ssh`.

## Architecture boundary

pvm supports only the host's native architecture: darwin/arm64 runs arm64 VMs, linux/amd64 and windows/amd64 run amd64 VMs. On Windows, pvm supports both delegating to a WSL2 Linux pvm instance (`PVM_BACKEND=wsl`) and running native Windows QEMU with WHPX (`PVM_BACKEND=native`, or `auto`). Build artifacts contain guest binaries for the current host architecture only — no cross-arch emulation or packages — and no QEMU: pvm runs the QEMU build set up with `pvm qemu`.

### Windows hosts

- `PVM_BACKEND=wsl` delegates commands to `pvm` in WSL2 and is the recommended mode for full `/share`, extra mounts, and KVM where available. `pvm mount` converts a Windows source such as `C:\work\proj` to `/mnt/c/work/proj` before delegation.
- `PVM_BACKEND=native` runs a Windows daemon on loopback TCP. All daemon APIs except `/v1/health` require the bearer token stored in `~/.pvm/run/admin.token`. `pvm daemon install` uses a Scheduled Task.
- No QEMU for Windows provides virtio-9p (neither the pvm-qemu build nor MSYS2's); `pvm qemu ls` shows it per build. `create` therefore uses `--share none` and prints a warning; explicitly requesting `private` or `global` fails with guidance to use WSL2. Use WSL2 when host/guest file sharing is required.
- Native mode uses WHPX only when Windows Hypervisor Platform is enabled and its probe succeeds. Otherwise it uses TCG and reports the fallback. `PVM_QEMU_ACCEL=whpx` forces a WHPX attempt; `PVM_QEMU_ACCEL=tcg` forces emulation. It applies only to a build that passed its checks, and `pvm daemon status` marks it as forced. When `create`, `start`, `clone`, or `image build` detects TCG, pvm prints backend-specific acceleration guidance. To enable WSL2 nested virtualization, set `nestedVirtualization=true` under `[wsl2]` in `%UserProfile%\.wslconfig`, run `wsl --shutdown`, restart WSL, and verify `/dev/kvm`. Explicitly setting `PVM_QEMU_ACCEL=tcg` suppresses the warning. Native Windows resolves through DNS-over-TCP to avoid Windows QEMU UDP DNS failures. Set `PVM_DNS_UPSTREAM` to override the upstream (for example `tcp://192.0.2.53:53`); Linux and WSL2 retain the default slirp resolver.
- `PVM_BACKEND=auto` is the default and chooses native when a native daemon is healthy or a native QEMU build is set up (or one from an older pvm waits to be taken over), then WSL2 if pvm is installed there, else native. A detected but unregistered MSYS2 QEMU does not count. To set up native QEMU while auto picks WSL2, run `PVM_BACKEND=native pvm qemu install` (or `PVM_BACKEND=native pvm qemu use official`); `pvm qemu` otherwise follows the backend like every VM command. Run `pvm doctor` to inspect backend, OpenSSH, QEMU, WSL2, and acceleration readiness.

## Prerequisites

- If pvm is on PATH, use `pvm ...`; inside the repo, use `./pvm ...` from the repo root.
- Run `pvm daemon status` first to confirm the daemon is healthy; `pvm list` shows existing VMs.
  - If the status shows `warning: N vm(s) running under tcg fallback`, the host's hardware accelerator is unavailable (on macOS this means Hypervisor.framework is wedged after long uptime — reboot the Mac to restore full speed). VMs work meanwhile, just slowly.
- On first use, `pvm image list` shows cached base and golden images. `pvm create` can fetch a missing base and build a missing golden; this first run is much slower. Use `pvm image fetch <distro:version>` and `pvm image build <distro:version>` to prepare them explicitly. Fetch rechecks the distribution checksum; `--force` re-downloads. `--keyring /trusted/distribution.gpg` requires `gpgv` and verifies signed checksums with only that explicitly trusted keyring, including cached images.
- Example exits: `my-exit` (WireGuard over UDP) and `my-exit-tcp` (WireGuard over TCP). Run `pvm exit ls` to see the names configured on the current host and do not re-add an existing name.
- The `pvm-exit.service` on the VPS (`pvm-exit-agent.service` on older deployments) must stay running. Never stop it unless you are deliberately testing the kill-switch or upgrading.

## Common choices

- **Images**: `ubuntu:26.04`, `ubuntu:24.04`, `debian:13`, `debian:12`
- **Exit providers**: pvm-exit (`udp` default / `tcp` fallback) or Cloudflare WARP (`udp` only); see `pvm exit add-warp` below
- **pvm-exit NAT backend**: `auto` (probe) / `nft` / `iptables` / `userspace` (no root; ICMP requires ping-socket permission)
- **Route targets**: `direct` (host egress) or an exit name such as `my-exit`
- **create flags**: `--image`, `--distro`, `--version`, `--name`, `--user`, `--cpus`, `--mem`, `--disk`, `--tz`, `--locale`, `--git-email`, `--git-name`, `--smbios-spoof`, `--share private|global|none`, `--no-start`, `--no-wait`

### Directory mounts (pvm mount and /share)

- New VMs mount their private `~/.pvm/vms/<name>/share` at `/share` (9p, bidirectional read-write). `--share none` disables it; `--share global` explicitly selects `~/.pvm/share`. Existing VMs keep their recorded behavior. Bake and gateway VMs do not expose host shares. `/share` uses the `coherent` profile.
- Manage extra mounts dynamically on a running VM; no reboot or recreate is needed:
  ```bash
  pvm mount dev1 ~/work/proj:/home/dev/proj
  pvm mount dev1 --profile readahead ~/work/data:/home/dev/data
  pvm mount ls dev1
  pvm mount rm dev1 /home/dev/proj
  ```
- `pvm mount` hot-plugs the 9p device, mounts it in the guest, and persists the configuration and profile in the guest's fstab and VM spec. `pvm mount rm` unmounts and detaches it without deleting host files. Read-write add and remove require the VM to be running and SSH-ready; `--read-only` mounts enforce QEMU backend restrictions and require the VM stopped for add/remove, then apply at startup; up to 13 extra mounts are supported. A VM started by an older pvm version needs one restart to reserve the dynamic slots. Clones inherit mount profiles, and `pvm mount ls` displays them.
- Mounts are virtio-9p. Good for source trees and documents; not for high-IOPS database directories. Extra mounts are managed only by `pvm mount`, not at create time. Like workspaces, `pvm mount` refuses the home directory, a filesystem root, `~/.pvm`, and folders containing credentials (`.ssh`, `.aws`, `.gitconfig`, `.npmrc`, `.env` / `.env.*` other than `.example`/`.sample`/`.template`/`.dist`, `*.pem`, `*.p12`, `*.pfx`, SSH private key names). Do not bypass this guard unless the user explicitly asks; `--allow-sensitive` overrides it and prints a warning.
- **Mount profiles**:
  - `coherent` is the default for `/share`, new extra mounts, and old specs with no profile. It uses `cache=none,msize=262144` and is intended for files actively read and written on both sides.
  - `readahead` uses `cache=readahead` and requests `msize=1048576`; QEMU and the guest negotiate the actual value, which can be smaller. It targets large sequential reads, not small-file create/stat or other metadata bottlenecks.
- Neither profile removes the metadata cost of source-tree builds. Do not claim a pnpm, Vite, or TypeScript build speedup without measuring it; keep Node/TypeScript dependencies and build caches on the guest's local disk.
- Only `coherent` and `readahead` are accepted; `loose`, `fscache`, and arbitrary mount options are rejected. To switch an extra mount's profile, close guest users of the mount, then use the normal `pvm mount rm` and `pvm mount --profile ...` path. Upgrading pvm does not rewrite existing cloud-init, automatically migrate profiles, force an unmount, or restart the VM.
- `--image debian:13` and `--distro debian --version 13` are equivalent; conflicting flags are rejected.
- **`pvm image build` proxy passthrough**: golden builds honor the host's `HTTP_PROXY/HTTPS_PROXY/ALL_PROXY/NO_PROXY` (loopback addresses are rewritten for the build VM). Bake uses host/slirp egress, independently of the final VM's exit route. Concurrent downloads/builds of the same base are rejected with a retry hint, and golden builds are serialized. Abandoned build VMs are preserved for explicit inspection and removal.

## Core workflows

### 1. Create and enter a dev machine

```bash
pvm create --image ubuntu:24.04 --name dev1 --user dev
# alias form: pvm create --distro debian --version 13 --name d1 --user dev
pvm list
pvm ssh dev1 'whoami && ls /share'
```

- Always pass `--user` explicitly. Interactive create prompts when it is omitted; non-interactive create rejects a missing `--user` instead of silently choosing one.
- `pvm list` (or `pvm ls`) shows each VM's STATUS: `stopped`, `starting (<stage>)` while the VM boots and provisions (stages are `cluster`, `disk+seed`, `boot`, `provision`, `ssh`, `agent`), `ready` once SSH, provisioning and the guest agent are up, or `failed`, with the hint printed on stderr below the table as `pvm: dev1 failed: <hint>`. A plain `starting` is a running VM the daemon has not probed yet, as after a daemon restart. Gateway VMs have no readiness stages and show `running`. Wait for `ready`, not for a running process, before using a VM.
- create follows progress by default (image, disk, boot, cloud-init and agent readiness); `--no-wait` returns immediately.
- Golden images come with Docker, common dev tools, Firefox (from Mozilla's APT repository, not the snap), sshfs, and a native XFCE desktop (TigerVNC + noVNC) preinstalled. Images baked before Firefox was added do not have it; rebake with `pvm image build`. User VMs get a virtio-gpu display device by default (`/dev/dri` in the guest); gateway and build VMs stay headless.
- New VMs use per-VM pvm management keypairs, no password; existing VMs retain their original key. `pvm ssh dev1` opens an interactive shell; when both local stdin/stdout are terminals, `pvm ssh dev1 codex` and other interactive commands also get a TTY automatically. Use `pvm ssh -t dev1 <cmd>` to force one or `-T` to disable it. SSH remote commands run through the user's login shell, loading `~/.profile`, Bash login files, or `~/.zprofile`; TTY commands also request interactive mode so `.bashrc`/`.zshrc` follow the shell's normal startup rules. When SSH is unreachable the virtio-serial agent channel takes over automatically (or use `pvm exec dev1 <cmd...>` explicitly - it needs no network).
- create/start pushes the VM's IP/MAC mapping to the gateway DHCP **synchronously** before booting and deploys the gateway agent before the user VM starts (new VMs get a DHCP offer on their first request); if the gateway is unreachable, start fails instead of booting a network-less VM.
- Cluster addressing: VMs live in `100.99.<cluster>.0/24` (CGNAT space), the gateway is `100.99.<cluster>.1`.

#### Default VM and starting on demand

- `ssh`, `exec`, `start`, `logs`, `audit`, `desktop`, `app`, `map`, `mount` and `workspace create` may leave the VM name out. pvm then uses the VM of the workspace whose host folder contains the current directory (the innermost one), else the stored default VM, else the only user VM, and prints `pvm: using vm dev1 (workspace web)`, `pvm: using the default vm, dev1` or `pvm: using the only vm, dev1` on stderr. With several VMs, no workspace and no default VM it fails and lists the names. A workspace whose VM was deleted is an error, not a fallback to another VM.
- `pvm default-vm dev1` stores the default VM as `default_vm` in `~/.pvm/config.yaml` (the VM must exist and not be a gateway), `pvm default-vm` prints it and `pvm default-vm --clear` removes it. A default VM that no longer exists is an error naming it, not a fallback to the only VM; `pvm rm` of the default VM clears the setting and says so.
- A first word that names an existing VM is the VM; anything else is the command, so `pvm ssh ls` runs `ls` in the default VM. `pvm ssh -- dev1` runs a command called `dev1`.
- `stop`, `rm`, `compact`, `resize`, `snapshot`, `clone` and `route` always need the name. As an agent, pass the name explicitly anyway; the default exists for people at a terminal.
- Inside a workspace, `pvm ssh` starts in the matching guest directory: `~/src/web/api` in workspace `web` becomes `/workspace/web/api`, also with an explicit name of the workspace's own VM. A login falls back to the workspace root when the folder is not in the VM yet (a sync workspace before `push`); a command fails instead of running elsewhere. A command started with `pvm app` starts in the same directory and falls back to the workspace root the same way, printing `pvm: /workspace/web/api is not in the vm; starting in /workspace/web` on stderr; it fails when the workspace root is not in the VM either. Web apps share one Firefox and do not get a directory. `pvm exec` and the agent fallback of `pvm ssh` run as root from `/`: root cannot enter the login user's reverse sshfs mount.
- At a terminal pvm asks `[Y/n]` before starting a stopped daemon or VM, then follows provisioning like `pvm start`; `--yes` (`-y` for `ssh` and `workspace create`) starts the VM without asking. Without a terminal the daemon still starts on its own, but a stopped VM fails with the `pvm start` hint, so scripts never start machines by surprise. After `pvm daemon stop` the daemon is not started until `pvm daemon start`.

### 2. Route all of a VM's traffic through the VPS

```bash
pvm exit ls
pvm route dev1 --via my-exit     # UDP preferred
pvm route dev1 --via my-exit-tcp # TCP fallback
pvm audit dev1
pvm exit test my-exit
```

- The region profile switches the VM's timezone/locale to the explicitly configured exit profile or its local-system timezone suggestion (`en_US.UTF-8`, applied over SSH). For a VM that is stopped when its route changes, the profile is applied by its next `pvm start`, before the VM is ready; if that fails, the start fails with the reason and the following start retries. `pvm audit` is read-only: it reports route, TCP, UDP, ICMP, DNS, IPv6, timezone, and locale without starting/stopping the VM or blocking startup.
- Restore host egress: `pvm route dev1 --via direct`.
- **Fail-closed**: when the exit tunnel is down, VMs bound to it lose all networking. Networking recovers when the VPS service is back.
- Switching `--via` stops the VM first, applies the new route, then starts it again if it was running. A VM that was already stopped is not started automatically; when no gateway is running either, the route is saved and applied at the VM's next start, and `pvm route` prints `route saved` instead of `config pushed`.
- The gateway reads actual policy rules and nft state every 10s, repairs missing or incorrect rules, and probes TCP, UDP, and ICMP through the selected exit. Route-only drift is repaired without rebuilding the tunnel. Two failed data-plane checks (~20s) rebuild the tunnel, during which bound VMs stay offline (expected).
- Gateway peer keys persist in the gateway VM, so daemon/gateway restarts re-enroll as the same peer instead of leaking stale registrations. TCP exits use keepalives and bounded writes to recover from half-open connections.
- Default-exit domains resolve only through the selected exit (on the VPS or through the WARP tunnel), return real IPv4 addresses, and use direct L3 tunnel forwarding. Only explicit domain proxy rules use fake-ip relays. `pvm exit test` uses the exit-agent's own TCP, UDP, and ICMP probes; WARP is handshake-only. No third-party STUN or probe service is required.
- TCP, UDP, and ICMP follow the selected exit for both real-IP flows and explicit fake-ip split rules. Only explicit direct/internal rules use the direct path. Unsupported packets and flows with no live tunnel or resolver are dropped; userspace NAT supports TCP, UDP, and ICMP, not arbitrary IP protocols.
- Direct egress (`--via direct`, direct-domain rules and direct VMs) stays off the host LAN, slirp host loopback (`10.0.2.2`), CGNAT, link-local and reserved ranges. Only explicit `internal` domain rules and explicit `direct --cidr` rules may open private destinations; add a narrow `pvm route add direct --cidr <subnet>` when a VM must reach a LAN service.
- Exit-routed traffic is held to the same ranges at the gateway, whatever the exit; reaching a private network behind an exit needs `pvm route add proxy --cidr <subnet> --via <exit>` plus `domain_allow_cidr` in that exit server's config.

### 3. Cluster-wide default route

```bash
pvm route default --via my-exit-tcp # every VM without an explicit route uses this exit
pvm route default --via direct      # back to host egress for unmatched VMs
pvm route ls                     # shows rules, per-VM routes, and the cluster default
```

- Priority: built-in internal suffixes > explicit domain rules > explicit CIDR rules > per-VM default > cluster default > direct.
- A VM with an explicit `--via direct` uses host egress for unmatched traffic even when a cluster default exit is set; explicit rules still take precedence.
- Same fail-closed semantics as per-VM routes: if the default tunnel dies, every VM following it goes offline.

### 4. Browser desktop (native XFCE)

```bash
pvm desktop dev1
# prints http://127.0.0.1:<port>/vnc.html#password=... — open in a browser, no login page
pvm desktop dev1 --public
# optional: listen on 0.0.0.0 so another device can use http://<host-ip>:<port>/vnc.html#password=...
pvm desktop dev1 --stop
```

- Requires a VM built from a **golden image** (XFCE + TigerVNC + noVNC preinstalled). Base images lack the desktop packages and `pvm desktop` says so (a 503 means the VM is still booting — retry shortly).
- This is the VM's own desktop (not a container): the filesystem, users, and processes are the same machine you see over SSH, including the `/share` mount.
- The session persists inside the VM (closing the browser changes nothing); timezone/locale follow the VM's region profile. `--open` opens the system browser. The desktop follows the browser window size; `--size 2560x1440` sets the size it starts with.
- On VMs with `pvm.stream`, the desktop is not a port map and does not show in `pvm map ls`: the daemon's listener carries it over the channel, bypassing the gateway VM. Desktop maps created by older versions are removed the next time `pvm desktop` runs. VMs without the port keep their existing gateway map/relay and migrate automatically after a restart; app and other stream-only features need that restart first.
- `--public` makes the daemon's listener listen on `0.0.0.0` and returns an `0.0.0.0` URL; use the host's actual LAN/VPN IP when connecting remotely. Switching between public and loopback keeps the same desktop session.
- Clipboard is enabled by default in noVNC. Open the Clipboard panel on the left and grant clipboard read/write permission in Chrome when prompted; loopback URLs are considered secure contexts. Golden images include Noto fonts for CJK and the other major scripts, color emoji, and an IBus pinyin input method, with `ibus-daemon` started in each desktop session. This only adds display support and Chinese input; the default locale remains `en_US.UTF-8`.

### 5. Single apps as host windows (pvm app)

```bash
pvm app dev1 -- gedit                       # an X11 program as its own host window
pvm app dev1 https://mail.example.com       # a web page in the VM's Firefox as a window; --kiosk for full screen
pvm app dev1 --name notes --no-attach -- gedit notes.txt
pvm app ls [dev1]
pvm app attach dev1                         # reopen the windows after closing the Xpra client
pvm app stop dev1 notes                     # or --all
pvm app config dev1 --clipboard both --drop on --audio both
pvm app catalog                             # built-in apps: ID NAME SOURCE WINDOW DESCRIPTION
pvm app install dev1 claude-code codex      # install catalog apps in the VM
```

- Uses Xpra: one Xpra server per VM runs as the VM user, and each app is a window on it. The page or program runs in the VM, so cookies, fingerprint and egress stay with the VM and its route; the host only shows pixels.
- The host needs the Xpra 6.5.x client from xpra.org; the Windows installers include it. On Windows pvm looks in `xpra\Xpra.exe` next to `pvm.exe` and in `Program Files\Xpra`, on macOS in `/Applications/Xpra.app`, elsewhere on `PATH`; `PVM_XPRA` overrides the path. A client from another release line is refused. The first app in a VM without Xpra installs it from xpra.org's APT repository, which takes a few minutes and needs the VM's network; golden images built from this version on include it.
- Type with the host's own input method, such as Microsoft Pinyin or the Windows emoji panel: committed text, emoji included, reaches the app. On Windows hosts the candidates open at the app's text cursor.
- Sound plays on the host's default output device. `--audio both` also records the host's default microphone into the VM while a client is attached, and on Windows hosts switches to a new default, such as a headset plugged in; `--audio off` silences the VM. The default is `out`. While the VM plays nothing, no sound is sent, which saves CPU in the VM; `--audio-silence send` sends it anyway.
- The renderers of Chromium and Electron apps run on vCPUs 0-1; the browser process and what the app runs, such as Claude Code in Claude Desktop, keep every vCPU. `--render-cpus N` changes it for apps started afterwards; `0` lifts the limit.
- `--keyring host` encrypts the VM's login keyring with a password kept on the host (`keyring.secret` in the VM's app directory) and unlocks it before apps start, so apps such as Claude Desktop stay signed in across app, VM and daemon restarts. The default, `off`, leaves the keyring alone, and apps may ask for its password. A keyring with a password pvm did not set makes `pvm app` refuse to start apps; use `--keyring off` to keep it. Switching from `host` to `off` signs such apps out once the VM restarts.
- `--scale 1.25|1.5|2` has the VM draw its windows at fewer pixels and the host client enlarge them, 1 by default: faster drawing, softer text of the same size. Open windows keep their size on the host.
- The first app in a VM also installs what came after its Xpra: the audio server, `gtk3-nocsd`, and Noto fonts for all major scripts and color emoji, so VMs baked earlier stop showing boxes. Restart apps that were running to pick up new fonts.
- The command must be installed in the VM; `pvm app` does not install it. Window titles end in `[dev1]`.
- `pvm app install [<vm>] <id>...` installs apps from the built-in catalog that `pvm app catalog` lists: `claude-code`, `codex`, `claude-desktop` and `firefox`, each the vendor's official Linux build. Images already have Firefox; the entry installs it into a VM that lacks it.
  - The first word is the VM unless it is a catalog id. Without a VM the usual default applies, and `--yes` starts a stopped VM without asking.
  - Each app has an install script built into pvm. It runs as root inside the VM through the guest agent, and its output is shown on stderr. There is no way to pass your own script.
  - Downloads happen inside the VM and leave through the VM's route and exit; the host downloads nothing. Claude Code and Claude Desktop come from Anthropic's APT repositories, whose signing key is pinned by fingerprint; Codex comes from OpenAI's install script over HTTPS and goes into the VM user's home directory, with a link at `/usr/local/bin/codex`; Firefox comes from Mozilla's APT repository, key pinned the same way.
  - Apps install one after another, each within 15 minutes. The first failure stops the command with a non-zero exit and names the app and the ones not attempted; apps installed before it stay installed and recorded. Running it again is safe: an installed app is left as it is.
  - A window app (`claude-desktop`, `firefox`) also gets the Xpra app support first. Start it with `pvm app dev1 -- claude-desktop`; terminal apps run in `pvm ssh`.
  - `pvm app ls` lists running apps and then, per VM, the catalog apps it has. A running VM is asked which catalog commands it has, so an app installed with the image or by hand counts too; a stopped VM shows what was last seen.
- Run inside a workspace folder, a command starts in the matching guest directory, so `pvm app -- gedit notes.txt` opens the file next to you; outside a workspace it starts in the VM user's home directory.
- GTK programs run with `gtk3-nocsd` preloaded, so their title bar and minimize, maximize and close buttons come from the host, like Firefox's. On Windows hosts, programs that draw their own frame, such as Electron apps, get the host's frame too, above their own top bar.
- Chromium-based programs, recognized by `v8_context_snapshot.bin` next to the binary (Electron apps such as Claude Desktop and VS Code, and Chrome), get `--disable-gpu`: the VM has no GPU, and Chromium's software OpenGL is slower than its plain software drawing. A wrapper script that starts Chromium is not recognized; pass the flag yourself.
- Closing a window closes that app. Quitting the Xpra client leaves apps running; `pvm app attach` brings them back. `pvm stop` and `pvm rm` close the VM's windows on the host; a VM restart ends its apps.
- Clipboard and drops are off by default, per VM:
  - `--clipboard to-host`: copy in the VM, paste on the host (text, images and, on Windows hosts, files and folders).
  - `--clipboard to-guest`: host content enters the VM only when one of its windows comes to the front, or when you copy while it is there, never in the background. Content that password managers mark as excluded is never sent. Text, images, and files and folders copied in Explorer. Windows hosts only so far.
  - Copied files go up to 256 MB in all. Files from the host land in `~/.cache/pvm/clipboard` in the VM and paste in Thunar, file dialogs, browsers and Electron apps; files from the VM land under the VM's directory in pvm's data and paste in Explorer. Each copy replaces the previous one. Problems go to `client.log` and `clipboard.log` in the VM's `app` directory.
  - `--clipboard both`: both of the above.
  - `--drop on`: files dropped on a VM window upload into `~/Downloads` in the VM (up to 100 MB each, never opened). A file the VM tries to send asks for confirmation on the host.
  - A change to the clipboard, drops, audio, silence or scale applies at once: `pvm app config` attaches a connected client again, and the windows blink once. With none connected, it applies when they are next attached.
  - While the clipboard is off, `pvm app` and `pvm app attach` say so on stderr when they start the client.
- The data goes over the VM's `pvm.stream` channel, not the gateway VM.

### 6. Port maps

```bash
pvm map dev1 8080:80
curl http://127.0.0.1:8080/
pvm map ls
```

- The host listens on loopback only. TCP maps on running VMs with `pvm.stream` go directly over the VM's `pvm.stream` channel (`transport: stream`), consuming no gateway socket; other maps (UDP, stopped VMs, or VMs without `pvm.stream`) use host relay -> gateway -> VM.
- Native Windows backend: at most 32 gateway-carried maps per cluster (TCP and UDP counted separately). `pvm.stream` TCP maps do not count toward that limit. Each gateway map is a socket in the gateway's QEMU user network, and Windows QEMU polls at most 64 of those; past that the gateway's own UDP (DNS, UDP exits) silently stops, so pvm refuses the 33rd gateway map instead.
- `pvm route pause [vm]` / `pvm route resume [vm]` cuts or restores external egress immediately (resetting live external connections) while keeping SSH, mounts, cluster-local links and host port maps up.
- `pvm route require-check <vm> [--off]` enables a fail-closed admission gate: the VM stays in `unverified4` (no external egress) until `pvm check <vm>` passes and the daemon renews a 90-second lease (`__grant`) every 30s; lease expiry or daemon loss cuts external traffic and flushes conntrack.
- Deleting a VM cleans up its relays, desktop state, and agent connections.

### 7. DNS and split-routing rules

```bash
# internal domains: resolved via the host's DNS, traffic goes direct
pvm route add internal --domain-suffix corp.internal
# optionally pin a dedicated internal resolver
pvm route add internal --domain-suffix corp.internal --dns 10.0.0.53
# plain direct rules
pvm route add direct --domain-suffix example.com
pvm route add direct --cidr 10.0.0.0/8
# proxy rules: force a domain/CIDR through a specific exit
pvm route add proxy --domain-suffix example.com --via my-exit
# match types: --domain-suffix / --domain-prefix / --domain-keyword / --cidr
pvm route ls
pvm route rm <index>
pvm dns ls
```

- Explicit proxy-rule domains get a fake-ip (198.18.x.x) and use the policy relays. Direct/internal rules return real addresses and use their direct path. Unmatched domains also return real addresses: exit-bound VMs resolve only through their selected tunnel and forward TCP, UDP, and ICMP directly at L3; direct VMs use the host uplink DNS and normal direct forwarding. Unsupported packets are dropped.
- The cluster is IPv4-only: AAAA queries return NODATA on fake-ip, direct and internal paths so dual-stack clients fall back to A immediately. Non-A/AAAA query types for proxied names also return NODATA and are never forwarded to the host resolver. PTR for exit-routed real IPs returns NODATA because there is no in-tunnel PTR resolver. Direct/internal A queries and CIDRs use their designated direct resolver.
- Domain rules affect resolution that goes through the gateway DNS.
- Rules take effect immediately, but established connections are not retroactively switched.

### 8. Stop, delete, clone

```bash
pvm stop dev1
pvm start dev1
pvm rm dev1 -y                  # or --force
pvm clone dev1 dev2             # source VM must be stopped; --no-start clones without booting; --no-wait returns after boot
pvm resize dev1 --cpus 4 --mem 8192   # a stopped VM's vCPUs and memory (MB); pvm resize dev1 40 grows its disk to 40 GB
```

- `pvm rm` double-checks: interactively type y to confirm; **non-interactive/scripts must pass `-y` (or `--force`)** or the command refuses.
- `pvm start` and starting clones wait for guest provisioning and the agent before returning; pass `--no-wait` when you only want to boot QEMU and will poll readiness yourself.
- `pvm resize` needs the VM stopped and changes only what it is given: a disk size in GB (grow-only, see section 10), `--cpus N` and `--mem MB`, alone or together. vCPUs and memory cannot change while the VM has an unconsumed RAM snapshot, which only restores into the same machine: `pvm start dev1` restores it, `pvm snapshot rm dev1` deletes it.
- clone inherits mounts, CPU/memory, user, timezone, SMBIOS, and the GPU device; the effective default exit is explicitly inherited before boot; port maps are not inherited by default but can be interactively carried over with new ports (`--inherit-ports` / `--no-inherit-ports`).
- On first boot a clone regenerates its hostname, machine-id, SSH host keys, and management key. Other authorized keys and application identities remain copied. A private `/share` starts empty in the clone; extra mounts still refer to the explicitly configured host sources.

### 9. Workspaces (pvm workspace)

A workspace binds one host folder to one VM at `/workspace/<id>` (id: lowercase letters, digits, `-`).

```bash
pvm workspace create web [dev1] --mode mount|sync|copy [--path DIR] [--read-only] [-y]
pvm workspace ls
pvm workspace status web                 # sync only: changes on each side and conflicts
pvm workspace push web [--prefer host|vm | --keep-both]
pvm workspace pull web [--prefer host|vm | --keep-both]
pvm workspace clone web ./web-result     # dest must be missing or empty; never overwrites
pvm workspace ssh web [command...]       # like pvm ssh, starting in /workspace/web
pvm workspace rm web [--delete-guest] [-y]
```

- `--path` defaults to the current directory, and the VM to the default VM. `create`, `ssh`, `status`, `push`, `pull`, `clone` of a sync or copy workspace and `rm --delete-guest` need the VM running; at a terminal they offer to start it. A read-only 9p mount is created while the VM is stopped.
- `mount`: one shared copy. 9p on Linux, macOS and WSL2; reverse sshfs on the native Windows backend (pvm serves SFTP over the existing SSH connection, the daemon remounts after drops, and `sshfs` is installed in the guest on first use). `--read-only` is enforced on the host. Host edits raise no inotify events in the VM; file watchers need polling, for example `CHOKIDAR_USEPOLLING=1`.
- `sync`: a copy on each side. push/pull use a three-way comparison with the last baseline; on any conflict they write nothing and list the paths. Resolve with `--prefer host`, `--prefer vm`, or `--keep-both` (the other side's version is kept as `<name>.pvm-conflict-<host|vm>`). Run git on one side only.
- `copy`: one upload, then `clone` downloads the VM copy.
- Uploads skip `.gitignore` matches and `node_modules`; `.pvmignore` adds patterns and `!pattern` re-includes. `.git` is transferred. Create refuses the home directory, a file system root, `~/.pvm`, and trees containing `.ssh`, `.gnupg`, `.aws`, `.kube`, `.docker`, `.netrc`, `.git-credentials`, `.gitconfig`, `.npmrc`, `.pypirc`, `.azure`, `.gcloud`, `.env` / `.env.*` (templates ending `.example`/`.sample`/`.template`/`.dist` are allowed), `*.pem`, `*.p12`, `*.pfx`, or SSH private key files unless `.pvmignore` excludes them; mount mode and `pvm mount` refuse the same folders and ignore `.pvmignore`, since a mount shares every file (`pvm mount --allow-sensitive` overrides explicitly). Uploads above 1 GiB need confirmation (`-y` in scripts). A full guest disk fails with a `pvm resize` hint.
- Downloads reject absolute or `..` paths, devices, escaping symlinks, paths below a host symlink, Windows-reserved names and case clashes (with each other or with existing host files), and list them; the command then exits non-zero and the baseline is not updated. After a download pvm lists changed `.git/hooks/*`, `.git/config`, `.vscode/*.json` and `.envrc` files for review.
- `rm` never deletes host files and keeps the VM copy unless `--delete-guest`, which refuses while anything is mounted below the VM copy. It warns when the VM copy has changes not yet pulled or cloned. `pvm rm <vm>` lists the VM's workspaces and keeps them as orphans for `pvm workspace rm`.

### 10. Disk capacity and host space

- VM disks use sparse qcow2 files. `--disk` at create and `pvm resize` set the virtual capacity ceiling; neither allocates the full size on the host up front. `resize` is grow-only and never shrinks the guest partition or filesystem.
- QEMU enables `discard=unmap` and `detect-zeroes=unmap`. Deleting files alone can leave historical allocations and metadata behind, so run `sudo fstrim -av` in the guest before offline compaction for the best result.
- Reclaim host space with an offline sparse conversion:
  ```bash
  pvm ssh dev1 'sudo fstrim -av'
  pvm stop dev1
  pvm compact dev1
  pvm start dev1
  ```
- `pvm compact` requires a stopped VM and refuses an unconsumed RAM snapshot. It validates the new image, virtual capacity, and backing chain, then atomically replaces the original only when the new file uses less host space. It does not enable compression or reduce virtual capacity.

### 11. Snapshots and troubleshooting

```bash
pvm snapshot create dev1        # capture RAM+disk state and stop (VM must be running)
pvm start dev1                  # detects the snapshot and restores quickly; processes resume intact
pvm snapshot rm dev1            # VM must be stopped first
pvm logs dev1 --follow
pvm daemon status
pvm daemon stop && pvm daemon start
```

- Native Windows (WHPX) has no RAM snapshots with any QEMU build: QEMU blocks migration of WHPX VMs, so `pvm snapshot create` refuses up front with "native Windows has no RAM snapshots: QEMU blocks migration of WHPX VMs (missing dirty memory tracking support); use the WSL2 backend for snapshots".
- A snapshot restores only into the QEMU build (build id and qemu-system SHA-256) and CPU arguments that wrote it; see QEMU builds below.
- Snapshots suit machines left unused for days, or a checkpoint before risky operations. Existing snapshot artifacts are not overwritten implicitly. After a restore, the active restore overlay and any `.bak` hold VM data: snapshot removal or another snapshot is refused. Clone the stopped VM to flatten its current disk before creating a new checkpoint; do not manually delete backing files.
- `pvm doctor`, `pvm audit dev1 --privacy`, and `pvm route explain dev1 example.com` inspect local state without network probes or starting the daemon; output is redacted. Route explanations describe configuration, not observed traffic, and do not resolve DNS.
- `pvm diag [output.zip] [--offline]` exports a whitelist-only, redacted ZIP (manifest, host/VM metadata projections, bounded daemon/console/QEMU log tails, and live gateway `journalctl` when the daemon is running) for troubleshooting or sharing. It never includes raw private keys, exit tokens or full `~/.pvm`. Daemon and VM console logs rotate at 8 MiB (QEMU stderr at 2 MiB) instead of growing unbounded.
- If `create`/`start` fails during cloud-init, read the reported `cloud-init status --long` and `/var/log/cloud-init-output.log` tail. Rebooting (`pvm stop && pvm start`) helps with transient SSH/agent timeouts, but does **not** rerun failed cloud-init `runcmd` steps; inspect with `pvm ssh <name>` or recreate the VM.
- `pvm check <vm> [--json]` runs active evidence-based checks (`pvm-check/1`, saved to `vms/<vm>/last-check.json`): two-path public exit IP sampling (guest terminal + gateway `SO_MARK` via `pvm-gateway __probe-exit`), negative probes to metadata/DoT/private ranges cross-checked against gateway nft `pvm_drop_*` counters, IPv6 global address count, resolver confinement and locale/timezone evidence. Missing evidence is `unknown` (fails critical checks). Only run on demand or when admission renewal requires it.
- `pvm exit import <name> --file <config.yaml|-> [--select NODE] [--chain relay,exit]` imports Clash/Mihomo/Surge YAML or share links (`pvm exit nodes <name>` lists usable nodes). Mihomo runs as a separate GPL-3.0 systemd service (`DynamicUser=yes`, root-only config) inside the gateway VM, never linked into pvm and never exposed to dev VMs; gateway relays dial its loopback SOCKS5 with remote domain resolution. `pvm exit set <name> --ip-mode static|dynamic [--ip-scope city|region|country|any] [--rebind]` anchors an exit baseline and fails the exit closed on IP/region drift or mainland China egress. `pvm exit test <name>` also verifies real public internet egress through the selected tunnel.
- `PVM_DNS_UPSTREAM` accepts comma-separated DoH endpoints (e.g. `https://1.1.1.1/dns-query,https://dns.google/dns-query`) with 2.5s first-try failover and 5-minute primary re-probe, in addition to UDP/`tcp://` upstreams. Host control-plane clients bind to the physical IPv4 NIC (`internal/hostnet`) to avoid TUN/VPN loops (`PVM_NO_BIND_IF=1` disables).
- `pvm image build <distro:version> [--fresh]` resumes an interrupted `bake-<stem>` VM and its cached apt packages on failure; `--fresh` discards it and starts clean. `pvm doctor --fix` tightens 0700 directory modes, removes unreferenced temp artifacts, and rebases stopped VM overlays to relative `PVM_HOME` backing paths so `PVM_HOME` can be moved between disks.
- Automatic update checks are off; opt in with `update_check: true` in config.yaml or `PVM_UPDATE_CHECK=1`. `PVM_NO_UPDATE_CHECK=1` overrides either. Failed checks are also cached for 24 hours.
- The Windows installers also place PVM Desktop (`pvm-desktop.exe`), a window for people over the same commands. Agents keep using the CLI. `pvm upgrade` changes neither QEMU nor PVM Desktop; rerun the installer to update the app.
- Installer and `pvm upgrade` require GitHub CLI (`gh`) for release attestation verification; the installer also requires Python 3. `pvm upgrade --rollback` restores the saved binary and VM recovery plan, and, only when rolling back to a v1.3.x recovery point, that version's QEMU runtime folder. `pvm upgrade --to` refuses tags below v1.4.0; use `pvm upgrade --rollback -y` or that version's installer. After validating the new binary, `pvm upgrade` checks that it accepts the current default QEMU (`pvm qemu check --default --read-only`, native backend only) and aborts before replacing anything if it does not; with no QEMU set up it only prints the setup hint.
- `pvm daemon restart`, and a host reboot with the login service installed, starts the user VMs that were running before (`desired_running` in `vms/<name>/status.json`); `pvm daemon stop` or `pvm stop <name>` clears that flag, so those VMs stay stopped. Readiness, `last_error` and per-cluster readiness persist and show in `pvm daemon status`. `pvm upgrade` restarts the daemon the same way; with `--no-restart`, run `pvm daemon restart` yourself.
- If the gateway pvm-gw1 spins at high CPU with SSH unreachable for a long time, recover with `pvm stop pvm-gw1 && pvm start pvm-gw1`. Never delete pvm-gw1 (the daemon recreates it, but cluster networking is down and custom state is lost in the meantime).
- The daemon holds a single-instance lock; on `another pvm daemon is already running`, find the existing daemon instead of spawning a second one.
- A boot watchdog restarts a VM only after two consecutive checks show full CPU with zero console progress; slow TCG boots are left alone.

## QEMU builds

pvm runs QEMU only from the build selected with `pvm qemu`, by absolute path; it never searches PATH, `/usr/local` or Program Files, and never switches builds on its own. Builds sit side by side and one is the default. There are two sources:

- **Project build**: QEMU built from the signed upstream source by the pvm-qemu project, pinned by digest in this pvm release. It is installed into `~/.pvm/runtime/qemu/builds/<tag>/<arch>` and needs no administrator rights or `gh`.
- **Official build**: your platform's own channel, installed by you. macOS: Homebrew under `/opt/homebrew` (`brew install qemu`). Linux: your distribution's packages in `/usr/bin` (Debian, Ubuntu, Fedora, Arch; 8.2.0 or newer; RHEL's `qemu-kvm` is not accepted), for example `sudo apt install qemu-system-x86 qemu-utils`. Windows: MSYS2 UCRT64 (`pacman -S mingw-w64-ucrt-x86_64-qemu` in a UCRT64 shell). pvm detects and checks it, but never runs a package manager or downloads it.

```bash
pvm qemu                               # same as pvm qemu ls: builds, default, recommended pin, leftovers (--json)
pvm qemu install -y                    # the recommended project build; the first build becomes the default
pvm qemu install 11.1.3-pvm1 --use -y  # a given tag, made the default
pvm qemu install --file pvm-qemu-11.1.3-pvm1-linux-amd64.tar.gz -y   # offline: must match a pinned digest
pvm qemu use official -y               # Homebrew, your distribution's QEMU, or MSYS2 UCRT64
pvm qemu use 11.1.3-pvm1 -y            # switch the default back
pvm qemu check --default               # exit 0 ok, 3 not set up, 1 failing, 2 usage
pvm qemu rm 11.1.3-pvm1 -y             # refuses the default and builds running VMs use; rm official only unregisters
pvm qemu gc -y                         # remove staging, trash and folders left by older pvm versions
```

- `install`, `rm` and `gc` ask at a terminal and need `-y` otherwise. A tag this pvm does not pin needs `--unpinned` and the GitHub CLI (`gh`) to verify its attestation.
- Downloads resume and honour `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY`, and on Windows the static system proxy. A proxy script (PAC) cannot be read: set `HTTPS_PROXY`, or download the archive and use `--file`.
- Every build passes a minimum version per host (macOS and Windows 11.1.0, Linux 8.2.0), accelerator and device probes, a test start with pvm's arguments and a firmware check (on macOS also the hypervisor entitlement) before it is registered and again whenever its binary changes. A project build whose files changed is refused; an official build that a package manager upgraded is checked again. Errors name the next command; do not work around them by running QEMU yourself.
- The daemon runs without QEMU: `pvm ls`, `rm`, `snapshot rm`, `exit` and `route` work, while create, start, clone, compact, resize and mounts fail with "no QEMU yet; pvm needs one to run VMs. Choose one, once: pvm qemu install (a QEMU built by the pvm-qemu project) or pvm qemu use official (...)". Clusters that cannot recover for want of QEMU are reported as blocked and stay cut off. The daemon picks up `pvm qemu install` or `use` within a few seconds, without a restart; running VMs keep the build they started with until they restart.
- `pvm daemon status` shows a `qemu:` line with the build and its path, or `none usable: <reason>`, with notes for VMs still running on another build and for blocked clusters. `pvm doctor` has read-only `qemu`, `qemu_builds`, `qemu_update`, `qemu_leftovers` and, on Windows, `qemu_desktop` checks.
- RAM snapshots bind to the build and CPU arguments that wrote them; a restore under another build is refused, with `pvm qemu use <tag>` or `pvm snapshot rm dev1` as the ways out. After a package manager upgrades an official build, its older snapshots cannot be restored, so snapshot users hold the package (`brew pin qemu`, `sudo apt-mark hold qemu-system-x86 qemu-utils`) or use a project build. Snapshots written before pvm v1.4.0 are refused; restore them under the old version (`pvm upgrade --rollback -y`, `pvm start dev1`, then `pvm upgrade`) or drop them with `pvm snapshot rm dev1`.
- The QEMU folder that pvm v1.3.x or the v1.4.0 Windows installer placed is taken over as a project build at the daemon's next start when it matches a pinned build file by file. Any other old folder is left alone and listed by `pvm qemu ls` and `pvm qemu gc`.

## Adding an exit (only when the user explicitly asks)

### Standard flow: deploy an exit over SSH

The VPS runs `pvm-exit`, a server-only binary from the same release (`pvm-exit_linux_amd64.tar.gz`, `pvm-exit_linux_arm64.tar.gz` and a `.deb` for each). `pvm exit deploy` does the whole setup from the host over SSH:

```bash
pvm exit deploy my-exit --ssh root@vps.example.com
pvm exit test my-exit
pvm exit test my-exit --speed --samples 5
```

What it does:
- Probes the VPS over SSH (OS, architecture, root, an installed `pvm-exit`).
- Downloads the `pvm-exit` release matching this pvm, checks its attestation with `gh` and its SHA256 on the host, and uploads it; the VPS needs neither `gh` nor GitHub access. `--tag vX.Y.Z` picks another release; `--binary FILE` uploads a local Linux build.
- Runs `pvm-exit init` unless it is already set up, which keeps an existing config; after an upload it restarts the service on the new binary.
- Creates a one-time invite with `pvm-exit invite` and adds the exit with it. SSH has authenticated the VPS, so the fingerprint inside the invite is trusted without a manual check. The invite is never printed.

Options:
- The whole deploy is one SSH connection, so SSH asks for its password or key passphrase at most once. As a sudo user, the commands run under `sudo`; when sudo needs a password, pvm asks for it once on the host's terminal (three tries) and stops before changing anything if there is no terminal. `--user-mode` installs a systemd user service of the SSH user instead, with userspace NAT and ports from 1024; it would stop when the SSH session ends, so deploy requires lingering for that user (`sudo loginctl enable-linger USER`, run by an administrator) and stops before changing anything without it.
- `--public-endpoint HOST` when clients reach the VPS by another name than the SSH host. `--client NAME` names this host's token (default: the hostname). `--ssh-opt=-p --ssh-opt=2222` passes SSH arguments.
- `--migrate` takes over a VPS deployed with `pvm exit-agent install`; see below.
- A VPS with the `.deb` installed keeps it: deploy only creates the invite, and refuses `--binary` or another `--tag` (upgrade the package instead).

Without SSH from the host, set up the VPS by hand and pass the invite:

```bash
# on the VPS, one of:
sudo apt install ./pvm-exit_<version>_amd64.deb                 # starts the service
sh install-exit.sh --public-endpoint vps.example.com             # scripts/install-exit.sh; as root or as a user; needs gh, no login
sudo pvm-exit invite laptop --host vps.example.com --raw         # prints the invite, pvmx1:...

# on the host, paste it on stdin so it stays out of shell history:
pvm exit add my-exit --invite -
# or from a private file: pvm exit add my-exit --invite-file invite.txt
```

The VPS side:
- `pvm-exit init [--user]` installs and starts the service; `/etc/pvm-exit/config.yaml` (user mode: `~/.config/pvm-exit/config.yaml`) holds `listen`, `public_endpoint`, `transport`, `nat`, `tun_prefix`, `subnet_base`, `udp_port`, `tcp_port`, `timezone` and `domain_allow_cidr`; unknown keys are rejected. Edit it and run `sudo systemctl restart pvm-exit`.
- The system service runs as the `pvm-exit` user. Kernel NAT gets `CAP_NET_ADMIN` and `CAP_NET_RAW` only, with forwarding enabled in `/etc/sysctl.d/60-pvm-exit.conf`; userspace NAT runs with no capabilities.
- `pvm-exit invite <name> [--role enroll|admin] [--ttl 15m] [--max-peers 64]`, `invite ls`, `invite rm <id>`: an invite works once, within its TTL (at most 24h). An enroll token registers peers and sees and removes only its own; an admin token can do everything.
- `pvm-exit token ls` shows each token with its peers; `pvm-exit token rm <id>` revokes it and removes its peers (`--keep-peers` keeps them). On the host, `pvm exit rm <name>` removes its peer and revokes the host's own enroll token once it has no peer left; an admin token is never revoked this way. `pvm-exit peers` and `pvm-exit info` show the rest.
- `pvm-exit uninstall [--user]` stops the service and keeps the config, token, certificate and peers.

Migrating a `pvm exit-agent install` deployment: `sudo pvm-exit init --migrate` (or `pvm exit deploy my-exit --ssh ... --migrate` for a new host entry) reads the old unit's flags into a config and keeps the state directory, so the fingerprint, token and peers carry over and existing hosts keep working. A token file in `/etc/pvm-exit` takes the standard name `admin.token`, and the old `/etc/sysctl.d/99-pvm-exit.conf` gives way to `60-pvm-exit.conf`. The old service is stopped and its unit renamed to `pvm-exit-agent.service.migrated`. The old unit seldom set a public address, so pass `--public-endpoint HOST` to `init --migrate`, or `--host HOST` to each `invite`. Existing hosts still hold the admin token; to give one an enroll token, `pvm exit rm` it and add it again with an invite. Without `--migrate`, `init` refuses to run next to the old service. `pvm exit-agent` stays in v1.4.x and is removed in v1.5.0.

Notes:
- Existing production exits are already running — do not redeploy a name that is already registered. To upgrade, run `pvm exit deploy` with a new name only when a new host entry is wanted; otherwise upgrade the package, or install the new binary and run `sudo pvm-exit init` again, which keeps the config and restarts the service.
- Both amd64 and arm64 binaries come from GitHub Releases — no local build needed.
- Speed probes never touch third-party public servers: the exit server serves `/speed?bytes=N` (64MB cap) and `/ping` at `.1:18081` on each tunnel NIC, reachable only inside the tunnel. The gateway uses `/ping` as its data-plane health probe.
- Registrations idle for 24h are reaped by the exit server; a gateway returning after sleep re-enrolls automatically with its persisted peer key. `pvm exit test` re-enrolls the host entry when the handshake fails, or when the handshake works but the probes get no reply because the server holds the key under another tunnel IP; it saves the corrected entry and retries.
- Exit timezone suggestions use the server's system timezone, or `timezone` in its config; no geolocation service is contacted. Private/metadata/local-host destinations are blocked unless explicitly permitted by a narrow `domain_allow_cidr`. Restart after changing VPS interface addresses.
- Exit NAT backends (`nat` in the config): `nft` / `iptables` / `userspace`; userspace needs no root and forwards TCP/UDP/ICMP. ICMP uses Linux ping sockets, so insufficient socket permission drops only that ICMP flow and never falls back to another egress.
- VPS firewall: accept TCP 18443 (`listen`), UDP 18443 (`udp_port`) and TCP 18444 (`tcp_port`). With the `nft`/`iptables` backends, also forward from the server's TUN devices `pvmexit0` (UDP transport) and `pvmexit1` (TCP transport); the prefix follows `tun_prefix`. A forward drop outside the server's table, such as ufw's default `deny (routed)`, cannot be overridden by the server: `pvm exit test` passes while routed VMs cannot open connections (ping may still work). Fix with `ufw route allow in on pvmexit0 out on <uplink>` and the same for `pvmexit1`.
- Tunnel pools use separate `/16` networks: UDP `10.<base>.0.0/16`, TCP `10.<base+1>.0.0/16` (default base `99`). Set a different `subnet_base` for multiple exit servers on one VPS or to avoid VPS-internal networks. A larger pool does not prevent address conflicts. Changing the layout drops stale registrations; expect tunnels to re-enroll.

### Add a Cloudflare WARP exit without a VPS

```bash
pvm exit add-warp my-warp
pvm exit test my-warp
pvm route dev1 --via my-warp
```

- `add-warp` registers a device directly through Cloudflare's API. pvm does not install `warp-cli` or `wgcf`, does not generate a `wgcf-profile.conf`, and does not modify any VM image.
- If registration fails and remote rollback also fails, pvm writes `<name>.warp-pending` and refuses to register that name again until the device is restored or revoked. Follow the recovery instruction in the error.
- Registration alone changes no routes. Bind a VM explicitly with `pvm route`, or use `pvm route default --via my-warp` deliberately. `pvm exit rm my-warp` removes the local entry and makes a best-effort Cloudflare device deletion.
- WARP uses UDP WireGuard only. `pvm exit test` is handshake-only for WARP; the self-contained TCP/UDP/ICMP probes and VPS-only `--speed` / `client-config` are not available. Default-exit and explicit fake-ip split domains resolve through `1.1.1.1` inside the WARP tunnel, never through host DNS.
- WARP egress location can vary and has no pvm VPS timezone probe. Set the intended region explicitly with `--tz` / `--locale`, or later with `pvm exit set`, before routing a VM.

### Connect a standard WireGuard client

```bash
pvm exit client-config my-exit --output phone.conf
```

- Use a UDP exit; the custom TCP transport is not compatible with standard WireGuard clients.
- The command enrolls a new client key, writes a mode-`0600` config, and prints a QR code for mobile apps.
- The output path must not already exist. The profile routes IPv4 through the VPS with persistent keepalive; it does not include pvm's fake-IP DNS or split-routing rules. IPv6 is not tunneled.
