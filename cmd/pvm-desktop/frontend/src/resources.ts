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
