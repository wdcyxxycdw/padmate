// What a shortcut capture records (ADR-0076 section 5, which replaces the
// keyboard-only capture of ADR-0043 and ADR-0062). Recording is one step: the
// first key that is not a modifier ends it at once, together with the
// modifiers that are held; modifiers alone are recorded when they are all let
// go; and a shortcut has at most three keys. The mouse is a key too, a button
// or one notch of the wheel inside the window. Pure: the field feeds in what
// the helper heard (it sees the keyboard, with the sided names) and what the
// window saw (releases, the mouse), and shows or applies what comes back.
import {
  chordModifiers,
  isModifierOnly,
  type Modifier,
  type MouseKey,
  modifierOrder,
} from "./shortcut-keys";

/** The most keys one shortcut records: Ctrl+Shift+Z is three. */
export const MAX_CAPTURE_KEYS = 3;

/**
 * What to do after one input: go on, showing the chord heard so far (null:
 * nothing yet), or end the capture with chord (null: nothing is recorded).
 */
export type CaptureStep =
  | { done: false; shown: string | null }
  | { done: true; chord: string | null };

/** Which modifiers are held, as the window reports them on a mouse event. */
export type HeldModifiers = Record<Modifier, boolean>;

const done = (chord: string | null): CaptureStep => ({ done: true, chord });
const wait = (shown: string | null): CaptureStep => ({ done: false, shown });

const keyCount = (chord: string) => chord.split("+").length;

/**
 * One capture. What waits is the modifiers heard so far, or a bare Esc: it
 * is a key to record, but also how a capture is cancelled (held for a second),
 * so it is recorded when it is let go.
 */
export class ShortcutRecorder {
  private waiting: { chord: string; value: string } | null = null;

  /**
   * A key went down, as the helper heard it: chord is its name with the
   * modifiers held ("LCtrl+Z"), value what to record for it when that is not
   * the name (the key's position, "LCtrl+sc:0x2C").
   */
  heard(chord: string, value: string = chord): CaptureStep {
    // Esc was the first key; this one is a second.
    if (this.waiting?.chord === "Esc") return done(this.waiting.value);
    if (keyCount(chord) > MAX_CAPTURE_KEYS) {
      // A fourth key: the three before it are the shortcut. The ones that
      // went down first, which the helper's own order may not keep.
      const before = this.waiting?.value;
      return done(
        before && keyCount(before) === MAX_CAPTURE_KEYS
          ? before
          : value.split("+").slice(0, MAX_CAPTURE_KEYS).join("+"),
      );
    }
    if (isModifierOnly(chord)) {
      this.waiting = { chord, value };
      return wait(value);
    }
    if (chord === "Esc") {
      this.waiting = { chord, value };
      return wait(value);
    }
    return done(value);
  }

  /** Every modifier is up: the ones heard are the shortcut. */
  modifiersReleased(): CaptureStep {
    const waiting = this.waiting;
    if (waiting && waiting.chord !== "Esc") return done(waiting.value);
    return wait(waiting?.value ?? null);
  }

  /** Esc was let go before its second was up: it is the shortcut. */
  escReleased(): CaptureStep {
    const waiting = this.waiting;
    if (waiting?.chord === "Esc") return done(waiting.value);
    return wait(waiting?.value ?? null);
  }

  /**
   * A mouse button or a notch of the wheel, with the modifiers the window says
   * are held. The helper's names give their sides; a modifier it did not hear
   * (it may be late) goes by the name of its kind, which is either side.
   */
  mouse(key: MouseKey, held: HeldModifiers): CaptureStep {
    if (this.waiting?.chord === "Esc") return done(this.waiting.value);
    const heard = chordModifiers(this.waiting?.chord ?? "");
    const mods = modifierOrder
      .filter((mod) => held[mod])
      .map((mod) => heard[mod] ?? mod);
    // The mouse key would be the fourth.
    if (mods.length >= MAX_CAPTURE_KEYS)
      return done(mods.slice(0, MAX_CAPTURE_KEYS).join("+"));
    return done([...mods, key].join("+"));
  }

  /** The capture ends on its own (the window lost focus): what is held is kept. */
  end(): CaptureStep {
    return done(this.waiting?.value ?? null);
  }

  /** Esc was held for the second: nothing is recorded. */
  cancel(): CaptureStep {
    this.waiting = null;
    return done(null);
  }
}
