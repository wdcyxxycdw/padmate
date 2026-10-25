import { describe, expect, it } from "vitest";
import type { Config } from "../shared/generated/schema";
import {
  CALIBRATION_DEFAULTS,
  calibrationStick,
  clampStick,
  clearCalibration,
  DEADZONE_MAX,
  hasCalibration,
  MIN_GAP,
  OUTER_MIN,
  profileStickValues,
  setStickCalibration,
  shownStick,
  stickPercent,
  sticksOf,
} from "./calibration-model";

const KEY = "03000000de2800000fff000000000000";
const base: Config = { schemaVersion: 0 };

describe("stick calibration of a controller (IN-14)", () => {
  it("writes one member of one stick under the controller's key", () => {
    expect(setStickCalibration(base, KEY, "ls", { deadzone: 0.22 })).toEqual({
      schemaVersion: 0,
      devices: { [KEY]: { calibration: { ls: { deadzone: 0.22 } } } },
    });
    const withLeft = setStickCalibration(base, KEY, "ls", {
      deadzone: 0.22,
      saturation: 0.9,
    }) as Config;
    expect(
      setStickCalibration(withLeft, KEY, "rs", { deadzone: 0.18 }),
    ).toEqual({
      schemaVersion: 0,
      devices: {
        [KEY]: {
          calibration: {
            ls: { deadzone: 0.22, saturation: 0.9 },
            rs: { deadzone: 0.18 },
          },
        },
      },
    });
    // The other member of a stick stays as it is.
    expect(
      setStickCalibration(withLeft, KEY, "ls", { saturation: 0.8 }),
    ).toEqual({
      schemaVersion: 0,
      devices: {
        [KEY]: { calibration: { ls: { deadzone: 0.22, saturation: 0.8 } } },
      },
    });
  });

  it("changes nothing when the value is the one saved", () => {
    const config = setStickCalibration(base, KEY, "ls", {
      deadzone: 0.2,
    }) as Config;
    expect(
      setStickCalibration(config, KEY, "ls", { deadzone: 0.2 }),
    ).toBeNull();
    expect(setStickCalibration(config, KEY, "ls", {})).toBeNull();
    expect(setStickCalibration(base, KEY, "ls", { deadzone: null })).toBeNull();
  });

  it("keeps the other devices, other members and the ignored flag", () => {
    const other = "0300000000000000aa";
    const config = {
      schemaVersion: 0,
      enabled: false,
      devices: {
        [other]: { ignored: true, calibration: { rs: { saturation: 0.7 } } },
        [KEY]: { ignored: false },
      },
    } as Config;
    expect(setStickCalibration(config, KEY, "ls", { deadzone: 0.3 })).toEqual({
      schemaVersion: 0,
      enabled: false,
      devices: {
        [other]: { ignored: true, calibration: { rs: { saturation: 0.7 } } },
        [KEY]: { ignored: false, calibration: { ls: { deadzone: 0.3 } } },
      },
    });
    expect(config.devices?.[KEY]).toEqual({ ignored: false });
  });

  it("removes an emptied stick, calibration and device entry", () => {
    const config = setStickCalibration(base, KEY, "ls", {
      deadzone: 0.2,
    }) as Config;
    expect(setStickCalibration(config, KEY, "ls", { deadzone: null })).toEqual({
      schemaVersion: 0,
    });
    // An entry that ignores the device stays without its calibration.
    const ignored = {
      schemaVersion: 0,
      devices: {
        [KEY]: { ignored: true, calibration: { ls: { deadzone: 0.2 } } },
      },
    } as Config;
    expect(setStickCalibration(ignored, KEY, "ls", { deadzone: null })).toEqual(
      { schemaVersion: 0, devices: { [KEY]: { ignored: true } } },
    );
  });

  it("restores the defaults by removing the controller's calibration", () => {
    const config = {
      schemaVersion: 0,
      devices: {
        [KEY]: {
          calibration: { ls: { deadzone: 0.2 }, rs: { saturation: 0.8 } },
        },
        other: { calibration: { ls: { deadzone: 0.1 } } },
      },
    } as Config;
    expect(hasCalibration(config, KEY)).toBe(true);
    expect(clearCalibration(config, KEY)).toEqual({
      schemaVersion: 0,
      devices: { other: { calibration: { ls: { deadzone: 0.1 } } } },
    });
    expect(
      clearCalibration(clearCalibration(config, KEY) as Config, "other"),
    ).toEqual({ schemaVersion: 0 });
    // Nothing to remove: no change.
    expect(clearCalibration(base, KEY)).toBeNull();
    expect(clearCalibration(config, "missing")).toBeNull();
    // The ignored flag stays.
    expect(
      clearCalibration(
        {
          schemaVersion: 0,
          devices: { [KEY]: { ignored: true, calibration: {} } },
        } as Config,
        KEY,
      ),
    ).toEqual({ schemaVersion: 0, devices: { [KEY]: { ignored: true } } });
    expect(config.devices?.[KEY]?.calibration?.ls).toEqual({ deadzone: 0.2 });
  });

  it("reads what is saved and nothing else", () => {
    const config = {
      schemaVersion: 0,
      devices: {
        [KEY]: {
          calibration: {
            ls: { deadzone: 0.2, saturation: 0.9 },
            rs: { deadzone: "x", saturation: 2 },
          },
        },
      },
    } as unknown as Config;
    expect(calibrationStick(config, KEY, "ls")).toEqual({
      deadzone: 0.2,
      saturation: 0.9,
    });
    // A value that is no fraction is not a calibration.
    expect(calibrationStick(config, KEY, "rs")).toEqual({});
    expect(calibrationStick(config, "missing", "ls")).toEqual({});
    expect(calibrationStick(null, KEY, "ls")).toEqual({});
    expect(hasCalibration(null, KEY)).toBe(false);
    expect(hasCalibration(base, KEY)).toBe(false);
    expect(
      hasCalibration(
        { schemaVersion: 0, devices: { [KEY]: { calibration: {} } } },
        KEY,
      ),
    ).toBe(false);
  });

  it("shows the profile's values for what is not calibrated", () => {
    const profile = { deadzone: 0.1, saturation: 0.8 };
    const config = setStickCalibration(base, KEY, "ls", {
      deadzone: 0.3,
    }) as Config;
    expect(shownStick(config, KEY, "ls", profile)).toEqual({
      deadzone: 0.3,
      saturation: 0.8,
    });
    expect(shownStick(config, KEY, "rs", profile)).toEqual(profile);
    expect(shownStick(null, KEY, "ls", profile)).toEqual(profile);
  });
});

describe("the values of the profile in use", () => {
  it("reads the stick's deadzone and saturation", () => {
    const text = JSON.stringify({
      schemaVersion: 1,
      analog: {
        ls: { deadzone: 0.2, saturation: 0.8 },
        rs: { deadzone: 0.05 },
      },
    });
    expect(profileStickValues(text, "ls")).toEqual({
      deadzone: 0.2,
      saturation: 0.8,
    });
    expect(profileStickValues(text, "rs")).toEqual({
      deadzone: 0.05,
      saturation: CALIBRATION_DEFAULTS.saturation,
    });
  });

  it("falls back to 15 % and 95 %", () => {
    expect(CALIBRATION_DEFAULTS).toEqual({ deadzone: 0.15, saturation: 0.95 });
    for (const text of [
      undefined,
      "",
      "{",
      "[]",
      "null",
      '{"analog":null}',
      '{"analog":{"ls":{"deadzone":"x","saturation":null}}}',
      '{"analog":{"ls":{"deadzone":2,"saturation":-1}}}',
      // A pair the daemon would reject is not shown as the profile's.
      '{"analog":{"ls":{"deadzone":0.9,"saturation":0.5}}}',
    ])
      expect(profileStickValues(text, "ls"), String(text)).toEqual(
        CALIBRATION_DEFAULTS,
      );
    // A byte order mark in front of the document.
    expect(
      profileStickValues('﻿{"analog":{"ls":{"deadzone":0.3}}}', "ls"),
    ).toEqual({ deadzone: 0.3, saturation: 0.95 });
  });
});

describe("the sliders", () => {
  it("has the dead zone up to 50 % and the outer edge from 50 %", () => {
    expect(DEADZONE_MAX).toBe(0.5);
    expect(OUTER_MIN).toBe(0.5);
    expect(stickPercent(0.15)).toBe(15);
    expect(stickPercent(0.955)).toBe(96);
  });

  it("stays within its range, in whole percents", () => {
    const current = { deadzone: 0.15, saturation: 0.95 };
    expect(clampStick("deadzone", 0.234, current).deadzone).toBe(0.23);
    expect(clampStick("deadzone", -0.2, current).deadzone).toBe(0);
    expect(clampStick("deadzone", 0.9, current).deadzone).toBe(DEADZONE_MAX);
    expect(clampStick("saturation", 0.3, current).saturation).toBe(OUTER_MIN);
    expect(clampStick("saturation", 1.4, current).saturation).toBe(1);
    expect(clampStick("saturation", 0.876, current).saturation).toBe(0.88);
    expect(clampStick("deadzone", Number.NaN, current)).toEqual(current);
  });

  it("keeps the outer edge above the dead zone", () => {
    expect(MIN_GAP).toBe(0.05);
    // The dead zone cannot reach the outer edge: it stops a gap below it.
    expect(
      clampStick("deadzone", 0.5, { deadzone: 0.1, saturation: 0.5 }),
    ).toEqual({ deadzone: 0.45, saturation: 0.5 });
    // Nor the outer edge the dead zone: a profile with a larger dead zone.
    expect(
      clampStick("saturation", 0.5, { deadzone: 0.6, saturation: 0.9 }),
    ).toEqual({ deadzone: 0.6, saturation: 0.65 });
    // Away from each other nothing is held back.
    expect(
      clampStick("deadzone", 0.4, { deadzone: 0.1, saturation: 0.5 }),
    ).toEqual({ deadzone: 0.4, saturation: 0.5 });
    // From any pair that holds, a move leaves one that does.
    for (let dz = 0; dz <= 95; dz += 5)
      for (let sat = dz + 5; sat <= 100; sat += 5)
        for (let to = 0; to <= 100; to += 5) {
          const start = { deadzone: dz / 100, saturation: sat / 100 };
          for (const result of [
            clampStick("deadzone", to / 100, start),
            clampStick("saturation", to / 100, start),
          ])
            expect(
              result.saturation - result.deadzone,
              `${dz}/${sat} to ${to}`,
            ).toBeGreaterThanOrEqual(MIN_GAP - 1e-9);
        }
  });

  it("offers the sticks the controller has", () => {
    expect(sticksOf(undefined)).toEqual(["ls", "rs"]);
    expect(sticksOf(0)).toEqual(["ls", "rs"]);
    expect(sticksOf(0b001111)).toEqual(["ls", "rs"]);
    expect(sticksOf(0b000011)).toEqual(["ls"]);
    expect(sticksOf(0b001100)).toEqual(["rs"]);
    // One axis of a stick is not a stick.
    expect(sticksOf(0b001001)).toEqual([]);
  });
});
