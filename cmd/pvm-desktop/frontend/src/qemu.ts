// QEMU setup: the page shown until a QEMU build is the default, and the
// QEMU panel of Settings. Every change runs `pvm qemu` as a task.

import { backend, openURL, type QEMUBuild, type QEMUState, type Task } from "./api";
import { badge, button, callout, command, dialog, h, mono, spinner } from "./dom";
import { t } from "./i18n";
import { icon } from "./icons";
import { act, busy, data } from "./store";

const downloads = "https://www.qemu.org/download/#windows";
const pacman = "pacman -S mingw-w64-ucrt-x86_64-qemu";

// failure is the last setup task that failed, shown on the page with a
// way to try again.
let failure: { text: string; again: () => void } | null = null;

function run(again: () => void, call: () => Promise<Task | void>): void {
  failure = null;
  void act(["qemu"], call, true).then((o) => {
    const text = o.error ?? (o.task && !o.task.ok ? o.task.error : "");
    failure = text ? { text, again } : null;
  });
}

const install = (): void => run(install, () => backend().InstallQEMU());

function installFile(): void {
  run(installFile, async () => {
    const path = await backend().PickQEMUArchive();
    return path === "" ? undefined : backend().InstallQEMUFromFile(path);
  });
}

const useOfficial = (): void => run(useOfficial, () => backend().UseOfficialQEMU());

function checkAgain(): void {
  void act(["qemu"], () => backend().CheckQEMUAgain());
}

function problem(): HTMLElement | false {
  if (!failure) {
    return false;
  }
  const f = failure;
  // a PAC-only proxy cannot be followed by pvm; a downloaded archive can
  const pac = /\bPAC\b|proxy auto-config/i.test(f.text);
  return callout(
    "danger",
    "alert",
    h("p", { lang: "en", class: "mono" }, f.text),
    pac && h("p", null, t.qemu.pacHint),
    h("div", { class: "qemu-actions" }, button(t.qemu.tryAgain, f.again, { size: "sm", icon: "play" }), pac && button(t.qemu.fromFile, installFile, { size: "sm", icon: "upload" })),
  );
}

function projectCard(q: QEMUState): HTMLElement {
  const working = busy.has("qemu");
  const r = q.recommended;
  return h(
    "section",
    { class: "panel qemu-card" },
    h("div", { class: "panel-head" }, h("h2", { class: "fieldset-title" }, t.qemu.projectTitle, " ", badge(t.qemu.recommended, "accent"))),
    h(
      "div",
      { class: "setting setting--stack" },
      h("p", { class: "hint" }, t.qemu.projectText(r.size_mb)),
      r.id === ""
        ? callout("warn", "note", t.qemu.noPin)
        : h(
            "div",
            { class: "qemu-actions" },
            button(t.qemu.install, install, { kind: "primary", icon: "download", disabled: working, key: "qemu:install" }),
            button(t.qemu.fromFile, installFile, { icon: "upload", disabled: working, key: "qemu:file" }),
          ),
    ),
  );
}

function officialCard(q: QEMUState): HTMLElement {
  const working = busy.has("qemu");
  const o = q.official;
  return h(
    "section",
    { class: "panel qemu-card" },
    h("div", { class: "panel-head" }, h("h2", { class: "fieldset-title", lang: "en" }, "QEMU from MSYS2 UCRT64")),
    h(
      "div",
      { class: "setting setting--stack" },
      h("p", { class: "hint" }, t.qemu.officialText),
      o.found
        ? h(
            "div",
            null,
            h("p", null, t.qemu.found(o.version), " ", mono(o.path)),
            h("div", { class: "qemu-actions" }, button(t.qemu.useOfficial, useOfficial, { kind: "primary", icon: "check", disabled: working, key: "qemu:official" })),
          )
        : h(
            "div",
            null,
            h("p", { class: "hint" }, t.qemu.notFound),
            command(pacman),
            h(
              "div",
              { class: "qemu-actions" },
              button(t.qemu.openDownloads, () => openURL(downloads), { icon: "route", key: "qemu:downloads" }),
              button(t.qemu.checkAgain, checkAgain, { icon: "clock", disabled: working, key: "qemu:check" }),
            ),
          ),
    ),
  );
}

// page is the first-run page. Under WSL2 it also says that setting up
// QEMU moves the app to the native backend.
export function page(wsl: boolean, back?: () => void): HTMLElement {
  const q = data.state!.qemu;
  const working = busy.has("qemu");
  return h(
    "div",
    { class: "state qemu-page" },
    working ? h("div", { class: "state-art state-art--accent", "aria-hidden": "true" }, spinner()) : h("div", { class: "state-art", "aria-hidden": "true" }, icon("cpu", "xl")),
    h("h1", { class: "state-title" }, working ? t.qemu.working : t.qemu.title),
    h("p", { class: "state-text" }, t.qemu.text),
    wsl && h("div", { class: "state-block" }, callout("info", "note", ...t.qemu.wslNote(mono("wsl -- pvm")))),
    q.problem !== "" && h("div", { class: "state-block" }, callout("warn", "note", h("span", { lang: "en" }, q.problem))),
    h("div", { class: "state-block qemu-block" }, problem(), projectCard(q), officialCard(q)),
    back && button(t.qemu.back, back, { kind: "ghost" }),
    h("p", { class: "hint qemu-notice" }, t.qemu.notice),
  );
}

function removeDialog(b: QEMUBuild): void {
  dialog({
    title: [t.qemu.removeTitle(b.id)],
    content: [h("p", null, b.source === "project" ? t.qemu.removeProject : t.qemu.removeOfficial)],
    submit: t.qemu.remove,
    submitIcon: "trash",
    danger: true,
    narrow: true,
    onSubmit: () => void act(["qemu"], () => backend().RemoveQEMU(b.id, true)),
  });
}

function buildRow(b: QEMUBuild): HTMLElement {
  const working = busy.has("qemu");
  return h(
    "div",
    { class: "setting" },
    h(
      "div",
      { class: "setting-main" },
      h("div", { class: "setting-head" }, h("span", { class: "setting-title", lang: "en" }, b.label), b.default && badge(t.qemu.isDefault, "ok", icon("check"))),
      h("p", { class: "hint" }, mono(b.path)),
      !b.virtio_9p && h("p", { class: "hint" }, t.qemu.no9p),
      b.withdrawn !== "" && callout("danger", "alert", t.qemu.withdrawn(b.withdrawn)),
    ),
    !b.default &&
      h(
        "div",
        { class: "qemu-actions" },
        button(t.qemu.use, () => void act(["qemu"], () => backend().UseQEMU(b.id)), { size: "sm", disabled: working, key: "qemu:use:" + b.id }),
        button(t.qemu.remove, () => removeDialog(b), { size: "sm", kind: "ghost", icon: "trash", disabled: working, key: "qemu:rm:" + b.id }),
      ),
  );
}

// settingsPanel lists the registered builds and offers the ones not yet
// set up.
export function settingsPanel(): HTMLElement {
  const q = data.state!.qemu;
  const builds = data.qemuBuilds ?? [];
  const working = busy.has("qemu");
  const offers: HTMLElement[] = [];
  if (q.recommended.id !== "" && !q.recommended.installed) {
    offers.push(button(t.qemu.installNamed(q.recommended.id), install, { size: "sm", icon: "download", disabled: working, key: "qemu:install" }));
  }
  if (q.official.found && !q.official.registered) {
    offers.push(button(t.qemu.useOfficial, useOfficial, { size: "sm", disabled: working, key: "qemu:official" }));
  }
  return h(
    "section",
    { class: "panel" },
    h("div", { class: "panel-head" }, h("h2", { class: "fieldset-title" }, "QEMU")),
    ...builds.map(buildRow),
    offers.length > 0 && h("div", { class: "setting" }, h("div", { class: "qemu-actions" }, ...offers)),
    (failure || q.problem !== "") &&
      h("div", { class: "panel-notes" }, problem(), q.problem !== "" && callout("warn", "note", h("span", { lang: "en" }, q.problem))),
  );
}
