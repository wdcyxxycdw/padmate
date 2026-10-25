import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, it } from "vitest";
import type {
  Profile,
  ProfileDpad,
  ProfileStick,
} from "../../shared/generated/schema";
import {
  convertToFullPull,
  directionControls,
  setControlBinding,
} from "./analog-editor";
import { applyAnalogPreset } from "./analog-presets";

// This optional integration check uses the same Go parser/compiler as profile
// saves. The ordinary renderer suite remains runnable without Go binaries.
const validator = process.env.PADMATE_PROFILE_VALIDATOR;
it.skipIf(!validator)(
  "validates every analog mode and generated preset with padmatectl",
  () => {
    const root = mkdtempSync(join(tmpdir(), "padmate-m12-schema-"));
    try {
      const empty = (): Profile => ({
        schemaVersion: 1,
        name: "M12 generated",
        layers: { base: {} },
      });
      const cases: Profile[] = [];
      for (const group of ["ls", "rs", "dpad"] as const) {
        for (const preset of ["arrows", "wasd"] as const)
          cases.push(applyAnalogPreset(empty(), group, preset));
      }
      for (const mode of [
        "standard",
        "eightWay",
        "fourWay",
        "fourWayDiagonal",
      ] as ProfileStick["mode"][]) {
        let profile = empty();
        profile.analog = {
          ls: {
            mode,
            deadzone: 0.17,
            saturation: 0.95,
            directionThreshold: 0.3,
            diagonalWidth: 45,
            hysteresisDeg: 8,
            directionDelayMs: 2,
            diagonalFallback: "cardinals",
          },
        };
        for (const control of directionControls("ls", mode ?? "standard"))
          profile = setControlBinding(profile, control, {
            press: { type: "keys", keys: "F14" },
          });
        cases.push(profile);
      }
      for (const mode of [
        "buttons",
        "fourWay",
        "eightWay",
      ] as ProfileDpad["mode"][]) {
        for (const fold of ["keepFirst", "clockwise"] as const) {
          const profile = empty();
          profile.analog = { dpad: { mode, fold } };
          cases.push(profile);
        }
      }
      const triggers = empty();
      triggers.analog = {
        lt: { press: 0.9, release: 0.8 },
        rt: { press: 0.9, release: 0.8 },
        axisThreshold: 0.5,
      };
      for (const control of [
        "lt",
        "rt",
        "lefttrigger+",
        "righttrigger+",
        "leftx+",
        "leftx-",
        "lefty+",
        "lefty-",
        "ls.active",
      ] as const)
        triggers.layers.base[control] = {
          press: { type: "keys", keys: "F15" },
        };
      cases.push(triggers);
      // The settings of the advanced editor (UI-28): a knob with its ticks and
      // rings, the second stage, a rate that follows the push depth, and the
      // two-stage set-up of M12 moved to lt.full.
      const key = (keys: string, repeat?: object) => ({
        press: {
          type: "keys" as const,
          keys,
          mode: "tap" as const,
          ...(repeat ? { repeat } : {}),
        },
      });
      const advanced = empty();
      advanced.analog = {
        ls: {
          mode: "knob",
          knobStepDeg: 45,
          outerThreshold: 0.8,
          curve: "cubic",
        },
        rs: { outerThreshold: 0.5, curve: "quadratic" },
        lt: { fullPress: 0.95, fullRelease: 0.7, curve: "quadratic" },
        rt: { curve: "cubic" },
      };
      Object.assign(advanced.layers.base, {
        "ls.cw": key("]"),
        "ls.ccw": key("["),
        "ls.inner": key("F2"),
        "ls.outer": key("F3"),
        "rs.up": key("Up", { rateHz: 20, minRateHz: 3 }),
        "rs.outer": key("F4", { rateHz: 12, minRateHz: 12 }),
        "rightx+": key("F5", { rateHz: 20, minRateHz: 1 }),
        lt: key("B"),
        "lt.full": key("E", { rateHz: 20, minRateHz: 2 }),
        "rt.full": key("F6", { minRateHz: 3 }),
        "lefttrigger+": key("F7", { rateHz: 8, minRateHz: 8 }),
      });
      cases.push(advanced);
      // The pointer (UI-33): both sticks move it, one with every setting, and
      // the precision action on a press and on a hold.
      const pointer = empty();
      pointer.analog = {
        ls: {
          mode: "mouse",
          mouseSpeed: 2400,
          mouseSmoothingMs: 80,
          curve: "quadratic",
          directionThreshold: 0.2,
        },
        rs: { mode: "mouse" },
      };
      Object.assign(pointer.layers.base, {
        "ls.active": key("F2"),
        "ls.outer": key("F3"),
        lb: { press: { type: "mouse", op: "precision", factor: 0.1 } },
        rb: { hold: { type: "mouse", op: "precision" } },
      });
      cases.push(pointer);
      const edges = empty();
      edges.analog = {
        ls: { mode: "mouse", mouseSpeed: 50, mouseSmoothingMs: 0 },
        rs: { mode: "mouse", mouseSpeed: 10000, mouseSmoothingMs: 500 },
      };
      edges.layers.base.lt = {
        press: { type: "mouse", op: "precision", factor: 0.05 },
      };
      edges.layers.base.rt = {
        press: { type: "mouse", op: "precision", factor: 1 },
      };
      cases.push(edges);
      const moved = convertToFullPull(
        {
          ...empty(),
          analog: {
            lt: { press: 0.9, release: 0.8 },
            axisThreshold: 0.5,
          },
          layers: {
            base: { lt: key("E"), "lefttrigger+": key("B") },
          },
        },
        "lt",
      );
      cases.push(moved);
      const zero = empty();
      zero.analog = {
        ls: {
          deadzone: 0,
          saturation: 1,
          directionThreshold: 0,
          hysteresisDeg: 0,
          directionDelayMs: 0,
        },
        lt: { press: 0, release: 0 },
        axisThreshold: 0,
      };
      cases.push(zero);
      const paths = cases.map((profile, index) => {
        const path = join(root, `${index}.json`);
        writeFileSync(path, JSON.stringify(profile));
        return path;
      });
      const output = execFileSync(
        validator ?? "padmatectl",
        ["validate", "--json", ...paths],
        { encoding: "utf8", windowsHide: true },
      );
      expect(output).not.toContain('"severity":"error"');
      expect(cases).toHaveLength(22);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  },
);
