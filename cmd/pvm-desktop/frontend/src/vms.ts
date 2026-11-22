import { backend, errorText, type AppConfig, type CatalogApp, type VM } from "./api";
import {
  badge,
  banner,
  button,
  callout,
  dialog,
  dot,
  empty,
  field,
  focusKey,
  h,
  iconButton,
  input,
  keyed,
  loading,
  mono,
  moreButton,
  redraw,
  select,
  setFieldError,
  stagger,
  tag,
  type Child,
} from "./dom";
import { t } from "./i18n";
import { icon, type IconName } from "./icons";
import * as prefs from "./prefs";
import { cpuControl, limits, number, outside, renderControl, scaleControl } from "./resources";
import { act, actStart, busy, data, memory, nav, onlyFollowed } from "./store";

const stateKind = { stopped: "", starting: "info", ready: "ok", failed: "danger" } as const;

// The stages a starting VM goes through, as the daemon names them.
const stageNames = ["boot", "provision", "ssh", "agent"];

// stageBars draws where a starting VM is: one bar per stage, with the
// names under them where there is room. A stage the list does not know
// draws nothing.
export function stageBars(stage: string, named = false): HTMLElement | null {
  const at = stageNames.indexOf(stage);
  if (at < 0) {
    return null;
  }
  return h(
    "span",
    {
      class: named ? "stages stages--labeled grow" : "stages",
      role: "img",
      "aria-label": t.common.stage(at + 1, stageNames.length, stage),
      "data-tip": named ? undefined : stageNames.join(", "),
    },
    ...stageNames.map((name, i) => h("span", { class: "stage", "data-state": i < at ? "done" : i === at ? "active" : "todo" }, named && name)),
  );
}

// exitChoices are the values `pvm route --via` takes.
export function exitChoices(): { value: string; label: string }[] {
  return [{ value: "direct", label: t.common.direct }, ...(data.exits ?? []).map((e) => ({ value: e.name, label: e.name }))];
}

export function exitDialog(name: string, current: string): void {
  const choices = exitChoices();
  const gone = !choices.some((c) => c.value === current);
  const via = select(choices, gone ? choices[0].value : current);
  dialog({
    title: t.vms.exitTitle(mono(name)),
    text: t.vms.exitNow(gone ? h("span", null, t.vms.exitRemoved(current)) : mono(current)),
    content: [
      gone && callout("danger", "alert", t.vms.exitGone(current, name)),
      field(t.vms.exitLabel, via, t.vms.exitHint),
      callout("warn", "note", t.vms.exitRestart(name)),
    ],
    submit: t.vms.exitSubmit,
    onSubmit: () => void act(["vm:" + name], () => backend().SetVMExit(name, via.value, true)),
  });
}

// The settings chosen from a list; render_cpus is a number and scale a
// slider of its own.
type ChoiceSetting = Exclude<keyof AppConfig, "render_cpus" | "scale">;

// appConfigChoices are the values `pvm app config` takes.
export function appConfigChoices(): Record<ChoiceSetting, { value: string; label: string }[]> {
  const c = t.appConfig;
  return {
    clipboard: [{ value: "off", label: c.off }, { value: "to-guest", label: c.toGuest }, { value: "to-host", label: c.toHost }, { value: "both", label: c.both }],
    drop: [{ value: "off", label: c.off }, { value: "on", label: c.on }],
    audio: [{ value: "out", label: c.audioOut }, { value: "both", label: c.audioBoth }, { value: "off", label: c.off }],
    audio_silence: [{ value: "skip", label: c.silenceSkip }, { value: "send", label: c.silenceSend }],
    keyring: [{ value: "off", label: c.keyringOff }, { value: "host", label: c.keyringHost }],
  };
}

// What the Create form starts from: the app settings of a VM that
// `pvm app config` has not been run for.
export const newAppConfig: AppConfig = { clipboard: "off", drop: "off", audio: "out", audio_silence: "skip", render_cpus: "2", scale: "1", keyring: "off" };


// programCard is a catalog program to tick. The name and the description
// come from the catalog as they are.
export function programCard(app: CatalogApp, checked: boolean): { box: HTMLInputElement; el: HTMLElement } {
  const box = h("input", { type: "checkbox", value: app.id });
  box.checked = checked;
  const source = app.source === "official" ? t.catalog.official : app.source === "community" ? t.catalog.community : app.source;
  const el = h(
    "label",
    { class: "pick" },
    box,
    h(
      "span",
      { class: "pick-head" },
      h("span", { class: "pick-title", lang: "en" }, app.name),
      tag(app.window ? t.catalog.window : t.catalog.terminal),
      tag(source, app.source !== "official"),
    ),
    h("span", { class: "pick-desc", lang: "en" }, app.description),
  );
  return { box, el };
}

// appDialog asks for a command line or a URL. programs, when given,
// loads the one-click entries shown under the field.
export function appDialog(
  title: Child[],
  text: Child[] | undefined,
  run: (target: string) => void,
  programs?: (list: HTMLElement, close: () => void) => void,
): void {
  const target = input({ class: "input input--mono", placeholder: t.vms.runPlaceholder });
  const list = h("div");
  const d = dialog({
    title,
    text,
    content: [
      field(t.vms.runLabel, target, t.vms.runHint),
      programs && h("div", { class: "field" }, h("span", { class: "label", id: "installed-programs" }, t.vms.installed), list),
    ],
    submit: t.vms.open,
    validate: () => {
      if (target.value.trim() !== "") {
        return true;
      }
      setFieldError(target, t.vms.runEmpty);
      return false;
    },
    onSubmit: () => run(target.value),
  });
  programs?.(list, d.close);
}

function vmAppDialog(vm: VM): void {
  const key = ["vm:" + vm.name];
  appDialog(
    t.vms.runTitle(mono(vm.name)),
    undefined,
    (target) => void act(key, () => backend().RunApp(vm.name, target)),
    (list, close) => {
      list.replaceChildren(loading());
      backend()
        .InstalledApps(vm.name)
        .then(
          (apps) => {
            if (apps.length === 0) {
              list.replaceChildren(h("p", { class: "hint" }, t.vms.noneInstalled));
              return;
            }
            // a terminal program is listed, not started: it would start in
            // ~, and a workspace terminal starts in the folder it works on
            const entries = apps.map((app) =>
              !app.window
                ? h(
                    "div",
                    { class: "menu-item menu-item--static" },
                    icon("terminal"),
                    h("span", null, h("span", { lang: "en" }, app.name), h("small", null, t.vms.inTerminal)),
                  )
                : h(
                    "button",
                    {
                      type: "button",
                      class: "menu-item",
                      "data-tip": app.description,
                      onclick: () => {
                        close();
                        void act(key, () => backend().RunCatalogApp(vm.name, app.id));
                      },
                    },
                    icon("window"),
                    h("span", { lang: "en" }, app.name),
                  ),
            );
            list.replaceChildren(h("div", { class: "dialog-list", role: "group", "aria-labelledby": "installed-programs" }, ...entries));
          },
          (e) => list.replaceChildren(h("p", { class: "field-error", role: "alert" }, t.vms.installedError, h("span", { lang: "en" }, errorText(e)))),
        );
    },
  );
}

function installDialog(vm: VM): void {
  const list = h("div", { class: "pick-grid pick-grid--stack", role: "group", "aria-labelledby": "dialog-title" }, loading());
  let boxes: HTMLInputElement[] = [];
  const chosen = () => boxes.filter((b) => b.checked).map((b) => b.value);
  const d = dialog({
    title: t.vms.installTitle(mono(vm.name)),
    text: [t.vms.installText],
    content: [list],
    submit: t.vms.installSubmit(0),
    submitState: () => ({ disabled: chosen().length === 0, label: t.vms.installSubmit(chosen().length) }),
    onSubmit: () => {
      const ids = chosen();
      void act(["vm:" + vm.name], () => backend().InstallApps(vm.name, ids));
    },
  });
  backend()
    .Catalog()
    .then(
      (catalog) => {
        const cards = catalog.map((app) => programCard(app, false));
        boxes = cards.map((c) => c.box);
        list.replaceChildren(...cards.map((c) => c.el));
        if (catalog.length === 0) {
          list.replaceChildren(h("p", { class: "hint" }, t.vms.catalogEmpty));
        }
      },
      (e) => {
        list.replaceChildren();
        d.fail(t.vms.catalogError + errorText(e));
      },
    );
}

// The settings are per VM and kept by the daemon, so the dialog reads
// them first and shows what is set now.
function settingsDialog(vm: VM): void {
  const choices = appConfigChoices();
  const controls: Record<ChoiceSetting, HTMLSelectElement> = {
    clipboard: select(choices.clipboard, newAppConfig.clipboard),
    drop: select(choices.drop, newAppConfig.drop),
    audio: select(choices.audio, newAppConfig.audio),
    audio_silence: select(choices.audio_silence, newAppConfig.audio_silence),
    keyring: select(choices.keyring, newAppConfig.keyring),
  };
  // the slider ends at the VM's vCPUs
  const render = renderControl("settings:render");
  render.setMost(vm.cpus);
  const scale = scaleControl("settings:scale");
  const fields = h("div", { class: "slot" }, loading());
  let now: AppConfig | null = null;
  // an empty field is left as it is: only what differs is sent
  const changed = (name: ChoiceSetting): string => (controls[name].value === now?.[name] ? "" : controls[name].value);
  // a setting the user did not move is left as it is, even one above
  // the VM's vCPUs, which the slider shows at its end
  const renderChanged = (): string => (!render.touched() || render.bad() || render.value() === now?.render_cpus ? "" : render.value());
  const change = (): AppConfig => ({
    clipboard: changed("clipboard"),
    drop: changed("drop"),
    audio: changed("audio"),
    audio_silence: changed("audio_silence"),
    keyring: changed("keyring"),
    render_cpus: renderChanged(),
    scale: scale.value() === now?.scale ? "" : scale.value(),
  });
  const d = dialog({
    title: t.vms.settingsTitle(mono(vm.name)),
    text: [t.vms.settingsText],
    content: [fields],
    submit: t.vms.apply,
    // a number out of range counts as a change, so that submitting says why
    submitState: () => ({ disabled: now === null || (Object.values(change()).join("") === "" && !render.bad()) }),
    validate: () => {
      const bad = render.bad();
      setFieldError(render.num, bad ? t.appConfig.renderCpusRange(vm.cpus) : "");
      return !bad;
    },
    onSubmit: () => void act(["vm:" + vm.name], () => backend().ConfigureApps(vm.name, change())),
  });
  backend()
    .AppSettings(vm.name)
    .then(
      (config) => {
        now = config;
        controls.clipboard.value = config.clipboard;
        controls.drop.value = config.drop;
        controls.audio.value = config.audio;
        controls.audio_silence.value = config.audio_silence;
        controls.keyring.value = config.keyring;
        render.load(config.render_cpus);
        scale.load(config.scale);
        fields.replaceChildren(
          field(t.appConfig.clipboard, controls.clipboard),
          field(t.appConfig.drop, controls.drop, ...t.vms.dropHint(mono("~/Downloads"))),
          field(t.appConfig.audio, controls.audio),
          field(t.appConfig.audioSilence, controls.audio_silence, t.appConfig.audioSilenceHint),
          scale.field,
          render.field,
          field(t.appConfig.keyring, controls.keyring, t.appConfig.keyringHint),
        );
        controls.clipboard.focus();
      },
      (e) => {
        fields.replaceChildren();
        d.fail(t.vms.settingsError + errorText(e));
      },
    );
}

function resizeDialog(vm: VM): void {
  const running = vm.state !== "stopped";
  const cpus = cpuControl("resize:cpus");
  cpus.set(vm.cpus);
  const mem = number({ min: limits.mem[0], max: limits.mem[1], value: vm.mem_mb });
  // the disk only grows
  const disk = number({ min: vm.disk_gb, max: limits.disk[1], value: vm.disk_gb });
  // memory and disk sit in one row and share the line under it
  const sizeError = h("p", { class: "field-error", role: "alert" });
  const wanted = () => ({ cpus: cpus.value(), mem_mb: Number(mem.value), disk_gb: Number(disk.value) });
  // the dialog has said that a running VM restarts
  const call = () => backend().ResizeVM(vm.name, wanted(), running);
  dialog({
    title: t.vms.resizeTitle(mono(vm.name)),
    text: [t.vms.resizeText],
    content: [
      running && callout("warn", "note", t.vms.resizeRestart(vm.name)),
      cpus.field,
      h(
        "div",
        { class: "form-grid form-grid--2" },
        field(t.resources.memory, mem),
        field(t.resources.disk, disk),
        h("div", { class: "field field--span field--tight" }, sizeError, h("p", { class: "hint" }, t.vms.diskGrows(vm.disk_gb))),
      ),
    ],
    submit: running ? t.vms.resizeRestartSubmit : t.vms.apply,
    submitState: () => {
      const w = wanted();
      return { disabled: w.cpus === vm.cpus && w.mem_mb === vm.mem_mb && w.disk_gb === vm.disk_gb };
    },
    validate: () => {
      const complaints: [HTMLInputElement, string][] = [
        [mem, outside(mem, ...limits.mem) ? t.resources.memoryRange(...limits.mem) : ""],
        [
          disk,
          !outside(disk, vm.disk_gb, limits.disk[1])
            ? ""
            : Number(disk.value) < vm.disk_gb && !outside(disk, ...limits.disk)
              ? t.vms.diskShrink(vm.disk_gb)
              : t.resources.diskRange(vm.disk_gb, limits.disk[1]),
        ],
      ];
      // the shared line says the first complaint, so that one is written last
      const bad = complaints.filter(([, text]) => text !== "");
      complaints.filter(([, text]) => text === "").forEach(([el]) => setFieldError(el, "", sizeError));
      bad.reverse().forEach(([el, text]) => setFieldError(el, text, sizeError));
      return bad.length === 0;
    },
    // a running VM is started again, and that wait can be ended from its row
    onSubmit: () => void (running ? actStart(vm.name, call) : act(["vm:" + vm.name], call)),
  });
}

function deleteDialog(vm: VM): void {
  dialog({
    title: t.vms.deleteTitle(mono(vm.name)),
    content: [h("p", null, ...t.vms.deleteText(h("strong", { lang: "en" }, vm.name)))],
    submit: t.vms.deleteSubmit(vm.name),
    submitIcon: "trash",
    danger: true,
    narrow: true,
    onSubmit: () => void act(["vm:" + vm.name], () => backend().DeleteVM(vm.name, true)),
  });
}

const sep = (): HTMLElement => h("span", { class: "sep", "aria-hidden": "true" }, "·");

// status is what decides how a VM's row looks, besides the VM itself.
function status(vm: VM): { working: boolean; waiting: boolean } {
  const working = busy.has("vm:" + vm.name);
  // a VM that is only being waited for can be stopped, looked at and
  // deleted, which ends the wait
  return { working, waiting: working && onlyFollowed(vm.name) && vm.state === "starting" };
}

function row(vm: VM): HTMLElement {
  const key = ["vm:" + vm.name];
  const { working, waiting } = status(vm);
  const locked = working && !waiting;
  const up = vm.state === "ready" || vm.state === "failed";
  const starting = vm.state === "starting";
  const k = (action: string) => "vm:" + vm.name + ":" + action;
  const needsUp = (name: string) => (up ? name : t.vms.startFirst);
  return h(
    "article",
    { class: "item", "data-busy": locked ? "true" : undefined },
    locked && h("div", { class: "busybar", role: "progressbar", "aria-label": t.common.workingOn(vm.name) }),
    h(
      "div",
      { class: "item-row" },
      h(
        "div",
        { class: "item-id" },
        h(
          "div",
          { class: "item-title" },
          h("span", { class: "glyph", "data-state": vm.state, "aria-hidden": "true" }),
          h("h2", { class: "item-name", lang: "en" }, vm.name),
          vm.default && badge(t.common.isDefault, "accent"),
        ),
        h(
          "p",
          { class: "item-sub" },
          h("span", { class: "mono" }, vm.image),
          h(
            "span",
            { class: "item-specs" },
            h("span", null, t.vms.cpus(vm.cpus)),
            sep(),
            h("span", null, t.vms.memory(memory(vm.mem_mb))),
            sep(),
            h("span", null, t.vms.disk(vm.disk_gb)),
          ),
        ),
      ),
      h(
        "div",
        { class: "item-meta" },
        h(
          "div",
          { class: "meta meta--status" },
          h("span", { class: "meta-value" }, badge(starting && vm.stage !== "" ? t.vms.startingAt(vm.stage) : t.vms.state[vm.state], stateKind[vm.state], dot(starting))),
          starting && stageBars(vm.stage),
        ),
        h(
          "div",
          { class: "meta meta--exit" },
          h("span", { class: "meta-value" }, icon("route", "sm"), h("span", { lang: "en", "data-tip": vm.exit }, vm.exit)),
          !vm.exit_own && h("span", { class: "meta-note" }, t.common.defaultExit),
        ),
      ),
      h(
        "div",
        { class: "item-actions" },
        vm.state === "stopped"
          ? button(t.vms.start, () => void actStart(vm.name, () => backend().StartVM(vm.name)), {
              kind: "primary",
              size: "sm",
              icon: "play",
              disabled: locked,
              key: k("power"),
            })
          : button(t.vms.stop, () => void act(key, () => backend().StopVM(vm.name)), { size: "sm", icon: "stop", disabled: locked, key: k("power") }),
        iconButton("terminal", t.vms.terminalIn(vm.name), () => void act([], () => backend().OpenTerminal(vm.name)), {
          tip: t.common.terminal,
          disabled: locked,
          key: k("terminal"),
        }),
        iconButton("window", t.vms.appIn(vm.name), () => vmAppDialog(vm), { tip: needsUp(t.common.app), disabled: working || !up, key: k("app") }),
        iconButton("files", t.vms.filesOf(vm.name), () => void act(key, () => backend().OpenFiles(vm.name)), {
          tip: needsUp(t.common.files),
          disabled: working || !up,
          key: k("files"),
        }),
        moreButton(
          t.common.moreFor(vm.name),
          [
            { label: t.vms.resize, icon: "cpu", action: () => resizeDialog(vm), disabled: waiting },
            { label: t.vms.appSettings, icon: "sliders", action: () => settingsDialog(vm), disabled: waiting },
            { label: t.vms.install, icon: "download", action: () => installDialog(vm), disabled: vm.state !== "ready" },
            { label: t.vms.changeExit, icon: "route", action: () => exitDialog(vm.name, vm.exit), disabled: waiting },
            { label: t.vms.setDefault, icon: "star", action: () => void act(key, () => backend().SetDefaultVM(vm.name)), disabled: waiting || vm.default },
            { label: t.vms.remove, icon: "trash", action: () => deleteDialog(vm), danger: true, divided: true },
          ],
          { disabled: locked, key: k("more") },
        ),
      ),
    ),
    vm.state === "failed" &&
      vm.hint !== "" &&
      h("div", { class: "item-note item-note--danger", role: "status" }, icon("alert", "sm"), h("span", { lang: "en" }, vm.hint)),
  );
}

// The list shows as rows or as cards; the choice is the window's own.
type Layout = "rows" | "cards";
let layout: Layout = prefs.load("vm-layout") === "cards" ? "cards" : "rows";

function layoutSwitch(): HTMLElement {
  const option = (value: Layout, name: IconName, label: string) =>
    h(
      "button",
      {
        type: "button",
        class: "seg-btn",
        "aria-pressed": String(layout === value),
        "aria-label": label,
        "data-tip": label,
        "data-key": "vms:layout:" + value,
        onclick: () => {
          layout = value;
          prefs.save("vm-layout", value === "rows" ? null : value);
          render();
          focusKey("vms:layout:" + value);
        },
      },
      icon(name),
    );
  return h("div", { class: "seg", role: "group", "aria-label": t.vms.layout }, option("rows", "rows", t.vms.rows), option("cards", "cards", t.vms.cards));
}

const head = h("header", { class: "page-head" });
const notice = h("div", { class: "slot" });
const rest = h("div", { class: "slot" });
const list = h("div", { class: "list" });
const page = h("div", { class: "page" }, head, h("div", { class: "page-body" }, notice, rest, list));

export function enter(): void {
  stagger(list);
}

export function render(): HTMLElement {
  const create = () => nav.go("create");
  const all = data.vms;
  const error = data.errors.vms ?? "";
  redraw(head, [all?.length, all?.filter((vm) => vm.state === "ready").length, layout], () => [
    h("h1", { class: "page-title" }, t.vms.title),
    all && all.length > 0 && h("span", { class: "page-sub" }, t.vms.summary(all.length, all.filter((vm) => vm.state === "ready").length)),
    h(
      "div",
      { class: "page-actions" },
      all && all.length > 0 && layoutSwitch(),
      button(t.vms.create, create, { kind: "primary", icon: "plus", key: "vms:new" }),
    ),
  ]);
  redraw(notice, error, () => [error !== "" && banner(t.vms.readError, error)]);
  redraw(rest, [all === null, all?.length === 0, error !== ""], () => {
    if (!all) {
      return [error === "" && loading()];
    }
    return [
      all.length === 0 &&
        empty("server", t.vms.emptyTitle, t.vms.emptyText, button(t.vms.emptyAction, create, { kind: "primary", icon: "plus", key: "vms:first" })),
    ];
  });
  list.hidden = !all || all.length === 0;
  list.dataset.layout = layout === "cards" ? "grid" : "rows";
  keyed(
    list,
    all ?? [],
    (vm) => vm.name,
    (vm) => [vm, status(vm)],
    row,
  );
  return page;
}
