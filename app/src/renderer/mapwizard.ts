// The rules of the controller mapping wizard (IN-19, ADR-0072): from the raw
// events of one joystick, which element the user pressed or pushed for each
// control that the wizard asks for, and the SDL mapping string of the answers.
//
// A pure module: no daemon, no DOM, no clock (event times come with the
// events). It is the TypeScript copy of daemon/internal/mapwizard, the Go
// reference, which follows SDL 3.4.16's test/testcontroller.c; mapwizard.test.ts
// runs the cases of that package's testdata/vectors.json through both. Change
// the two together. Times are nanoseconds on one clock the caller chooses; only
// differences matter.

/** The thresholds, in SDL joystick axis units (-32768..32767) and ns. */
/** How far an axis must move from its rest value before it counts. */
export const EXCURSION = 16000;
/** How close to the rest value it must come back. */
export const RETURN = 10000;
/** The movement SDL itself ignores (32767/80). */
export const JITTER = 409;
/** How long the wizard watches before the first prompt (the quiet period). */
export const QUIET_NS = 1_000_000_000;
/** How many events an element sends in the quiet period to be called noisy. */
export const NOISE_EVENTS = 4;

export type RawKind = "button" | "axis" | "hat";

/** One raw event of the joystick being mapped. */
export interface RawEvent {
  kind: RawKind;
  index: number;
  /**
   * 1 (down) or 0 (up) for a button, the position for an axis, and the
   * direction mask for a hat: up 1, right 2, down 4, left 8, 0 centered.
   */
  value: number;
  time: number;
}

/** What the wizard needs to know about the joystick (devices.rawInfo). */
export interface PadInfo {
  axes: number;
  buttons: number;
  hats: number;
  /** The rest value of each axis; an axis beyond its length rests at 0. */
  axisRest: number[];
}

/**
 * An element the wizard found: a button, a hat direction (mask) or an axis
 * with its rest and farthest values.
 */
export interface WizardInput {
  kind: RawKind;
  index: number;
  /** The hat direction, for a hat. */
  mask: number;
  /** The rest value and the farthest value of an axis. */
  rest: number;
  far: number;
}

/** What Detector.feed concludes from an event. */
export type Outcome =
  | { status: "waiting" }
  | { status: "done"; input: WizardInput }
  | { status: "rejected"; reason: typeof REASON_DIAGONAL };

/** A hat pushed to a diagonal: only one direction counts. */
export const REASON_DIAGONAL = "diagonal";

const waiting: Outcome = { status: "waiting" };

/**
 * The sign of the axis excursion: 1 when the axis went up from rest, -1 when
 * it went down, 0 for no excursion or another kind.
 */
export function inputDirection(input: WizardInput): number {
  if (input.kind !== "axis") return 0;
  if (input.far > input.rest) return 1;
  if (input.far < input.rest) return -1;
  return 0;
}

/**
 * Identifies the element and, for an axis, the direction it was pushed in: two
 * inputs with the same key read the same thing.
 */
export function inputKey(input: WizardInput): string {
  switch (input.kind) {
    case "button":
      return `b${input.index}`;
    case "hat":
      return `h${input.index}.${input.mask}`;
    case "axis":
      return `a${input.index}${inputDirection(input) < 0 ? "-" : "+"}`;
  }
}

/**
 * The binding value that reads the element as a button, a trigger or a half
 * axis: bN, hN.M, or for an axis axisSpec.
 */
export function inputValue(input: WizardInput): string {
  switch (input.kind) {
    case "button":
      return `b${input.index}`;
    case "hat":
      return `h${input.index}.${input.mask}`;
    case "axis":
      return axisSpec(input.index, input.rest, input.far);
  }
}

/** The zones of SDL's test program: above 16383 is 1, below -16384 is -1. */
function quantize(v: number): number {
  if (v > 16383) return 1;
  if (v < -16384) return -1;
  return 0;
}

/**
 * The binding value for axis index that rests at rest and was pushed to far,
 * in SDL's terms: the half of the range that the axis covers, and "~" when it
 * runs backwards. SDL maps the range from the rest value to the far value onto
 * the whole travel of the element it drives, and reads 0 for a value outside
 * of it, so the range must not be narrower than what the axis can do:
 *
 *     rest 0,      far 32767:  +aN   (a trigger that rests at 0)
 *     rest 0,      far -32768: -aN
 *     rest -32768, far anywhere above: aN   (a trigger on the full range)
 *     rest 32767,  far anywhere below: aN~  (reversed full range)
 *
 * An axis that rests at one end of its range and was moved towards the other
 * gets the whole range, however far it was pushed: the user may not have pulled
 * a trigger all the way, and a half range would read 0 for the rest of the
 * pull. An axis that rests in the middle gets the half it was pushed into.
 */
export function axisSpec(index: number, rest: number, far: number): string {
  const r = quantize(rest);
  const f = quantize(far);
  let prefix = "";
  let suffix = "";
  if (r === 0 && f > 0) prefix = "+";
  else if (r === 0 && f < 0) prefix = "-";
  else if (r < 0 && far > rest) {
    // the whole range, upwards
  } else if (r > 0 && far < rest) suffix = "~";
  else if (far > rest) prefix = "+";
  else prefix = "-";
  return `${prefix}a${index}${suffix}`;
}

const elementKey = (kind: RawKind, index: number) => `${kind}:${index}`;

/**
 * Finds the element a user presses or pushes for one prompt at a time. Use
 * observe during the quiet period, begin to start a prompt and feed for the
 * events of the prompt.
 */
export class Detector {
  private readonly seen = new Map<string, number>();
  private readonly noisyElements = new Set<string>();
  private readonly last = new Map<string, number>();
  private readonly cur: number[];
  private readonly hatCur = new Map<number, number>();

  // State of the prompt.
  private pressed = new Set<number>();
  private hatAway = new Map<number, boolean>();
  private armed: boolean[] = [];
  private far: number[] = [];
  private started = false;

  constructor(private readonly info: PadInfo) {
    this.cur = Array.from({ length: info.axes }, (_, a) => this.rest(a));
  }

  private rest(axis: number): number {
    return axis >= 0 && axis < this.info.axisRest.length
      ? this.info.axisRest[axis]
      : 0;
  }

  /** Whether the element moved on its own in the quiet period and is left out. */
  noisy(kind: RawKind, index: number): boolean {
    return this.noisyElements.has(elementKey(kind, index));
  }

  /** The indices of the axes left out as noisy, in order. */
  noisyAxes(): number[] {
    const out: number[] = [];
    for (let a = 0; a < this.info.axes; a++)
      if (this.noisy("axis", a)) out.push(a);
    return out;
  }

  /**
   * Takes an event of the quiet period, which comes before the first prompt:
   * an axis that sends NOISE_EVENTS events away from its rest value, or a
   * button or hat that sends as many, moves on its own and is not used until
   * it has sent nothing for QUIET_NS (see feed). It also keeps the position of
   * each axis for begin.
   */
  observe(event: RawEvent): void {
    const key = elementKey(event.kind, event.index);
    switch (event.kind) {
      case "axis":
        if (event.index < 0 || event.index >= this.cur.length) return;
        this.cur[event.index] = event.value;
        if (Math.abs(event.value - this.rest(event.index)) <= JITTER) return;
        break;
      case "hat":
        this.hatCur.set(event.index, event.value);
        break;
      case "button":
        break;
      default:
        return;
    }
    const count = (this.seen.get(key) ?? 0) + 1;
    this.seen.set(key, count);
    this.last.set(key, event.time);
    if (count >= NOISE_EVENTS) this.noisyElements.add(key);
  }

  /**
   * Takes an event that comes while the prompts do not listen (the window is
   * not in front, so the user's moves are no answers): it keeps the position
   * of each axis and hat, and the time a noisy element last moved, so that the
   * next prompt (begin) starts from where the controller is now. Without it, a
   * hat or a stick that was let go in the meantime would still count as held
   * and the first move of it would be taken for the way back. It answers
   * nothing. TypeScript only: the Go reference is not driven across a loss of
   * focus.
   */
  track(event: RawEvent): void {
    const key = elementKey(event.kind, event.index);
    switch (event.kind) {
      case "axis":
        if (event.index < 0 || event.index >= this.cur.length) return;
        this.cur[event.index] = event.value;
        break;
      case "hat":
        this.hatCur.set(event.index, event.value);
        break;
      case "button":
        break;
      default:
        return;
    }
    this.stillNoisy(key, event.time);
  }

  /**
   * Whether the element, if it is marked noisy, is still moving on its own at
   * time t, and takes the event as one more sign of it. An element that has
   * sent nothing for QUIET_NS is not: a thumb that rested on a stick while the
   * dialog opened moved it for a while, then let go, and the user is to map it.
   * The mark is lifted, and the event counts. Chatter does not stop for a
   * second.
   */
  private stillNoisy(key: string, t: number): boolean {
    if (!this.noisyElements.has(key)) return false;
    if (t - (this.last.get(key) ?? 0) >= QUIET_NS) {
      this.noisyElements.delete(key);
      this.seen.delete(key);
      return false;
    }
    this.last.set(key, t);
    return true;
  }

  /**
   * Starts a prompt: it forgets what the last one saw. An axis that is away
   * from its rest value now does not count until it has come back.
   */
  begin(): void {
    this.started = true;
    this.pressed = new Set();
    this.hatAway = new Map();
    for (const [hat, value] of this.hatCur) this.hatAway.set(hat, value !== 0);
    this.armed = [];
    this.far = [];
    for (let a = 0; a < this.info.axes; a++) {
      const rest = this.rest(a);
      this.far.push(rest);
      this.armed.push(Math.abs(this.cur[a] - rest) <= RETURN);
    }
  }

  /**
   * Takes an event of the prompt and says what it makes of it. Buttons count
   * when released, after they went down in this prompt. A hat counts when
   * pushed from the center to one direction; a diagonal is rejected, and the
   * hat must come back to the center before the next direction counts. An axis
   * counts when it has moved EXCURSION from its rest value and come back within
   * RETURN of it, once no other axis is still out. Of axes that all came back,
   * the one that moved farthest wins. An element that observe marked noisy is
   * left out, until it has sent nothing for QUIET_NS.
   */
  feed(event: RawEvent): Outcome {
    if (!this.started) this.begin();
    switch (event.kind) {
      case "button": {
        if (this.stillNoisy(elementKey("button", event.index), event.time))
          return waiting;
        if (event.value !== 0) {
          this.pressed.add(event.index);
          return waiting;
        }
        if (this.pressed.has(event.index)) {
          this.pressed.delete(event.index);
          return {
            status: "done",
            input: {
              kind: "button",
              index: event.index,
              mask: 0,
              rest: 0,
              far: 0,
            },
          };
        }
        return waiting;
      }
      case "hat":
        return this.feedHat(event);
      case "axis":
        return this.feedAxis(event);
    }
    return waiting;
  }

  /**
   * A hat counts when it leaves the center in one of the four directions; one
   * that is away from the center (it was when the prompt began, or it has
   * counted or been rejected) must come back to the center first.
   */
  private feedHat(event: RawEvent): Outcome {
    if (this.stillNoisy(elementKey("hat", event.index), event.time))
      return waiting;
    const away = this.hatAway.get(event.index) ?? false;
    this.hatCur.set(event.index, event.value);
    this.hatAway.set(event.index, event.value !== 0);
    if (event.value === 0 || away) return waiting;
    switch (event.value) {
      case 1:
      case 2:
      case 4:
      case 8:
        return {
          status: "done",
          input: {
            kind: "hat",
            index: event.index,
            mask: event.value,
            rest: 0,
            far: 0,
          },
        };
    }
    return { status: "rejected", reason: REASON_DIAGONAL };
  }

  private feedAxis(event: RawEvent): Outcome {
    const a = event.index;
    if (a < 0 || a >= this.cur.length) return waiting;
    this.cur[a] = event.value;
    if (this.stillNoisy(elementKey("axis", a), event.time)) return waiting;
    const rest = this.rest(a);
    const dist = Math.abs(event.value - rest);
    if (!this.armed[a]) {
      if (dist <= RETURN) {
        this.armed[a] = true;
        this.far[a] = rest;
      }
      return waiting;
    }
    if (dist > Math.abs(this.far[a] - rest)) this.far[a] = event.value;
    let best = -1;
    let bestDist = 0;
    for (let i = 0; i < this.cur.length; i++) {
      if (!this.armed[i] || this.noisy("axis", i)) continue;
      const r = this.rest(i);
      const out = Math.abs(this.far[i] - r);
      if (out < EXCURSION) continue;
      // Another axis is still out: wait for it.
      if (Math.abs(this.cur[i] - r) > RETURN) return waiting;
      if (out > bestDist) {
        best = i;
        bestDist = out;
      }
    }
    if (best < 0) return waiting;
    return {
      status: "done",
      input: {
        kind: "axis",
        index: best,
        mask: 0,
        rest: this.rest(best),
        far: this.far[best],
      },
    };
  }
}

/** One control the wizard asks for. */
export interface Step {
  /** A stable name of the step. */
  id: string;
  /**
   * The SDL element key the answer binds: "a", "dpup", "lefttrigger", or
   * "-leftx" and "+leftx" for the two directions of a stick axis.
   */
  output: string;
}

/**
 * The order of the prompts (ADR-0072): the face buttons, the d-pad, the left
 * stick (left, right, up, down) and its click, the same for the right stick,
 * the shoulders and triggers, View, Menu and Home. SDL names View back and Menu
 * start.
 */
export const DEFAULT_STEPS: readonly Step[] = [
  { id: "a", output: "a" },
  { id: "b", output: "b" },
  { id: "x", output: "x" },
  { id: "y", output: "y" },
  { id: "dpup", output: "dpup" },
  { id: "dpdown", output: "dpdown" },
  { id: "dpleft", output: "dpleft" },
  { id: "dpright", output: "dpright" },
  { id: "ls-left", output: "-leftx" },
  { id: "ls-right", output: "+leftx" },
  { id: "ls-up", output: "-lefty" },
  { id: "ls-down", output: "+lefty" },
  { id: "ls-click", output: "leftstick" },
  { id: "rs-left", output: "-rightx" },
  { id: "rs-right", output: "+rightx" },
  { id: "rs-up", output: "-righty" },
  { id: "rs-down", output: "+righty" },
  { id: "rs-click", output: "rightstick" },
  { id: "lb", output: "leftshoulder" },
  { id: "rb", output: "rightshoulder" },
  { id: "lt", output: "lefttrigger" },
  { id: "rt", output: "righttrigger" },
  { id: "view", output: "back" },
  { id: "menu", output: "start" },
  { id: "home", output: "guide" },
];

/**
 * Something the user should know before saving the mapping. View and Menu are
 * function keys and the emergency combo (ADR-0069).
 */
export interface Warning {
  code: "noBack" | "noStart";
  /** The step the warning is about. */
  step: string;
}

/** What Session.feed concludes. */
export interface Result {
  /** The step that got an element from this event, or "". */
  bound: string;
  /** Its element. */
  input?: WizardInput;
  /**
   * The steps whose binding was cleared because bound took the same element:
   * SDL's rule is that the later binding wins, and the user is to be told.
   */
  cleared: string[];
  /** Why the event was refused ("diagonal"), or "". */
  rejected: string;
}

const none = (): Result => ({ bound: "", cleared: [], rejected: "" });

const stickAxes = ["leftx", "lefty", "rightx", "righty"];

/**
 * The axis key of a stick direction output ("-leftx" is "leftx"), or "" for
 * any other output.
 */
function stickAxis(output: string): string {
  if (output.length < 2 || (output[0] !== "-" && output[0] !== "+")) return "";
  const base = output.slice(1);
  return stickAxes.includes(base) ? base : "";
}

const guidPattern = /^[0-9a-f]{32}$/;

/**
 * One run of the wizard over a joystick: the quiet period, then the prompts
 * of DEFAULT_STEPS in order, with skip, back and redo.
 */
export class Session {
  private readonly guid: string;
  private readonly detector: Detector;
  private began = false;
  private cur = 0;
  private readonly bound: (WizardInput | null)[];

  constructor(
    guid: string,
    info: PadInfo,
    private readonly start: number,
  ) {
    this.guid = guid.toLowerCase();
    this.detector = new Detector(info);
    this.bound = DEFAULT_STEPS.map(() => null);
  }

  /** The prompts, in order. */
  steps(): readonly Step[] {
    return DEFAULT_STEPS;
  }

  /** Whether the quiet period is still running at time now. */
  quiet(now: number): boolean {
    return now - this.start < QUIET_NS;
  }

  /** How many nanoseconds of the quiet period are left at time now. */
  quietLeft(now: number): number {
    return Math.max(0, QUIET_NS - (now - this.start));
  }

  /** The axes that the quiet period found moving on their own. */
  noisy(): number[] {
    return this.detector.noisyAxes();
  }

  /** The step being asked for, or null when every step is answered or skipped. */
  current(): { index: number; step: Step } | null {
    if (this.cur >= DEFAULT_STEPS.length) return null;
    return { index: this.cur, step: DEFAULT_STEPS[this.cur] };
  }

  /** Whether the session has gone through every step. */
  done(): boolean {
    return this.cur >= DEFAULT_STEPS.length;
  }

  /** Moves the clock to now: when the quiet period is over, the first prompt begins. */
  tick(now: number): void {
    if (!this.began && !this.quiet(now)) {
      this.began = true;
      this.detector.begin();
    }
  }

  /** Whether the first prompt has begun. */
  get prompting(): boolean {
    return this.began;
  }

  /**
   * Takes a raw event. During the quiet period it only observes; after it, an
   * event that the Detector accepts binds the current step, which then moves
   * on to the next.
   */
  feed(event: RawEvent): Result {
    this.tick(event.time);
    if (!this.began) {
      this.detector.observe(event);
      return none();
    }
    if (this.done()) return none();
    const outcome = this.detector.feed(event);
    if (outcome.status === "rejected")
      return { ...none(), rejected: outcome.reason };
    if (outcome.status === "done") return this.bind(outcome.input);
    return none();
  }

  /**
   * Takes an event while the prompts do not listen (Detector.track): it
   * answers nothing, and the next prompt starts from the positions it leaves.
   */
  track(event: RawEvent): void {
    this.detector.track(event);
  }

  /**
   * Answers the current step with input, clears the earlier steps that read
   * the same element, and moves on.
   */
  private bind(input: WizardInput): Result {
    const result: Result = {
      bound: DEFAULT_STEPS[this.cur].id,
      input,
      cleared: [],
      rejected: "",
    };
    const key = inputKey(input);
    this.bound.forEach((other, i) => {
      if (other && i !== this.cur && inputKey(other) === key) {
        this.bound[i] = null;
        result.cleared.push(DEFAULT_STEPS[i].id);
      }
    });
    this.bound[this.cur] = input;
    this.advance(this.cur + 1);
    return result;
  }

  private advance(index: number): void {
    this.cur = Math.max(0, Math.min(index, DEFAULT_STEPS.length));
    this.detector.begin();
  }

  /** Leaves the current step without an element and moves on. */
  skip(): void {
    if (this.done()) return;
    this.bound[this.cur] = null;
    this.advance(this.cur + 1);
  }

  /** Goes to the previous step and throws its answer away, to ask again. */
  back(): void {
    if (this.cur === 0) return;
    this.advance(this.cur - 1);
    this.bound[this.cur] = null;
  }

  /**
   * Asks the current step again from the start of its prompt. After the last
   * step it asks the last one again.
   */
  redo(): void {
    if (this.done()) this.cur = DEFAULT_STEPS.length - 1;
    this.bound[this.cur] = null;
    this.advance(this.cur);
  }

  /** The element bound to the step with id, if any. */
  binding(id: string): WizardInput | undefined {
    const index = DEFAULT_STEPS.findIndex((step) => step.id === id);
    return index >= 0 ? (this.bound[index] ?? undefined) : undefined;
  }

  /** How many steps have an element. */
  get boundCount(): number {
    return this.bound.filter((input) => input !== null).length;
  }

  /**
   * Builds the SDL mapping of the answers: the GUID of the session, the name
   * "*" (the device keeps its own name), the bindings in the order of the
   * steps, and platform:Windows. A stick direction pair that read the two ends
   * of one axis becomes the whole axis (leftx:aN, with "~" when the first
   * direction went up); otherwise each direction binds its half. The warnings
   * list what the emergency combo needs and is missing. Throws when no step has
   * an element or the GUID is not 32 hex digits.
   */
  mapping(): { mapping: string; warnings: Warning[] } {
    if (!guidPattern.test(this.guid))
      throw new Error(`the GUID ${this.guid} is not 32 hex digits`);
    const byOutput = new Map<string, WizardInput | null>();
    DEFAULT_STEPS.forEach((step, i) => {
      byOutput.set(step.output, this.bound[i]);
    });
    const fields: string[] = [];
    const doneAxes = new Set<string>();
    for (const step of DEFAULT_STEPS) {
      const input = byOutput.get(step.output) ?? null;
      const axis = stickAxis(step.output);
      if (axis) {
        if (doneAxes.has(axis)) continue;
        doneAxes.add(axis);
        const neg = byOutput.get(`-${axis}`) ?? null;
        const pos = byOutput.get(`+${axis}`) ?? null;
        if (
          neg &&
          pos &&
          neg.kind === "axis" &&
          pos.kind === "axis" &&
          neg.index === pos.index &&
          inputDirection(neg) !== 0 &&
          inputDirection(neg) === -inputDirection(pos)
        ) {
          fields.push(
            `${axis}:a${neg.index}${inputDirection(neg) > 0 ? "~" : ""}`,
          );
          continue;
        }
        if (neg) fields.push(`-${axis}:${inputValue(neg)}`);
        if (pos) fields.push(`+${axis}:${inputValue(pos)}`);
        continue;
      }
      if (input) fields.push(`${step.output}:${inputValue(input)}`);
    }
    if (fields.length === 0) throw new Error("no control was mapped");
    fields.push("platform:Windows");
    const warnings: Warning[] = [];
    if (!byOutput.get("back")) warnings.push({ code: "noBack", step: "view" });
    if (!byOutput.get("start"))
      warnings.push({ code: "noStart", step: "menu" });
    return { mapping: `${this.guid},*,${fields.join(",")}`, warnings };
  }
}
