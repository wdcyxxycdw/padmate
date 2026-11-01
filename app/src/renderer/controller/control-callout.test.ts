import { describe, expect, it } from "vitest";
import {
  calloutFor,
  calloutOf,
  isStickPress,
  stickPress,
} from "./control-callout";

describe("a stick's two cells", () => {
  it("names the press cell after the stick, with .press", () => {
    expect(stickPress("leftstick")).toBe("leftstick.press");
    expect(stickPress("rightstick")).toBe("rightstick.press");
    expect(isStickPress("leftstick.press")).toBe(true);
    expect(isStickPress("rightstick.press")).toBe(true);
    // The movement cell keeps the stick's own id.
    expect(isStickPress("leftstick")).toBe(false);
    expect(isStickPress("south")).toBe(false);
    expect(isStickPress("ls.press")).toBe(false);
  });

  it("puts both cells on the callout of the stick", () => {
    expect(calloutOf("leftstick.press")).toBe("leftstick");
    expect(calloutOf("rightstick.press")).toBe("rightstick");
    expect(calloutOf("leftstick")).toBe("leftstick");
    expect(calloutOf("south")).toBe("south");
  });
});

describe("calloutFor", () => {
  it("sends the stick's own binding (and its L3/R3 alias) to the press cell", () => {
    expect(calloutFor("leftstick")).toBe("leftstick.press");
    expect(calloutFor("l3")).toBe("leftstick.press");
    expect(calloutFor("rightstick")).toBe("rightstick.press");
    expect(calloutFor("r3")).toBe("rightstick.press");
    // Already a cell id: left as it is.
    expect(calloutFor("leftstick.press")).toBe("leftstick.press");
  });

  it("keeps directions, rings, ticks and raw axes on the movement cell", () => {
    for (const key of [
      "ls.up",
      "ls.upleft",
      "ls.active",
      "ls.inner",
      "ls.outer",
      "ls.cw",
      "ls.ccw",
      "leftx+",
      "lefty-",
    ])
      expect(calloutFor(key), key).toBe("leftstick");
    for (const key of ["rs.down", "rs.cw", "rightx-", "righty+"])
      expect(calloutFor(key), key).toBe("rightstick");
  });

  it("leaves the other controls as they were", () => {
    expect(calloutFor("a")).toBe("south");
    expect(calloutFor("lt")).toBe("lefttrigger");
    expect(calloutFor("rt.full")).toBe("righttrigger");
    expect(calloutFor("lefttrigger+")).toBe("lefttrigger");
    expect(calloutFor("dpad.upleft")).toBe("dpad.up");
    expect(calloutFor("dpdown")).toBe("dpad.down");
  });
});
