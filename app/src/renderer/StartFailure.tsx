import { useEffect, useRef, useState } from "react";
import type { StartFailure as Failure, PadmateAPI } from "../shared/api";
import { startFailureText } from "../shared/start-failure-text";
import type { Language } from "./store";
import { Icon } from "./ui/Icon";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/**
 * The whole window when the mapping service did not start (UI-45): which
 * file, where in it and what is wrong, with 打开文件夹, 重试 and 退出.
 * retrying: 重试 is running; the last failure shows until it is decided.
 */
export function StartFailure({
  api,
  failure,
  retrying,
  language,
}: {
  api: Pick<PadmateAPI, "openStartFolder" | "retryStart" | "quit">;
  failure: Failure;
  retrying: boolean;
  language: Language;
}) {
  const text = startFailureText(failure, language);
  const [problem, setProblem] = useState("");
  const retry = useRef<HTMLButtonElement>(null);
  // 重试 has focus when the page opens, and again when a retry that took
  // it (by disabling it) has failed.
  useEffect(() => {
    const active = document.activeElement;
    if (!retrying && (!active || active === document.body))
      retry.current?.focus();
  }, [retrying]);
  // A new failure replaces the old one's problem with the folder.
  // biome-ignore lint/correctness/useExhaustiveDependencies: Reset when the failure changes.
  useEffect(() => setProblem(""), [failure]);
  return (
    <main className="start-failure" data-testid="start-failure">
      <section className="start-failure-panel" aria-labelledby="start-title">
        <Icon name="error" size={28} stroke={1.8} className="error" />
        <h1 id="start-title">{text.title}</h1>
        <p className="start-failure-summary" data-testid="start-summary">
          {text.summary}
        </p>
        {text.path && (
          <p className="start-failure-path" data-testid="start-path">
            {text.path}
          </p>
        )}
        {text.more.length > 0 && (
          <ul
            className="start-failure-more"
            aria-label={tx(language, "其他错误", "More errors")}
          >
            {text.more.map((line, index) => (
              // Lines can repeat; their order is their identity.
              // biome-ignore lint/suspicious/noArrayIndexKey: Fixed list.
              <li key={index}>{line}</li>
            ))}
          </ul>
        )}
        {problem && (
          <p className="start-failure-problem" role="alert">
            {problem}
          </p>
        )}
        <div className="start-failure-actions">
          <button
            type="button"
            className="btn"
            onClick={() => {
              setProblem("");
              void api
                .openStartFolder()
                .catch(() =>
                  setProblem(
                    tx(
                      language,
                      "无法打开文件夹。",
                      "Could not open the folder.",
                    ),
                  ),
                );
            }}
          >
            <Icon name="folder" size={15} stroke={2} />
            {tx(language, "打开文件夹", "Open folder")}
          </button>
          <button
            ref={retry}
            type="button"
            className="btn-primary"
            disabled={retrying}
            onClick={() => {
              setProblem("");
              void api.retryStart().catch(() => {});
            }}
          >
            <Icon name="refresh" size={15} stroke={2} />
            {retrying
              ? tx(language, "正在重试…", "Retrying…")
              : tx(language, "重试", "Retry")}
          </button>
          <button
            type="button"
            className="btn"
            onClick={() => void api.quit().catch(() => {})}
          >
            {tx(language, "退出", "Quit")}
          </button>
        </div>
      </section>
    </main>
  );
}
