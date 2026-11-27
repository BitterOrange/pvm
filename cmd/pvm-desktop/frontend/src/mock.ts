// A made-up backend for `npm run dev`: the page in a browser, without a
// Wails window, pvm or Windows. main.ts loads it only under the dev
// server; a build leaves it out. It keeps its state in memory and plays
// each action as a task with steps and output, the way internal/desktop
// does, so every screen can be seen at work.
//
// ?mock=<scene> in the address opens another state: loading, error, wsl,
// stopped, down (the daemon is started once by itself), empty, orphan
// (the default exit was removed), noqemu (no QEMU set up yet), installing
// (as noqemu, with a slow download), pending (an older pvm's QEMU waits to
// be taken over) and blocked (the default build failed its check).

import {
  connect,
  type AppConfig,
  type Backend,
  type CatalogApp,
  type CreateRequest,
  type Exit,
  type Image,
  type QEMUBuild,
  type Resources,
  type Settings,
  type State,
  type Task,
  type TaskOutput,
  type VM,
  type Workspace,
} from "./api";

const scene = new URLSearchParams(location.search).get("mock") ?? "";
const empty = scene === "empty";

const sleep = (ms: number): Promise<void> => new Promise((done) => window.setTimeout(done, ms));
const never = <T>(): Promise<T> => new Promise<T>(() => {});

interface Machine {
  name: string;
  image: string;
  cpus: number;
  mem_mb: number;
  disk_gb: number;
  state: VM["state"];
  stage: string;
  hint: string;
  // route is the VM's own exit; "" follows the default
  route: string;
}

// The app settings of each VM; one without an entry has pvm's defaults.
const appSettings: Record<string, AppConfig> = { apptest: { clipboard: "both", drop: "off", audio: "out", audio_silence: "skip", render_cpus: "2", scale: "1", keyring: "host" } };

const brokenHint = "provisioning stopped: the vm could not reach its exit my-warp; test the exit, then start broken again";

let machines: Machine[] = empty
  ? []
  : [
      { name: "dev1", image: "ubuntu:26.04", cpus: 4, mem_mb: 4096, disk_gb: 20, state: "ready", stage: "", hint: "", route: "" },
      { name: "dev2", image: "debian:13", cpus: 2, mem_mb: 2048, disk_gb: 20, state: "stopped", stage: "", hint: "", route: "my-warp" },
      { name: "apptest", image: "ubuntu:24.04", cpus: 4, mem_mb: 8192, disk_gb: 40, state: "starting", stage: "ssh", hint: "", route: "my-exit" },
      { name: "broken", image: "debian:12", cpus: 2, mem_mb: 4096, disk_gb: 20, state: "failed", stage: "", hint: brokenHint, route: "my-warp" },
    ];

interface ExitNode {
  name: string;
  kind: Exit["kind"];
  endpoint: string;
  tz: string;
  locale: string;
  rules: string[];
}

let nodes: ExitNode[] = empty
  ? []
  : [
      { name: "my-exit", kind: "agent", endpoint: "vps.example.com:18443", tz: "Asia/Tokyo", locale: "ja_JP.UTF-8", rules: ["domain example.com"] },
      { name: "my-warp", kind: "warp", endpoint: "", tz: "America/Los_Angeles", locale: "en_US.UTF-8", rules: [] },
    ];
if (scene === "orphan") {
  nodes = nodes.filter((n) => n.name !== "my-exit");
}
let defaultExit = empty ? "direct" : "my-exit";

let workspaces: Workspace[] = empty
  ? []
  : [
      { id: "my-project", vm: "dev1", mode: "mount", host_path: "C:\\Users\\me\\projects\\my-project", guest_path: "/workspace/my-project", read_only: false, last_transfer: "" },
      { id: "notes", vm: "dev2", mode: "sync", host_path: "C:\\Users\\me\\notes", guest_path: "/workspace/notes", read_only: false, last_transfer: new Date(Date.now() - 12 * 60000).toISOString() },
    ];

const settings: Settings = { default_vm: empty ? "" : "dev1", daemon_at_login: true, app_at_login: false };

const noQEMU = scene === "noqemu" || scene === "installing";
const tag = "11.1.3-pvm1";

const projectBuild = (): QEMUBuild => ({
  id: tag,
  label: tag + " (project build, QEMU 11.1.3)",
  source: "project",
  version: "11.1.3",
  path: "C:\\Users\\me\\.pvm\\runtime\\qemu\\builds\\" + tag + "\\x86_64\\bin\\qemu-system-x86_64.exe",
  default: true,
  virtio_9p: false,
  withdrawn: "",
});

let qemuBuilds: QEMUBuild[] = noQEMU || scene === "pending" ? [] : [projectBuild()];

const checkFailed = tag + " failed its check: bin/qemu-system-x86_64.exe differs from SHA256SUMS; reinstall: pvm qemu rm " + tag + " -y && pvm qemu install " + tag + " --use";
const pending = "C:\\Users\\me\\.pvm\\runtime\\qemu\\x86_64 holds a QEMU from an older pvm that has not been taken over yet; restart the daemon (pvm daemon restart) or run pvm qemu ls";

const state: State = {
  backend: scene === "wsl" ? "wsl" : "native",
  daemon_healthy: scene !== "stopped" && scene !== "down",
  daemon_stopped: scene === "stopped",
  app_version: "v1.4.0",
  daemon_version: "v1.4.1",
  accel: "whpx",
  tcg_fallback: 1,
  data_dir: "C:\\Users\\me\\.pvm",
  host_cpus: 8,
  qemu: {
    ready: !noQEMU,
    pending_adoption: scene === "pending",
    default: qemuBuilds.length > 0 ? tag : "",
    default_label: qemuBuilds.length > 0 ? qemuBuilds[0].label : "",
    problem: scene === "blocked" ? checkFailed : scene === "pending" ? pending : "",
    blocked: scene === "blocked" ? ["cluster 1: " + checkFailed] : [],
    recommended: { id: tag, size_mb: 72, installed: qemuBuilds.length > 0 },
    official: noQEMU
      ? { found: false, label: "", version: "", path: "", problem: "QEMU from MSYS2 UCRT64 is not installed in C:\\msys64", registered: false }
      : { found: true, label: "QEMU from MSYS2 UCRT64", version: "11.1.0-1", path: "C:\\msys64\\ucrt64\\bin\\qemu-system-x86_64.exe", problem: "", registered: false },
  },
  marker: "pvm-desktop:qemu-builds/1",
};

// setDefault makes a build the default, as pvm qemu use does.
function setDefault(b: QEMUBuild): void {
  qemuBuilds = [...qemuBuilds.filter((x) => x.id !== b.id).map((x) => ({ ...x, default: false })), { ...b, default: true }];
  state.qemu = { ...state.qemu, ready: true, pending_adoption: false, default: b.id, default_label: b.label, problem: "", blocked: [] };
  state.qemu.recommended.installed = qemuBuilds.some((x) => x.id === tag);
  state.qemu.official.registered = qemuBuilds.some((x) => x.id === "official");
}

function installQEMU(command: string, download: boolean): Promise<Task> {
  const lines = [
    ...(download ? ["downloading pvm-qemu-" + tag + "-windows-amd64.tar.gz", ...[10, 30, 50, 70, 90, 100].map((p) => "  " + p + "%")] : []),
    "verifying pvm-qemu-" + tag + "-windows-amd64.tar.gz",
    "checking " + tag,
    "installed " + tag + " (QEMU 11.1.3) and made it the default",
  ];
  return runTask("Install QEMU", [said("PVM_BACKEND=native " + command, lines, () => setDefault(projectBuild()), scene === "installing" ? 2500 : 400)]);
}

const images: Image[] = [
  { spec: "ubuntu:26.04", golden: true },
  { spec: "ubuntu:24.04", golden: false },
  { spec: "debian:13", golden: true },
  { spec: "debian:12", golden: true },
];

const catalog: CatalogApp[] = [
  { id: "claude-code", name: "Claude Code", description: "Anthropic's coding agent for the terminal", homepage: "", source: "official", window: false, start: "claude" },
  { id: "claude-desktop", name: "Claude Desktop", description: "Anthropic's desktop app (Linux beta)", homepage: "", source: "official", window: true, start: "claude-desktop" },
  { id: "codex", name: "Codex CLI", description: "OpenAI's coding agent for the terminal", homepage: "", source: "official", window: false, start: "codex" },
];

const installed: Record<string, string[]> = { dev1: ["claude-code", "claude-desktop"] };

const taskListeners: ((task: Task) => void)[] = [];
const outputListeners: ((output: TaskOutput) => void)[] = [];
let ids = 0;

type Say = (line: string) => void;

// A step plays its output and its effect; throwing fails it.
interface Step {
  command: string;
  play(say: Say): Promise<void>;
}

const publish = (task: Task): void => taskListeners.forEach((f) => f(structuredClone(task)));

// runTask runs the steps in order and stops at the first that fails, as
// runTask in internal/desktop does.
async function runTask(title: string, steps: Step[]): Promise<Task> {
  const task: Task = { id: ++ids, title, steps: steps.map((s) => ({ command: s.command, state: "pending" })), done: false, ok: false, error: "", remaining: [] };
  for (const [i, step] of steps.entries()) {
    task.steps[i].state = "running";
    publish(task);
    try {
      await step.play((line) => outputListeners.forEach((f) => f({ id: task.id, step: i, line })));
    } catch (e) {
      task.steps[i].state = "failed";
      task.done = true;
      task.error = e instanceof Error ? e.message : String(e);
      task.remaining = steps.slice(i + 1).map((s) => s.command);
      publish(task);
      return structuredClone(task);
    }
    task.steps[i].state = "done";
  }
  task.done = task.ok = true;
  publish(task);
  return structuredClone(task);
}

// said is a step that prints its lines over a while, then takes effect.
function said(command: string, lines: string[], effect: () => void = () => {}, ms = 500): Step {
  return {
    command,
    async play(say) {
      for (const line of lines) {
        await sleep(ms);
        say(line);
      }
      await sleep(ms);
      effect();
    },
  };
}

const machine = (name: string): Machine => {
  const m = machines.find((x) => x.name === name);
  if (!m) {
    throw new Error("vm " + name + " does not exist");
  }
  return m;
};

// startStep starts a VM and follows its stages, as the app's own wait
// does. broken never gets past provisioning.
function startStep(name: string, pace = 1300): Step {
  return {
    command: "pvm start " + name,
    async play(say) {
      const m = machine(name);
      m.hint = "";
      for (const stage of ["boot", "provision", "ssh", "agent"]) {
        m.state = "starting";
        m.stage = stage;
        say(name + ": starting (" + stage + ")");
        await sleep(pace);
        if (machine(name).state !== "starting") {
          throw new Error(name + " stopped before it was ready; see `pvm logs " + name + "`");
        }
        if (name === "broken" && stage === "provision") {
          m.state = "failed";
          m.stage = "";
          m.hint = brokenHint;
          throw new Error(name + " failed: " + brokenHint);
        }
      }
      m.state = "ready";
      m.stage = "";
      say(name + ": ready");
    },
  };
}

// waitStep follows a VM that is starting already, as the step that only
// waits does in internal/desktop.
function waitStep(name: string): Step {
  return {
    command: "pvm start " + name,
    async play(say) {
      let shown = "";
      for (;;) {
        const m = machine(name);
        if (m.state === "ready") {
          say(name + ": ready");
          return;
        }
        if (m.state === "failed") {
          throw new Error(name + " failed: " + m.hint);
        }
        if (m.state === "stopped") {
          throw new Error(name + " stopped before it was ready; see `pvm logs " + name + "`");
        }
        if (m.stage !== "" && m.stage !== shown) {
          shown = m.stage;
          say(name + ": starting (" + m.stage + ")");
        }
        await sleep(300);
      }
    },
  };
}

// startFirst is what a task given start runs before `pvm app`: a start
// for a stopped VM, a wait for a starting one, nothing for the rest.
function startFirst(name: string, start: boolean): Step[] {
  const m = machines.find((x) => x.name === name);
  if (!start || !m) {
    return [];
  }
  return m.state === "stopped" ? [startStep(name)] : m.state === "starting" ? [waitStep(name)] : [];
}

// appStep is `pvm app`, which refuses a VM that is not running.
function appStep(name: string, args: string): Step {
  return {
    command: "pvm app " + name + args,
    async play() {
      await sleep(500);
      if (machine(name).state === "stopped") {
        throw new Error("vm " + name + " is not running; pvm start " + name);
      }
    },
  };
}

const appArgs = (target: string): string => (/^https?:\/\/\S+$/.test(target.trim()) ? " " : " -- ") + target.trim();

function configFlags(c: AppConfig): string[] {
  return [c.clipboard && "--clipboard " + c.clipboard, c.drop && "--drop " + c.drop, c.audio && "--audio " + c.audio, c.audio_silence && "--audio-silence " + c.audio_silence, c.render_cpus && "--render-cpus " + c.render_cpus, c.scale && "--scale " + c.scale, c.keyring && "--keyring " + c.keyring].filter((f): f is string => f !== "");
}

const configure = (name: string, c: AppConfig): Step =>
  said("pvm app config " + name + " " + configFlags(c).join(" "), ["app settings saved"], () => {
    const now = (appSettings[name] ??= { clipboard: "off", drop: "off", audio: "out", audio_silence: "skip", render_cpus: "2", scale: "1", keyring: "off" });
    appSettings[name] = { clipboard: c.clipboard || now.clipboard, drop: c.drop || now.drop, audio: c.audio || now.audio, audio_silence: c.audio_silence || now.audio_silence, render_cpus: c.render_cpus || now.render_cpus, scale: c.scale || now.scale, keyring: c.keyring || now.keyring };
  });

// resizeSteps passes `pvm resize` only what differs, around a stop and a
// start when the VM runs, as ResizeVM in internal/desktop does.
function resizeSteps(m: Machine, r: Resources): Step[] {
  const flags = [r.disk_gb !== m.disk_gb && String(r.disk_gb), r.cpus !== m.cpus && "--cpus " + r.cpus, r.mem_mb !== m.mem_mb && "--mem " + r.mem_mb].filter(
    (f): f is string => f !== false,
  );
  const lines = [
    r.cpus !== m.cpus && m.name + ": cpus set to " + r.cpus,
    r.mem_mb !== m.mem_mb && m.name + ": memory set to " + r.mem_mb + " MB",
    r.disk_gb !== m.disk_gb && m.name + ": disk resized to " + r.disk_gb + "G",
  ].filter((l): l is string => l !== false);
  const resize = said("pvm resize " + m.name + " " + flags.join(" "), lines, () => Object.assign(m, r));
  if (m.state === "stopped") {
    return [resize];
  }
  const stop = said("pvm stop " + m.name, ["stopped " + m.name], () => {
    m.state = "stopped";
    m.stage = m.hint = "";
  });
  return [stop, resize, startStep(m.name)];
}

function installStep(name: string, apps: string[]): Step {
  const lines = apps.flatMap((id) => [id + ": downloading", id + ": installing", "installed " + id]);
  return said("pvm app install " + name + " " + apps.join(" "), lines, () => (installed[name] = [...new Set([...(installed[name] ?? []), ...apps])]), 600);
}

function createSteps(r: CreateRequest): Step[] {
  const golden = images.find((i) => i.spec === r.image)?.golden ?? true;
  const flags = configFlags(r.app_config);
  return [
    said(
      "pvm create --name " + r.name + " --image " + r.image + " --user " + r.user + " --cpus " + r.cpus + " --mem " + r.mem_mb + " --disk " + r.disk_gb + " --no-start",
      [...(golden ? [] : ["no prepared image for " + r.image + " yet", "downloading the base image", "baking the prepared image", "prepared " + r.image]), "created " + r.name],
      () => machines.push({ name: r.name, image: r.image, cpus: r.cpus, mem_mb: r.mem_mb, disk_gb: r.disk_gb, state: "stopped", stage: "", hint: "", route: "" }),
      golden ? 500 : 900,
    ),
    ...(r.exit === "" ? [] : [said("pvm route " + r.name + " --via " + r.exit, [r.name + " -> " + r.exit], () => (machine(r.name).route = r.exit))]),
    ...(flags.length === 0 ? [] : [configure(r.name, r.app_config)]),
    startStep(r.name),
    ...(r.apps.length === 0 ? [] : [installStep(r.name, r.apps)]),
  ];
}

const one = (title: string, command: string, lines: string[] = [], effect?: () => void): Promise<Task> => runTask(title, [said(command, lines, effect)]);

const refuse = (text: string): Promise<never> => Promise.reject(text);

const backend: Backend = {
  State: () => (scene === "loading" ? never() : scene === "error" ? refuse("pvm.exe is not next to PVM Desktop (C:\\Users\\me\\pvm); run the pvm installer again") : Promise.resolve({ ...state })),
  VMs: async () =>
    machines
      .map((m) => ({
        name: m.name,
        image: m.image,
        state: m.state,
        stage: m.stage,
        hint: m.hint,
        cpus: m.cpus,
        mem_mb: m.mem_mb,
        disk_gb: m.disk_gb,
        exit: m.route === "" ? defaultExit : m.route,
        exit_own: m.route !== "",
        default: m.name === settings.default_vm,
      }))
      .sort((a, b) => a.name.localeCompare(b.name)),
  InstalledApps: async (vm) => {
    await sleep(400);
    return catalog.filter((app) => (installed[vm] ?? []).includes(app.id));
  },
  AppSettings: async (vm) => {
    await sleep(250);
    machine(vm);
    return { ...(appSettings[vm] ?? { clipboard: "off", drop: "off", audio: "out", audio_silence: "skip", render_cpus: "2", scale: "1", keyring: "off" }) };
  },
  Catalog: async () => catalog,
  Exits: async () =>
    nodes.map((n) => ({
      name: n.name,
      kind: n.kind,
      endpoint: n.endpoint,
      tz: n.tz,
      locale: n.locale,
      default: defaultExit === n.name,
      vms: machines.filter((m) => (m.route === "" ? defaultExit : m.route) === n.name).map((m) => m.name).sort(),
      rules: n.rules,
    })),
  DefaultExit: async () => defaultExit,
  Workspaces: async () => structuredClone(workspaces),
  Settings: async () => ({ ...settings }),
  Images: async () => images,
  CreateDefaults: async () => ({ user: "pvm", cpus: 4, mem_mb: 4096, disk_gb: 20, exit: defaultExit }),
  PickFolder: async () => "C:\\Users\\me\\projects\\my-project",
  QEMUBuilds: async () => structuredClone(qemuBuilds),
  PickQEMUArchive: async () => "C:\\Users\\me\\Downloads\\pvm-qemu-" + tag + "-windows-amd64.tar.gz",
  CheckQEMUAgain: async () => {
    await sleep(300);
  },
  InstallQEMU: () => installQEMU("pvm qemu install --use -y", true),
  InstallQEMUFromFile: (path) => installQEMU("pvm qemu install --file " + path + " --use -y", false),
  UseOfficialQEMU: () => {
    const o = state.qemu.official;
    if (!o.found) {
      return refuse(o.problem);
    }
    const b: QEMUBuild = { id: "official", label: "QEMU from MSYS2 UCRT64, QEMU 11.1.0", source: "official", version: "11.1.0", path: o.path, default: true, virtio_9p: false, withdrawn: "" };
    return one("Use the official QEMU", "PVM_BACKEND=native pvm qemu use official", ["using " + b.label + " (" + o.path + ")"], () => setDefault(b));
  },
  UseQEMU: (id) => {
    const b = qemuBuilds.find((x) => x.id === id);
    return b ? one("Use QEMU " + id, "PVM_BACKEND=native pvm qemu use " + id, ["using " + id], () => setDefault(b)) : refuse("QEMU build " + id + " is not installed");
  },
  RemoveQEMU: (id, confirmed) => {
    if (!confirmed) {
      return refuse("removing QEMU build " + id + " was not confirmed");
    }
    if (state.qemu.default === id) {
      return refuse(id + " is the default; pick another first: pvm qemu use <build>|official");
    }
    return one("Remove QEMU " + id, "PVM_BACKEND=native pvm qemu rm " + id + " -y", ["removed " + id], () => {
      qemuBuilds = qemuBuilds.filter((x) => x.id !== id);
      state.qemu.recommended.installed = qemuBuilds.some((x) => x.id === tag);
      state.qemu.official.registered = qemuBuilds.some((x) => x.id === "official");
    });
  },

  StartDaemon: () =>
    one("Start the daemon", "pvm daemon start", ["daemon started"], () => {
      state.daemon_healthy = true;
      state.daemon_stopped = false;
    }),
  StopDaemon: () =>
    one("Stop the daemon", "pvm daemon stop", ["daemon stopped"], () => {
      state.daemon_healthy = false;
      state.daemon_stopped = true;
    }),
  StartVM: (name) => runTask("Start " + name, [startStep(name)]),
  StopVM: (name) =>
    one("Stop " + name, "pvm stop " + name, ["stopped " + name], () => {
      const m = machine(name);
      m.state = "stopped";
      m.stage = m.hint = "";
    }),
  DeleteVM: (name, confirmed) =>
    confirmed ? one("Delete " + name, "pvm rm " + name + " -y", ["deleted " + name], () => (machines = machines.filter((m) => m.name !== name))) : refuse("deleting " + name + " was not confirmed"),
  RunApp: (name, target, start) => runTask("Open an app in " + name, [...startFirst(name, start), appStep(name, appArgs(target))]),
  RunCatalogApp: (name, id, start) => {
    const app = catalog.find((a) => a.id === id);
    if (!app) {
      return refuse('unknown app "' + id + '"');
    }
    return app.window
      ? runTask("Open " + app.name + " in " + name, [...startFirst(name, start), appStep(name, " -- " + app.start)])
      : refuse(app.name + " runs in a terminal");
  },
  OpenFiles: (name, start) => runTask("Open files in " + name, [...startFirst(name, start), appStep(name, " -- thunar")]),
  ConfigureApps: (name, config) => (configFlags(config).length === 0 ? refuse("nothing to change") : runTask("App settings of " + name, [configure(name, config)])),
  InstallApps: (name, apps) => runTask("Install programs in " + name, [installStep(name, apps)]),
  SetDefaultVM: (name) => one("Default vm: " + name, "pvm default-vm " + name, [], () => (settings.default_vm = name)),
  ClearDefaultVM: () => one("Clear the default vm", "pvm default-vm --clear", [], () => (settings.default_vm = "")),
  ResizeVM: (name, r, restart) => {
    const m = machines.find((x) => x.name === name);
    if (!m) {
      return refuse("vm " + name + " does not exist");
    }
    if (r.disk_gb < m.disk_gb) {
      return refuse("the disk of " + name + " is " + m.disk_gb + " GB and can only grow");
    }
    if (r.cpus === m.cpus && r.mem_mb === m.mem_mb && r.disk_gb === m.disk_gb) {
      return refuse("nothing to change");
    }
    return m.state === "stopped" || restart ? runTask("Resize " + name, resizeSteps(m, r)) : refuse(name + " is running, and restarting it to resize was not confirmed");
  },
  SetVMExit: (name, via, confirmed) =>
    confirmed
      ? one("Exit of " + name + ": " + via, "pvm route " + name + " --via " + via, [name + " -> " + via], () => (machine(name).route = via))
      : refuse("changing the exit of " + name + " was not confirmed; a running vm restarts"),
  SetDefaultExit: (via) => one("Default exit: " + via, "pvm route default --via " + via, ["default -> " + via], () => (defaultExit = via)),
  CreateVM: (r) => (machines.some((m) => m.name === r.name) ? refuse("vm " + r.name + " already exists") : runTask("Create " + r.name, createSteps(r))),
  AddExit: (r) =>
    one("Add exit " + r.name, "pvm exit add " + r.name + " --server " + r.server + " --token-file <token file>", ["probing " + r.server, "added " + r.name], () =>
      nodes.push({ name: r.name, kind: "agent", endpoint: r.server, tz: "", locale: "", rules: [] }),
    ),
  AddWarpExit: (name) =>
    one("Add exit " + name, "pvm exit add-warp " + name, ["registering a WARP device", "added " + name], () =>
      nodes.push({ name, kind: "warp", endpoint: "", tz: "", locale: "", rules: [] }),
    ),
  TestExit: (name, speed) => one("Test exit " + name, "pvm exit test " + name + (speed ? " --speed" : ""), ["handshake: ok", "dns: ok", ...(speed ? ["down 182 Mbit/s, up 41 Mbit/s"] : []), name + ": ok"]),
  RemoveExit: (name, confirmed) =>
    confirmed ? one("Remove exit " + name, "pvm exit rm " + name, ["removed " + name], () => (nodes = nodes.filter((n) => n.name !== name))) : refuse("removing exit " + name + " was not confirmed"),
  RunWorkspaceApp: (id, target, start) => {
    const ws = workspaces.find((w) => w.id === id);
    if (!ws) {
      return refuse("workspace " + id + " does not exist");
    }
    return runTask("Open an app in workspace " + id, [...startFirst(ws.vm, start), appStep(ws.vm, appArgs(target))]);
  },
  OpenWorkspaceFiles: (id, start) => {
    const ws = workspaces.find((w) => w.id === id);
    if (!ws) {
      return refuse("workspace " + id + " does not exist");
    }
    return runTask("Open files in workspace " + id, [...startFirst(ws.vm, start), appStep(ws.vm, " -- thunar " + ws.guest_path)]);
  },
  SyncWorkspace: (id, direction) =>
    one("Workspace " + id + ": " + direction, "pvm workspace " + direction + " " + id, [direction + ": 14 files"], () => {
      const ws = workspaces.find((w) => w.id === id);
      if (ws) {
        ws.last_transfer = new Date().toISOString();
      }
    }),
  CreateWorkspace: (r) =>
    one(
      "Create workspace " + r.id,
      "pvm workspace create " + r.id + " " + r.vm + " --mode " + r.mode + " --path " + r.host_path + (r.read_only ? " --read-only" : "") + (r.confirmed ? " -y" : ""),
      ["created " + r.id],
      () =>
        workspaces.push({
          id: r.id,
          vm: r.vm,
          mode: r.mode as Workspace["mode"],
          host_path: r.host_path,
          guest_path: "/workspace/" + r.id,
          read_only: r.read_only,
          last_transfer: r.mode === "mount" ? "" : new Date().toISOString(),
        }),
    ),
  RemoveWorkspace: (id, confirmed) =>
    confirmed ? one("Remove workspace " + id, "pvm workspace rm " + id + " -y", ["removed " + id], () => (workspaces = workspaces.filter((w) => w.id !== id))) : refuse("removing workspace " + id + " was not confirmed"),
  SetDaemonAtLogin: (on) =>
    on
      ? one("Start the daemon at login", "pvm daemon install", [], () => (settings.daemon_at_login = true))
      : one("Do not start the daemon at login", "pvm daemon uninstall", [], () => (settings.daemon_at_login = false)),

  OpenTerminal: async () => {},
  OpenWorkspaceTerminal: async () => {},
  OpenWorkspaceFolder: async () => {},
  SetAppAtLogin: async (on) => void (settings.app_at_login = on),
};

// past shows a task that ended before the page opened.
function past(title: string, steps: { command: string; state: "done" | "failed" | "pending"; lines?: string[] }[], error = ""): void {
  const id = ++ids;
  const failed = steps.findIndex((s) => s.state === "failed");
  publish({
    id,
    title,
    steps: steps.map((s) => ({ command: s.command, state: s.state })),
    done: true,
    ok: failed < 0,
    error,
    remaining: failed < 0 ? [] : steps.slice(failed + 1).map((s) => s.command),
  });
  steps.forEach((s, step) => (s.lines ?? []).forEach((line) => outputListeners.forEach((f) => f({ id, step, line }))));
}

// seed is what the page finds when it opens: a failed task, a finished
// one, and the create of apptest still waiting for the VM.
function seed(): void {
  if (empty || !state.daemon_healthy || state.backend !== "native" || scene === "loading" || scene === "error") {
    return;
  }
  past(
    "Create broken",
    [
      { command: "pvm create --name broken --image debian:12 --user pvm --cpus 2 --mem 4096 --disk 20 --no-start", state: "done", lines: ["created broken"] },
      { command: "pvm route broken --via my-warp", state: "done", lines: ["broken -> my-warp"] },
      { command: "pvm start broken", state: "failed", lines: ["broken: starting (boot)", "broken: starting (provision)"] },
      { command: "pvm app install broken claude-code codex", state: "pending" },
    ],
    "broken failed: " + brokenHint,
  );
  past("Start dev1", [{ command: "pvm start dev1", state: "done", lines: ["dev1: starting (boot)", "dev1: starting (provision)", "dev1: starting (ssh)", "dev1: starting (agent)", "dev1: ready"] }]);
  void runTask("Create apptest", [
    said("pvm create --name apptest --image ubuntu:24.04 --user pvm --cpus 4 --mem 8192 --disk 40 --no-start", ["created apptest"], undefined, 0),
    said("pvm route apptest --via my-exit", ["apptest -> my-exit"], undefined, 0),
    said("pvm app config apptest --clipboard both --audio out", ["app settings saved"], undefined, 0),
    {
      command: "pvm start apptest",
      async play(say) {
        ["apptest: starting (boot)", "apptest: starting (provision)", "apptest: starting (ssh)"].forEach(say);
        const m = machine("apptest");
        // long enough to look at the page while it waits
        await sleep(45000);
        if (m.state !== "starting") {
          throw new Error("apptest stopped before it was ready; see `pvm logs apptest`");
        }
        m.stage = "agent";
        say("apptest: starting (agent)");
        await sleep(4000);
        m.state = "ready";
        m.stage = "";
        say("apptest: ready");
      },
    },
    installStep("apptest", ["claude-code"]),
  ]);
}

export function install(): void {
  connect({
    backend: () => backend,
    onTask: (callback) => {
      taskListeners.push(callback);
      if (taskListeners.length === 1) {
        // once the page listens for output too
        window.setTimeout(seed);
      }
    },
    onTaskOutput: (callback) => void outputListeners.push(callback),
    windowIsMinimised: async () => document.hidden,
    copyText: (text) => navigator.clipboard.writeText(text).then(() => true),
    openURL: (url) => void window.open(url, "_blank", "noopener"),
  });
}
