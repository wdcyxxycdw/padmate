// layer() and layers from SCRIPTING §2.1, reduced. In M5 layers are host
// state; the M0d stub has only on, key and sleep, so layer(name).on tags
// the registration with opts.layer for the spike host to resolve, and
// layers keeps the set of toggled layers in the script.
import { on } from "padmate";
import type { GestureOptions, Handler, InputEvent } from "./types";

const active = new Set<string>();

export const layers = {
  toggle(name: string): void {
    if (!active.delete(name)) {
      active.add(name);
    }
  },
  isActive(name: string): boolean {
    return active.has(name);
  },
};

export function layer(name: string) {
  return {
    on(control: string, gesture: string, fn: Handler<InputEvent>, opts: GestureOptions = {}): void {
      on(control, gesture, fn, { ...opts, layer: name });
    },
  };
}
