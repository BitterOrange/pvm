import { backend, errorText, type AppConfig, type CatalogApp, type Task, type VM } from "./api";
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
import type { Part } from "./en";
import { t } from "./i18n";
import { icon, type IconName } from "./icons";
import * as prefs from "./prefs";
import { cpuControl, limits, number, outside, renderControl, scaleControl, type RenderControl } from "./resources";
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
    drop: [{ value: "off", label: c.off }, { value: "on", label: c.dropOn }],
    audio: [{ value: "out", label: c.audioOut }, { value: "both", label: c.audioBoth }, { value: "off", label: c.off }],
    audio_silence: [{ value: "skip", label: c.silenceSkip }, { value: "send", label: c.silenceSend }],
    keyring: [{ value: "off", label: c.keyringOff }, { value: "host", label: c.keyringHost }],
  };
}

// What the Create form starts from: the app settings of a VM that
// `pvm app config` has not been run for.
export const newAppConfig: AppConfig = { clipboard: "off", drop: "off", audio: "out", audio_silence: "skip", render_cpus: "2", scale: "1", keyring: "off" };

type Setting = keyof AppConfig;

// When a change takes effect: at once, for programs started afterwards,
// or at the next start of an app.
type When = "now" | "later" | "next";

// The groups of App settings in the order they show, each with its
// settings in order. A new setting of `pvm app config` takes a place here,
// a line in settingSpecs, a control in appSettingsForm and its words in
// en.ts and zh.ts.
export type SettingGroup = "interaction" | "performance" | "security";
const settingGroups: [SettingGroup, Setting[]][] = [
  ["interaction", ["clipboard", "drop", "audio"]],
  ["performance", ["scale", "render_cpus", "audio_silence"]],
  ["security", ["keyring"]],
];

interface SettingSpec {
  name: () => string;
  // hint is the long explanation, shown on request.
  hint?: () => string;
  when: When;
  // effect says, once the setting is changed, when that applies.
  effect: () => string;
}

const settingSpecs: Record<Setting, SettingSpec> = {
  clipboard: { name: () => t.appConfig.clipboard, when: "now", effect: () => t.appConfig.effectReconnect },
  drop: { name: () => t.appConfig.drop, when: "now", effect: () => t.appConfig.effectNow },
  audio: { name: () => t.appConfig.audio, when: "now", effect: () => t.appConfig.effectNow },
  scale: { name: () => t.appConfig.scale, hint: () => t.appConfig.scaleHint, when: "now", effect: () => t.appConfig.effectScale },
  render_cpus: { name: () => t.appConfig.renderCpus, hint: () => t.appConfig.renderCpusHint, when: "later", effect: () => t.appConfig.effectLater },
  audio_silence: { name: () => t.appConfig.audioSilence, hint: () => t.appConfig.audioSilenceHint, when: "now", effect: () => t.appConfig.effectReconnect },
  keyring: { name: () => t.appConfig.keyring, hint: () => t.appConfig.keyringHint, when: "next", effect: () => t.appConfig.effectNext },
};

const whenText = (when: When): string => (when === "now" ? t.appConfig.whenNow : when === "later" ? t.appConfig.whenLater : t.appConfig.whenNext);
const groupName = (group: SettingGroup): string => t.appConfig[group];

// A control of one setting, whatever its kind.
interface SettingControl {
  // id is the control a label is for.
  id: string;
  // body is the control as it shows, with what is drawn with it.
  body: HTMLElement;
  value(): string;
  load(value: string): void;
  // sends is what Apply sends against what the VM has: "" leaves it.
  sends(now: string): string;
  // said puts a value into the words the control shows it with.
  said(value: string): string;
  label(): void;
}

function choiceControl(prefix: string, key: ChoiceSetting): SettingControl {
  const el = select([], newAppConfig[key], { id: prefix + "-" + key, "data-key": prefix + ":" + key });
  const choices = () => appConfigChoices()[key];
  // the words change with the language; the choice stays
  const fill = () => {
    const chosen = el.options.length === 0 ? newAppConfig[key] : el.value;
    el.replaceChildren(...choices().map((o) => h("option", { value: o.value }, o.label)));
    el.value = chosen;
  };
  fill();
  return {
    id: el.id,
    body: el,
    value: () => el.value,
    load: (value) => (el.value = value),
    sends: (now) => (el.value === now ? "" : el.value),
    said: (value) => choices().find((o) => o.value === value)?.label ?? value,
    label: fill,
  };
}

export interface AppSettingsForm {
  // groups are laid out by the caller, as its room allows.
  groups: Record<SettingGroup, HTMLElement>;
  // render is the renderer slider, which ends at the VM's vCPUs.
  render: RenderControl;
  // load shows the settings of a VM, or the ones a new VM gets.
  load(config: AppConfig): void;
  value(key: Setting): string;
  // change holds what differs from what was loaded, "" for the rest: the
  // renderer CPUs only once moved or typed, as a stored number above the
  // VM's vCPUs shows at the end of the slider.
  change(): AppConfig;
  changes(): number;
  label(): void;
  focus(): void;
}

// appSettingsForm builds the App settings in their groups. live is for a
// VM that exists: each setting says when a change applies, and a changed
// one what it was and what the change does.
export function appSettingsForm(prefix: string, opts: { live: boolean; vm?: string }): AppSettingsForm {
  const render = renderControl(prefix + ":render");
  const scale = scaleControl(prefix + ":scale");
  const controls: Record<Setting, SettingControl> = {
    clipboard: choiceControl(prefix, "clipboard"),
    drop: choiceControl(prefix, "drop"),
    audio: choiceControl(prefix, "audio"),
    audio_silence: choiceControl(prefix, "audio_silence"),
    keyring: choiceControl(prefix, "keyring"),
    render_cpus: {
      id: render.id,
      body: render.body,
      value: render.value,
      load: render.load,
      sends: (now) => (!render.touched() || render.bad() || render.value() === now ? "" : render.value()),
      said: (value) => (Number(value) === 0 ? t.appConfig.renderCpusAll : t.appConfig.renderCpusCount(Number(value))),
      label: render.label,
    },
    scale: {
      id: scale.id,
      body: scale.body,
      value: scale.value,
      load: scale.load,
      sends: (now) => (scale.value() === now ? "" : scale.value()),
      said: (value) => t.appConfig.scaleOf(value),
      label: scale.label,
    },
  };
  let now: AppConfig | null = null;

  const rows = settingGroups.flatMap(([, keys]) => keys).map((key) => {
    const spec = settingSpecs[key];
    const control = controls[key];
    const id = control.id;
    const name = h("label", { class: "label", for: id });
    const hint = spec.hint && h("p", { class: "hint opt-hint", id: id + "-hint", hidden: true });
    // the long explanation opens on request, under the control
    let help: HTMLButtonElement | undefined;
    if (hint) {
      const button = iconButton(
        "info",
        "",
        () => {
          hint.hidden = !hint.hidden;
          button.setAttribute("aria-expanded", String(!hint.hidden));
        },
        { small: true, key: prefix + ":" + key + ":help" },
      );
      button.classList.add("opt-help");
      button.setAttribute("aria-expanded", "false");
      button.setAttribute("aria-controls", hint.id);
      help = button;
    }
    const when = opts.live && h("span", { class: "opt-when", id: id + "-when" }, icon("clock", "sm"), h("span"));
    const note = h("div", { class: "opt-note", id: id + "-note", "aria-live": "polite" });
    const el = h(
      "div",
      { class: "field opt", "data-setting": key },
      h("div", { class: "opt-head" }, name, help, when),
      control.body,
      h("p", { class: "field-error", role: "alert", id: id + "-error" }),
      hint,
      note,
    );
    // every control of the setting is described by its hint, when it
    // applies and what a change does
    const described = [id + "-error", hint && hint.id, when && when.id, note.id].filter(Boolean).join(" ");
    for (const c of control.body.matches("input, select") ? [control.body] : control.body.querySelectorAll("input, select")) {
      c.setAttribute("aria-describedby", described);
    }

    function draw(): void {
      const sends = now && opts.live ? control.sends(now[key]) : "";
      el.toggleAttribute("data-changed", sends !== "");
      if (when) {
        when.toggleAttribute("data-changed", sends !== "");
      }
      const was = now && sends !== "" ? t.appConfig.was(control.said(now[key])) : "";
      // leaving the kept keyring password signs apps out at the VM's next
      // start: the one change here that loses something
      const leaving = key === "keyring" && sends === "off";
      redraw(note, [sends, was], () => {
        if (sends === "") {
          return [];
        }
        if (leaving) {
          return [callout("warn", "note", was + " " + spec.effect() + " " + t.appConfig.keyringLeave(opts.vm ?? ""))];
        }
        return [h("p", null, was + " " + spec.effect() + (key === "keyring" ? " " + t.appConfig.keyringKeep : ""))];
      });
    }

    function label(): void {
      name.textContent = spec.name();
      if (hint && help) {
        hint.textContent = spec.hint?.() ?? "";
        help.setAttribute("aria-label", t.appConfig.about(spec.name()));
        help.dataset.tip = t.appConfig.about(spec.name());
      }
      if (when) {
        when.lastElementChild!.textContent = whenText(spec.when);
      }
      control.label();
      // the note is written in the language of the page
      delete note.dataset.shows;
      draw();
    }

    control.body.addEventListener("input", () => {
      const before = note.childElementCount;
      draw();
      // what a change does shows as it is made, also at the foot of a
      // body that scrolls
      if (note.childElementCount > before) {
        note.scrollIntoView({ block: "nearest" });
      }
    });
    label();
    return { key, el, draw, label };
  });

  const groups = Object.fromEntries(
    settingGroups.map(([group, keys]) => {
      const title = h("h3", { class: "opt-group-title", id: prefix + "-group-" + group });
      const el = h("section", { class: "opt-group", "aria-labelledby": title.id }, title, ...rows.filter((r) => keys.includes(r.key)).map((r) => r.el));
      return [group, el];
    }),
  ) as Record<SettingGroup, HTMLElement>;

  const form: AppSettingsForm = {
    groups,
    render,
    load(config) {
      now = config;
      for (const r of rows) {
        controls[r.key].load(config[r.key]);
        r.draw();
      }
    },
    value: (key) => controls[key].value(),
    change: () => Object.fromEntries(rows.map((r) => [r.key, now ? controls[r.key].sends(now[r.key]) : controls[r.key].value()])) as unknown as AppConfig,
    changes: () => rows.filter((r) => now !== null && controls[r.key].sends(now[r.key]) !== "").length,
    label() {
      for (const [group] of settingGroups) {
        groups[group].firstElementChild!.textContent = groupName(group);
      }
      rows.forEach((r) => r.label());
    },
    focus: () => controls.clipboard.body.focus(),
  };
  form.label();
  return form;
}

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

// How a VM takes an action that needs it running: at once, once the start
// it is in has ended, or after a start the user agrees to.
export type Readiness = "now" | "wait" | "start";

export const readiness = (vm: VM | undefined): Readiness => (vm?.state === "stopped" ? "start" : vm?.state === "starting" ? "wait" : "now");

export const strong = (name: string): HTMLElement => h("strong", { lang: "en" }, name);

// runIn runs an action that needs the VM running. A task that starts the
// VM first, or waits for it, is followed as a start is: the VM's row shows
// the stages, and Stop there ends the wait and with it the action.
export function runIn(vm: string, keys: string[], need: Readiness, call: (start: boolean) => Promise<Task>): void {
  void (need === "now" ? act(keys, () => call(false)) : actStart(vm, () => call(true)));
}

// startDialog asks before a stopped VM starts for what was clicked. text
// names the VM and what opens once it is ready.
export function startDialog(vm: string, text: Part[], run: () => void): void {
  dialog({
    title: t.vms.startTitle(mono(vm)),
    content: [h("p", null, ...text), h("p", { class: "hint" }, t.vms.startNote)],
    submit: t.vms.startSubmit,
    submitIcon: "play",
    narrow: true,
    onSubmit: run,
  });
}

// openTerminal opens a terminal, and starts a stopped VM first once the
// user agrees; pvm ssh would ask in the console instead. The terminal
// opens only when the start ended with the VM ready.
export function openTerminal(vm: VM | undefined, name: string, text: () => Part[], open: () => Promise<void>): void {
  if (readiness(vm) !== "start") {
    void act([], open);
    return;
  }
  startDialog(name, text(), () =>
    void actStart(name, () => backend().StartVM(name)).then(({ task }) => {
      if (task?.ok) {
        void act([], open);
      }
    }),
  );
}

export interface AppDialogOptions {
  title: Child[];
  // text is a line under the title.
  text?: Child[];
  // need says whether the VM is started first, or is starting.
  need: Readiness;
  vm: string;
  run: (target: string) => void;
  // programs, when given, loads the one-click entries under the field.
  programs?: (list: HTMLElement, close: () => void) => void;
}

// appDialog asks for a command line or a URL. On a stopped VM it says that
// the VM starts first, so the one choice the user makes here is also the
// consent to start it: no second dialog asks again.
export function appDialog(o: AppDialogOptions): void {
  const target = input({ class: "input input--mono", placeholder: t.vms.runPlaceholder });
  const list = h("div");
  const d = dialog({
    title: o.title,
    text: o.text,
    content: [
      o.need === "start" && callout("info", "note", ...t.vms.startApp(strong(o.vm))),
      o.need === "wait" && callout("info", "note", ...t.vms.waitApp(strong(o.vm))),
      field(t.vms.runLabel, target, t.vms.runHint),
      o.programs && h("div", { class: "field" }, h("span", { class: "label", id: "installed-programs" }, t.vms.installed), list),
    ],
    submit: o.need === "start" ? t.vms.startSubmit : t.vms.open,
    submitIcon: o.need === "start" ? "play" : undefined,
    validate: () => {
      if (target.value.trim() !== "") {
        return true;
      }
      setFieldError(target, t.vms.runEmpty);
      return false;
    },
    onSubmit: () => o.run(target.value),
  });
  o.programs?.(list, d.close);
}

function vmAppDialog(vm: VM): void {
  const need = readiness(vm);
  const run = (call: (start: boolean) => Promise<Task>) => runIn(vm.name, ["vm:" + vm.name], need, call);
  appDialog({
    title: t.vms.runTitle(mono(vm.name)),
    need,
    vm: vm.name,
    run: (target) => run((start) => backend().RunApp(vm.name, target, start)),
    // the list is the one kept by the daemon, so a stopped VM has it too
    programs: (list, close) => {
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
                        run((start) => backend().RunCatalogApp(vm.name, app.id, start));
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
  });
}

function openFiles(vm: VM): void {
  const need = readiness(vm);
  const run = () => runIn(vm.name, ["vm:" + vm.name], need, (start) => backend().OpenFiles(vm.name, start));
  if (need === "start") {
    startDialog(vm.name, t.vms.startFiles(strong(vm.name)), run);
  } else {
    run();
  }
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
// them first and shows what is set now. The groups stand in two columns,
// so that every setting shows at once in the smallest window.
function settingsDialog(vm: VM): void {
  const form = appSettingsForm("settings", { live: true, vm: vm.name });
  // the renderer slider ends at the VM's vCPUs
  form.render.setMost(vm.cpus);
  const fields = h("div", { class: "slot" }, loading());
  let loaded = false;
  const d = dialog({
    title: t.vms.settingsTitle(mono(vm.name)),
    text: [t.vms.settingsText],
    content: [fields],
    submit: t.vms.apply,
    wide: true,
    // a number out of range counts as a change, so that submitting says why
    submitState: () => {
      const n = form.changes();
      return { disabled: !loaded || (n === 0 && !form.render.bad()), label: n > 0 ? t.vms.applyChanges(n) : t.vms.apply };
    },
    validate: () => {
      const bad = form.render.bad();
      setFieldError(form.render.num, bad ? t.appConfig.renderCpusRange(vm.cpus) : "");
      return !bad;
    },
    onSubmit: () => void act(["vm:" + vm.name], () => backend().ConfigureApps(vm.name, form.change())),
  });
  backend()
    .AppSettings(vm.name)
    .then(
      (config) => {
        form.load(config);
        loaded = true;
        const g = form.groups;
        fields.replaceChildren(h("div", { class: "opt-cols" }, h("div", { class: "opt-col" }, g.interaction, g.security), h("div", { class: "opt-col" }, g.performance)));
        form.focus();
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
  const starting = vm.state === "starting";
  const k = (action: string) => "vm:" + vm.name + ":" + action;
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
        // a stopped VM is started first, once the user agrees; a starting
        // one is waited for
        iconButton(
          "terminal",
          t.vms.terminalIn(vm.name),
          () => openTerminal(vm, vm.name, () => t.vms.startTerminal(strong(vm.name)), () => backend().OpenTerminal(vm.name)),
          { tip: t.common.terminal, disabled: locked, key: k("terminal") },
        ),
        iconButton("window", t.vms.appIn(vm.name), () => vmAppDialog(vm), { tip: t.common.app, disabled: working, key: k("app") }),
        iconButton("files", t.vms.filesOf(vm.name), () => openFiles(vm), { tip: t.common.files, disabled: working, key: k("files") }),
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
