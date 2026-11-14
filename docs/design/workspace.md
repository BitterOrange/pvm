# Workspaces

Status: proposed, not implemented. Planned in phases; see [Phases](#phases).

## Motivation

Large repositories are fastest when they live on the VM's own ext4 disk: package installs, builds, `git status` and file watchers all run at native speed. Today that means copying files in by hand. Shared directories (`/share`, `pvm mount`) keep the files on the host, so every metadata operation crosses the virtio-9p boundary, and the native Windows backend has no shared directories at all, because QEMU builds virtio-9p only for Linux, macOS and FreeBSD hosts.

A workspace is a directory that pvm manages between one host folder and one VM. Its mode decides where the primary copy lives and how changes move.

## Modes

| Mode | Primary copy | Speed inside the VM | Getting changes back to the host |
|---|---|---|---|
| `mount` | host | remote file system, slow for many small files | not needed, there is one copy |
| `sync` | both sides | native ext4 | `pvm workspace pull`, with conflict detection |
| `copy` | VM | native ext4 | `pvm workspace clone` |

### mount

The host folder appears at the guest path, and both sides see the same files.

- On Linux, macOS and the WSL2 backend, mount mode uses the existing virtio-9p path behind `pvm mount`, with the same profiles and limits.
- On the native Windows backend, mount mode uses reverse sshfs:
  - pvm.exe serves SFTP for the workspace folder;
  - `sshfs` in the guest runs in passive mode over the existing host-to-VM SSH connection.
  - The VM never opens a connection to the host, so the gateway's isolation rules stay as they are.
- The guest needs `sshfs`, and pvm installs it on first use.
- The daemon keeps the connection alive and remounts after it drops, so the mount point does not hang.
- sshfs caching is disabled by default, so host edits show up promptly.
- Changes made on the host do not raise inotify events in the guest. Development servers that watch files need polling, for example `CHOKIDAR_USEPOLLING=1`. `pvm workspace create` prints this note.

### sync

Both sides keep a full copy, and changes move only when the user asks.

- `push` copies host changes to the VM. `pull` copies VM changes to the host.
- Each successful push or pull records a baseline manifest. The manifest lists every file's path, type, size, modification time, SHA-256 and executable bit.
- Changes are found by a three-way comparison of the baseline, the host and the VM:
  - a path that changed on one side only is copied to the other side; this includes deletions;
  - a path that changed on both sides since the baseline is a conflict.
- On any conflict, push and pull stop before writing anything and list the conflicting paths. The user resolves them with one of these options:
  - `--prefer host` or `--prefer vm` overwrites the conflicting paths with that side's version;
  - `--keep-both` keeps both versions; the other side's version is written as `<name>.pvm-conflict-<host|vm>`.
- `pvm workspace status` shows what changed on each side since the baseline, and which paths conflict.
- For a git repository, committed work is usually easier to move with git itself. Sync mode is for directories outside git and for uncommitted work.

### copy

`pvm workspace create` uploads the host folder once. After that the VM copy is independent: nothing syncs back automatically, and later host changes are not uploaded. `pvm workspace clone` downloads the VM copy when the work is done.

## Commands

```
pvm workspace create <id> <vm> --mode mount|sync|copy [--path DIR] [--read-only] [-y]
pvm workspace ls
pvm workspace status <id>
pvm workspace push <id> [--prefer host|vm | --keep-both]
pvm workspace pull <id> [--prefer host|vm | --keep-both]
pvm workspace clone <id> <dest-dir>
pvm workspace ssh <id> [command...]
pvm workspace rm <id> [--delete-guest] [-y]
```

- `--mode` is required.
- `--path` defaults to the current directory, and `create` prints the resolved path before it uploads anything.
- `--read-only` applies to mount mode only and is enforced on the host side.
- `push`, `pull` and `status` apply to sync mode only. In any other mode they fail and name the right command.
- `clone` works in every mode. It downloads the VM copy into `<dest-dir>`, which must not exist or must be empty; clone never overwrites files. In mount mode the files are already on the host, so clone just copies them into the new directory.
- `ssh` opens `pvm ssh <vm>` and starts in the workspace directory.
- `rm`:
  - unmounts the workspace and deletes its metadata;
  - never deletes host files;
  - keeps the guest copy unless `--delete-guest` is passed;
  - in sync mode, asks for confirmation when the VM has changes that have not been pulled.

## Paths and metadata

- A workspace id matches `^[a-z0-9][a-z0-9-]{0,62}$`. Ids are unique per host, and each workspace is bound to one VM.
- The guest path is `/workspace/<id>`, for example `/workspace/hello-world`. `/workspace` and every workspace directory are owned by the VM's login user.
- Metadata lives in `~/.pvm/workspaces/<id>.json` and records:
  - the VM;
  - the mode;
  - the host path;
  - the guest path;
  - the location of the baseline manifest, in sync mode.
- Every write uses `fsutil.AtomicWrite`.

## Transfer

- Every transfer runs over the existing `pvm ssh` path through the gateway, as a tar stream, which handles many small files much better than one request per file.
- Upload (`create` in sync or copy mode, and `push`):
  - pvm.exe builds the tar stream with `archive/tar`;
  - the guest extracts it into a staging directory under `/workspace/.pvm-staging/`, then renames it into place.
  - An interrupted upload leaves the previous contents untouched.
- Download (`pull` and `clone`):
  - the guest runs `tar -c`;
  - pvm validates every entry before writing it; see [Security](#security).
  - Clone writes into a temporary directory next to `<dest-dir>` and renames it into place when complete.
  - Pull writes each file to a temporary file and renames it into place. An interrupted pull can be rerun, because the baseline is updated only after every path has been written.
- Before uploading, `create` checks the guest's free space. When the workspace does not fit, it fails and suggests `pvm resize`. `pvm resize` needs the VM stopped, and new VMs default to a 20 GB disk.

## Ignore rules

- Uploads and sync skip paths matched by the folder's `.gitignore` files, plus `node_modules`.
- A `.pvmignore` file in gitignore syntax adds patterns. `!pattern` re-includes a path that `.gitignore` excludes, such as a local `.env` that the VM needs.
- `.git` is transferred, so the VM copy is a working repository.
- In sync mode, run git commands on one side only; changes to `.git` on both sides surface as conflicts.

## Security

### Uploads: host to VM

`AGENTS.md` hard constraint 3 forbids sending the host's personal files into a VM. `create` refuses the following folders:

- the user's home directory, a drive or file system root, or any ancestor of the home directory;
- `~/.pvm`, or any folder inside it;
- a folder whose tree contains a credential directory such as `.ssh`, `.gnupg`, `.aws`, `.kube` or `.docker`. Excluding that directory with `.pvmignore` lifts the refusal.

`create` also prints the file count and total size. Above 1 GB it asks for confirmation; `-y` skips the prompt.

### Downloads: VM to host

The VM controls these contents, so pvm treats every entry as untrusted and writes through an `os.Root` opened on the destination. pvm rejects, and reports, any entry that:

- is an absolute path, or contains `..`;
- is a device, FIFO or socket;
- is a symlink that is absolute or points outside the workspace. Where the host cannot create symlinks, they are skipped and listed;
- uses a name Windows does not allow, such as `CON`, `aux.c`, or a name containing `:`;
- differs from another entry only by case, on a case-insensitive host file system.

After writing, pvm lists changed files that tools on the host may execute: `.git/hooks/*`, `.git/config`, `.vscode/*.json` and `.envrc`.

### Mount mode on native Windows

- The SFTP server resolves every request through an `os.Root` opened on the workspace folder.
- Read-only mounts reject writes, deletes and renames on the server side.
- Tests must cover the Windows-specific ways out of the folder:
  - directory junctions;
  - alternate data streams (`name:stream`);
  - reserved device names;
  - 8.3 short names;
  - case-insensitive names.

### Failures

`AGENTS.md` hard constraint 6 requires failures to be visible:

- every partial failure names the paths it affected and the command to retry;
- no command reports success after skipping files, unless it lists the skipped paths.

## VM lifecycle

- Commands that need the guest fail when the VM is stopped, and print `pvm start <vm>`.
- `pvm rm <vm>` lists the workspaces bound to the VM in its confirmation prompt, and warns about sync and copy workspaces whose VM copy has not been cloned or pulled since its last change.
  - After the VM is deleted, those workspaces are kept as orphans. `pvm workspace rm` cleans them up.
- `pvm clone <vm>` does not duplicate workspace metadata. The cloned disk still contains `/workspace`, so the files are there, but the new VM has no workspace entries.

## Phases

1. `copy` mode, plus `clone`, `ls`, `ssh` and `rm`: the transfer, ignore and security layers that the later phases reuse.
2. `sync` mode: baseline manifest, `status`, `push`, `pull` and conflict handling.
3. `mount` mode:
   - first the virtio-9p backend on Linux, macOS and WSL2;
   - then reverse sshfs on native Windows, with the SFTP server in pvm.exe.

## Non-goals

- Real-time two-way sync. Tools such as Mutagen already do this well over SSH; [`pvm ssh-config`](../roadmap.md#candidates) would make them easy to point at a VM.
- History or versioning. That is what git is for.
- One workspace shared by several VMs.
