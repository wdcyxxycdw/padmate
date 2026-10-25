import { type ReactNode, useEffect, useRef, useState } from "react";
import { useStore } from "zustand";
import type { ProfileDiagnostic } from "../shared/api";
import type { EditorStore } from "./editor-store";
import type { Language } from "./store";
import { Icon } from "./ui/Icon";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

const nextFrame = (callback: () => void) => {
  if (typeof requestAnimationFrame === "function")
    requestAnimationFrame(() => callback());
  else setTimeout(callback, 0);
};
const focusLost = () => {
  const active = document.activeElement;
  return !active || active === document.body || !active.isConnected;
};

type NoticeState = Pick<
  ReturnType<EditorStore["getState"]>,
  "error" | "saveProblem" | "reloaded" | "discarded" | "problem" | "diagnostics"
>;

/** Whether the mapping page has something to say about the profile file. */
export function hasEditorNotice(state: NoticeState): boolean {
  return (
    !!state.error ||
    !!state.saveProblem ||
    state.reloaded ||
    state.discarded ||
    !!state.problem ||
    state.diagnostics.length > 0
  );
}

/**
 * The mapping page saves each edit at once (UI-43, ADR-0054), so there is
 * no save bar: this line only shows when a save failed or was refused, the
 * file changed elsewhere, an edit was lost to a switch, the profile is not
 * in effect, or it has problems.
 */
export function EditorNotice({
  editor,
  language,
  onLocate,
  describe,
}: {
  editor: EditorStore;
  language: Language;
  onLocate: (diagnostic: ProfileDiagnostic) => void;
  describe: (diagnostic: ProfileDiagnostic) => string;
}) {
  const state = useStore(editor);
  const [problems, setProblems] = useState(false);
  const host = useRef<HTMLDivElement>(null);
  const toggle = useRef<HTMLButtonElement>(null);
  const diagnostics = state.diagnostics;
  // The drawer shows the key's own problems, so the list only opens from
  // its toggle. Parts still empty are left out of a save (savedForm), so
  // a refusal is a real one.
  const refused = state.saveProblem === "invalid";
  useEffect(() => {
    if (!diagnostics.length) setProblems(false);
  }, [diagnostics.length]);
  if (!hasEditorNotice(state)) return null;
  // The button that was clicked goes away: focus the selected chip (or the
  // first one) once the workspace has re-rendered.
  const focusWorkspace = () => {
    const scope: ParentNode =
      host.current?.closest(".controller-workspace") ?? document;
    nextFrame(() => {
      if (!focusLost()) return;
      (
        scope.querySelector<HTMLElement>(
          'button[data-callout][aria-pressed="true"]',
        ) ?? scope.querySelector<HTMLElement>("button[data-callout]")
      )?.focus();
    });
  };
  const button = (label: string, run: () => void) => (
    <button
      type="button"
      className="btn"
      onClick={() => {
        run();
        focusWorkspace();
      }}
    >
      {label}
    </button>
  );
  const retry = tx(language, "重试", "Retry");
  let kind: "error" | "warning" = "warning";
  let text = "";
  let action: ReactNode = null;
  if (state.error) {
    kind = "error";
    text = `${tx(language, "配置载入失败：", "Could not load the profile: ")}${state.error}`;
    action = button(retry, () => void state.reload());
  } else if (state.saveProblem === "failed") {
    kind = "error";
    text = `${tx(language, "保存失败：", "Could not save: ")}${state.saveError}`;
    action = button(retry, state.retry);
  } else if (refused) {
    kind = "error";
    text = tx(language, "修改未保存", "Changes not saved");
  } else if (state.reloaded) {
    text = tx(
      language,
      "配置文件已在别处修改，已重新载入。",
      "The profile file changed elsewhere and was reloaded.",
    );
    action = (
      <button
        type="button"
        className="icon-btn bare"
        aria-label={tx(language, "关闭", "Dismiss")}
        onClick={() => {
          state.dismissReloaded();
          focusWorkspace();
        }}
      >
        <Icon name="close" size={14} stroke={2} />
      </button>
    );
  } else if (state.discarded) {
    text = tx(
      language,
      "切换配置前的修改未能保存。",
      "Edits made before the profile changed were not saved.",
    );
    action = (
      <button
        type="button"
        className="icon-btn bare"
        aria-label={tx(language, "关闭", "Dismiss")}
        onClick={() => {
          state.dismissDiscarded();
          focusWorkspace();
        }}
      >
        <Icon name="close" size={14} stroke={2} />
      </button>
    );
  } else if (state.problem) {
    text = `${tx(language, "未生效：", "Not in effect: ")}${state.problem}`;
  } else if (!diagnostics.some((item) => item.severity === "error")) {
    // Only warnings: the profile is saved and in effect.
    const count = diagnostics.length;
    text = tx(
      language,
      `配置有 ${count} 个警告`,
      `The profile has ${count} warning${count === 1 ? "" : "s"}`,
    );
  }
  return (
    <div className="editor-notice-host" ref={host}>
      {problems && diagnostics.length > 0 && (
        // Non-modal list next to the line; Escape returns to its toggle.
        <section
          className="editor-problems"
          aria-label={tx(language, "校验结果", "Validation results")}
          onKeyDown={(event) => {
            if (event.key !== "Escape") return;
            event.preventDefault();
            setProblems(false);
            toggle.current?.focus();
          }}
        >
          {diagnostics.map((diagnostic) => {
            const isError = diagnostic.severity === "error";
            return (
              <button
                type="button"
                key={`${diagnostic.pointer}:${diagnostic.message}:${diagnostic.offset}`}
                data-testid="profile-diagnostic"
                data-severity={isError ? "error" : "warning"}
                onClick={() => {
                  setProblems(false);
                  onLocate(diagnostic);
                  // Locating may open nothing (a part the editor does not
                  // show); then the list's toggle keeps focus.
                  nextFrame(() => {
                    if (focusLost()) toggle.current?.focus();
                  });
                }}
              >
                <Icon
                  name={isError ? "error" : "warn"}
                  size={14}
                  stroke={2}
                  className={isError ? "error" : "warning"}
                />
                <span className="editor-problem-text">
                  <strong>
                    {isError
                      ? tx(language, "错误：", "Error: ")
                      : tx(language, "警告：", "Warning: ")}
                    {describe(diagnostic)}
                  </strong>
                  <span>{diagnostic.message}</span>
                </span>
              </button>
            );
          })}
        </section>
      )}
      <div
        className="editor-notice"
        data-state={kind}
        data-testid="editor-notice"
      >
        <Icon
          name={kind === "error" ? "error" : "warn"}
          size={15}
          stroke={2}
          className={kind}
        />
        <span
          className="editor-notice-text"
          role={kind === "error" ? "alert" : "status"}
        >
          {text}
        </span>
        {diagnostics.length > 0 && (
          <button
            type="button"
            ref={toggle}
            className="btn editor-problems-toggle"
            aria-expanded={problems}
            onClick={() => setProblems(!problems)}
          >
            {tx(
              language,
              `${diagnostics.length} 个问题`,
              `${diagnostics.length} problem${diagnostics.length === 1 ? "" : "s"}`,
            )}
          </button>
        )}
        {action}
      </div>
    </div>
  );
}
