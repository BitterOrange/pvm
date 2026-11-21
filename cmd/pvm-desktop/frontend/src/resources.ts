// The numbers of a VM: vcpus, memory and disk. The Create form and the
// Resize dialog ask for them the same way.

import { button, callout, h, input, redraw, slider } from "./dom";
import { t } from "./i18n";
import { data } from "./store";

// The daemon's own limits.
export const limits = { cpus: [1, 64], mem: [128, 1048576], disk: [1, 4096] } as const;

// outside reports a number field that does not hold a whole number from
// least to most.
export function outside(el: HTMLInputElement, least: number, most: number): boolean {
  const n = Number(el.value);
  return el.value.trim() === "" || !Number.isInteger(n) || n < least || n > most;
}

export const number = (props: Record<string, string | number> = {}): HTMLInputElement =>
  input({ type: "number", step: 1, inputmode: "numeric", ...props });

export interface CpuControl {
  // field is the labelled slider with its hint and warning.
  field: HTMLElement;
  range: HTMLInputElement;
  value(): number;
  set(value: number): void;
  // label writes the words again, as after a change of language.
  label(): void;
}

let controls = 0;

// cpuControl is the vcpu slider. It ends at the host's logical processors,
// or at what the VM already has, and warns when the VM would take them
// all: the host then has none left for itself.
export function cpuControl(key: string): CpuControl {
  const id = "cpus-" + ++controls;
  const s = slider((value, most) => t.resources.cpusOf(value, most), { id, "data-key": key, "aria-describedby": id + "-hint" });
  const name = h("label", { class: "label", for: id });
  const hint = h("p", { class: "hint", id: id + "-hint" });
  const warning = h("div", { class: "slot", "aria-live": "polite" });
  const value = () => Number(s.range.value);
  const host = () => data.state?.host_cpus ?? 1;

  function label(): void {
    name.textContent = t.resources.cpus;
    hint.textContent = t.resources.cpusHost(host());
    const full = host() > 1 && value() >= host();
    s.range.toggleAttribute("data-full", full);
    redraw(warning, full ? host() : 0, () => [
      full &&
        callout(
          "warn",
          "note",
          h("p", null, t.resources.cpusAll(host(), host() - 1)),
          button(
            t.resources.cpusUse(host() - 1),
            () => {
              set(host() - 1);
              // the form hears of it as of a move of the thumb
              s.range.dispatchEvent(new Event("input", { bubbles: true }));
              s.range.focus();
            },
            { size: "sm" },
          ),
        ),
    ]);
  }

  function set(n: number): void {
    s.set(n, Math.min(limits.cpus[1], Math.max(host(), n)));
    label();
  }

  s.range.addEventListener("input", label);
  label();
  return { field: h("div", { class: "field" }, name, s.el, hint, warning), range: s.range, value, set, label };
}

export interface RenderControl {
  // field is the labelled slider and number with their hint.
  field: HTMLElement;
  // num is the typed number, which carries the field's error.
  num: HTMLInputElement;
  // value is the setting as --render-cpus takes it: the end of the
  // slider, all of the VM's vCPUs, is 0, no limit.
  value(): string;
  // touched is set once the user moves or types it.
  touched(): boolean;
  // bad reports a typed number outside 0 to the VM's vCPUs.
  bad(): boolean;
  // load shows a setting read from the daemon or the default.
  load(value: string): void;
  // setMost sets the VM's vCPUs, which end the slider.
  setMost(most: number): void;
  label(): void;
}

// renderControl is the renderer vCPU setting: a slider from 1 to the
// VM's vCPUs with a number to type, where the end means no limit. A
// setting at or above the VM's vCPUs limits nothing, so it shows there.
export function renderControl(key: string): RenderControl {
  const id = "render-" + ++controls;
  let most = 1;
  let touched = false;
  const range = h("input", { type: "range", class: "slider-range", min: 1, max: 1, step: 1, id, "data-key": key, "aria-describedby": id + "-hint" });
  // the number stands where the vCPU slider has its value: "2 / 4"
  const num = number({ class: "input slider-num", min: 0, max: 1, "aria-label": "", "aria-describedby": id + "-error " + id + "-hint" });
  const of = h("span", { class: "slider-value", "aria-hidden": "true" });
  // the scale under the track names its ends; the right one is lit when
  // it is the setting
  const first = h("span", null, "1");
  const end = h("span");
  const scale = h("div", { class: "slider-scale", "aria-hidden": "true" }, first, end);
  const name = h("label", { class: "label", for: id });
  const hint = h("p", { class: "hint", id: id + "-hint" });
  const at = () => Number(range.value);
  const unlimited = () => at() >= most;

  function draw(): void {
    // one vCPU leaves the slider a single place: it is drawn full, with
    // the thumb at the end, and only the number takes input
    const single = most === 1;
    range.min = single ? "0" : "1";
    range.max = String(most);
    range.disabled = single;
    num.max = String(most);
    range.style.setProperty("--at", String(most > 1 ? (at() - 1) / (most - 1) : 1));
    of.textContent = "/ " + most;
    first.hidden = single;
    end.textContent = t.appConfig.renderCpusAll;
    end.toggleAttribute("data-on", unlimited());
    range.setAttribute("aria-valuetext", unlimited() ? t.appConfig.renderCpusAll : t.appConfig.renderCpusOf(at(), most));
  }

  function label(): void {
    name.textContent = t.appConfig.renderCpus;
    hint.textContent = t.appConfig.renderCpusHint;
    num.setAttribute("aria-label", t.appConfig.renderCpusNumber);
    draw();
  }

  function show(n: number): void {
    range.value = String(n === 0 || n >= most ? most : n);
    num.value = range.value;
    draw();
  }

  range.addEventListener("input", () => {
    touched = true;
    num.value = range.value;
    num.removeAttribute("aria-invalid");
    draw();
  });
  num.addEventListener("input", () => {
    touched = true;
    if (!outside(num, 0, most)) {
      range.value = String(Number(num.value) === 0 ? most : Number(num.value));
      draw();
    }
  });
  label();
  return {
    field: h(
      "div",
      { class: "field" },
      name,
      h("div", { class: "slider slider--typed" }, range, h("span", { class: "slider-typed" }, num, of), scale),
      h("p", { class: "field-error", role: "alert", id: id + "-error" }),
      hint,
    ),
    num,
    value: () => (unlimited() ? "0" : range.value),
    touched: () => touched,
    bad: () => outside(num, 0, most),
    load(value) {
      touched = false;
      show(Number(value));
    },
    setMost(n) {
      const was = unlimited() ? 0 : at();
      most = Math.max(1, n);
      show(was);
    },
    label,
  };
}
