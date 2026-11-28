# pvm

[English](README.md) | 简体中文

单个二进制的个人 QEMU 开发虚拟机：一条命令创建隔离的 Linux 开发机，并让它的全部流量经由你自己的 VPS 出网。

## 亮点

- **一条命令得到开发机。** 预装 Docker、常用工具、Firefox 和桌面。首次烘焙镜像之后，新虚拟机约 30 秒就绪。
- **流量只从你的出口走。** 虚拟机可以经 WireGuard 走你的 VPS、Cloudflare WARP，或导入的 Clash/Mihomo/Surge 节点。隧道断开时虚拟机直接断网，不会改走宿主机的网络。
- **分流。** 指定的域名或网段可以直连或走另一个出口，规则即时生效。
- **可以自己检查。** `pvm check dev1` 实测虚拟机这条路径的真实出口 IP；`pvm route require-check` 让虚拟机在检查通过前保持断网。
- **虚拟机里的程序作为宿主机窗口。** `pvm app dev1 -- gedit` 把一个程序作为独立窗口显示在桌面上；它的 cookie、指纹和流量都留在虚拟机里。
- **把项目目录带进虚拟机。** 挂载目录、两边各保留一份同步，或上传一次再下载结果。
- **适合脚本和 agent。** `--json` 输出、`pvm capabilities`、`pvm job`，以及只读的 `pvm dashboard`。
- **Windows 无需管理员权限。** 一个安装包装好 pvm、PVM Desktop 应用、QEMU 和 Xpra 客户端。

## 安装

pvm 支持 arm64 的 macOS、amd64 的 Linux 和 amd64 的 Windows。

### Windows

1. 从 [Releases](../../releases) 下载 `pvm_windows_amd64_setup.exe` 或 `pvm_windows_amd64_setup.msi`。如需先校验：`gh attestation verify pvm_windows_amd64_setup.exe --repo BitterOrange/pvm`。
2. 运行安装包，不需要管理员权限。
3. 打开新的终端运行 `pvm doctor`，或从开始菜单打开 **PVM Desktop**。

两种安装包都会把 `pvm.exe` 和 `pvm-desktop.exe` 放到 `%LOCALAPPDATA%\pvm`，把该目录加入用户 `PATH`，添加开始菜单快捷方式，安装 Xpra 客户端，并把固定的 [pvm-qemu 构建](#qemu) 放进 pvm 数据目录（`%USERPROFILE%\.pvm`，或 `PVM_HOME`）。

- 一台机器只用一种安装包：两者都会拒绝覆盖另一种。要换用另一种，先在“设置 > 应用”里卸载。
- `pvm upgrade` 只更新 `pvm.exe`。更新 PVM Desktop 需要运行新的安装包。
- 卸载会保留 `~/.pvm` 下的虚拟机、密钥、配置和 QEMU 构建。

<details>
<summary>关于 Windows 安装包的更多说明</summary>

- 正在使用的文件无法替换。交互式安装会提示关闭 PVM Desktop 并停止 daemon（运行中的虚拟机也会停止）；静默安装（`/S`、`msiexec /qn`）会直接拒绝，请先关闭 PVM Desktop 并运行 `pvm daemon stop`。
- 静默安装 MSI：`msiexec /i pvm_windows_amd64_setup.msi /qn`。传入 `PVMHOME=D:\pvm-home` 可指定数据目录，升级和卸载会沿用它。
- `.exe` 安装包的最后一页提供“打开 PVM Desktop”；MSI 没有这一页。
- 卸载会移除 pvm、PVM Desktop 及其快捷方式和“登录时打开”设置、Xpra 客户端和 `PATH` 条目。

已知限制：

- 提示停止运行中 daemon 的流程还没有完整测试过。如果没有出现提示，取消安装，运行 `pvm daemon stop` 后重试。
- 安装程序停止 daemon 时最多等待约三分钟让虚拟机关机，期间不显示进度。
- 没有 daemon 却仍在运行的虚拟机，安装程序无法停止。它会列出这些虚拟机并拒绝继续，请先关掉它们。
- 杀毒软件可能短暂锁定新解压的文件。安装程序会重试 10 秒，仍失败就停止并保留旧版本。
- `pvm upgrade` 之后，“设置 > 应用”里显示的仍是安装包的版本。

</details>

Windows 有两种后端，用 `PVM_BACKEND` 或 `~/.pvm/config.yaml` 中的 `backend` 选择：

- `native`：Windows 版 QEMU 加 WHPX。Windows 版 QEMU 都没有 virtio-9p，所以 `/share` 不可用；宿主机目录请用[工作区](#工作区)。
- `wsl`：命令交给 WSL2 里的 `pvm`，支持 `/share`、额外挂载和 KVM。
- `auto`（默认）：有原生 daemon 在运行或已配置原生 QEMU 时用 native，否则 WSL2 里装了 pvm 就用 wsl，再否则用 native。

加速、DNS 和 WSL2 的设置见 [SKILL.md](SKILL.md)。

### macOS 和 Linux

先安装 GitHub CLI（`gh`）和 Python 3，然后：

```bash
curl -fsSL https://raw.githubusercontent.com/BitterOrange/pvm/master/scripts/install.sh | sh
```

脚本把 `pvm` 安装到 `~/.local/bin`（可用 `INSTALL_DIR` 修改），并安装固定的 QEMU 构建（`PVM_SKIP_QEMU=1` 跳过）。替换任何文件之前，它会校验校验和以及 GitHub 构建证明，并保留上一个版本。

### 快速开始

daemon 按需启动。第一次 `create` 会下载并烘焙镜像，只需一次，大约十分钟。

```bash
pvm create --image ubuntu:26.04 --name dev1 --user dev
pvm ssh dev1
pvm desktop dev1
pvm map dev1 8080:80
pvm exit add-warp my-warp
pvm route dev1 --via my-warp
```

`pvm list` 把每台虚拟机显示为 `stopped`、`starting`（附当前阶段）、`ready` 或 `failed`。`pvm image default ubuntu:26.04` 之后，创建时可以省略 `--image`。

## 功能

### 失败即断网的出口路由

注册一个出口，让整台虚拟机经它出网。TCP、UDP 和 ICMP 都经 WireGuard（UDP 或 TCP 传输）。隧道断开时流量被丢弃，绝不改走宿主机。

```bash
pvm exit add my-exit --server vps.example.com:18443 --token YOUR_TOKEN   # 自己的 VPS
pvm exit add-warp my-warp                                                # Cloudflare WARP，无需部署
pvm exit import my-sub --file clash.yaml --select NODE                   # Clash/Mihomo/Surge 节点
pvm route dev1 --via my-exit
pvm route default --via my-exit                       # 所有没有单独设置路由的虚拟机
pvm route add direct --domain-suffix example.com      # 分流
pvm route ls
```

只有显式的 `direct` 和 `internal` 规则会绕过出口。直连的虚拟机默认访问不到宿主机局域网，除非有规则放行。自己部署出口见[在 VPS 上部署出口](#在-vps-上部署出口)。

### 检查与自动化

- `pvm check dev1` 在虚拟机和网关两条路径上采样公网出口 IP，并用网关的计数器核对被拦截的探测。
- `pvm route require-check dev1` 让虚拟机在检查通过前保持断网，之后靠 daemon 续期的 90 秒租约维持联网；daemon 不在了，虚拟机就断网。`pvm exit set my-exit --ip-mode static` 会在出口的 IP 或地区变化时切断它。
- `pvm route pause` 和 `resume` 切断和恢复虚拟机的外部流量。
- 只读命令支持 `--json`；`pvm capabilities` 离线给出接口约定；`pvm job` 跟踪耗时操作。
- `pvm dashboard` 在本机回环地址上提供只读状态页；`pvm diag` 导出脱敏的诊断压缩包。
- `pvm audit dev1 --privacy`、`pvm doctor` 和 `pvm route explain dev1 example.com` 只检查本地配置，不做网络探测。

### 应用作为宿主机窗口

`pvm app` 通过 [Xpra](https://xpra.org) 把虚拟机里的单个程序或网页显示为宿主机窗口。宿主机需要 Xpra 6.5 客户端，Windows 安装包已自带。

```bash
pvm app dev1 -- gedit
pvm app dev1 https://mail.example.com --kiosk
pvm app config dev1 --clipboard both --drop on --audio both
pvm app install dev1 claude-code codex claude-desktop
pvm app ls
pvm app stop dev1 --all
```

- **剪贴板和拖放**默认关闭，需按虚拟机开启。宿主机内容只在该虚拟机的窗口位于前台时进入虚拟机，被密码管理器标记为排除的内容永远不会发送。
- **输入法**：宿主机输入法（比如微软拼音或表情面板）输入的文字能到达应用。
- **声音**在宿主机播放；`--audio both` 还会把麦克风借给虚拟机。
- **速度**：`--render-cpus N` 限制 Chromium 和 Electron 应用的渲染进程（默认 2 个），`--scale 1.25|1.5|2` 让虚拟机少画像素。
- **密钥环**：`--keyring host` 用保存在宿主机上的密码加密虚拟机的 login 密钥环，Claude Desktop 等应用因此保持登录。默认的 `off` 交给虚拟机自己管理。
- **应用目录**：`pvm app catalog` 列出 Claude Code、Codex CLI、Claude Desktop 和 Firefox。`pvm app install` 在虚拟机里运行内置脚本，下载经虚拟机的出口进行。

用 `pvm app config` 修改的设置对已打开的窗口立即生效。详见 [docs/design/app.md](docs/design/app.md)。

### 浏览器桌面

`pvm desktop dev1` 通过 noVNC 在浏览器标签页里打开虚拟机的 XFCE 桌面，地址只在本机回环上。`--public` 监听所有网卡，仍需要 VNC 密码。

### 工作区

工作区把一个宿主机目录绑定到一台虚拟机的 `/workspace/<id>`：

- `mount`：共享一份（9p，原生 Windows 上为反向 sshfs）。文件监听需要轮询，比如 `CHOKIDAR_USEPOLLING=1`。
- `sync`：两边各一份；`push` 和 `pull` 搬运改动，遇到冲突会停下。
- `copy`：上传一次，之后用 `clone` 下载结果。

```bash
cd ~/src/web
pvm workspace create web dev1 --mode sync
pvm ssh 'npm install && npm test'
pvm workspace pull web
```

上传会跳过 `.gitignore` 匹配的路径和 `node_modules`，`.pvmignore` 可追加规则。pvm 拒绝共享主目录、文件系统根目录、它自己的数据目录，以及含 `.ssh`、`.aws`、`.env` 等凭据的目录（`pvm mount --allow-sensitive` 可覆盖）。`pvm workspace rm` 从不删除宿主机文件。详见 [docs/design/workspace.md](docs/design/workspace.md)。

**可以省略虚拟机名。** 在工作区目录里，`pvm ssh`、`pvm exec`、`pvm app` 和 `pvm map` 使用该工作区的虚拟机，并从对应目录开始。在别处则使用 `pvm default-vm dev1` 设置的虚拟机，没有设置就用唯一的那台。在终端里，虚拟机没启动时 pvm 会先问你。

### PVM Desktop

常用操作的图形窗口，用于使用原生后端的 Windows。从开始菜单打开。

- **虚拟机**：状态和启动阶段；启动、停止、删除、调整大小；打开终端、应用或文件管理器；安装目录应用。
- **创建**：新建虚拟机，设置大小、出口、应用设置和预装的应用。
- **出口**、**工作区**、**QEMU** 和**设置**（默认虚拟机、登录时启动、英文或简体中文界面）。

每个改动都通过运行 `pvm.exe` 完成，所以应用做不了命令行做不了的事。它需要 Microsoft Edge WebView2 Runtime，Windows 10 和 11 通常自带。从安装包安装后的完整流程还没有测试过。详见 [docs/design/gui.md](docs/design/gui.md)。

### 架构

宿主机上的 `pvm` 二进制既是命令行也是 daemon，还可以在 VPS 上作为出口 agent 运行。虚拟机里运行单独的小型 guest agent。由 pvm 管理的网关虚拟机负责 DNS、路由、WireGuard 隧道和 kill switch。

```text
控制：pvm CLI -> pvm daemon -> 网关虚拟机和开发虚拟机
数据：开发虚拟机 -> 网关虚拟机 -> WireGuard 隧道 -> VPS 出口、Cloudflare WARP 或 Mihomo 节点 -> 互联网
```

## QEMU

pvm 只按绝对路径运行你用 `pvm qemu` 选定的构建，从不在 `PATH` 里查找，也不会自行换用其他构建。来源有两种：

- **项目构建**：由 [pvm-qemu](https://github.com/BitterOrange/pvm-qemu) 从 QEMU 签名源码构建，每个发布都附完整源码。只有 SHA-256 与 pvm 发布中固定的摘要一致，pvm 才会安装。它不是 QEMU 官方发布。
- **官方构建**：本平台自己的 QEMU：macOS 的 Homebrew、Linux 发行版的软件包（8.2 或更新），或 Windows 上的 MSYS2 UCRT64。

```bash
pvm qemu                    # 已有构建、默认构建和推荐版本
pvm qemu install -y         # 安装推荐的项目构建
pvm qemu use official       # 使用 Homebrew、发行版或 MSYS2 的 QEMU
pvm qemu check --default    # 退出码 0 正常，3 未配置，1 不合格
pvm qemu gc -y              # 清理旧版本留下的目录
```

每个构建在首次使用前和二进制变化后，都必须通过版本、加速器、设备、试启动和固件检查；文件被改动的项目构建会被拒绝。RAM 快照只能恢复到写入它的同一构建。没有 QEMU 时 daemon 照常运行，装好新构建后无需重启即可使用。

## 支持的镜像和宿主机

- 镜像：`ubuntu:26.04`、`ubuntu:24.04`、`debian:13`、`debian:12`。
- 宿主机：arm64 的 macOS、amd64 的 Linux、amd64 的 Windows（原生 WHPX 或 WSL2）。
- 虚拟机使用宿主机的架构，不支持跨架构模拟。Linux arm64 版本只用于 VPS 出口。

## 在 VPS 上部署出口

VPS 上运行的是只含服务端功能的 `pvm-exit`。在宿主机上一条命令即可经 SSH 安装、启动并添加出口：

```bash
pvm exit deploy my-exit --ssh root@vps.example.com
```

SSH 已经验证了 VPS 的身份，所以证书指纹随一次性邀请串一起传回，不需要手动复制 token。用有 sudo 权限的用户时安装系统服务；`--user-mode` 安装 systemd 用户服务，使用用户态 NAT，完全不需要 root；但需要管理员为该用户开启 linger（执行一次 `sudo loginctl enable-linger USER`），否则用户退出登录后服务就会停止。

系统服务以 `pvm-exit` 用户运行：内核 NAT 只授予 `CAP_NET_ADMIN` 和 `CAP_NET_RAW`，用户态 NAT 不授予任何 capability。配置在 `/etc/pvm-exit/config.yaml`。如果想在 VPS 上自己安装，可以用发布页的 `.deb`（`sudo apt install ./pvm-exit_<version>_amd64.deb`）或 `scripts/install-exit.sh`（它用 `gh` 校验发布的 attestation，不需要登录 GitHub），然后在 VPS 上生成邀请串：

```bash
sudo pvm-exit invite laptop        # 输出：pvm exit add my-exit --invite pvmx1:...
```

邀请串只能用一次，15 分钟后过期。它给宿主机发放一个独立的 token，只能注册 peer，不能管理服务器；用 `pvm-exit token ls` 和 `pvm-exit token rm <id>` 查看和吊销；`pvm exit rm` 在本机 token 不再有 peer 时也会吊销它。已经用 `pvm exit-agent install` 部署的 VPS，用 `pvm-exit init --migrate`（或 `pvm exit deploy ... --migrate`）迁移，token、指纹和 peer 都保留；`pvm exit-agent` 在 v1.5.0 删除。详见 [SKILL.md](SKILL.md#standard-flow-deploy-an-exit-over-ssh)。

VPS 防火墙需要放行 TCP 18443（控制面）、UDP 18443（WireGuard over UDP）和 TCP 18444（WireGuard over TCP），并允许转发 `pvmexit0`、`pvmexit1` 设备的流量。ufw 默认的 `deny (routed)` 会拦截这种转发，需要放行（把 `eth0` 换成 VPS 的公网网卡）：

```bash
ufw route allow in on pvmexit0 out on eth0
ufw route allow in on pvmexit1 out on eth0
```

手机或笔记本可以用标准 WireGuard 客户端连接：`pvm exit client-config my-exit --output phone.conf` 导出配置并显示二维码。每台设备用一份配置。它是普通的 IPv4 全局隧道，没有 pvm 的 DNS 和分流。

## 升级

`pvm upgrade` 校验新版本，检查它能否使用你当前的 QEMU，保存恢复点，替换二进制，重启 daemon，并启动之前在运行的虚拟机。它从不改动 QEMU 或 PVM Desktop。`pvm upgrade --rollback` 可回退。

自动检查更新默认关闭；在 `~/.pvm/config.yaml` 里设置 `update_check: true` 开启。

## 路线图

近期版本（全部版本见 [docs/release-notes](docs/release-notes)）：

- **v1.4.1**（进行中）：合并上游 v1.3.0-1.3.1，带来 Mihomo 出口、`pvm check`、`pvm diag`、`pvm dashboard`、`--json`、`pvm job`、`pvm ssh-config`、`pvm-exit` 及 `pvm exit deploy` 和邀请串、`pvm image default`、daemon 重启后恢复虚拟机、内存 balloon、DoH 主备切换，以及 macOS 和 Linux 上的剪贴板桥。切换路由时按出口公网 IP 的所在地设置时区。安装脚本、`pvm upgrade` 和 `pvm exit deploy` 用 `gh` 校验发布签名，但不需要登录 GitHub。已在 Windows（WHPX）、Linux（KVM）和 M1 Mac 上端到端测试，包括项目构建和官方构建的 QEMU。
- **v1.4.0**：`pvm qemu`，使用固定的 pvm-qemu 构建或本平台官方 QEMU；M1 Mac 上网关可以启动。
- **v1.3.x**：PVM Desktop、应用目录、密钥环设置、`pvm app` 的窗口缩放和输入法。

接下来：

- 安装包和 `install.sh` 不再自带 QEMU，改由 `pvm qemu install` 安装（[计划](docs/design/qemu.md)）
- v1.4.2：Mihomo 出口支持订阅链接导入，以及手动和自动更新，更新时绝不自动切换已选节点
- WHPX：虚拟机内可用 AVX，并按虚拟机选择 QEMU
- PVM Desktop：从安装包完整测试、自行更新、托盘图标，以及快照、端口映射和挂载的界面
- `pvm desktop` 像 `pvm app` 一样作为宿主机窗口
- WSL2 上的 `pvm app`
- Windows CI 覆盖其余的包，并对安装包做冒烟测试
- v1.5.0：删除 `pvm exit-agent`，由 `pvm-exit` 取代

细节和候选项见 [docs/roadmap.md](docs/roadmap.md)。

## 文档

- [SKILL.md](SKILL.md)：面向人和 agent 的完整手册；`pvm skill` 打印同一内容。
- [SECURITY.md](SECURITY.md)（[简体中文](SECURITY.zh-CN.md)）：信任边界、凭据、镜像、QEMU 和网络检查；`pvm security` 打印英文版。
- [AGENTS.md](AGENTS.md)：仓库地图、构建测试流程和开发规则。

## 开发

构建使用 Go 1.27.4 和 garble v0.18.0，两者版本都在 CI 中固定。

```bash
go install mvdan.cc/garble@v0.18.0
./script/build.sh
go test ./...
```

- Windows 上先安装 Git for Windows、Go 和 garble，然后在 PowerShell 里运行 `.\script\build.ps1`，它会生成 `pvm.exe`。
- `./script/build-desktop.sh` 构建 `pvm-desktop.exe`，需要 Node.js 22；`go build ./...` 和 `go test ./...` 不需要。
- 不要在 Windows 上运行全量 `go test ./...`：测试套件按 Unix 编写，许多测试会因无关原因失败。请只运行相关的定向测试（如 `go test ./internal/qemu -run Accel`），全量测试放在 Linux、macOS 或 WSL2 上。
- 任何构建都不包含 QEMU。CI 会对照 pvm-qemu 的发布和构建证明核对固定的构建。

## 许可证

pvm 以 [MIT 许可证](LICENSE)发布。第三方许可证见 [THIRD_PARTY_LICENSES](THIRD_PARTY_LICENSES)。
