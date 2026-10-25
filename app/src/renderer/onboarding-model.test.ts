import { describe, expect, it } from "vitest";
import type { DeviceEntry, ProfileEntry, StatusResult } from "../shared/api";
import { ADVANCE_MS, padNames, shouldOffer } from "./onboarding-model";

const status = {
  profileId: "default",
  devices: [],
  slots: [],
} as unknown as StatusResult | null;

// A new library: the daemon made the fixed default and uses it.
const fresh = {
  connection: { phase: "connected" as const },
  status,
  profiles: [{ id: "default", name: "默认", default: true }] as ProfileEntry[],
  profilesLoaded: true,
  profilesError: "",
  onboardingDone: false,
};

describe("when the first-run page opens by itself", () => {
  it("opens for a new library with only the fixed default", () => {
    expect(shouldOffer(fresh)).toBe(true);
    // Still before the daemon listed the default, or with mapping off.
    expect(shouldOffer({ ...fresh, profiles: [] })).toBe(true);
    expect(
      shouldOffer({
        ...fresh,
        status: { ...status, profileId: "" } as StatusResult,
      }),
    ).toBe(true);
  });

  it.each([
    ["the page is done", { onboardingDone: true }],
    ["not connected", { connection: { phase: "connecting" as const } }],
    ["no status yet", { status: null }],
    ["the list has not answered", { profilesLoaded: false }],
    ["a session without a library", { profilesError: "no profile library" }],
    [
      "other profiles exist (an upgrade)",
      { profiles: [...fresh.profiles, { id: "paint" }] },
    ],
    [
      "another profile is in use (a session started with one)",
      { status: { ...status, profileId: "paint" } as StatusResult },
    ],
  ])("stays closed when %s", (_name, change) => {
    expect(shouldOffer({ ...fresh, ...change })).toBe(false);
  });
});

describe("first-run page text", () => {
  it("names every connected controller in order", () => {
    const device = (id: number, name: string) =>
      ({ info: { id, name } }) as DeviceEntry;
    expect(padNames(null)).toEqual([]);
    expect(padNames(status)).toEqual([]);
    expect(
      padNames({
        ...status,
        devices: [device(2, "Nova 2 Lite"), device(1, "Joy-Con (L)")],
      } as StatusResult),
    ).toEqual(["Nova 2 Lite", "Joy-Con (L)"]);
  });

  it("waits long enough to read the name, but not long", () => {
    expect(ADVANCE_MS).toBeGreaterThanOrEqual(1000);
    expect(ADVANCE_MS).toBeLessThanOrEqual(3000);
  });
});
