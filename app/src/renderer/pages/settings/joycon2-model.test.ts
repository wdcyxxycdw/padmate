import { describe, expect, it } from "vitest";
import type { JoyCon2Status } from "../../../shared/joycon2";
import {
  PAIR_SECONDS,
  pairingAdvance,
  pairingBegin,
  pairingFound,
  pairingSecondsLeft,
  pairingStart,
} from "./joycon2-model";

const status = (change: Partial<JoyCon2Status> = {}): JoyCon2Status => ({
  available: true,
  autoConnect: true,
  windowOpen: false,
  windowLeftMs: 0,
  known: [],
  links: [],
  ...change,
});

const left = {
  addr: "A1",
  serial: "S-L",
  side: "L",
  connected: true,
} as const;
const right = {
  addr: "A2",
  serial: "S-R",
  side: "R",
  connected: true,
} as const;

describe("what a pairing window found", () => {
  it("is nothing while the state is as it was", () => {
    const before = status({ known: [left] });
    expect(pairingFound(pairingStart(before), before)).toBeNull();
    expect(pairingFound(pairingStart(null), status())).toBeNull();
  });

  it("is the side of a serial number that was not remembered", () => {
    const start = pairingStart(status({ known: [left] }));
    expect(pairingFound(start, status({ known: [left, right] }))).toBe("R");
    expect(pairingFound(pairingStart(null), status({ known: [left] }))).toBe(
      "L",
    );
  });

  it("is the side of a remembered controller that connected during the window", () => {
    const start = pairingStart(
      status({ known: [{ ...left, connected: false }] }),
    );
    const link = {
      addr: "A1",
      serial: "S-L",
      side: "L",
      state: "connected",
      known: true,
    } as const;
    expect(pairingFound(start, status({ known: [left], links: [link] }))).toBe(
      "L",
    );
    // A connection that was there before, one that is closing and one that is
    // not remembered are not finds.
    expect(
      pairingFound(
        pairingStart(status({ known: [left], links: [link] })),
        status({ known: [left], links: [link] }),
      ),
    ).toBeNull();
    expect(
      pairingFound(
        start,
        status({ known: [left], links: [{ ...link, state: "closing" }] }),
      ),
    ).toBeNull();
    expect(
      pairingFound(
        start,
        status({ known: [left], links: [{ ...link, known: false }] }),
      ),
    ).toBeNull();
  });
});

describe("the pairing dialog", () => {
  it("begins with the whole window", () => {
    const begin = pairingBegin(status({ known: [left] }), 1000);
    expect(begin).toMatchObject({
      phase: "waiting",
      opened: false,
      deadline: 1000 + PAIR_SECONDS * 1000,
    });
  });

  it("takes its end from the daemon while the window is open", () => {
    const begin = pairingBegin(null, 0);
    const open = pairingAdvance(
      begin,
      status({ windowOpen: true, windowLeftMs: 29000 }),
      500,
    );
    expect(open).toMatchObject({
      phase: "waiting",
      opened: true,
      deadline: 29500,
    });
  });

  it("waits through a state from before the window opened", () => {
    const begin = pairingBegin(null, 0);
    expect(pairingAdvance(begin, status(), 100)).toBe(begin);
  });

  it("says what was found when the window has been open and is closed", () => {
    const begin = pairingBegin(null, 0);
    const open = pairingAdvance(
      begin,
      status({ windowOpen: true, windowLeftMs: 20000 }),
      0,
    );
    expect(pairingAdvance(open, status({ known: [right] }), 5000)).toEqual({
      phase: "found",
      side: "R",
    });
    expect(pairingAdvance(open, status(), 30000)).toEqual({ phase: "none" });
  });

  it("is over for good, and a daemon that cannot do Joy-Con 2 any more is a failure", () => {
    const begin = pairingBegin(null, 0);
    expect(pairingAdvance(begin, status({ available: false }), 0)).toEqual({
      phase: "failed",
    });
    const none = { phase: "none" } as const;
    expect(
      pairingAdvance(none, status({ windowOpen: true, windowLeftMs: 1000 }), 0),
    ).toBe(none);
  });

  it("counts whole seconds up, never below zero", () => {
    expect(pairingSecondsLeft(30000, 0)).toBe(30);
    expect(pairingSecondsLeft(30000, 100)).toBe(30);
    expect(pairingSecondsLeft(30000, 1000)).toBe(29);
    expect(pairingSecondsLeft(30000, 29001)).toBe(1);
    expect(pairingSecondsLeft(30000, 30000)).toBe(0);
    expect(pairingSecondsLeft(30000, 40000)).toBe(0);
  });
});
