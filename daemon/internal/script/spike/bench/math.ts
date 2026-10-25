// Stand-ins for padmate/math and StickState.direction (SCRIPTING §2.1),
// which the M0d stub does not have. Angles: 0° is up, clockwise.

export function clamp(v: number, lo: number, hi: number): number {
  return v < lo ? lo : v > hi ? hi : v;
}

export function remap(v: number, inLo: number, inHi: number, outLo: number, outHi: number): number {
  const t = clamp((v - inLo) / (inHi - inLo), 0, 1);
  return outLo + t * (outHi - outLo);
}

export function angle(x: number, y: number): number {
  const a = (Math.atan2(x, y) * 180) / Math.PI;
  return a < 0 ? a + 360 : a;
}

export interface DirectionOptions {
  deadzone?: number;
  hysteresisDeg?: number;
  previous?: number;
}

/** The sector the stick points at, 0 = up, or -1 inside the deadzone. The
 * previous sector is kept until the angle leaves it by more than the
 * hysteresis. */
export function direction(x: number, y: number, sectors: 4 | 8, opts: DirectionOptions = {}): number {
  if (Math.hypot(x, y) < (opts.deadzone ?? 0)) {
    return -1;
  }
  const size = 360 / sectors;
  const a = angle(x, y);
  const prev = opts.previous ?? -1;
  if (prev >= 0) {
    let d = Math.abs(a - prev * size) % 360;
    if (d > 180) {
      d = 360 - d;
    }
    if (d <= size / 2 + (opts.hysteresisDeg ?? 0)) {
      return prev;
    }
  }
  return Math.floor(((a + size / 2) % 360) / size);
}

/** Turns a rate in Hz into whole events per call (SCRIPTING §1.4): dt is
 * cut to two tick periods and a call returns at most maxPerCall. */
export class RateAccumulator {
  private acc = 0;

  constructor(
    private readonly tickMs = 8,
    private readonly maxPerCall = 1,
  ) {}

  step(hz: number, dtMs: number): number {
    this.acc += (hz * Math.min(dtMs, 2 * this.tickMs)) / 1000;
    const n = Math.min(Math.floor(this.acc), this.maxPerCall);
    this.acc = Math.min(this.acc - n, 1);
    return n;
  }

  reset(): void {
    this.acc = 0;
  }
}
