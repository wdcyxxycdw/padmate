import { type CSSProperties, useEffect, useState } from "react";
import {
  defaultOverlayPrefs,
  hintsFadeMs,
  hintsLayout,
  type OverlayAPI,
  type OverlayHintsMessage,
  type OverlayPanelsMessage,
  scriptPanelLines,
} from "../shared/overlay";
import { arrangeHints, hintControlName } from "./overlay-hints";
import { prefersReducedMotion, toastSchedule } from "./overlay-toast";

type Phase = "in" | "out";

/** The key hints on the page: the message, and whether the panel is faded in. */
interface Hints {
  message: OverlayHintsMessage;
  visible: boolean;
}

/** A script's panels on the page: the message, and whether they are faded in. */
interface ScriptPanels {
  message: OverlayPanelsMessage;
  visible: boolean;
}

/**
 * The overlay's whole content (HUD-01, HUD-04, HUD-03, SCR-22, ADR-0063): the
 * toast Main sends when something changed or a script asks for one, one at a
 * time, the key hints panel while the hints button is held and the panels a
 * script keeps. A new toast replaces the one before at once; each fades in,
 * holds (for the time a script's toast names, else 1500 ms) and fades out
 * (CSS transitions on the toast's opacity, overlay.css).
 *
 * The pill's opacity (HUD-05) is the UI's choice, which Main sends as
 * settings: it multiplies the opacity the pill fades to, as the custom
 * property --overlay-opacity on the root (overlay.css). 100 % until Main has
 * said otherwise. The hints panel's background follows it too.
 *
 * The root's data-visible attribute is how the toast is shown and hidden
 * (opacity, overlay.css) and is true while a toast is on screen. The window
 * around it stays shown and is never activated.
 *
 * The toast element is always there, empty and transparent when idle: a fade
 * needs an element that already has a style to change from, and a live region
 * announces text that is put into one that exists. The hints panel is the
 * same, for the same reason.
 *
 * The key hints (HUD-03) are a panel of up to maxHintsShown rows of
 * "control  what it does" in two columns, on the side of the toast area that
 * is away from the screen's edge (Main makes the window taller for it and the
 * toast area stays where it was). It fades in and out over hintsFadeMs; the
 * entries stay on the page until the fade out is over.
 *
 * A script's panels (SCR-22) are drawn in the same place and style: one row
 * per line, a panel's title first, then its rows as "left  right", up to
 * maxPanelRows rows in all and a last line "…" for more. They fade as the
 * hints do. While the hints are on the page they are held back, and come
 * back when the hints are gone.
 */
export function Overlay({ api = window.padmateOverlay }: { api?: OverlayAPI }) {
  const [toast, setToast] = useState<{ text: string; phase: Phase } | null>(
    null,
  );
  const [opacity, setOpacity] = useState<number>(defaultOverlayPrefs.opacity);
  const [hints, setHints] = useState<Hints | null>(null);
  const [scriptPanels, setScriptPanels] = useState<ScriptPanels | null>(null);
  // Where the window sits, from the last hints message: the toast area is the
  // edge-side 160 px of the window, which only matters once it is taller.
  const [position, setPosition] = useState<"top" | "bottom">(
    defaultOverlayPrefs.position,
  );

  useEffect(() => {
    if (!api) return;
    return api.onPrefs((settings) => setOpacity(settings.opacity));
  }, [api]);

  useEffect(() => {
    if (!api) return;
    let timers: ReturnType<typeof setTimeout>[] = [];
    const clear = () => {
      for (const timer of timers) clearTimeout(timer);
      timers = [];
    };
    const stop = api.onToast((text, ms) => {
      // The old toast's timers end with it: the new one starts its own.
      clear();
      const { fadeOutAt, endAt } = toastSchedule(prefersReducedMotion(), ms);
      setToast({ text, phase: "in" });
      timers = [
        setTimeout(
          () => setToast((now) => (now ? { ...now, phase: "out" } : now)),
          fadeOutAt,
        ),
        setTimeout(() => setToast(null), endAt),
      ];
    });
    return () => {
      stop();
      clear();
    };
  }, [api]);

  useEffect(() => {
    if (!api) return;
    let gone: ReturnType<typeof setTimeout> | undefined;
    const stop = api.onHints((message) => {
      if (gone) clearTimeout(gone);
      gone = undefined;
      setPosition(message.position);
      if (message.entries.length > 0) {
        setHints({ message, visible: true });
        return;
      }
      // Fade out, keeping the entries until it is over (at once without
      // animation); a message with entries in between cancels the removal.
      setHints((now) => (now ? { ...now, visible: false } : now));
      gone = setTimeout(
        () => {
          gone = undefined;
          setHints(null);
        },
        prefersReducedMotion() ? 0 : hintsFadeMs,
      );
    });
    return () => {
      stop();
      if (gone) clearTimeout(gone);
    };
  }, [api]);

  useEffect(() => {
    if (!api) return;
    let gone: ReturnType<typeof setTimeout> | undefined;
    const stop = api.onPanels((message) => {
      if (gone) clearTimeout(gone);
      gone = undefined;
      setPosition(message.position);
      if (message.panels.length > 0) {
        setScriptPanels({ message, visible: true });
        return;
      }
      // As the hints go: fade out keeping the panels until it is over.
      setScriptPanels((now) => (now ? { ...now, visible: false } : now));
      gone = setTimeout(
        () => {
          gone = undefined;
          setScriptPanels(null);
        },
        prefersReducedMotion() ? 0 : hintsFadeMs,
      );
    });
    return () => {
      stop();
      if (gone) clearTimeout(gone);
    };
  }, [api]);

  const style = {
    "--overlay-opacity": String(opacity / 100),
  } as CSSProperties;
  const arranged = hints ? arrangeHints(hints.message.entries) : undefined;
  const rows = arranged ? hintsLayout(arranged.shown.length).rows : 0;
  const layout = scriptPanels
    ? scriptPanelLines(scriptPanels.message.panels)
    : undefined;
  return (
    <div className="overlay-page" data-position={position}>
      <div
        className="overlay-root"
        data-testid="overlay-root"
        data-visible={toast ? "true" : "false"}
        style={style}
      >
        <div
          className="overlay-toast"
          data-testid="overlay-toast"
          data-phase={toast?.phase ?? "idle"}
          role="status"
        >
          {toast?.text}
        </div>
      </div>
      <div
        className="overlay-hints"
        data-testid="overlay-hints"
        data-visible={hints?.visible ? "true" : "false"}
        aria-hidden={hints?.visible ? undefined : true}
        style={style}
      >
        {hints && arranged ? (
          <>
            <ul
              className="overlay-hints-list"
              style={{ "--hint-rows": rows } as CSSProperties}
            >
              {arranged.shown.map((entry) => (
                <li className="overlay-hint" key={entry.control}>
                  <span className="overlay-hint-control">
                    {hintControlName(
                      entry.control,
                      hints.message.pad,
                      hints.message.language,
                    )}
                  </span>
                  <span className="overlay-hint-label">{entry.label}</span>
                </li>
              ))}
            </ul>
            {arranged.more ? <div className="overlay-hints-more">…</div> : null}
          </>
        ) : null}
      </div>
      <div
        className="overlay-hints overlay-panels"
        data-testid="overlay-panels"
        data-visible={scriptPanels?.visible && !hints ? "true" : "false"}
        aria-hidden={scriptPanels?.visible && !hints ? undefined : true}
        style={style}
      >
        {layout ? (
          <>
            <ul className="overlay-panels-list">
              {layout.lines.map((line, index) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: Rows may repeat; a message replaces them all.
                <li key={index} className={`overlay-panel-${line.kind}`}>
                  {line.kind === "title" ? (
                    line.text
                  ) : (
                    <>
                      <span className="overlay-panel-left">{line.left}</span>
                      <span className="overlay-panel-right">{line.right}</span>
                    </>
                  )}
                </li>
              ))}
            </ul>
            {layout.more ? <div className="overlay-hints-more">…</div> : null}
          </>
        ) : null}
      </div>
    </div>
  );
}
