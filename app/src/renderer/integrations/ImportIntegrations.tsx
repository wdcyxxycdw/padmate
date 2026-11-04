// What happens after a profile with an integration's items is imported
// (ARCHITECTURE §16.5, ADR-0019 §7). The items are compared with the app on
// this computer. With differences the user chooses: change the app to the
// imported settings, keep the app and move the profile's keys, or later. When
// changing the app would take keys from other local functions, they are listed
// first and then each such item is decided in a blocking dialog: use the
// imported setting, keep the local one, or give one side another key. Only when
// every item is decided are the changes assembled, checked (the app must be
// closed) and written, with an undo to follow.
import { useEffect, useState } from "react";
import type { PadmateAPI } from "../../shared/api";
import type {
  IntegrationBackup,
  IntegrationCheck,
  IntegrationInfo,
  IntegrationItems,
  IntegrationPlan,
  IntegrationPlanEntry,
} from "../../shared/integrations";
import {
  assembleChanges,
  type ConflictDecision,
  checkText,
  conflictEntries,
  type DifferenceRow,
  differenceRows,
  keyMoves,
  movedItems,
  retargetKeys,
  writable,
} from "../state/integrations-model";
import type { Language, PadmateStore } from "../state/store";
import { Dialog } from "../ui/controls";
import { Keycaps } from "../ui/Keycaps";
import type { CaptureField } from "./FunctionPicker";
import { integrationFailure, useIntegrationHost } from "./IntegrationContext";
import "./integrations.css";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/**
 * Moves the keys of a profile that is not the open one, through its file: the
 * profile is read, its shortcuts changed and written back with its revision.
 * Returns how many shortcuts changed.
 */
export async function retargetProfileFile(
  api: Pick<PadmateAPI, "getProfile" | "putProfile">,
  profile: string,
  moves: ReadonlyArray<{ from: string; to: string }>,
): Promise<number> {
  const doc = await api.getProfile(profile);
  const moved = retargetKeys(JSON.parse(doc.text) as unknown, moves);
  if (moved.changed > 0)
    await api.putProfile(
      profile,
      `${JSON.stringify(moved.value, null, 2)}\n`,
      doc.revision,
    );
  return moved.changed;
}

/** Writes the moved keys into the items file, from the items as they are now. */
async function moveItems(
  api: Pick<PadmateAPI, "getIntegrationItems" | "putIntegrationItems">,
  profile: string,
  integration: string,
  moves: ReadonlyArray<{ from: string; to: string }>,
): Promise<void> {
  if (moves.length === 0) return;
  const fresh = await api.getIntegrationItems(profile, integration);
  await api.putIntegrationItems({
    profile,
    integration,
    revision: fresh.revision,
    items: movedItems(fresh.items, moves),
  });
}

type Stage =
  | { kind: "offer" }
  | { kind: "busy" }
  | { kind: "displaced"; plan: IntegrationPlan }
  | { kind: "decide"; plan: IntegrationPlan }
  | { kind: "done"; text: string; undo?: IntegrationBackup };

/** What the user has chosen for one entry so far. */
interface Draft {
  choice?: "import" | "keep" | "rekeyImported" | "rekeyLocal";
  keys: string;
  check?: IntegrationCheck;
  checking?: boolean;
}

function decided(draft: Draft | undefined): boolean {
  if (!draft?.choice) return false;
  if (draft.choice === "rekeyImported" || draft.choice === "rekeyLocal")
    return !!draft.keys && !!draft.check && writable(draft.check);
  return true;
}

function toDecision(
  entry: IntegrationPlanEntry,
  draft: Draft,
): ConflictDecision {
  switch (draft.choice) {
    case "keep":
      return { kind: "keep" };
    case "rekeyImported":
      return { kind: "rekeyImported", keys: draft.keys };
    case "rekeyLocal":
      return {
        kind: "rekeyLocal",
        keys: draft.keys,
        function: entry.displaced[0]?.function ?? "",
      };
    default:
      return { kind: "import" };
  }
}

/**
 * The dialogs for one imported profile. Renders nothing until an integration
 * on this computer has differences with the profile's items.
 */
export function ImportIntegrations({
  store,
  profile,
  language,
  Capture,
  onDone,
}: {
  store: PadmateStore;
  /** The library ID of the profile that was imported. */
  profile: string;
  language: Language;
  Capture: CaptureField;
  onDone: () => void;
}) {
  const host = useIntegrationHost();
  const t = (zh: string, en: string) => tx(language, zh, en);
  const [offer, setOffer] = useState<{
    info: IntegrationInfo;
    items: IntegrationItems;
  } | null>(null);
  const [stage, setStage] = useState<Stage>({ kind: "offer" });
  const [problem, setProblem] = useState("");
  const [drafts, setDrafts] = useState<Draft[]>([]);

  // Compare with each integration the computer has: the first with a
  // difference is offered.
  // biome-ignore lint/correctness/useExhaustiveDependencies: runs for the profile
  useEffect(() => {
    let live = true;
    void (async () => {
      const state = store.getState();
      await state.refreshIntegrations();
      for (const info of store.getState().integrations ?? []) {
        if (!info.found) continue;
        try {
          const items = await store
            .getState()
            .api.getIntegrationItems(profile, info.id);
          if (!live) return;
          if (items.items.length > 0 && !items.consistent) {
            setOffer({ info, items });
            return;
          }
        } catch {
          // Not readable now: nothing to offer.
        }
      }
      if (live) onDone();
    })();
    return () => {
      live = false;
    };
  }, [store, profile]);

  if (!offer || !host) return null;
  const { info, items } = offer;
  const api = store.getState().api;
  const rows: DifferenceRow[] = differenceRows(
    items.items,
    items.statuses,
    info.name,
    language,
  );
  const close = () => {
    setOffer(null);
    onDone();
  };

  const guard = async (work: () => Promise<void>) => {
    setProblem("");
    setStage((was) => (was.kind === "offer" ? { kind: "busy" } : was));
    try {
      await work();
    } catch (error) {
      setProblem(integrationFailure(error, info.name, language));
      setStage({ kind: "offer" });
    }
  };

  const followApp = () =>
    guard(async () => {
      const moves = keyMoves(rows);
      await retargetProfileFile(api, profile, moves);
      await moveItems(api, profile, info.id, moves);
      setStage({
        kind: "done",
        text: t(
          `按键已按你的 ${info.name} 调整。`,
          `The keys now follow your ${info.name}.`,
        ),
      });
    });

  const plan = () =>
    guard(async () => {
      const made = await api.planIntegration(profile, info.id);
      if (conflictEntries(made.entries).length > 0) {
        setDrafts(made.entries.map(() => ({ keys: "" })));
        setStage({ kind: "displaced", plan: made });
        return;
      }
      await writeAll(made, new Map());
    });

  /** The final write, once every item is decided. */
  const writeAll = async (
    made: IntegrationPlan,
    decisions: Map<IntegrationPlanEntry, ConflictDecision>,
  ) => {
    const { changes, moves } = assembleChanges(made.entries, decisions);
    setStage({ kind: "busy" });
    const outcome = await host.write(info, changes);
    if (!outcome.ok) {
      if (!outcome.cancelled) setProblem(outcome.message);
      setStage({ kind: "offer" });
      return;
    }
    await retargetProfileFile(api, profile, moves);
    await moveItems(api, profile, info.id, moves);
    setStage({
      kind: "done",
      text: t(
        `已把导入的设置写进 ${info.name}。`,
        `Wrote the imported settings into ${info.name}.`,
      ),
      undo: outcome.written.backup ?? undefined,
    });
  };

  const recordFor = (
    index: number,
    entry: IntegrationPlanEntry,
    keys: string,
    local: boolean,
  ) => {
    const fn = local ? entry.displaced[0]?.function : entry.item.function;
    setDrafts((list) =>
      list.map((draft, at) =>
        at === index
          ? { ...draft, keys, check: undefined, checking: !!keys }
          : draft,
      ),
    );
    if (!keys) return;
    api
      .checkIntegrationKey({
        integration: info.id,
        ...(fn ? { function: fn } : {}),
        keys,
      })
      .then((check) =>
        setDrafts((list) =>
          list.map((draft, at) =>
            at === index && draft.keys === keys
              ? { ...draft, check, checking: false }
              : draft,
          ),
        ),
      )
      .catch((error) => {
        setProblem(integrationFailure(error, info.name, language));
        setDrafts((list) =>
          list.map((draft, at) =>
            at === index ? { ...draft, checking: false } : draft,
          ),
        );
      });
  };

  const undo = async (backup: IntegrationBackup) => {
    setProblem("");
    try {
      if (!(await host.requireClosed(info))) return;
      await api.restoreIntegrationBackup(info.id, backup.id);
      close();
    } catch (error) {
      setProblem(integrationFailure(error, info.name, language));
    }
  };

  let body: React.ReactNode;
  let footer: React.ReactNode;
  let blocking = false;
  const working = stage.kind === "busy";
  if (stage.kind === "done") {
    const written = stage.undo;
    body = (
      <p className="dialog-text" role="status" data-testid="import-done">
        {stage.text}
      </p>
    );
    footer = (
      <>
        {written && (
          <button
            type="button"
            className="btn-ghost"
            onClick={() => void undo(written)}
          >
            {t("撤销这次写入", "Undo this write")}
          </button>
        )}
        <span className="spacer" />
        <button type="button" className="btn-primary" onClick={close}>
          {t("完成", "Done")}
        </button>
      </>
    );
  } else if (stage.kind === "displaced") {
    const entries = conflictEntries(stage.plan.entries);
    body = (
      <div data-testid="import-displaced">
        <p className="dialog-text">
          {t(
            `写入会让 ${info.name} 里这些功能失去现在的键：`,
            `Writing takes the keys of these functions in ${info.name}:`,
          )}
        </p>
        <ul className="integration-differences">
          {entries.flatMap((entry) =>
            entry.displaced.map((lost) => (
              <li
                key={`${entry.item.function}|${lost.function}`}
                className="integration-difference"
              >
                <span className="integration-difference-name">
                  {lost.name} <Keycaps keys={lost.keys} language={language} />
                </span>
                <span className="integration-difference-text">
                  {t(
                    `导入的“${entry.item.name}”要用这个键`,
                    `The imported "${entry.item.name}" uses this key`,
                  )}
                </span>
              </li>
            )),
          )}
        </ul>
      </div>
    );
    footer = (
      <>
        <button type="button" className="btn-ghost" onClick={close}>
          {t("取消", "Cancel")}
        </button>
        <span className="spacer" />
        <button
          type="button"
          className="btn-primary"
          onClick={() => setStage({ kind: "decide", plan: stage.plan })}
        >
          {t("逐项决定", "Decide one by one")}
        </button>
      </>
    );
  } else if (stage.kind === "decide") {
    blocking = true;
    const made = stage.plan;
    const open = made.entries
      .map((entry, index) => ({ entry, index }))
      .filter(({ entry }) => conflictEntries([entry]).length > 0);
    const ready = open.every(({ index }) => decided(drafts[index]));
    body = (
      <div data-testid="import-decide">
        <p className="dialog-text">
          {t(
            "每一项都要决定，决定完才会写入。",
            "Decide every item. Nothing is written until all are decided.",
          )}
        </p>
        {open.map(({ entry, index }) => {
          const draft = drafts[index] ?? { keys: "" };
          const local = entry.displaced[0];
          const setChoice = (choice: NonNullable<Draft["choice"]>) =>
            setDrafts((list) =>
              list.map((item, at) =>
                at === index
                  ? choice === "import" || choice === "keep"
                    ? { keys: "", choice }
                    : { ...item, choice }
                  : item,
              ),
            );
          const rekey =
            draft.choice === "rekeyImported" || draft.choice === "rekeyLocal";
          const text = draft.check
            ? checkText(draft.check, info.name, language)
            : null;
          return (
            <fieldset
              key={`${entry.item.function}|${entry.item.keys}`}
              className="integration-difference"
              data-testid="import-entry"
            >
              <legend className="integration-difference-name">
                {t(
                  `导入的“${entry.item.name}”和本机的“${local?.name ?? ""}”都要用 ${entry.item.keys}`,
                  `The imported "${entry.item.name}" and your "${local?.name ?? ""}" both want ${entry.item.keys}`,
                )}
              </legend>
              {(
                [
                  ["import", t("用导入的设置", "Use the imported setting")],
                  ["keep", t("保留本机的设置", "Keep my setting")],
                  [
                    "rekeyImported",
                    t(
                      "给导入的功能换一个键",
                      "Give the imported function another key",
                    ),
                  ],
                  [
                    "rekeyLocal",
                    t("给本机的功能换一个键", "Give my function another key"),
                  ],
                ] as const
              ).map(([value, label]) => (
                <label key={value} className="integration-choice">
                  <input
                    type="radio"
                    name={`import-entry-${index}`}
                    checked={draft.choice === value}
                    onChange={() => setChoice(value)}
                  />
                  <span>{label}</span>
                </label>
              ))}
              {rekey && (
                <>
                  <Capture
                    value={draft.keys}
                    onChange={(keys) =>
                      recordFor(
                        index,
                        entry,
                        keys,
                        draft.choice === "rekeyLocal",
                      )
                    }
                    label={t("新的键", "New key")}
                    language={language}
                  />
                  {draft.checking && (
                    <p className="picker-message" role="status">
                      {t("正在检查…", "Checking…")}
                    </p>
                  )}
                  {text && (
                    <p
                      className="dialog-text"
                      role="status"
                      data-level={text.level}
                    >
                      {text.text} {text.notes.join(" ")}
                    </p>
                  )}
                </>
              )}
            </fieldset>
          );
        })}
      </div>
    );
    footer = (
      <>
        <button type="button" className="btn-ghost" onClick={close}>
          {t("取消", "Cancel")}
        </button>
        <span className="spacer" />
        <button
          type="button"
          className="btn-primary"
          disabled={!ready}
          onClick={() => {
            const decisions = new Map<IntegrationPlanEntry, ConflictDecision>();
            for (const { entry, index } of open)
              decisions.set(entry, toDecision(entry, drafts[index]));
            void guard(() => writeAll(made, decisions));
          }}
        >
          {t(`写入 ${info.name}`, `Write to ${info.name}`)}
        </button>
      </>
    );
  } else {
    body = (
      <>
        <p className="dialog-text">
          {t(
            `导入的配置带着 ${info.name} 的快捷键设置，和你的 ${info.name} 有 ${rows.length} 项不一致：`,
            `The imported profile carries shortcut settings for ${info.name}. ${rows.length} item(s) differ from your ${info.name}:`,
          )}
        </p>
        <ul className="integration-differences" data-testid="import-list">
          {rows.map((row) => (
            <li
              key={`${row.function}|${row.keys}`}
              className="integration-difference"
            >
              <span className="integration-difference-name">
                {row.name} <Keycaps keys={row.keys} language={language} />
              </span>
              <span className="integration-difference-text">{row.text}</span>
            </li>
          ))}
        </ul>
      </>
    );
    footer = (
      <div className="integration-actions">
        <button
          type="button"
          className="btn"
          disabled={working}
          onClick={() => void plan()}
        >
          {t(
            `用导入的设置改我的 ${info.name}`,
            `Change my ${info.name} to the imported settings`,
          )}
        </button>
        <button
          type="button"
          className="btn"
          disabled={working}
          onClick={() => void followApp()}
        >
          {t(
            `不改，按我的 ${info.name} 调整按键`,
            `Keep my ${info.name} and adjust the keys`,
          )}
        </button>
        <button
          type="button"
          className="btn-ghost"
          disabled={working}
          onClick={close}
        >
          {t("以后再说", "Later")}
        </button>
      </div>
    );
  }
  return (
    <Dialog
      open
      title={t(`导入的 ${info.name} 设置`, `Imported ${info.name} settings`)}
      closeLabel={t("关闭", "Close")}
      closeDisabled={blocking}
      onClose={close}
      className="confirm-dialog"
      footer={footer}
    >
      {body}
      {problem && (
        <p className="picker-problem" role="alert">
          {problem}
        </p>
      )}
    </Dialog>
  );
}
