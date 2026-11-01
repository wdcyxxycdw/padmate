import { describe, expect, it } from "vitest";
import { parseJoyCon2Status } from "./joycon2";

// The shape of ipc_joycon2.go's rpcJoyCon2Status.
const status = {
  available: true,
  autoConnect: true,
  windowOpen: true,
  windowEndsAt: 1790000030000,
  windowLeftMs: 29500,
  scanning: true,
  known: [
    {
      addr: "C8:48:05:FA:01:01",
      serial: "FAKEJC2L0001",
      side: "L",
      connected: true,
    },
  ],
  links: [
    {
      addr: "C8:48:05:FA:01:01",
      serial: "FAKEJC2L0001",
      side: "L",
      player: 1,
      firmware: "2.1.4",
      interval: "simulated",
      state: "connected",
      device: 1001,
      known: true,
    },
  ],
};

describe("parseJoyCon2Status", () => {
  it("reads what the page uses and drops the rest", () => {
    expect(parseJoyCon2Status(status)).toEqual({
      available: true,
      autoConnect: true,
      windowOpen: true,
      windowLeftMs: 29500,
      known: [
        {
          addr: "C8:48:05:FA:01:01",
          serial: "FAKEJC2L0001",
          side: "L",
          connected: true,
        },
      ],
      links: [
        {
          addr: "C8:48:05:FA:01:01",
          serial: "FAKEJC2L0001",
          side: "L",
          state: "connected",
          known: true,
        },
      ],
    });
  });

  it("has no time left once the window is closed, and takes a link without a serial", () => {
    const parsed = parseJoyCon2Status({
      ...status,
      windowOpen: false,
      windowLeftMs: 500,
      links: [{ addr: "A", side: "R", state: "connecting", known: false }],
    });
    expect(parsed?.windowLeftMs).toBe(0);
    expect(parsed?.links).toEqual([
      { addr: "A", side: "R", state: "connecting", known: false },
    ]);
  });

  it("reads a daemon without Joy-Con 2 as unavailable, whatever else it holds", () => {
    expect(
      parseJoyCon2Status({
        available: false,
        unavailable: "this input has no Joy-Con 2 support",
        known: [],
        links: [],
      }),
    ).toEqual({
      available: false,
      autoConnect: false,
      windowOpen: false,
      windowLeftMs: 0,
      known: [],
      links: [],
    });
  });

  it("refuses anything else", () => {
    const bad: unknown[] = [
      null,
      "x",
      [],
      {},
      { available: "yes" },
      { ...status, autoConnect: 1 },
      { ...status, windowOpen: "no" },
      { ...status, windowLeftMs: -1 },
      { ...status, windowLeftMs: "3" },
      { ...status, known: null },
      { ...status, known: [{ ...status.known[0], side: "X" }] },
      { ...status, known: [{ ...status.known[0], serial: 7 }] },
      { ...status, known: [{ ...status.known[0], connected: "yes" }] },
      { ...status, known: [null] },
      { ...status, links: [{ ...status.links[0], state: 1 }] },
      { ...status, links: [{ ...status.links[0], serial: 1 }] },
      { ...status, links: {} },
      {
        ...status,
        known: Array.from({ length: 65 }, (_, i) => ({
          ...status.known[0],
          serial: `S${i}`,
        })),
      },
    ];
    for (const value of bad)
      expect(parseJoyCon2Status(value), JSON.stringify(value)).toBeNull();
  });
});
