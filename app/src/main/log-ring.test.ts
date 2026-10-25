import { describe, expect, it } from "vitest";
import { LogRing } from "./log-ring";

describe("the updater's log ring (PKG-10)", () => {
  it("keeps the latest lines with their level, oldest first", () => {
    const ring = new LogRing(3, () => 0);
    ring.info("one");
    ring.warn("two");
    ring.error("three");
    ring.info("four");
    expect(ring.lines()).toEqual([
      "1970-01-01T00:00:00.000Z WARN two",
      "1970-01-01T00:00:00.000Z ERROR three",
      "1970-01-01T00:00:00.000Z INFO four",
    ]);
  });

  it("takes the debug lines too, and what is not text", () => {
    const ring = new LogRing(10, () => 0);
    ring.debug("detail");
    ring.info(new Error("boom"));
    ring.info(undefined);
    ring.info({ a: 1 });
    expect(ring.lines().map((line) => line.slice(25))).toEqual([
      "DEBUG detail",
      "INFO Error: boom",
      "INFO ",
      'INFO {"a":1}',
    ]);
  });

  it("cuts a long line and flattens a line break", () => {
    const ring = new LogRing(2, () => 0);
    ring.info(`a${"b".repeat(1000)}`);
    ring.info("first\r\nsecond");
    const [long, broken] = ring.lines();
    expect(long.length).toBeLessThanOrEqual(25 + 5 + 500);
    expect(long.endsWith("…")).toBe(true);
    expect(broken.slice(25)).toBe("INFO first second");
  });

  it("answers with a copy", () => {
    const ring = new LogRing(2, () => 0);
    ring.info("x");
    ring.lines().length = 0;
    expect(ring.lines()).toHaveLength(1);
  });
});
