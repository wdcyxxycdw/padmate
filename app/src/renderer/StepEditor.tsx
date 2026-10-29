// The parts of the step editor of the key drawer (UI-31, ADR-0070): a wait
// step of a sequence, the buttons that move and remove a step, and the line
// that reads a sequence. A shortcut step is the shortcut field of
// BindingEditor. The drawer has no text step any more (ADR-0076); a text step
// of a file is only read in the line.

import type { ReactNode } from "react";
import { Fragment, useId } from "react";
import { shortText } from "./editor-model";
import {
  type NewStepKind,
  type Step,
  stepKind,
  waitRange,
} from "./sequence-model";
import type { Language } from "./store";
import { FieldError, numberParser, useCommitField } from "./ui/commit-field";
import { Icon } from "./ui/Icon";
import { Keycaps } from "./ui/Keycaps";

const tx = (language: Language | undefined, zh: string, en: string) =>
  language === "en" ? en : zh;

/** The name of a step's kind, which stands at the left of its row. */
export function stepKindName(
  kind: NewStepKind,
  language: Language | undefined,
): string {
  return kind === "keys"
    ? tx(language, "快捷键", "Shortcut")
    : tx(language, "等待", "Wait");
}

/** What a step moves to: up or down by one place. */
export interface StepMoves {
  up: boolean;
  down: boolean;
  onMove: (delta: -1 | 1) => void;
}

/**
 * The buttons at the end of a step's row: move it up, move it down, remove
 * it. label is the step's name ("第 2 步"), which they are named after. The
 * parent decides what a press does to the step, so a shortcut field can end
 * its capture first.
 */
export function StepButtons({
  label,
  language,
  moves,
  onRemove,
}: {
  label: string;
  language: Language | undefined;
  moves?: StepMoves;
  onRemove: () => void;
}) {
  const lower = label.toLowerCase();
  return (
    <>
      {moves && (
        <>
          <button
            type="button"
            className="icon-btn bare"
            aria-label={tx(language, `上移${label}`, `Move ${lower} up`)}
            title={tx(language, `上移${label}`, `Move ${lower} up`)}
            data-action="up"
            disabled={!moves.up}
            onClick={() => moves.onMove(-1)}
          >
            <Icon name="chevronUp" size={14} stroke={2} />
          </button>
          <button
            type="button"
            className="icon-btn bare"
            aria-label={tx(language, `下移${label}`, `Move ${lower} down`)}
            title={tx(language, `下移${label}`, `Move ${lower} down`)}
            data-action="down"
            disabled={!moves.down}
            onClick={() => moves.onMove(1)}
          >
            <Icon name="chevronDown" size={14} stroke={2} />
          </button>
        </>
      )}
      <button
        type="button"
        className="icon-btn bare"
        aria-label={tx(language, `删除${label}`, `Remove ${lower}`)}
        data-action="remove"
        onClick={onRemove}
      >
        <Icon name="close" size={14} stroke={2} />
      </button>
    </>
  );
}

/**
 * The row of a wait step: the milliseconds it waits, counted from the
 * release of the step before it, committed on Enter or when the field is
 * left (UI-43). 1 to 10000; a wait always has a number, so the field is never
 * empty.
 */
export function WaitStep({
  index,
  value,
  onChange,
  moves,
  onRemove,
  language,
}: {
  index: number;
  value: number;
  onChange: (wait: number) => void;
  moves: StepMoves;
  onRemove: () => void;
  language: Language | undefined;
}) {
  const label = tx(language, `第 ${index + 1} 步`, `Step ${index + 1}`);
  const field = useCommitField<number | undefined>({
    value: String(value),
    parse: numberParser(language, { ...waitRange, optional: false }),
    onCommit: (next) => {
      if (next !== undefined && next !== value) onChange(next);
    },
  });
  return (
    // biome-ignore lint/a11y/useSemanticElements: a row of controls around one value, not a form section
    <div
      className="drawer-step"
      role="group"
      aria-label={label}
      data-step={index}
      data-kind="wait"
    >
      <div className="drawer-step-row">
        <span className="drawer-step-label" aria-hidden="true">
          {stepKindName("wait", language)}
        </span>
        <span className="drawer-step-body drawer-step-wait">
          <input
            type="text"
            inputMode="numeric"
            className="input input-number"
            autoComplete="off"
            spellCheck={false}
            aria-label={tx(language, "等待时间（ms）", "Wait (ms)")}
            {...field.props}
          />
          <span className="drawer-number-unit" aria-hidden="true">
            ms
          </span>
        </span>
        <StepButtons
          label={label}
          language={language}
          moves={moves}
          onRemove={onRemove}
        />
      </div>
      <FieldError id={field.errorId} text={field.error} />
    </div>
  );
}

/**
 * A sequence read left to right, above its steps: a shortcut as keycaps, a
 * wait as its milliseconds, a text between quotes, cut short. Steps not filled
 * in yet are left out.
 */
export function StepsPreview({
  steps,
  language,
}: {
  steps: Step[];
  language: Language | undefined;
}) {
  const id = useId();
  const items: ReactNode[] = [];
  for (const [index, step] of steps.entries()) {
    const kind = stepKind(step);
    if (kind === "keys") {
      if (!(step.keys ?? "").trim()) continue;
      items.push(
        <Keycaps
          key={`${id}-${index}`}
          keys={step.keys ?? ""}
          language={language}
        />,
      );
    } else if (kind === "wait") {
      items.push(
        <span key={`${id}-${index}`} className="drawer-preview-note">
          {step.wait} ms
        </span>,
      );
    } else if (step.text) {
      items.push(
        <span
          key={`${id}-${index}`}
          className="drawer-preview-note"
          title={step.text}
        >
          {`“${shortText(step.text, 12)}”`}
        </span>,
      );
    }
  }
  return (
    <span className="drawer-preview-steps">
      {items.map((item, index) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: The items are positional parts of one line.
        <Fragment key={index}>
          {index > 0 && <span className="keycaps-sep">→</span>}
          {item}
        </Fragment>
      ))}
    </span>
  );
}
