# VM apps as host windows

Status: implemented on master, not yet released. Tested on native Windows with WHPX; macOS and Linux hosts are untested.

## Motivation

`pvm desktop` shows the whole XFCE desktop in a browser tab through noVNC. Two things were missing:

- Single applications as their own host windows: a web page in the VM's Firefox as a web app, and ordinary X11 programs such as an editor. The page renders in the VM, so cookies, fingerprint and egress stay with the VM and its exit; the host only receives pixels.
- Latency and clipboard. Pointer feedback in `pvm desktop` lagged by hundreds of milliseconds, and noVNC carries text only.

## Measurements

Measured with `pvm __probe-rtt` on native Windows with WHPX:

| Path | 64 B round trip p50 / p99 | Throughput |
|---|---|---|
| Inside the guest, loopback | 0.09 / 0.36 ms | - |
| Host relay, gateway VM, user VM (port maps, old desktop) | 1.4 / 2.5 ms | about 12 MB/s |
| `pvm.stream` | 0.6 / 1.6 ms | 171 MB/s guest to host, 41 MB/s host to guest |

Small messages were never slow and there were no 200 ms Nagle stalls. The port map path is throughput bound: a 1 MiB reply took 85 ms, so every large framebuffer update in `pvm desktop` cost tens to hundreds of milliseconds.

## The `pvm.stream` channel

Every VM has a second virtio-serial port, `pvm.stream`, next to the agent's. It carries multiplexed binary streams with per-stream credit flow control (`internal/stream`).

- Only the host opens streams, by naming a target. The guest agent connects only targets on its allowlist: `novnc` (the desktop's websockify), `xpra:<uid>` (that user's Xpra socket), `clip:<uid>:<kind>` (sets that user's clipboard), `clipout:<uid>:files` (copies the files on that user's clipboard) and three measurement targets. The guest cannot open a stream, reach host addresses or reach the network through it.
- The agent runs as root, so it connects a user socket only when the runtime directory and the socket belong to that uid, are not symlinks and are closed to other users.
- The daemon keeps one session per VM, because QEMU's chardev socket accepts one host connection. A handshake skips bytes left on the port by an earlier host connection, so a daemon restart recovers while the VM runs.
- RAM snapshots record whether the VM had the port. A VM restored from an older snapshot runs without it until its next cold boot.

`pvm desktop` uses it too: a daemon listener forwards to the `novnc` target, so the desktop no longer needs a port map or the gateway VM. The listener keeps its port across daemon restarts.

## Engine: Xpra

Xpra was the only option on a native Windows host that gives per-window host windows, text and image clipboard both ways and file drops, and sends only damaged regions.

Rejected:

- RDP RemoteApp: no working Linux server.
- Waypipe: no Windows client.
- ssh -X to an X server on Windows: weak isolation, text-only clipboard.
- SPICE, Selkies, KasmVNC, Guacamole and noVNC: whole desktops in a browser tab.

The guest runs Xpra 6.5 from xpra.org's APT repository, pinned to the 6.5 line, with the signing key fingerprint checked. Golden images include it, and `pvm app` installs it on first use in older VMs. The host needs the matching Windows client, 6.5.x. The Windows installers include it in `%LOCALAPPDATA%\pvm\xpra`: the release job downloads a pinned zip from xpra.org (`script/windows/fetch-xpra-client.sh`), checks its SHA256 and its signature by the same key as the APT repository, and ships it with its GPL text, a note on where to get the source, and four Python files of pvm added (see [Input methods](#input-methods) and [Sound](#sound)).

### Session model

- One Xpra server per VM, on display `:100`, as the VM user, run as the user systemd unit `pvm-xpra`. Linger keeps it running without a login session.
- Every app is its own user unit `pvm-app-<id>` on that display. Stopping an app stops its whole process tree; stopping the last one stops the server.
- Apps in one VM share the display, so they can copy and drag between each other inside the VM.
- Web apps are windows of one Firefox per VM (the user unit `pvm-webapps`) with one profile, so they share logins, cookies and cache; Firefox allows one process per profile. The profile is separate from the desktop's Firefox. It hides the tab strip and toolbars, skips first-run, terms and onboarding pages, and opens links meant for a new tab in a new window. Popups, which Firefox marks `popup-window`, keep their read-only address bar, without the bookmark star, so a sign-in window shows where it is.
- pvm records each web app's X window and tracks it with wmctrl: `pvm app stop` closes that window, `--kiosk` makes it full screen, and the shared Firefox stops with the last web app.
- The server listens only on a unix socket under `$XDG_RUNTIME_DIR/pvm`, mode 0700, with password authentication. The password is per VM: the host copy is in `~/.pvm/vms/<vm>/app/xpra.secret` and it reaches the guest over ssh stdin, followed by `xpra_start.py`, the script the server starts through. The script once travelled in the ssh command line, which was cut off at about 8 KB; the cut script started no server and still exited 0. A test keeps the scripts that still travel there under 6000 bytes.
- The host client connects to a daemon loopback listener that forwards to the `xpra:<uid>` target. Window titles end in `[<vm>]`.

### Encoding

Server: `--speed=100 --min-speed=80 --quality=80 --min-quality=50 --encodings=rgb,webp,jpeg,png --video-encoders=none`. The client repeats the minimums, because its defaults override the server's.

Measured on a 1280x993 Firefox canvas animation, with the server reporting:

| Setup | Frame latency | Frames per second | Guest Xpra CPU |
|---|---|---|---|
| No video encoder, client OpenGL on | 58 ms | 22 | 18% |
| vpx | 55 ms | 22 | 64% |
| openh264 | 66 ms | 22 | 36% |
| No video encoder, client OpenGL off | 43 ms | 50 | - |

The video encoders gave no gain under WHPX, where the guest has no AVX, and cost CPU. Client OpenGL painting on Windows held the frame rate down, because the server paces updates by client acknowledgements. The Windows client runs with `--opengl=no`; it used about 9% of one core.

For small updates (a terminal printing every 30 ms) the frame latency was 10 to 15 ms at 105 fps.

## Input methods

Text a host input method commits, such as Chinese from Microsoft Pinyin, reaches the Xpra server as a key event with keycode 0, the character as its string and a name such as `U+4F60`. The server only presses keycodes its keymap already has, and the keymap it builds from the client fills every keycode, so it dropped most of these characters. Some it mistook for keys: Xpra parses the name `U+FF08` as the X keysym 0xff08, which is BackSpace, so the full-width parenthesis Microsoft Pinyin types for Shift+9 deleted a character. Measured with Xpra 6.5.5's own name parsing: `U+FF08` gives BackSpace, `U+FF09` Tab, `U+FF1B` Escape, and `U+FFE5` (the full-width yen sign) Caps_Lock.

pvm starts the server through `xpra_start.py`, which takes such events before the keymap is asked. Xpra already runs `ibus-daemon` on the display and starts apps with `GTK_IM_MODULE`, `QT_IM_MODULE` and `XMODIFIERS` set to ibus. The script registers an ibus engine of its own inside the server process and makes it the global engine. The engine lets every key through and commits the text into the focused app's input context in one step: no keycode is borrowed, and modifiers still held, such as Shift for the parenthesis, do not touch it. ibus-daemon asks the server process for the engine before it answers the request that selects it, so the request is made asynchronously. Measured on native Windows with WHPX: after the change, Shift+9 in Chinese mode typed `（` in Claude Desktop.

When no app with ibus has the focus, or before the engine is registered, the script types the text with `xdotool type`. xdotool needs one empty keycode to borrow for each character, so keycode 254 (`XF86WWAN`) is cleared for it. The characters of one commit arrive milliseconds apart and are typed in one call, 40 ms apart; shorter gaps lost characters, as applications look the borrowed keycode up after the event. Before the engine, all text went this way. Typing `nihao` and `zhongwen` with Microsoft Pinyin into a GTK text box, most commits arrived intact, but one run lost a character from the first commit and showed stray Latin characters; the cause was not confirmed. Plain keys are unchanged.

The host input method's composition and candidate windows opened at the top left of the app's window, as the client did not know where the app's text cursor was. The engine also receives the focused input context's cursor rectangle (`SetCursorLocation`, in screen coordinates). The script sends it, relative to the focused window, as the window metadata `pvm-caret`; a stock client stores the unknown key and does nothing with it. The Windows installers add `xpra/client/gui/window_base.py` to the client. On `pvm-caret` it converts the rectangle to the window's device pixels and calls `ImmSetCompositionWindow` and `ImmSetCandidateWindow` on the window, with `CFS_EXCLUDE` so that the candidates never cover the line. It also moves a hidden system caret there for input methods that follow the caret instead. Measured: Microsoft Pinyin's candidates opened at the text cursor in gedit, Firefox and Claude Desktop.

GTK programs and Firefox report their cursor whenever it moves. Chromium, and so Electron apps such as Claude Desktop, hands its cursor to the input method only while it handles a key event: `InputMethodContextImplGtk::SetCursorLocation` only stores the rectangle, and `DispatchKeyEvent` passes it on before the key. The keys of a host input method never reach the guest, so after a click, a view switch or a committed word the candidates stayed where the last key had left the cursor. The script now presses and releases `Control_R`, or `Control_L` if the keymap lacks it, through XTest 50 ms, 300 ms and 1 s after a left button release, after an input context takes the focus, and after committed text. Chromium hands over its cursor before it passes the key on, and the engine eats the key, so the app never sees it. A focus that arrives while a press is pending schedules no new presses, so a press can never start a loop. Measured in Claude Desktop: after a click into text, the reported cursor stayed at the last typed position; a press 20 to 200 ms after the click reported the clicked position to within a character. With the presses in place, the candidates followed clicks, view switches and committed words. A Chromium app still does not report a cursor that moves without a click, a focus change or committed text, as when the text scrolls. GTK programs report it then too.

The key was first `XF86Launch9`, a key no keyboard has. Clicking a message in Claude Desktop then drew a blue focus ring around it. Blink counts every key press without Control, Alt or Meta held as keyboard use (`UpdateHadKeyboardEvent`), and an element a page focuses itself, as Claude Desktop does on a click, then gets its ring. Chromium also hands every key to the input method, modifiers included, and a key the input method eats still reaches the page later as a "Process" key with the same modifier flags. Measured: with the nudge keys taken out of the keymap, a click on a message drew no ring; then pressing Control drew none and pressing Shift drew one. So the nudge is Control: it still makes Chromium report its cursor, and it does not count as keyboard use.

Synthetic Unicode input (`SendInput` with `KEYEVENTF_UNICODE`, used by some on-screen keyboards and automation tools) still does not arrive: the Xpra Windows client turns it into an empty key before it sends it.

Characters outside the Basic Multilingual Plane, such as emoji from the Windows emoji panel, reach GTK on Windows as two key events, one per UTF-16 surrogate. The Xpra 6.5.5 client then failed to encode the lone surrogate, dropped the connection and lost every window. The Windows installers add `xpra/client/gui/keyboard_helper.py` to the client (`script/windows/xpra-patch`), which Python loads before the compiled module of the same name; it runs the original and joins the two halves into one key event, which the server types like other committed text. Measured: an emoji from the emoji panel arrived in gedit as `U+1F924`, and the client stayed connected. Clients installed some other way still drop the connection.

## Window size limits

Firefox's maximize button could not be clicked on Windows hosts. GTK gives Firefox's window a maximum size of 16366x16337. The Xpra Windows client removes the maximize button of any window whose maximum size is below 32767. `xpra_start.py` wraps Xpra's `sanitize_size_hints`, before the window model imports it, so that a maximum size larger than any screen, at least 8192 on both sides, is dropped before the server passes the window's size hints on.

## Window scale

`pvm app config <vm> --scale 1|1.25|1.5|2`, 1 by default, becomes the client's `--desktop-scaling`. The server's screen and windows shrink by the scale and the client enlarges them, so the VM draws fewer pixels: 64% of them at 1.25, 44% at 1.5 and 25% at 2. Xpra also lowers the DPI it gives the VM by the same factor, from 120 to 96 at 1.25 on a host at 125%. Claude Desktop then drew at 1x without a restart, so its text kept its size on the host and only looked softer. Xpra's own default, `on`, scales nothing, so pvm passes the option only above 1.

The scale is the client's, so a running client keeps the one it started with. A window that is already open keeps its size in the VM, so on the host it would change size by the same factor: at 1.25 a window 1781 pixels wide became 2226, and one that no longer fitted the screen's height was shrunk by the client to some size of its own. So when the scale changes while a client is connected, the daemon first resizes and moves every window that is neither maximized nor fullscreen by the old scale over the new one, with `wmctrl -lG`, `xprop` and `xdotool`, and the CLI then attaches the client again. Measured from 1.25 to 1.5: Claude Desktop went from 1354x788 to 1128x657 in the VM, and stayed 1711x1032 on the host.

`pvm app config` attaches a connected client again after any change to the client's settings, the clipboard, drops, audio, silence or scale, so they apply at once. `pvm app attach` with a client connected starts a new one; the server takes the new client and the old one exits, its clipboard bridge with it. The windows disappear and come back once.

Measured with Claude Desktop in a 10-vCPU WHPX VM on a host at 125%, with the host window 1761x1048 each time, so that Claude laid out the same page at every scale. A script scrolled a long conversation up and down, 25 wheel steps a second for 8.5 seconds; the frames are the window's damage events from the server's `xpra info`, read from the host. Three runs each:

| Scale | Window in the VM | DPI | Frames a second | Claude and Xpra, in cores | Xpra server | QEMU |
|---|---|---|---|---|---|---|
| 1 | 1761x1048 | 120 | 15 to 17 | 2.3 | 0.9 | 3.2 to 3.4 |
| 1.25 | 1409x838 | 96 | about 28 | 1.8 to 2.1 | 0.8 to 0.9 | 3.0 to 3.4 |
| 1.5 | 1174x699 | 80 | 22 to 24 | 1.9 to 2.0 | 0.7 | 3.0 to 3.1 |
| 2 | 880x524 | 60 | 26 to 29 | 2.2 to 2.4 | 1.1 | 3.1 to 3.4 |

- Scrolling saturates the VM, so fewer pixels show as more frames rather than less CPU. 1.25 drew about 1.7 times as many frames as 1 with a little less CPU.
- 1.5 and 2 drew no more than 1.25. Unconfirmed: once a frame has fewer pixels, the per-frame work, Claude's script and layout and Xpra's handling of each update, sets the pace; and below a device scale of 1, as at 0.8 and 0.625 here, Chromium may draw less efficiently. On a host at 125%, 1.25 is the scale at which Chromium draws at exactly 1x.
- With nothing changing, every scale was idle: about 0.2 cores and no frames.
- A first run with an artifact preview open in Claude saw no gain at any scale. Its pulsing status dots changed a small area about 50 times a second, which cost 2 cores in the VM whatever the scale: the cost of such updates is per frame, not per pixel.

## Sound

The server runs its own PulseAudio (`xpra-audio-server`) with `--speaker=on --microphone=on`. The guest session runs a PulseAudio of its own, with only a null sink, which apps would otherwise use; the server script puts `PULSE_SERVER` for the server's in the user manager's environment, which app units inherit, and starts the server without it, as its PulseAudio refuses to start while one is named. The client decides, from `pvm app config <vm> --audio`:

- `out` (default): `--speaker=on --microphone=no`; VM sound plays on the host's default output device.
- `both`: also `--microphone=on`; the host's default input device records into the VM's default source.
- `off`: neither.

Both directions use Opus. Measured on a Windows host: a 440 Hz tone played in the VM with `paplay` was recorded from the host's output device through WASAPI loopback at about 440 Hz, and a recording in the VM picked up the host's default microphone. Before `PULSE_SERVER` was set, apps played into the session's null sink and the host heard nothing.

The Windows client follows a change of the default output device, such as plugging in headphones, by restarting the speaker. It did nothing for the input device: the microphone kept recording the device that was the default when forwarding started. After a headset was unplugged, that device was gone and the VM heard nothing until the client was attached again. After one was plugged in, the VM kept hearing the built-in microphone. The Windows installers add `xpra/client/subsystem/audio.py` to the client. While the microphone is on, it watches for device changes with the client's own `IMMNotificationClient` monitor. One second after a change, as one plug sends several notifications, it restarts the microphone if the default input device is another one or its capture has stopped. A change of output device alone does not restart it. Measured: after unplugging a headset and plugging it in again, the client logged a microphone restart one second after each speaker restart, and the VM heard the headset's microphone again.

Sound cost the most while nothing played. Every audio buffer crossed several processes in the VM: the capture process, its pipe to the server, the server's protocol threads, the guest agent and the virtio channel, and the microphone came the other way. Each step woke a thread on another vCPU, and with `kernel-irqchip=off` each such wakeup is an IPI that exits to QEMU. Measured in a 10-vCPU VM with Claude Desktop open, nothing playing and `--audio both`: about 1,700 function-call IPIs and 1,700 timer interrupts a second, and QEMU used about one host core. With the client's sound off, the IPIs fell to 50 and the timer interrupts to 270 a second, and QEMU used about 20% of a core. ftrace put nearly all of those wakeups in the audio processes, PulseAudio, Xpra's protocol threads and the agent.

Three changes followed. The measurements were taken on one server, attaching the client again between them.

- **Fewer, larger packets.** Xpra starts its audio processes through `xpra_start.py` too (`XPRA_AUDIO_COMMAND`). There the capture reads PulseAudio every 50 ms instead of about 10, and encodes 60 ms Opus frames instead of 20. The client the installers ship does the same for the microphone (`xpra/audio/src.py`). The VM's sound reaches the host about 80 ms later, and the microphone reaches the VM about 40 ms later.
- **Silence.** `pvm app config <vm> --audio-silence skip|send`, `skip` by default: while the VM plays nothing, nothing is sent.
  - GStreamer's `cutter` passes nothing after half a second below -70 dB, which only digital silence reaches, and when a sound starts it passes the 0.1 s before it too.
  - It does not pass on the formats the encoder takes, so the source picked 44.1 kHz, which Opus refuses, and the capture stopped. A caps filter before it fixes 48 kHz.
  - Xpra's own handling of cutter's messages is turned off: they reach it late, and it would cut the start of a sound.
  - pvm writes the setting to `$XDG_RUNTIME_DIR/pvm/audio-silence` whenever a client attaches, and the audio processes read it when they start. So it applies once the client is attached again, which `pvm app config` does.
- **Results, in function-call IPIs a second and QEMU's share of one host core:**
  - 20 ms frames: 1,500 to 1,900 IPIs, 85% to 135%.
  - 60 ms frames: about 1,280 IPIs, 63% to 94%.
  - Silence skipped as well: about 1,250 IPIs, 75% to 77%.
  - 50 ms reads as well: about 800 IPIs, 54% to 69%.
  - Once frames were 60 ms, skipping silence saved little more. Most of the remaining wakeups came from the microphone. The client patch cut its packets by about 30%; `opus+ogg` already groups packets into pages.
- **Listening.** A sequence of short tones with 0.8 s gaps, which close the gate, sounded the same as with 0.15 s gaps, which keep it open. No start was cut.

Not done: pinning PulseAudio and the audio processes to one vCPU turns most of their wakeups into local ones; in one test it halved the IPIs of the old setup.

## Startup

GTK programs started through D-Bus activation services that need the display, such as the desktop portal's GTK backend. The user manager had no `DISPLAY`, so the backend exited, the portal hung until its start timed out, and gedit and Thunar took 50 to 60 seconds to show a window. The server script now puts `DISPLAY` in the activation environment and clears the failed units; both open in about one second.

Apps such as Claude Desktop keep secrets in the Secret Service. gnome-keyring locks it until a desktop login unlocks it, and the app display has no login, so a keyring dialog asked for a password on every start. When the server starts, `keyring_unlock.py` creates a `login` keyring with an empty password if there is none, makes it the default, and unlocks it, as a passwordless login would. Its contents are then stored unencrypted, readable only by the VM user. A keyring the user gave a password is left locked. Measured in a VM: the keyring was locked after a reboot and unlocked once `pvm app` started the server, and a secret was stored and read back without a prompt.

The first app in a VM installs what came after the VM's Xpra: `gtk3-nocsd`, `xpra-audio-server` and the Noto fonts for all major scripts and color emoji (about 25 MB). Packages the release lacks do not block, and an install that fails leaves a warning. `pvm app` shows the install's output.

## Clipboard

Measured: the Xpra Windows client sends the host clipboard to the server even while none of its windows has focus, so anything copied on the host, including passwords, would enter the VM. pvm therefore splits the directions:

- Guest to host: Xpra's own clipboard, `--clipboard-direction=to-client` on both ends. Text and images.
- Host to guest: pvm's bridge, `pvm app __clipboard`, started with the client. It sends the host clipboard only when one of the VM's windows comes to the front, and when the clipboard changes while it is there. It skips content the Xpra client or the bridge put on the clipboard, which came from the guest, and content marked `ExcludeClipboardContentFromMonitorProcessing` by password managers. Text and images (PNG, or DIB converted to PNG) go to the `clip` target, which sets the guest clipboard with xclip. Windows hosts only so far.

Files, which neither Xpra nor xclip carries, go through the same bridge on Windows hosts, as a tar of folders and regular files, up to 256 MB and 20000 entries; links and special files are left out.

- Host to guest: files copied in Explorer (`CF_HDROP`) go to `clip:files`. The agent unpacks them as the VM user into a new folder under `~/.cache/pvm/clipboard`, removing the previous copy, and `clip_files.py` owns the clipboard with them as `x-special/gnome-copied-files` (Thunar, file dialogs), `text/uri-list` (browsers, Electron) and their paths as text. xclip offers one target only, and GTK's API for several is not available from Python.
- Guest to host: when guest content reaches the host clipboard, and when focus leaves the VM's windows, the bridge opens `clipout:files`. The agent answers with a fingerprint of the files on the clipboard, by path, size and time; the bridge asks for them only when that changed, so a host copy made since is not overwritten by the same files. It unpacks them under `vms/<vm>/app/clipboard` in pvm's data, keeping only the latest copy, and puts them on the host clipboard as `CF_HDROP`, owned by a message-only window. Names Windows cannot hold, reserved device names and paths leaving the folder are skipped. Files that came from the host are not offered back.

`pvm app config <vm> --clipboard off|to-guest|to-host|both` sets the policy per VM; the default is `off`. The daemon refuses `clip` streams unless the policy allows host to guest, and `clipout` streams unless it allows guest to host.

## Drops

Xpra uses one setting for both directions of file transfer. The server always accepts uploads (`--file-transfer=yes --open-files=no --file-size-limit=100M`), and the client decides:

- `--drop off` (default): the client runs with `--file-transfer=no`, so drops do nothing and files the guest sends are refused.
- `--drop on`: the client runs with `--file-transfer=ask`. Files dropped on a VM window upload into `~/Downloads` in the VM and are never opened. A file the guest sends waits for the user to accept it in a host dialog.

A dropped file lands in the VM's download folder, not in the application under the pointer; Xpra does not support that yet. Dragging out of the VM is not supported.

## Commands

```bash
pvm app dev1 -- gedit                      # an X11 program
pvm app dev1 https://mail.example.com      # a web app; --kiosk for full screen
pvm app dev1 --name notes --no-attach -- gedit notes.txt
pvm app ls [dev1]
pvm app attach dev1                        # reopen the windows after closing the client
pvm app stop dev1 notes | --all
pvm app config dev1 --clipboard both --drop on --audio both
```

Closing a host window closes that app in the guest. Quitting the client detaches without stopping apps. `pvm stop` and `pvm rm` stop the VM's Xpra server first, so the client hears it shut down and closes its windows; before, it kept reconnecting with the windows left on the host. A VM restart ends its apps; `pvm app ls` reports them as exited once.

## Not done

- macOS and Linux hosts: the client starts, but the clipboard bridge, which carries host content into the VM and files either way, is Windows only.
- WSL2 backend: not supported.
- Drops into a specific application, and dragging out of the VM.
- Firefox memory caps per web app.
