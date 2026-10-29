import { useEffect, useReducer, useRef, useState } from "react";
import type {
  DaemonEvent,
  DeviceEntry,
  PadmateAPI,
  RawInfo,
  RawInputEvent,
} from "../../shared/api";
import { ControlIcon } from "../control-icon";
import { errorInfo } from "../library-model";
import {
  elementName,
  liveElement,
  mappingFailureText,
  mappingNotices,
  missingKeysNotice,
  padSelector,
  stepInfo,
} from "../mapping-model";
import {
  DEFAULT_STEPS,
  QUIET_NS,
  type RawKind,
  Session,
  type WizardInput,
} from "../mapwizard";
import type { Language } from "../store";
import { Dialog, Notice } from "../ui/controls";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/** What the wizard asks of the app's API. */
export type WizardAPI = Pick<
  PadmateAPI,
  | "onEvent"
  | "padCaptureStart"
  | "padCaptureStop"
  | "rawWatchStart"
  | "rawWatchStop"
  | "setMapping"
>;

const defaultClock = () => performance.now() * 1e6;

/**
 * When the window is back, the capture is asked for again (it is on, which
 * changes nothing; Main would have ended it only when the connection went)
 * and Main may not have the window in front yet. A few tries a moment apart,
 * then the wizard ends.
 */
const RESUME_TRIES = 3;
const RESUME_RETRY_MS = 300;

/** What the line under the prompt says about the last thing that happened. */
type Note =
  | {
      kind: "bound";
      step: string;
      input: WizardInput | undefined;
      cleared: string[];
    }
  | { kind: "rejected" }
  | { kind: "noisy"; axes: number[] };

interface Live {
  kind: RawKind;
  index: number;
  value: number;
}

const rawKinds: readonly string[] = ["button", "axis", "hat"];

function noteText(note: Note, language: Language): string {
  switch (note.kind) {
    case "bound": {
      const name = stepInfo(note.step, language).name;
      const element = note.input ? elementName(note.input, language) : "";
      const cleared = note.cleared.map((id) => stepInfo(id, language).name);
      const base = tx(
        language,
        `已记录 ${name}：${element}`,
        `Recorded ${name}: ${element}`,
      );
      if (cleared.length === 0) return base;
      const names = cleared.join(tx(language, "、", ", "));
      return tx(
        language,
        `${base}（与 ${names} 相同，${names} 的记录已清除）`,
        `${base} (the same as ${names}, which is cleared)`,
      );
    }
    case "rejected":
      return tx(
        language,
        "十字键只能按一个方向。请松开后再按一次。",
        "Press only one direction on the D-pad. Let go and press again.",
      );
    case "noisy": {
      const axes = note.axes.join(tx(language, "、", ", "));
      return tx(
        language,
        `轴 ${axes} 在自己抖动，先不算。`,
        `Axis ${axes} moves by itself and is not counted.`,
      );
    }
  }
}

type Phase = "starting" | "running" | "saving" | "saved" | "error";

/**
 * The mapping wizard (IN-17, IN-19, ADR-0072): for a controller SDL does not
 * know (建立映射), or one whose mapping is to be redone (重新映射), it asks
 * for each control in turn, finds the element the user used from the
 * controller's raw joystick events, and saves the SDL mapping it builds.
 *
 * While it is open the mapping is paused (the capture gate, so pressing the
 * buttons sends no keys) and the controller's raw events come as input.raw
 * events. The capture is asked for so that it stays when the window is not
 * in front (ADR-0076): the user may be in the painting app meanwhile, and
 * that must receive nothing, also after a lock or a sleep. Main ends it, and
 * the raw watch, when the page goes (a reload, a crash, the window closing);
 * every way this dialog is left ends both too, by the effect's cleanup. The
 * rules that find the element are in mapwizard.ts; times are those of the
 * clock, the moment an event arrives.
 */
export function MappingWizardDialog({
  open,
  language,
  api,
  device,
  refresh,
  clock = defaultClock,
  onClose,
}: {
  open: boolean;
  language: Language;
  api: WizardAPI;
  device: DeviceEntry;
  /** Reads the controllers again, after a mapping is saved. */
  refresh: () => Promise<void>;
  /** Nanoseconds on a clock that only goes forward. */
  clock?: () => number;
  onClose: () => void;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const selector = padSelector(device);
  // Whether it began as a remap: saving turns a joystick into a gamepad.
  const [remap] = useState(device.info.gamepad);
  const [phase, setPhase] = useState<Phase>("starting");
  const [failure, setFailure] = useState("");
  const [notices, setNotices] = useState<string[]>([]);
  const [note, setNote] = useState<Note | null>(null);
  const [live, setLive] = useState<Live | null>(null);
  const [away, setAway] = useState(false);
  const [, redraw] = useReducer((count: number) => count + 1, 0);
  const sessionRef = useRef<Session | null>(null);
  const infoRef = useRef<RawInfo | null>(null);
  const languageRef = useRef(language);
  languageRef.current = language;
  const clockRef = useRef(clock);
  clockRef.current = clock;
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;
  const refreshRef = useRef(refresh);
  refreshRef.current = refresh;
  // Lets the save end the watch at once: the controller is mapped again.
  const releaseRef = useRef<() => void>(() => {});
  const aliveRef = useRef(false);

  // The whole life of the watch: the capture, the raw events, the window's
  // focus. One cleanup for every way out.
  useEffect(() => {
    if (!open) return;
    let alive = true;
    let released = false;
    let requested = false;
    let focused = true;
    let active = false;
    let resuming = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let retry: ReturnType<typeof setTimeout> | undefined;
    aliveRef.current = true;
    sessionRef.current = null;
    infoRef.current = null;
    setPhase("starting");
    setFailure("");
    setNotices([]);
    setNote(null);
    setLive(null);
    setAway(false);

    function onEvent(event: DaemonEvent) {
      if (event.topic !== "input.raw") return;
      const session = sessionRef.current;
      const info = infoRef.current;
      const data = event.data as Partial<RawInputEvent> | null;
      if (
        !session ||
        !info ||
        !data ||
        data.device !== info.id ||
        typeof data.kind !== "string" ||
        !rawKinds.includes(data.kind) ||
        !Number.isInteger(data.index) ||
        typeof data.value !== "number"
      )
        return;
      const raw: Live = {
        kind: data.kind as RawKind,
        index: data.index as number,
        value: data.value,
      };
      if (!active) {
        // Nothing the controller does now is an answer (the window is away, or
        // the capture is not back), but where it is now is where the next
        // prompt starts from.
        session.track({ ...raw, time: clockRef.current() });
        return;
      }
      const result = session.feed({ ...raw, time: clockRef.current() });
      // An axis that moves by itself shows nothing and says nothing.
      const noise = raw.kind === "axis" && session.noisy().includes(raw.index);
      if (session.prompting && !noise) {
        const shown = liveElement(raw, info.axisRest, "en").text !== null;
        setLive((previous) =>
          shown
            ? raw
            : previous &&
                previous.kind === raw.kind &&
                previous.index === raw.index
              ? null
              : previous,
        );
      }
      if (result.bound)
        setNote({
          kind: "bound",
          step: result.bound,
          input: result.input,
          cleared: result.cleared,
        });
      else if (result.rejected) setNote({ kind: "rejected" });
      else if (!noise) setNote(null);
      redraw();
    }

    function onBlur() {
      focused = false;
      active = false;
      if (sessionRef.current) setAway(true);
    }

    // The capture stays while the window is away, a lock and a sleep too: it
    // is asked for again when the window is back, which is no change when it
    // is on. A press that began before is forgotten.
    function onFocus() {
      focused = true;
      if (!sessionRef.current || resuming) return;
      resuming = true;
      resume(1);
    }

    // The window's focus event can come before Main sees the window in front,
    // so a refusal is asked again a moment later. If it never works the
    // mapping is not paused: the wizard ends rather than wait for answers it
    // would not get.
    function resume(attempt: number) {
      api.padCaptureStart(true).then(
        () => {
          resuming = false;
          if (!alive || released || !focused) return;
          const session = sessionRef.current;
          if (session && !session.done()) session.redo();
          active = true;
          setAway(false);
          setNote(null);
          setLive(null);
          redraw();
        },
        (error) => {
          if (!alive || released || !focused) {
            resuming = false;
            return;
          }
          if (attempt < RESUME_TRIES) {
            retry = setTimeout(() => {
              // Away again meanwhile: the next focus starts over.
              if (focused) resume(attempt + 1);
              else resuming = false;
            }, RESUME_RETRY_MS);
            return;
          }
          resuming = false;
          setFailure(
            mappingFailureText(errorInfo(error), "start", languageRef.current),
          );
          setAway(false);
          setPhase("error");
          release();
        },
      );
    }

    const stopListening = api.onEvent(onEvent);
    window.addEventListener("blur", onBlur);
    window.addEventListener("focus", onFocus);

    const release = () => {
      if (released) return;
      released = true;
      active = false;
      clearTimeout(timer);
      clearTimeout(retry);
      stopListening();
      window.removeEventListener("blur", onBlur);
      window.removeEventListener("focus", onFocus);
      // Asked after the start if that is still on its way: Main keeps the order.
      if (requested) api.rawWatchStop(selector).catch(() => {});
      api.padCaptureStop().catch(() => {});
    };
    releaseRef.current = release;

    (async () => {
      try {
        await api.padCaptureStart(true);
        if (!alive) return;
        requested = true;
        const info = await api.rawWatchStart(selector);
        if (!alive) return;
        infoRef.current = info;
        const session = new Session(
          info.guid,
          {
            axes: info.axes,
            buttons: info.buttons,
            hats: info.hats,
            axisRest: info.axisRest,
          },
          clockRef.current(),
        );
        sessionRef.current = session;
        active = focused;
        setAway(!focused);
        setPhase("running");
        // A timer may fire a moment before the clock says the second is
        // over: it is set again for what is left.
        const wake = () => {
          const now = clockRef.current();
          session.tick(now);
          if (session.quietLeft(now) > 0) {
            timer = setTimeout(wake, Math.ceil(session.quietLeft(now) / 1e6));
            return;
          }
          const noisy = session.noisy();
          if (noisy.length > 0) setNote({ kind: "noisy", axes: noisy });
          redraw();
        };
        timer = setTimeout(wake, QUIET_NS / 1e6);
      } catch (error) {
        if (!alive) return;
        setFailure(
          mappingFailureText(errorInfo(error), "start", languageRef.current),
        );
        setPhase("error");
        release();
      }
    })();

    return () => {
      alive = false;
      aliveRef.current = false;
      release();
    };
  }, [open, api, selector]);

  const session = sessionRef.current;
  const info = infoRef.current;
  // Saving is the review with the save on its way: nothing is taken away.
  const running = (phase === "running" || phase === "saving") && !!session;
  const prompting = running && session.prompting;
  const current = session?.current() ?? null;
  const reviewing = running && session.done();
  const bound = session?.boundCount ?? 0;
  const busy = phase === "saving";

  const change = (action: (session: Session) => void) => {
    if (!sessionRef.current) return;
    action(sessionRef.current);
    setNote(null);
    setLive(null);
    redraw();
  };

  const save = async () => {
    const current = sessionRef.current;
    if (!current || busy) return;
    let mapping: string;
    try {
      mapping = current.mapping().mapping;
    } catch {
      return;
    }
    setPhase("saving");
    setFailure("");
    try {
      const result = await api.setMapping(selector, mapping);
      if (!aliveRef.current) return;
      // The controller is mapped again: nothing more to watch or hold.
      releaseRef.current();
      await refreshRef.current().catch(() => {});
      if (!aliveRef.current) return;
      const lines = mappingNotices(result, languageRef.current);
      if (lines.length > 0) {
        setNotices(lines);
        setPhase("saved");
      } else onCloseRef.current();
    } catch (error) {
      if (!aliveRef.current) return;
      setFailure(
        mappingFailureText(errorInfo(error), "save", languageRef.current),
      );
      setPhase("running");
    }
  };

  const title = remap
    ? t("重新映射", "Remap")
    : t("建立映射", "Set up mapping");
  const status = (() => {
    if (note?.kind === "rejected") return noteText(note, language);
    if (live) {
      const shown = liveElement(live, info?.axisRest ?? [], language).text;
      if (shown) return t(`检测到：${shown}`, `Detected: ${shown}`);
    }
    return note ? noteText(note, language) : "";
  })();
  const missing =
    reviewing && session
      ? missingKeysNotice(
          !session.binding("view"),
          !session.binding("menu"),
          language,
        )
      : null;

  const steps = (
    <ol
      className="wizard-steps"
      aria-label={t("已记录的控件", "Recorded controls")}
    >
      {DEFAULT_STEPS.map((step, index) => {
        const answer = session?.binding(step.id);
        const here = !reviewing && current?.index === index;
        return (
          <li
            key={step.id}
            className="wizard-step"
            aria-current={here ? "step" : undefined}
            data-bound={answer ? "true" : undefined}
          >
            <span className="wizard-step-name">
              {stepInfo(step.id, language).name}
            </span>
            <span
              className="wizard-step-value"
              title={answer ? undefined : t("未映射", "Not mapped")}
            >
              {answer ? elementName(answer, language) : "—"}
            </span>
          </li>
        );
      })}
    </ol>
  );

  const nowAsking = current ? stepInfo(current.step.id, language) : null;

  return (
    <Dialog
      open={open}
      title={title}
      subtitle={t(
        "向导期间，手柄映射暂停，按键不会发给其他程序",
        "While the wizard runs, the controller's mapping is paused and sends no keys",
      )}
      closeLabel={
        remap
          ? t("取消重新映射", "Cancel remapping")
          : t("取消建立映射", "Cancel setting up mapping")
      }
      // Closing while the mapping is on its way would lose what comes back:
      // the warnings, and the devices read again.
      closeDisabled={busy}
      onClose={onClose}
      className="wizard-dialog"
      footer={
        phase === "error" ? (
          <>
            <span className="spacer" />
            <button type="button" className="btn-primary" onClick={onClose}>
              {t("关闭", "Close")}
            </button>
          </>
        ) : phase === "saved" ? (
          <>
            <span className="spacer" />
            <button type="button" className="btn-primary" onClick={onClose}>
              {t("完成", "Done")}
            </button>
          </>
        ) : (
          <>
            <button
              type="button"
              className="btn-ghost"
              disabled={!prompting || busy || current?.index === 0}
              onClick={() => change((s) => s.back())}
            >
              {t("上一步", "Back")}
            </button>
            {!reviewing && (
              <>
                <button
                  type="button"
                  className="btn-ghost"
                  disabled={!prompting}
                  onClick={() => change((s) => s.redo())}
                >
                  {t("重做", "Redo")}
                </button>
                <button
                  type="button"
                  className="btn-ghost"
                  disabled={!prompting}
                  onClick={() => change((s) => s.skip())}
                >
                  {t("跳过", "Skip")}
                </button>
              </>
            )}
            <span className="spacer" />
            {!reviewing && (
              <button
                type="button"
                className="btn-ghost"
                disabled={!prompting || bound === 0}
                onClick={() =>
                  change((s) => {
                    while (!s.done()) s.skip();
                  })
                }
              >
                {t("完成", "Finish")}
              </button>
            )}
            <button
              type="button"
              className="btn-ghost"
              disabled={busy}
              onClick={onClose}
            >
              {t("取消", "Cancel")}
            </button>
            {reviewing && (
              <button
                type="button"
                className="btn-primary"
                disabled={busy || bound === 0}
                onClick={() => void save()}
              >
                {busy ? t("正在保存…", "Saving…") : t("保存", "Save")}
              </button>
            )}
          </>
        )
      }
    >
      <p className="wizard-pad" title={device.info.name}>
        {device.info.name}
      </p>
      {phase === "error" ? (
        <Notice kind="error" role="alert">
          {failure}
        </Notice>
      ) : phase === "saved" ? (
        <>
          <p className="wizard-saved" role="status">
            {t("映射已保存", "Mapping saved")}
          </p>
          {notices.map((line) => (
            <Notice key={line} kind="warning">
              {line}
            </Notice>
          ))}
        </>
      ) : (
        <>
          {away && (
            <Notice kind="warning" role="status">
              {t(
                "窗口不在前台，向导已暂停。回到这个窗口后继续。",
                "This window is not in front, so the wizard is paused. Come back to this window to go on.",
              )}
            </Notice>
          )}
          {!prompting && !reviewing && (
            <p className="wizard-wait" role="status">
              {t(
                "请先别碰手柄，稍等一下…",
                "Leave the controller alone for a moment…",
              )}
            </p>
          )}
          {prompting && nowAsking && current && !reviewing && (
            <div className="wizard-prompt" data-testid="wizard-prompt">
              <ControlIcon icon={nowAsking.icon} />
              <div className="wizard-prompt-text">
                <h3>{nowAsking.prompt}</h3>
                <p>{nowAsking.hint}</p>
              </div>
              <span className="wizard-count">
                {`${current.index + 1} / ${DEFAULT_STEPS.length}`}
              </span>
            </div>
          )}
          {prompting && !reviewing && (
            // Stays on the page through the prompts, empty until there is
            // something to say: a live region that arrives with its text is
            // often not announced.
            <p className="wizard-status" role="status">
              {status}
            </p>
          )}
          {reviewing && (
            <>
              <p className="wizard-saved">
                {t(
                  "所有控件都问过了，确认后保存。",
                  "Every control has been asked. Check them, then save.",
                )}
              </p>
              {missing && <Notice kind="warning">{missing}</Notice>}
            </>
          )}
          {failure && (
            <Notice kind="error" role="alert">
              {failure}
            </Notice>
          )}
          {(prompting || reviewing) && steps}
        </>
      )}
    </Dialog>
  );
}
