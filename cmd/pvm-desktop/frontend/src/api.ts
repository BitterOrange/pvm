// The typed edge to the Go backend. Field names are the json tags of
// internal/desktop; the Backend interface is checked against the Go
// methods by TestFrontendAPI.

export interface State {
  backend: string; // native or wsl
  daemon_healthy: boolean;
  daemon_stopped: boolean;
  app_version: string;
  daemon_version: string;
  accel: string;
  tcg_fallback: number;
  data_dir: string;
  host_cpus: number; // the host's logical processors
  qemu: QEMUState;
  marker: string;
}

// QEMUState is read without the daemon. ready is false until a default
// build is set; the app then shows its QEMU page.
export interface QEMUState {
  ready: boolean;
  pending_adoption: boolean;
  default: string;
  default_label: string;
  problem: string; // why VMs cannot start now, or ""
  blocked: string[];
  recommended: QEMURecommended;
  official: QEMUOfficial;
}

export interface QEMURecommended {
  id: string; // "" when this pvm pins no build for the host
  size_mb: number;
  installed: boolean;
}

export interface QEMUOfficial {
  found: boolean;
  label: string;
  version: string;
  path: string;
  problem: string; // how to install it, when not found
  registered: boolean;
}

export interface QEMUBuild {
  id: string;
  label: string;
  source: "project" | "official";
  version: string;
  path: string;
  default: boolean;
  virtio_9p: boolean;
  withdrawn: string;
}

export interface VM {
  name: string;
  image: string;
  state: "stopped" | "starting" | "ready" | "failed";
  stage: string;
  hint: string;
  cpus: number;
  mem_mb: number;
  disk_gb: number;
  exit: string;
  exit_own: boolean;
  default: boolean;
}

export interface CatalogApp {
  id: string;
  name: string;
  description: string;
  homepage: string;
  source: string; // official or community
  window: boolean;
  start: string;
}

export interface Exit {
  name: string;
  kind: "agent" | "warp";
  endpoint: string;
  tz: string;
  locale: string;
  default: boolean;
  vms: string[];
  rules: string[];
}

export interface Workspace {
  id: string;
  vm: string;
  mode: "mount" | "sync" | "copy";
  host_path: string;
  guest_path: string;
  read_only: boolean;
  last_transfer: string; // RFC 3339, or empty
}

export interface Settings {
  default_vm: string;
  daemon_at_login: boolean;
  app_at_login: boolean;
}

export interface Image {
  spec: string;
  golden: boolean;
}

export interface CreateDefaults {
  user: string;
  cpus: number;
  mem_mb: number;
  disk_gb: number;
  exit: string;
}

// Sent, an empty field leaves that setting as it is; read, every field
// is named.
export interface AppConfig {
  clipboard: string; // off, to-guest, to-host or both
  drop: string; // on or off
  audio: string; // off, out or both
  audio_silence: string; // skip or send
  render_cpus: string; // vCPUs for Chromium renderers, 0 no limit
  scale: string; // 1, 1.25, 1.5 or 2: how much the host enlarges the windows
  keyring: string; // off or host
}

export interface CreateRequest {
  name: string;
  image: string;
  user: string;
  cpus: number;
  mem_mb: number;
  disk_gb: number;
  exit: string;
  app_config: AppConfig;
  apps: string[];
}

export interface Resources {
  cpus: number;
  mem_mb: number;
  disk_gb: number;
}

export interface AddExitRequest {
  name: string;
  invite: string;
  server: string;
  token: string;
}

export interface CreateWorkspaceRequest {
  id: string;
  vm: string;
  mode: string;
  host_path: string;
  read_only: boolean;
  confirmed: boolean;
}

export interface Step {
  command: string;
  state: "pending" | "running" | "done" | "failed";
}

export interface Task {
  id: number;
  title: string;
  steps: Step[];
  done: boolean;
  ok: boolean;
  error: string;
  remaining: string[];
}

export interface TaskOutput {
  id: number;
  step: number;
  line: string;
}

// A read, or an action the backend refused, rejects with an error
// string. An action that ran resolves with its Task; ok says how it went.
// start, where a method takes it, says the user agreed to start a stopped
// VM: the task starts it, or waits for one that is starting, before `pvm app`.
export interface Backend {
  State(): Promise<State>;
  VMs(): Promise<VM[]>;
  InstalledApps(vm: string): Promise<CatalogApp[]>;
  AppSettings(vm: string): Promise<AppConfig>;
  Catalog(): Promise<CatalogApp[]>;
  Exits(): Promise<Exit[]>;
  DefaultExit(): Promise<string>;
  Workspaces(): Promise<Workspace[]>;
  Settings(): Promise<Settings>;
  Images(): Promise<Image[]>;
  CreateDefaults(): Promise<CreateDefaults>;
  PickFolder(): Promise<string>;
  QEMUBuilds(): Promise<QEMUBuild[]>;
  PickQEMUArchive(): Promise<string>;
  CheckQEMUAgain(): Promise<void>;

  StartDaemon(): Promise<Task>;
  StopDaemon(confirmed: boolean): Promise<Task>;
  StartVM(name: string): Promise<Task>;
  StopVM(name: string): Promise<Task>;
  DeleteVM(name: string, confirmed: boolean): Promise<Task>;
  RunApp(name: string, target: string, start: boolean): Promise<Task>;
  RunCatalogApp(name: string, id: string, start: boolean): Promise<Task>;
  OpenFiles(name: string, start: boolean): Promise<Task>;
  ConfigureApps(name: string, config: AppConfig): Promise<Task>;
  InstallApps(name: string, ids: string[]): Promise<Task>;
  SetDefaultVM(name: string): Promise<Task>;
  ClearDefaultVM(): Promise<Task>;
  ResizeVM(name: string, resources: Resources, restart: boolean): Promise<Task>;
  SetVMExit(name: string, via: string, confirmed: boolean): Promise<Task>;
  SetDefaultExit(via: string): Promise<Task>;
  CreateVM(request: CreateRequest): Promise<Task>;
  AddExit(request: AddExitRequest): Promise<Task>;
  AddWarpExit(name: string): Promise<Task>;
  TestExit(name: string, speed: boolean): Promise<Task>;
  RemoveExit(name: string, confirmed: boolean): Promise<Task>;
  RunWorkspaceApp(id: string, target: string, start: boolean): Promise<Task>;
  OpenWorkspaceFiles(id: string, start: boolean): Promise<Task>;
  SyncWorkspace(id: string, direction: "push" | "pull"): Promise<Task>;
  CreateWorkspace(request: CreateWorkspaceRequest): Promise<Task>;
  RemoveWorkspace(id: string, confirmed: boolean): Promise<Task>;
  SetDaemonAtLogin(on: boolean): Promise<Task>;
  InstallQEMU(): Promise<Task>;
  InstallQEMUFromFile(path: string): Promise<Task>;
  UseOfficialQEMU(): Promise<Task>;
  UseQEMU(id: string): Promise<Task>;
  RemoveQEMU(id: string, confirmed: boolean): Promise<Task>;

  OpenTerminal(name: string): Promise<void>;
  OpenWorkspaceTerminal(id: string): Promise<void>;
  OpenWorkspaceFolder(id: string): Promise<void>;
  SetAppAtLogin(on: boolean): Promise<void>;
}

interface Runtime {
  EventsOn(name: string, callback: (data: never) => void): () => void;
  WindowIsMinimised(): Promise<boolean>;
  ClipboardSetText(text: string): Promise<boolean>;
  BrowserOpenURL(url: string): void;
}

declare global {
  interface Window {
    go: { desktop: { App: Backend } };
    runtime: Runtime;
  }
}

// Bridge is all the page needs from its host: the bound methods, the two
// events and two services of the window.
export interface Bridge {
  backend(): Backend;
  onTask(callback: (task: Task) => void): void;
  onTaskOutput(callback: (output: TaskOutput) => void): void;
  windowIsMinimised(): Promise<boolean>;
  copyText(text: string): Promise<boolean>;
  openURL(url: string): void;
}

let bridge: Bridge = {
  backend: () => window.go.desktop.App,
  onTask: (callback) => void window.runtime.EventsOn("task", callback),
  onTaskOutput: (callback) => void window.runtime.EventsOn("task-output", callback),
  windowIsMinimised: () => window.runtime.WindowIsMinimised(),
  copyText: (text) => window.runtime.ClipboardSetText(text),
  openURL: (url) => window.runtime.BrowserOpenURL(url),
};

// bound is false where no Wails window carries the page, as under
// `npm run dev` in a browser.
export const bound = (): boolean => "go" in window;

// connect puts another host in place of the Wails bindings: mock.ts.
export function connect(other: Bridge): void {
  bridge = other;
}

export const backend = (): Backend => bridge.backend();

export const onTask = (callback: (task: Task) => void): void => bridge.onTask(callback);

export const onTaskOutput = (callback: (output: TaskOutput) => void): void => bridge.onTaskOutput(callback);

export const windowIsMinimised = (): Promise<boolean> => bridge.windowIsMinimised();

export const copyText = (text: string): Promise<boolean> => bridge.copyText(text);

export const openURL = (url: string): void => bridge.openURL(url);

// errorText reads a rejected call: the backend rejects with a string.
export function errorText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
