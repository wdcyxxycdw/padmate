import type { ReactNode } from "react";
import type {
  ProfileAction,
  ProfileBinding,
  ProfileTiming,
} from "../shared/generated/schema";
import {
  actionText,
  bindingDelays,
  type Gesture,
  gestureName,
  gestures,
  isPrecisionAction,
  resolveTiming,
} from "./editor-model";
import { StepsPreview } from "./StepEditor";
import { isEmptyStep } from "./sequence-model";
import type { Language } from "./store";
import { Icon } from "./ui/Icon";
import { chordKeys, Keycaps } from "./ui/Keycaps";

const tx = (language: Language | undefined, zh: string, en: string) =>
  language === "en" ? en : zh;

/** What a card says of an action still to be filled in. */
function notSet(language: Language | undefined): ReactNode {
  return (
    <span className="drawer-summary-empty">
      {tx(language, "未设置", "Not set")}
    </span>
  );
}

const tapCount: Partial<Record<Gesture, number>> = {
  tap: 1,
  doubleTap: 2,
  tripleTap: 3,
  quadTap: 4,
};

/** Script registrations that can delay this control's gestures. */
export interface DelaySource {
  gesture: string;
  timing?: Record<string, number>;
}

/** The binding the engine effectively waits on: declared plus script gestures. */
function delayBinding(
  binding: ProfileBinding,
  timing: ProfileTiming,
  scripts: DelaySource[],
): ProfileBinding {
  const result: ProfileBinding = { ...binding };
  const base = resolveTiming(timing, binding.timing);
  for (const source of scripts) {
    if (gestures.includes(source.gesture as Gesture))
      result[source.gesture as Gesture] ??= { type: "keys", keys: "" };
    if (source.timing?.holdMs || source.timing?.multiTapMs)
      result.timing = {
        ...result.timing,
        holdMs: Math.max(
          result.timing?.holdMs ?? base.holdMs,
          source.timing.holdMs ?? 0,
        ),
        multiTapMs: Math.max(
          result.timing?.multiTapMs ?? base.multiTapMs,
          source.timing.multiTapMs ?? 0,
        ),
      };
  }
  return result;
}

function names(list: Gesture[], language: Language | undefined) {
  return list
    .map((gesture) => gestureName(gesture, language))
    .join(language === "en" ? " and " : "和");
}

/**
 * The added wait of one gesture (UI-12): a short tag for the card header and
 * one plain sentence for the expanded card. Empty when nothing waits.
 */
export function delayNote(
  gesture: Gesture,
  binding: ProfileBinding,
  timing: ProfileTiming,
  language: Language | undefined,
  scripts: DelaySource[] = [],
): { tag?: string; text?: string } {
  const effective = delayBinding(binding, timing, scripts);
  const row = bindingDelays(effective, timing).find(
    (entry) => entry.gesture === gesture,
  );
  if (!row) return {};
  const own = (list: Gesture[]) => list.every((entry) => binding[entry]);
  const lead = (list: Gesture[]) =>
    own(list)
      ? tx(language, "此按键还设置了", "This key also has ")
      : tx(language, "脚本还在此按键上使用了", "The script also uses ");
  const name = gestureName(gesture, language);
  if (row.reason === "hold" && row.ms > 0) {
    const taps = gestures.filter(
      (entry) => tapCount[entry] && effective[entry],
    );
    return {
      tag: tx(language, `延迟 ${row.ms} ms`, `+${row.ms} ms`),
      text: tx(
        language,
        `${lead(taps)}${names(taps, language)}，因此需按住 ${row.ms} ms 才判定为长按，并在此时触发。`,
        `${lead(taps)}${names(taps, language).toLowerCase()}, so a hold only counts after ${row.ms} ms and fires then.`,
      ),
    };
  }
  if (row.reason === "multi" && row.ms > 0) {
    const count = tapCount[gesture] ?? 0;
    const more = gestures.filter(
      (entry) => (tapCount[entry] ?? 0) > count && effective[entry],
    );
    return {
      tag: tx(language, `延迟 ${row.ms} ms`, `+${row.ms} ms`),
      text: tx(
        language,
        `${lead(more)}${names(more, language)}，因此${name}后需再等待 ${row.ms} ms，确认没有再次按下后才触发。`,
        `${lead(more)}${names(more, language).toLowerCase()}, so ${name.toLowerCase()} waits ${row.ms} ms for another press before it fires.`,
      ),
    };
  }
  if (gesture === "tap" && effective.hold) {
    const holdMs = resolveTiming(timing, effective.timing).holdMs;
    return {
      tag: tx(language, `延迟 ≤${holdMs} ms`, `≤${holdMs} ms`),
      text: tx(
        language,
        `${lead(["hold"])}长按，因此点按需等到松开（最多 ${holdMs} ms）后才触发。`,
        `${lead(["hold"])}a hold, so a tap fires when you let go (within ${holdMs} ms).`,
      ),
    };
  }
  return {};
}

/** Compact one-line description of an action for a collapsed card. */
export function gestureSummary(
  action: ProfileAction,
  language: Language | undefined,
  actions: { name: string; label?: string }[] = [],
  profiles: { id: string; name?: string }[] = [],
  /** Macro files bindings run that the macros folder lacks. */
  missing: readonly string[] = [],
): ReactNode {
  switch (action.type) {
    case "keys": {
      const sequence = Array.isArray(action.keys) ? action.keys : [action.keys];
      if (!sequence.some((chord) => chordKeys(chord).length))
        return notSet(language);
      return <Keycaps keys={action.keys} language={language} />;
    }
    // A script (macro, SCR-50): its name, or a warning when it is gone.
    case "macro": {
      const lost = missing.includes(action.file);
      const text = actionText(action, language, actions, profiles, missing);
      return (
        <span
          className="drawer-summary-macro"
          data-missing={lost || undefined}
          title={text}
        >
          <Icon
            name={lost ? "warn" : "script"}
            size={12}
            stroke={2.2}
            className={lost ? "drawer-macro-missing" : "drawer-macro-icon"}
          />
          <span className="drawer-summary-text">{text}</span>
        </span>
      );
    }
    // The key hints (HUD-03) are the editor's own and hold no key: just
    // their name.
    case "hud":
      return (
        <span className="drawer-summary-text">
          {actionText(action, language, actions, profiles)}
        </span>
      );
    // A sequence (MAP-22) is read like a list of shortcuts, with its waits and
    // texts among them; a text (OUT-21) by what it types, cut short. Both are
    // the step editor's own (UI-31), so neither is locked. Until something is
    // filled in, they say so as a shortcut does.
    case "sequence":
      if (action.steps.every(isEmptyStep)) return notSet(language);
      return <StepsPreview steps={action.steps} language={language} />;
    case "text": {
      if (!action.text) return notSet(language);
      const text = actionText(action, language);
      return (
        <span className="drawer-summary-text" title={action.text}>
          {text}
        </span>
      );
    }
    // Set in the file only: shown locked (one app, one profile; ADR-0045;
    // a legacy script's actions, SCR-50; a pointer jump, MOU-08). Precision
    // (MOU-05) is the editor's own and holds no key: its name and factor.
    case "script":
    case "profile":
    case "layer":
    case "mouse": {
      // A long profile name ends in an ellipsis; the tooltip has all of it.
      const name = actionText(action, language, actions, profiles);
      if (isPrecisionAction(action))
        return <span className="drawer-summary-text">{name}</span>;
      const text =
        action.type === "script"
          ? tx(language, `脚本动作 ${name}`, `Script action ${name}`)
          : name;
      return (
        <span className="drawer-summary-locked" title={text}>
          <Icon name="lock" size={12} stroke={2.2} />
          <span className="drawer-summary-text">{text}</span>
        </span>
      );
    }
  }
}

/** One gesture of a key: a header row that expands into its editor. */
export function GestureCard({
  gesture,
  open,
  onToggle,
  summary,
  delay,
  language,
  bodyId,
  children,
}: {
  gesture: Gesture;
  open: boolean;
  onToggle: () => void;
  summary: ReactNode;
  delay: { tag?: string; text?: string };
  language: Language | undefined;
  bodyId: string;
  children: ReactNode;
}) {
  return (
    <div className="drawer-gesture" data-open={open} data-gesture={gesture}>
      <button
        type="button"
        className="drawer-gesture-head"
        aria-expanded={open}
        aria-controls={open ? bodyId : undefined}
        onClick={onToggle}
      >
        <span className="drawer-gesture-name">
          {gestureName(gesture, language)}
        </span>
        <span className="drawer-gesture-summary">{summary}</span>
        {delay.tag && (
          <span className="tag amber drawer-delay-tag" title={delay.text}>
            {delay.tag}
          </span>
        )}
        <Icon
          name={open ? "chevronUp" : "chevronDown"}
          size={14}
          stroke={2}
          className="drawer-gesture-chevron"
        />
      </button>
      {open && (
        <div id={bodyId} className="drawer-gesture-body">
          {children}
        </div>
      )}
    </div>
  );
}
