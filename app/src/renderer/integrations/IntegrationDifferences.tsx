// The dialog for the differences between the active profile's items and the
// app (ARCHITECTURE §16.5, ADR-0019 §6). The daemon only tells the user by
// overlay and tray and never raises this window; the dialog comes up when the
// user has the window open on a profile that is bound to the integration and
// has differences. It blocks until the user chooses: the profile wins (the
// plan, the functions it displaces, the running check, the write), the app wins
// (the buttons keep their function and take its new keys, one undo step), or
// not now.
import { useCallback, useEffect, useRef, useState } from "react";
import { useStore } from "zustand";
import type {
  IntegrationBackup,
  IntegrationInfo,
  IntegrationItems,
  IntegrationPlan,
} from "../../shared/integrations";
import type { EditorStore } from "../editor/editor-store";
import {
  type DifferenceRow,
  differenceRows,
  keyMoves,
  movedItems,
  plainChanges,
  retargetKeys,
} from "../state/integrations-model";
import type { Language, PadmateStore } from "../state/store";
import { Dialog } from "../ui/controls";
import { Keycaps } from "../ui/Keycaps";
import {
  integrationFailure,
  useBoundIntegrations,
  useIntegrationHost,
} from "./IntegrationContext";
import "./integrations.css";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

type Stage =
  | { kind: "list" }
  | { kind: "planning" }
  | { kind: "confirm"; plan: IntegrationPlan }
  | { kind: "done"; text: string; undo?: IntegrationBackup };

interface Open {
  info: IntegrationInfo;
  items: IntegrationItems;
}

/**
 * Reads the items of the profile the user is looking at, and gives the first
 * set of differences they have not put off. It reads again when the app's
 * state changes (integration.changed), when the binding changes (the list) and
 * when the profile does.
 */
function useDifferences(
  store: PadmateStore,
  profileId: string,
  looking: boolean,
): { current: Open | null; settle: () => void } {
  const bound = useBoundIntegrations(profileId);
  const event = useStore(store, (state) => state.integrationEvent);
  const putOff = useStore(store, (state) => state.putOffDifferences);
  const [current, setCurrent] = useState<Open | null>(null);
  const [turn, setTurn] = useState(0);
  const boundKey = bound.map((info) => info.id).join(",");
  // biome-ignore lint/correctness/useExhaustiveDependencies: boundKey stands for bound
  useEffect(() => {
    if (!looking || !profileId || bound.length === 0) {
      setCurrent(null);
      return;
    }
    let live = true;
    void (async () => {
      for (const info of bound) {
        try {
          const items = await store
            .getState()
            .loadIntegrationItems(profileId, info.id);
          if (!live) return;
          const id = items.differenceId ?? "";
          const off = store
            .getState()
            .putOffDifferences.includes(`${profileId}|${info.id}|${id}`);
          // The daemon says the set was put off in an earlier run (state.json);
          // putOffDifferences is this run's memory of it.
          if (!items.consistent && !items.dismissed && !off) {
            setCurrent({ info, items });
            return;
          }
        } catch {
          // The app's settings cannot be read now: there is nothing to show.
        }
      }
      if (live) setCurrent(null);
    })();
    return () => {
      live = false;
    };
  }, [store, looking, profileId, boundKey, event?.seq, putOff, turn]);
  return { current, settle: () => setTurn((value) => value + 1) };
}

export function IntegrationDifferences({
  store,
  editor,
  language,
  profileId,
  looking,
}: {
  store: PadmateStore;
  editor: EditorStore;
  language: Language;
  /** The profile the window edits: the active one. */
  profileId: string;
  /** The window shows the mapping page, so the user is looking at the profile. */
  looking: boolean;
}) {
  const host = useIntegrationHost();
  const { current, settle } = useDifferences(store, profileId, looking);
  const [stage, setStage] = useState<Stage>({ kind: "list" });
  const [problem, setProblem] = useState("");
  const [busy, setBusy] = useState(false);
  const shown = useRef<Open | null>(null);
  if (current) shown.current = current;
  // What the dialog was opened on stays while the stage is not the list: the
  // reads that follow a write must not take the dialog away mid-way.
  const open = stage.kind === "list" ? current : shown.current;
  const t = (zh: string, en: string) => tx(language, zh, en);

  const info = open?.info;
  const rows: DifferenceRow[] = open
    ? differenceRows(
        open.items.items,
        open.items.statuses,
        open.info.name,
        language,
      )
    : [];

  const close = useCallback(() => {
    setStage({ kind: "list" });
    setProblem("");
    shown.current = null;
    settle();
  }, [settle]);

  if (!open || !info || !host || !profileId) {
    // The dialog is mounted either way, so its focus handling stays simple.
    return null;
  }

  const planFirst = async () => {
    setBusy(true);
    setProblem("");
    setStage({ kind: "planning" });
    try {
      const plan = await store
        .getState()
        .api.planIntegration(profileId, info.id);
      setStage({ kind: "confirm", plan });
    } catch (error) {
      setProblem(integrationFailure(error, info.name, language));
      setStage({ kind: "list" });
    } finally {
      setBusy(false);
    }
  };

  const writePlan = async (plan: IntegrationPlan) => {
    setBusy(true);
    setProblem("");
    try {
      const outcome = await host.write(info, plainChanges(plan.entries));
      if (!outcome.ok) {
        if (!outcome.cancelled) setProblem(outcome.message);
        return;
      }
      await store.getState().loadIntegrationItems(profileId, info.id);
      setStage({
        kind: "done",
        text: t(
          `已把 padmate 里的设置写进 ${info.name}。`,
          `Wrote the settings of padmate into ${info.name}.`,
        ),
        undo: outcome.written.backup ?? undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  const appWins = async () => {
    const moves = keyMoves(rows);
    const editorState = editor.getState();
    setBusy(true);
    setProblem("");
    try {
      if (moves.length > 0) {
        const draft = editorState.draft;
        if (editorState.id !== profileId || !draft || editorState.readOnly) {
          setProblem(
            t(
              "现在不能改这个配置的按键，请稍后再试。",
              "The keys of this profile cannot be changed now. Try again later.",
            ),
          );
          return;
        }
        // One edit, so one undo step; autosave writes it.
        const moved = retargetKeys(draft, moves);
        if (moved.changed > 0) {
          editorState.edit(moved.value);
          if (!(await editorState.flush())) {
            setProblem(t("按键没有保存。", "The keys were not saved."));
            return;
          }
        }
        const fresh = await store
          .getState()
          .loadIntegrationItems(profileId, info.id);
        await store.getState().api.putIntegrationItems({
          profile: profileId,
          integration: info.id,
          revision: fresh.revision,
          items: movedItems(fresh.items, moves),
        });
      }
      await store.getState().loadIntegrationItems(profileId, info.id);
      const kept = rows.filter(
        (row) => !moves.some((move) => move.from === row.keys),
      );
      setStage({
        kind: "done",
        text:
          kept.length === 0
            ? t(
                `按键已改成 ${info.name} 里现在的键。`,
                `The keys now follow ${info.name}.`,
              )
            : t(
                `按键已改成 ${info.name} 里现在的键。${kept
                  .map((row) => row.name)
                  .join("、")} 在 ${info.name} 里没有快捷键，保留了原来的键。`,
                `The keys now follow ${info.name}. ${kept
                  .map((row) => row.name)
                  .join(
                    ", ",
                  )} has no shortcut in ${info.name}; its key was kept.`,
              ),
      });
    } catch (error) {
      setProblem(integrationFailure(error, info.name, language));
    } finally {
      setBusy(false);
    }
  };

  const notNow = async () => {
    const id = open.items.differenceId ?? "";
    setBusy(true);
    setProblem("");
    try {
      if (id)
        await store.getState().api.dismissIntegrationDifferences({
          profile: profileId,
          integration: info.id,
          differenceId: id,
        });
      store.getState().putOffIntegrationDifferences(profileId, info.id, id);
      close();
    } catch (error) {
      setProblem(integrationFailure(error, info.name, language));
    } finally {
      setBusy(false);
    }
  };

  const undo = async (backup: IntegrationBackup) => {
    setBusy(true);
    setProblem("");
    try {
      if (!(await host.requireClosed(info))) return;
      await store.getState().api.restoreIntegrationBackup(info.id, backup.id);
      await store.getState().loadIntegrationItems(profileId, info.id);
      close();
    } catch (error) {
      setProblem(integrationFailure(error, info.name, language));
    } finally {
      setBusy(false);
    }
  };

  let body: React.ReactNode;
  let footer: React.ReactNode;
  if (stage.kind === "done") {
    const written = stage.undo;
    body = (
      <p className="dialog-text" role="status" data-testid="difference-done">
        {stage.text}
      </p>
    );
    footer = (
      <>
        {written && (
          <button
            type="button"
            className="btn-ghost"
            disabled={busy}
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
  } else if (stage.kind === "confirm") {
    const plan = stage.plan;
    const displaced = plan.entries.flatMap((entry) => entry.displaced);
    const skipped = plan.entries.filter((entry) => entry.skipped);
    const writes = plan.entries.filter((entry) => !entry.skipped);
    body = (
      <div data-testid="difference-confirm">
        <p className="dialog-text">
          {t(
            `会把 ${writes.length} 项写回 ${info.name}。`,
            `${writes.length} item(s) will be written back to ${info.name}.`,
          )}
        </p>
        {displaced.length > 0 && (
          <>
            <p className="dialog-text">
              {t(
                `写入后，下面这些功能会失去它们的键：`,
                "After writing, these functions lose their keys:",
              )}
            </p>
            <ul className="integration-differences">
              {displaced.map((entry) => (
                <li
                  key={`${entry.function}|${entry.keys}`}
                  className="integration-difference"
                >
                  <span className="integration-difference-name">
                    {entry.name}
                  </span>
                  <Keycaps keys={entry.keys} language={language} />
                </li>
              ))}
            </ul>
          </>
        )}
        {skipped.length > 0 && (
          <>
            <p className="dialog-text">
              {t("下面这些项不会写入：", "These items will not be written:")}
            </p>
            <ul className="integration-differences">
              {skipped.map((entry) => (
                <li
                  key={`${entry.item.function}|${entry.item.keys}`}
                  className="integration-difference"
                >
                  <span className="integration-difference-name">
                    {entry.item.name}
                  </span>
                  <span className="integration-difference-text">
                    {entry.skipped}
                  </span>
                </li>
              ))}
            </ul>
          </>
        )}
      </div>
    );
    footer = (
      <>
        <button
          type="button"
          className="btn-ghost"
          disabled={busy}
          onClick={() => setStage({ kind: "list" })}
        >
          {t("返回", "Back")}
        </button>
        <span className="spacer" />
        <button
          type="button"
          className="btn-primary"
          disabled={busy || writes.length === 0}
          onClick={() => void writePlan(plan)}
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
            `这份配置里的按键和 ${info.name} 现在的设置不一致：`,
            `The keys of this profile no longer match the settings of ${info.name}:`,
          )}
        </p>
        <ul className="integration-differences" data-testid="difference-list">
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
          disabled={busy}
          onClick={() => void planFirst()}
        >
          {t("以 padmate 为准", "Keep padmate's keys")}
        </button>
        <button
          type="button"
          className="btn"
          disabled={busy}
          onClick={() => void appWins()}
        >
          {t(`以 ${info.name} 为准`, `Follow ${info.name}`)}
        </button>
        <button
          type="button"
          className="btn-ghost"
          disabled={busy}
          onClick={() => void notNow()}
        >
          {t("暂不处理", "Not now")}
        </button>
      </div>
    );
  }
  return (
    <Dialog
      open
      title={t(`${info.name} 的设置变了`, `${info.name} settings changed`)}
      closeLabel={t("关闭", "Close")}
      closeDisabled
      onClose={() => {}}
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
