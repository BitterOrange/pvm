# Roadmap

Planned work for the next release of this fork. Items move to the release notes once they ship.

## Planned

- **`pvm exit-agent install|uninstall`.** Register the exit agent as a systemd service with one command, like `pvm daemon install` on the host. It writes `pvm-exit-agent.service` with the given `exit-agent` flags, refuses when another agent already holds the ports, keeps the existing token file and certificate so registered hosts need no re-enrollment, and runs `systemctl enable --now`. `uninstall` stops and removes the unit and leaves the token, certificate and registrations in place. Today the unit is written by hand; see [SKILL.md](../SKILL.md#standard-flow-deploy-an-exit-over-ssh).
- **Workspaces.** `pvm workspace` manages a directory between one host folder and one VM in `mount`, `sync` or `copy` mode, with `clone` to download the VM copy. It covers mounting on the native Windows backend through reverse sshfs, and replaces the idea of a `pvm ssh --here` flag with `pvm workspace ssh`. Delivered in phases; see [docs/design/workspace.md](design/workspace.md).
- **`pvm rm` for a VM whose files were deleted by hand.** When `~/.pvm/vms/<name>` is removed while the VM is still a cluster member, the daemon reports not ready and `pvm rm` fails with "not found". The daemon should log the orphan and stay ready, and `pvm rm` should drop the leftover membership, port maps and gateway link. It must refuse while a QEMU process may still be using the VM's address.

## Candidates

Not yet scheduled.

- **UDP transport under the native Windows backend.** The gateway VM's QEMU user-mode network forwards the whole 100-port map pool for both TCP and UDP, about 200 host sockets. Windows QEMU polls sockets with `select()`, which handles 64 by default, and listening TCP sockets come first, which would explain why UDP replies never arrive. This is not yet confirmed. Confirm by shrinking the forwarded range, then add forwards on demand through QMP `hostfwd_add` when a map is created.
- **`pvm ssh-config <vm>`.** Print an OpenSSH config entry for a VM, with its key, user and a `ProxyJump` through the gateway, so tools such as VS Code Remote-SSH, Mutagen and rsync can reach it by name. It prints only; pvm still never writes to `~/.ssh`.
- **Exit egress probe in `pvm exit test`.** The test only probes the agent itself, so it passes while a VPS firewall drops forwarded traffic. Add a probe that opens a connection to the internet through the tunnel.
- **Gateway rule drift false positive.** The gateway installs `to <server>/32 iif lo fwmark 0 lookup main`. `ip rule` omits a zero fwmark, so the drift check never matches and re-adds the rule every 10 seconds, logging `restored missing rule` each time.
