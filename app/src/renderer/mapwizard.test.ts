import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import {
  axisSpec,
  DEFAULT_STEPS,
  Detector,
  inputKey,
  inputValue,
  type Outcome,
  type PadInfo,
  QUIET_NS,
  type RawEvent,
  type RawKind,
  Session,
} from "./mapwizard";

// daemon/internal/mapwizard is the Go reference of these rules (ADR-0072);
// testdata/vectors.json holds the cases both copies must agree on, like
// ipc-wiring.test.ts reads the Go files it checks.
interface Vectors {
  axisSpec: {
    name: string;
    index: number;
    rest: number;
    far: number;
    want: string;
  }[];
  detect: {
    name: string;
    info: { axes: number; buttons: number; hats: number; axisRest?: number[] };
    quiet?: VectorEvent[];
    prompt: VectorEvent[];
    want: string[];
  }[];
}
interface VectorEvent {
  k: "b" | "a" | "h";
  i: number;
  v: number;
  waitMs?: number;
}

const vectors = JSON.parse(
  readFileSync(
    new URL(
      "../../../daemon/internal/mapwizard/testdata/vectors.json",
      import.meta.url,
    ),
    "utf8",
  ),
) as Vectors;

const kinds: Record<VectorEvent["k"], RawKind> = {
  b: "button",
  a: "axis",
  h: "hat",
};
// An absent waitMs and a waitMs of 0 are both 10 ms, as in the Go runner
// (vEv.step in detect_test.go): `||`, not `??`.
const stepNs = (event: VectorEvent) => (event.waitMs || 10) * 1_000_000;

function describeOutcome(outcome: Outcome): string {
  if (outcome.status === "done") return `done:${inputValue(outcome.input)}`;
  if (outcome.status === "rejected") return `rejected:${outcome.reason}`;
  return "";
}

describe("the wizard's rules agree with the Go reference (vectors.json)", () => {
  it("has the vectors", () => {
    expect(vectors.axisSpec.length).toBeGreaterThan(10);
    expect(vectors.detect.length).toBeGreaterThan(20);
    // The optional waitMs of an event is used by some of them.
    expect(
      vectors.detect.some((c) => c.prompt.some((e) => e.waitMs !== undefined)),
    ).toBe(true);
  });

  it.each(vectors.axisSpec)("axisSpec: $name", (c) => {
    expect(axisSpec(c.index, c.rest, c.far)).toBe(c.want);
  });

  it.each(vectors.detect)("detect: $name", (c) => {
    expect(c.want).toHaveLength(c.prompt.length);
    const detector = new Detector({
      axes: c.info.axes,
      buttons: c.info.buttons,
      hats: c.info.hats,
      axisRest: c.info.axisRest ?? [],
    });
    let now = 0;
    const event = (e: VectorEvent): RawEvent => {
      now += stepNs(e);
      return { kind: kinds[e.k], index: e.i, value: e.v, time: now };
    };
    for (const e of c.quiet ?? []) detector.observe(event(e));
    detector.begin();
    c.prompt.forEach((e, index) => {
      expect(describeOutcome(detector.feed(event(e))), `event ${index}`).toBe(
        c.want[index],
      );
    });
  });
});

describe("keeping track while the prompts do not listen", () => {
  const info: PadInfo = { axes: 2, buttons: 4, hats: 1, axisRest: [] };
  const tick = (start: number) => {
    let t = start;
    return (kind: RawKind, index: number, value: number): RawEvent => {
      t += 10_000_000;
      return { kind, index, value, time: t };
    };
  };

  it("counts a hat again that was let go while away", () => {
    const d = new Detector(info);
    const ev = tick(0);
    d.begin();
    expect(d.feed(ev("hat", 0, 1)).status).toBe("done");
    // The next prompt begins with the hat still held; the user looks away
    // and lets go; back, the prompt is asked again.
    d.begin();
    d.track(ev("hat", 0, 0));
    d.begin();
    const next = d.feed(ev("hat", 0, 4));
    expect(next.status).toBe("done");
  });

  it("still waits for a hat that is held when the prompt comes back", () => {
    const d = new Detector(info);
    const ev = tick(0);
    d.begin();
    expect(d.feed(ev("hat", 0, 1)).status).toBe("done");
    d.begin();
    d.track(ev("hat", 0, 1));
    d.begin();
    expect(d.feed(ev("hat", 0, 4)).status).toBe("waiting");
    d.feed(ev("hat", 0, 0));
    expect(d.feed(ev("hat", 0, 4)).status).toBe("done");
  });

  it("arms an axis that came back while away", () => {
    const d = new Detector(info);
    const ev = tick(0);
    d.begin();
    // Out, and the user looks away without letting go.
    expect(d.feed(ev("axis", 0, -30000)).status).toBe("waiting");
    d.track(ev("axis", 0, 0));
    d.begin();
    expect(d.feed(ev("axis", 0, -30000)).status).toBe("waiting");
    const back = d.feed(ev("axis", 0, 0));
    expect(back.status).toBe("done");
    if (back.status === "done") expect(inputKey(back.input)).toBe("a0-");
  });

  it("does not arm an axis that is still out when the prompt comes back", () => {
    const d = new Detector(info);
    const ev = tick(0);
    d.begin();
    d.track(ev("axis", 0, -30000));
    d.begin();
    // It has to come back before it counts: the first move is that way.
    expect(d.feed(ev("axis", 0, 0)).status).toBe("waiting");
    expect(d.feed(ev("axis", 0, -30000)).status).toBe("waiting");
    expect(d.feed(ev("axis", 0, 0)).status).toBe("done");
  });

  it("keeps an element that moves by itself marked noisy across the time away", () => {
    const d = new Detector(info);
    let t = 0;
    for (let i = 0; i < 6; i++) {
      t += 10_000_000;
      d.observe({
        kind: "axis",
        index: 1,
        value: i % 2 ? -20000 : 20000,
        time: t,
      });
    }
    expect(d.noisy("axis", 1)).toBe(true);
    d.begin();
    // It was still sending 900 ms in, while the window was away; 700 ms
    // later it is within a second of that, though more than a second after
    // the quiet period.
    d.track({ kind: "axis", index: 1, value: 20000, time: 900_000_000 });
    d.feed({ kind: "axis", index: 1, value: -20000, time: 1_600_000_000 });
    expect(d.noisy("axis", 1)).toBe(true);
  });

  it("answers nothing, also through a session", () => {
    const s = new Session("ff00aabb000000000000000000007600", info, 0);
    s.tick(QUIET_NS);
    s.track({ kind: "button", index: 0, value: 1, time: QUIET_NS + 1 });
    s.track({ kind: "button", index: 0, value: 0, time: QUIET_NS + 2 });
    expect(s.boundCount).toBe(0);
    expect(s.current()?.step.id).toBe("a");
  });
});

describe("the session builds the mapping the Go session builds (session_test.go)", () => {
  const guid = "ff00aabb000000000000000000007600";

  /** Drives a Session with the clock after the quiet period. */
  class Run {
    readonly session: Session;
    now: number;
    constructor(info: Partial<PadInfo>, guidText = guid.toUpperCase()) {
      this.session = new Session(
        guidText,
        { axes: 0, buttons: 0, hats: 0, axisRest: [], ...info },
        0,
      );
      this.now = QUIET_NS;
      this.session.tick(this.now);
    }
    feed(kind: RawKind, index: number, value: number) {
      this.now += 5_000_000;
      return this.session.feed({ kind, index, value, time: this.now });
    }
    press(button: number) {
      this.feed("button", button, 1);
      return this.feed("button", button, 0);
    }
    push(axis: number, value: number, rest: number) {
      this.feed("axis", axis, value);
      return this.feed("axis", axis, rest);
    }
    bound(result: { bound: string }, step: string) {
      const current = this.session.current();
      expect(
        result.bound,
        `at step ${current?.index} ${current?.step.id}`,
      ).toBe(step);
    }
    skipTo(id: string) {
      for (;;) {
        const current = this.session.current();
        if (!current) throw new Error(`no step ${id}`);
        if (current.step.id === id) return;
        this.session.skip();
      }
    }
    mapping(): string {
      return this.session.mapping().mapping;
    }
  }

  it("asks for the 25 controls in the ADR's order", () => {
    expect(DEFAULT_STEPS.map((s) => s.id)).toEqual([
      "a",
      "b",
      "x",
      "y",
      "dpup",
      "dpdown",
      "dpleft",
      "dpright",
      "ls-left",
      "ls-right",
      "ls-up",
      "ls-down",
      "ls-click",
      "rs-left",
      "rs-right",
      "rs-up",
      "rs-down",
      "rs-click",
      "lb",
      "rb",
      "lt",
      "rt",
      "view",
      "menu",
      "home",
    ]);
    expect(DEFAULT_STEPS).toHaveLength(25);
  });

  it("binds nothing in the quiet period, then the first release", () => {
    const s = new Session(
      guid,
      { axes: 2, buttons: 8, hats: 0, axisRest: [] },
      100,
    );
    expect(s.quiet(100 + QUIET_NS - 1)).toBe(true);
    expect(
      s.feed({ kind: "button", index: 1, value: 1, time: 200 }).bound,
    ).toBe("");
    expect(
      s.feed({ kind: "button", index: 1, value: 0, time: 300 }).bound,
    ).toBe("");
    expect(s.current()?.step.id).toBe("a");
    expect(
      s.feed({ kind: "button", index: 2, value: 1, time: 100 + QUIET_NS + 1 })
        .bound,
    ).toBe("");
    expect(
      s.feed({ kind: "button", index: 2, value: 0, time: 100 + QUIET_NS + 2 })
        .bound,
    ).toBe("a");
  });

  it("builds a whole mapping", () => {
    const r = new Run({
      axes: 6,
      buttons: 14,
      hats: 1,
      axisRest: [0, 0, 0, 0, -32768, -32768],
    });
    r.bound(r.press(0), "a");
    r.bound(r.press(1), "b");
    r.bound(r.press(2), "x");
    r.bound(r.press(3), "y");
    for (const mask of [1, 4, 8, 2]) {
      r.feed("hat", 0, mask);
      r.feed("hat", 0, 0);
    }
    expect(r.session.current()?.step.id).toBe("ls-left");
    r.bound(r.push(0, -30000, 0), "ls-left");
    r.bound(r.push(0, 30000, 0), "ls-right");
    r.bound(r.push(1, -30000, 0), "ls-up");
    r.bound(r.push(1, 30000, 0), "ls-down");
    r.bound(r.press(8), "ls-click");
    r.bound(r.push(2, -30000, 0), "rs-left");
    r.bound(r.push(2, 30000, 0), "rs-right");
    r.bound(r.push(3, 30000, 0), "rs-up"); // this stick's y runs backwards
    r.bound(r.push(3, -30000, 0), "rs-down");
    r.bound(r.press(9), "rs-click");
    r.bound(r.press(4), "lb");
    r.bound(r.press(5), "rb");
    r.bound(r.push(4, 32767, -32768), "lt");
    r.bound(r.push(5, 32767, -32768), "rt");
    r.bound(r.press(6), "view");
    r.bound(r.press(7), "menu");
    r.bound(r.press(10), "home");
    expect(r.session.done()).toBe(true);
    const built = r.session.mapping();
    expect(built.mapping).toBe(
      `${guid},*,a:b0,b:b1,x:b2,y:b3,dpup:h0.1,dpdown:h0.4,dpleft:h0.8,dpright:h0.2,` +
        "leftx:a0,lefty:a1,leftstick:b8,rightx:a2,righty:a3~,rightstick:b9," +
        "leftshoulder:b4,rightshoulder:b5,lefttrigger:a4,righttrigger:a5,back:b6,start:b7,guide:b10,platform:Windows",
    );
    expect(built.warnings).toEqual([]);
  });

  it.each([
    [
      "left stick the natural way",
      (r: Run) => {
        r.skipTo("ls-left");
        r.push(0, -30000, 0);
        r.push(0, 30000, 0);
      },
      "leftx:a0",
    ],
    [
      "left stick inverted",
      (r: Run) => {
        r.skipTo("ls-left");
        r.push(0, 30000, 0);
        r.push(0, -30000, 0);
      },
      "leftx:a0~",
    ],
    [
      "left and right on buttons",
      (r: Run) => {
        r.skipTo("ls-left");
        r.press(8);
        r.press(9);
      },
      "-leftx:b8,+leftx:b9",
    ],
    [
      "left and right on two axes",
      (r: Run) => {
        r.skipTo("ls-left");
        r.push(0, -30000, 0);
        r.push(2, 30000, 0);
      },
      "-leftx:-a0,+leftx:+a2",
    ],
    [
      "only one direction",
      (r: Run) => {
        r.skipTo("ls-left");
        r.push(0, -30000, 0);
      },
      "-leftx:-a0",
    ],
    [
      "only the other direction, on a button",
      (r: Run) => {
        r.skipTo("ls-right");
        r.press(5);
      },
      "+leftx:b5",
    ],
    [
      "vertical axis, up is negative",
      (r: Run) => {
        r.skipTo("ls-up");
        r.push(1, -30000, 0);
        r.push(1, 30000, 0);
      },
      "lefty:a1",
    ],
    [
      "the same direction twice is a duplicate, not a pair",
      (r: Run) => {
        r.skipTo("ls-left");
        r.push(0, -30000, 0);
        r.push(0, -30000, 0);
      },
      "+leftx:-a0",
    ],
  ])("stick directions: %s", (_name, act, fields) => {
    const r = new Run({ axes: 4, buttons: 12 });
    act(r);
    expect(r.mapping()).toBe(`${guid},*,${fields},platform:Windows`);
  });

  it("clears the earlier step when a later one takes the same element", () => {
    const r = new Run({ buttons: 8 });
    r.bound(r.press(3), "a");
    const result = r.press(3);
    r.bound(result, "b");
    expect(result.cleared).toEqual(["a"]);
    expect(r.session.binding("a")).toBeUndefined();
    expect(r.session.binding("b")?.index).toBe(3);
  });

  it("skips, goes back and redoes", () => {
    const r = new Run({ buttons: 8 });
    r.bound(r.press(0), "a");
    r.session.skip(); // b
    expect(r.session.current()?.step.id).toBe("x");
    r.bound(r.press(2), "x");
    r.session.back();
    expect(r.session.current()?.step.id).toBe("x");
    expect(r.session.binding("x")).toBeUndefined();
    r.bound(r.press(5), "x");
    // Redo drops a half-done prompt: a press that never released.
    r.feed("button", 6, 1);
    r.session.redo();
    expect(r.feed("button", 6, 0).bound).toBe("");
    r.bound(r.press(6), "y");
    // Back at the first step stays there.
    const first = new Session(
      guid,
      { axes: 0, buttons: 0, hats: 0, axisRest: [] },
      0,
    );
    first.back();
    expect(first.current()?.index).toBe(0);
    expect(r.mapping()).toBe(`${guid},*,a:b0,x:b5,y:b6,platform:Windows`);
  });

  it("asks the last step again for Redo after the end", () => {
    const r = new Run({ buttons: 4 });
    for (const _ of DEFAULT_STEPS) r.session.skip();
    expect(r.session.done()).toBe(true);
    expect(r.session.current()).toBeNull();
    r.session.redo();
    expect(r.session.current()?.step.id).toBe("home");
  });

  it("warns when View or Menu is not mapped", () => {
    const r = new Run({ buttons: 8 });
    r.bound(r.press(0), "a");
    expect(r.session.mapping().warnings).toEqual([
      { code: "noBack", step: "view" },
      { code: "noStart", step: "menu" },
    ]);
    r.skipTo("view");
    r.press(6);
    expect(r.session.mapping().warnings).toEqual([
      { code: "noStart", step: "menu" },
    ]);
  });

  it("refuses a mapping of nothing", () => {
    const r = new Run({});
    expect(() => r.session.mapping()).toThrow(/no control/);
  });

  it("rejects a diagonal hat and takes the next direction after the centre", () => {
    const r = new Run({ hats: 1, buttons: 4 });
    r.skipTo("dpup");
    const diagonal = r.feed("hat", 0, 3);
    expect(diagonal.rejected).toBe("diagonal");
    expect(diagonal.bound).toBe("");
    r.feed("hat", 0, 0);
    r.bound(r.feed("hat", 0, 1), "dpup");
  });

  it("reports the axes the quiet period found moving on their own", () => {
    const s = new Session(
      guid,
      { axes: 3, buttons: 0, hats: 0, axisRest: [] },
      0,
    );
    for (let i = 0; i < 6; i++)
      s.feed({
        kind: "axis",
        index: 2,
        value: i % 2 === 1 ? -20000 : 20000,
        time: i * 1_000_000,
      });
    expect(s.noisy()).toEqual([2]);
  });

  it("maps a stick that a thumb rested on once it has been still for a second", () => {
    const s = new Session(
      guid,
      { axes: 2, buttons: 8, hats: 0, axisRest: [] },
      0,
    );
    [600, 900, 1200, 800, 500, 300].forEach((value, i) => {
      s.feed({ kind: "axis", index: 0, value, time: (i + 1) * 100_000_000 });
    });
    expect(s.noisy()).toEqual([0]);
    for (let i = 0; i < 8; i++) s.skip(); // a b x y and the d-pad
    expect(s.current()?.step.id).toBe("ls-left");
    const at = 3_000_000_000;
    s.feed({ kind: "axis", index: 0, value: -30000, time: at });
    const result = s.feed({
      kind: "axis",
      index: 0,
      value: 0,
      time: at + 10_000_000,
    });
    expect(result.bound).toBe("ls-left");
    expect(result.input && inputKey(result.input)).toBe("a0-");
    expect(s.noisy()).toEqual([]);
  });

  it("lowercases the GUID of the mapping", () => {
    const r = new Run({ buttons: 2 }, "FF00AABB000000000000000000007600");
    r.press(0);
    expect(r.mapping().startsWith(`${guid},*,`)).toBe(true);
  });

  it("refuses a GUID that is not 32 hex digits", () => {
    const r = new Run({ buttons: 2 }, "not-a-guid");
    r.press(0);
    expect(() => r.session.mapping()).toThrow(/GUID/);
  });
});
