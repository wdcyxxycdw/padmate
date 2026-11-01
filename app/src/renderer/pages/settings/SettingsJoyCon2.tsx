import { useEffect, useRef, useState } from "react";
import type { JoyCon2Known, JoyCon2Side } from "../../../shared/joycon2";
import { errorInfo } from "../../state/library-model";
import type { Language } from "../../state/store";
import { Dialog, Notice, Switch } from "../../ui/controls";
import {
  type JoyCon2Api,
  PAIR_SECONDS,
  type Pairing,
  pairingAdvance,
  pairingBegin,
  pairingSecondsLeft,
  useJoyCon2,
} from "./joycon2-model";
import "../pages.css";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/** The daemon's code for a controller it does not remember (any more). */
const NOT_REMEMBERED = -32004;

function padName(side: JoyCon2Side, language: Language): string {
  const hand =
    side === "L" ? tx(language, "左", "left") : tx(language, "右", "right");
  return tx(language, `Joy-Con 2（${hand}）`, `Joy-Con 2 (${hand})`);
}

/**
 * Settings · Joy-Con 2: 自动连接记住的 Joy-Con 2, the Joy-Con 2 padmate
 * remembers with 忘记, and 连接新的 Joy-Con 2, which opens the window that
 * connects one (a dialog, because the user has to do something on the
 * controller within its time). Not shown when the daemon has no Joy-Con 2
 * support. The daemon's joycon2.changed events are asked for while this is
 * mounted (useJoyCon2). The connection and the battery of a Joy-Con 2 are not
 * here: it is a gamepad like any other, and shows in the rows of 手柄调校.
 */
export function JoyCon2Section({
  api,
  language,
  autoConnect,
  disabled,
  onAutoConnect,
}: {
  api: JoyCon2Api;
  language: Language;
  /** config.json's joycon2.autoConnect. */
  autoConnect: boolean;
  /** config.json cannot be written now. */
  disabled: boolean;
  onAutoConnect: (on: boolean) => void;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const { status, apply } = useJoyCon2(api);
  const [pairing, setPairing] = useState<Pairing | null>(null);
  const [now, setNow] = useState(() => Date.now());
  const [forgetting, setForgetting] = useState<JoyCon2Known | null>(null);
  const [forgetBusy, setForgetBusy] = useState(false);
  const [forgetFailed, setForgetFailed] = useState(false);
  const waiting = pairing?.phase === "waiting";
  const waitingRef = useRef(false);
  waitingRef.current = waiting;
  const connectButton = useRef<HTMLButtonElement>(null);

  // Every state the daemon sends moves the dialog on.
  useEffect(() => {
    if (!status) return;
    setPairing((current) =>
      current ? pairingAdvance(current, status, Date.now()) : current,
    );
  }, [status]);

  // The seconds left count down while the window is open; the clock is read
  // again whenever the daemon moves the end.
  const deadline = pairing?.phase === "waiting" ? pairing.deadline : 0;
  // biome-ignore lint/correctness/useExhaustiveDependencies: deadline restarts the count.
  useEffect(() => {
    if (!waiting) return;
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 250);
    return () => clearInterval(timer);
  }, [waiting, deadline]);

  // A page that goes away with the window open closes it.
  const cancelPair = api.cancelJoyCon2Pair;
  useEffect(
    () => () => {
      if (waitingRef.current) void cancelPair().catch(() => {});
    },
    [cancelPair],
  );

  if (!status?.available) return null;

  const connect = async () => {
    if (pairing) return;
    setPairing(pairingBegin(status, Date.now()));
    try {
      const next = await api.pairJoyCon2();
      // The window was asked for: a state that has it closed already is the
      // end of it.
      setPairing((current) =>
        current?.phase === "waiting"
          ? pairingAdvance({ ...current, opened: true }, next, Date.now())
          : current,
      );
      apply(next);
    } catch {
      setPairing((current) => (current ? { phase: "failed" } : current));
    }
  };
  const cancel = () => {
    setPairing(null);
    api
      .cancelJoyCon2Pair()
      .then(apply)
      .catch(() => {});
  };
  const forget = async () => {
    if (!forgetting || forgetBusy) return;
    setForgetBusy(true);
    setForgetFailed(false);
    try {
      apply(await api.forgetJoyCon2(forgetting.serial));
    } catch (error) {
      // A controller that is gone already is as good as forgotten.
      if (errorInfo(error).code !== NOT_REMEMBERED) {
        setForgetBusy(false);
        setForgetFailed(true);
        return;
      }
    }
    setForgetBusy(false);
    setForgetting(null);
  };

  const left =
    pairing?.phase === "waiting"
      ? pairingSecondsLeft(pairing.deadline, now)
      : 0;
  let result = "";
  if (pairing?.phase === "found")
    result = t(
      `已连接并记住 ${padName(pairing.side, "zh-CN")}。`,
      `Connected and remembered the ${padName(pairing.side, "en")}.`,
    );
  else if (pairing?.phase === "none")
    result = t(
      `${PAIR_SECONDS} 秒内没有找到 Joy-Con 2。`,
      `No Joy-Con 2 found within ${PAIR_SECONDS} seconds.`,
    );
  else if (pairing?.phase === "failed")
    result = t("未能开始连接 Joy-Con 2。", "Could not start connecting.");

  return (
    <section className="page-section" data-testid="joycon2-settings">
      <h2 className="section-title">Joy-Con 2</h2>
      <div className="card">
        <div className="row">
          <div className="row-text">
            <span className="row-title">
              {t(
                "自动连接记住的 Joy-Con 2",
                "Connect remembered Joy-Con 2 automatically",
              )}
            </span>
          </div>
          <Switch
            label={t(
              "自动连接记住的 Joy-Con 2",
              "Connect remembered Joy-Con 2 automatically",
            )}
            checked={autoConnect}
            disabled={disabled}
            onChange={onAutoConnect}
          />
        </div>
        {status.known.map((entry) => (
          <div className="row" key={entry.serial}>
            <div className="row-text">
              <span className="row-title">
                {padName(entry.side, language)}
                {entry.connected ? (
                  <span className="tag accent">{t("已连接", "Connected")}</span>
                ) : (
                  <span className="tag">{t("未连接", "Not connected")}</span>
                )}
              </span>
              <span className="row-hint">{entry.serial}</span>
            </div>
            <button
              type="button"
              className="btn"
              aria-label={t(
                `忘记 ${padName(entry.side, language)} ${entry.serial}`,
                `Forget ${padName(entry.side, language)} ${entry.serial}`,
              )}
              onClick={() => {
                setForgetFailed(false);
                setForgetting(entry);
              }}
            >
              {t("忘记", "Forget")}
            </button>
          </div>
        ))}
        <div className="row">
          <div className="row-text" />
          <button
            ref={connectButton}
            type="button"
            className="btn"
            disabled={!!pairing}
            onClick={() => void connect()}
          >
            {t("连接新的 Joy-Con 2", "Connect a new Joy-Con 2")}
          </button>
        </div>
      </div>
      <Dialog
        open={!!pairing}
        title={t("连接新的 Joy-Con 2", "Connect a new Joy-Con 2")}
        closeLabel={
          waiting
            ? t("取消连接 Joy-Con 2", "Cancel connecting the Joy-Con 2")
            : t("关闭", "Close")
        }
        onClose={waiting ? cancel : () => setPairing(null)}
        className="confirm-dialog"
        returnFocus={() => connectButton.current}
        footer={
          <>
            <span className="spacer" />
            {waiting ? (
              <button type="button" className="btn-ghost" onClick={cancel}>
                {t("取消", "Cancel")}
              </button>
            ) : (
              <button
                type="button"
                className="btn btn-primary"
                onClick={() => setPairing(null)}
              >
                {t("确定", "OK")}
              </button>
            )}
          </>
        }
      >
        {waiting ? (
          <>
            <p className="dialog-text">
              {t(
                `请在 ${PAIR_SECONDS} 秒内按住 Joy-Con 2 侧面的 SYNC 键。`,
                `Hold the SYNC button on the side of the Joy-Con 2 within ${PAIR_SECONDS} seconds.`,
              )}
            </p>
            <p className="dialog-text" role="timer">
              {t(`还剩 ${left} 秒`, `${left} s left`)}
            </p>
          </>
        ) : (
          <p className="dialog-text" role="status">
            {result}
          </p>
        )}
      </Dialog>
      <Dialog
        open={!!forgetting}
        title={
          forgetting
            ? t(
                `忘记 ${padName(forgetting.side, language)}？`,
                `Forget the ${padName(forgetting.side, language)}?`,
              )
            : ""
        }
        closeLabel={t("取消忘记", "Cancel forgetting")}
        onClose={() => setForgetting(null)}
        className="confirm-dialog"
        footer={
          <>
            <span className="spacer" />
            <button
              type="button"
              className="btn-ghost"
              onClick={() => setForgetting(null)}
            >
              {t("取消", "Cancel")}
            </button>
            <button
              type="button"
              className="btn btn-danger"
              disabled={forgetBusy}
              onClick={() => void forget()}
            >
              {t("忘记", "Forget")}
            </button>
          </>
        }
      >
        {forgetFailed && (
          <Notice kind="error" role="alert">
            {t(
              "未能忘记，请稍后再试。",
              "Could not forget it. Try again shortly.",
            )}
          </Notice>
        )}
      </Dialog>
    </section>
  );
}
