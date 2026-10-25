import { useEffect, useRef, useState } from "react";
import { useStore } from "zustand";
import { ControlIcon, type ControlIconSpec, controlIcon } from "./control-icon";
import type { ControllerFamily } from "./controller-model";
import { type KeyPreviewLine, keyPreviewLine } from "./key-preview";
import type { Language, PadmateStore } from "./store";
import { Icon } from "./ui/Icon";

/** How long the line shows after a press, before it fades. */
export const KEY_PREVIEW_MS = 3000;
/** The fade (CSS) after which the text goes and the page's hint is back. */
export const FADE_MS = 400;

const windowFocused = () =>
  typeof document.hasFocus === "function" ? document.hasFocus() : true;

/**
 * One line on the mapping page (UI-44) saying what the pad sends, such as
 * "A → Ctrl+Shift+Z", or "A → 复制全部" with the script icon for a script
 * (SCR-50): shown for each press that runs an action while
 * padmate's own window is in front, then it fades. The control is drawn as
 * the pad shows it (ControlIcon); screen readers read its name instead. It
 * never takes the focus, and screen readers read it politely.
 */
export function KeyPreview({
  store,
  device,
  labels,
  family = "generic",
  scriptActions,
  language,
  onShown,
  focused = windowFocused,
}: {
  store: PadmateStore;
  /** The pad the page shows; presses of other pads are left out. */
  device: number;
  labels: Record<string, string>;
  /** The pad's family, which decides how its controls are drawn. */
  family?: ControllerFamily;
  scriptActions?: readonly { name: string; label?: string }[];
  language: Language;
  /** Whether the line is shown, so the page can hide its hint meanwhile. */
  onShown?: (shown: boolean) => void;
  /** Whether padmate's window is in front; document.hasFocus by default. */
  focused?: () => boolean;
}) {
  const preview = useStore(store, (state) => state.keyPreview);
  const [line, setLine] = useState<
    (KeyPreviewLine & { icon: ControlIconSpec }) | null
  >(null);
  const [shown, setShown] = useState(false);
  // A press from before the page was shown is not news.
  const seen = useRef(store.getState().keyPreview?.seq ?? 0);
  const timers = useRef<ReturnType<typeof setTimeout>[]>([]);
  const latest = useRef({
    device,
    labels,
    family,
    scriptActions,
    language,
    onShown,
  });
  latest.current = {
    device,
    labels,
    family,
    scriptActions,
    language,
    onShown,
  };
  const focusedRef = useRef(focused);
  focusedRef.current = focused;
  useEffect(
    () => () => {
      for (const timer of timers.current) clearTimeout(timer);
    },
    [],
  );
  useEffect(() => {
    if (!preview || preview.seq <= seen.current) return;
    seen.current = preview.seq;
    const current = latest.current;
    if (preview.device !== null && preview.device !== current.device) return;
    if (!focusedRef.current()) return;
    const next = keyPreviewLine(
      preview,
      current.labels,
      current.scriptActions,
      current.language,
    );
    if (!next) return;
    for (const timer of timers.current) clearTimeout(timer);
    setLine({
      ...next,
      icon: controlIcon(preview.control, current.family, current.labels),
    });
    setShown(true);
    current.onShown?.(true);
    // The page's hint comes back once the line has faded, not over it.
    timers.current = [
      setTimeout(() => setShown(false), KEY_PREVIEW_MS),
      setTimeout(() => {
        setLine(null);
        latest.current.onShown?.(false);
      }, KEY_PREVIEW_MS + FADE_MS),
    ];
  }, [preview]);
  return (
    <p
      className="key-preview"
      role="status"
      aria-live="polite"
      data-testid="key-preview"
      data-shown={shown || undefined}
    >
      {line && (
        <>
          <span className="key-preview-control">
            <ControlIcon icon={line.icon} />
            <span className="visually-hidden">{line.control}</span>
          </span>
          {" → "}
          {line.macro && (
            <Icon
              name="script"
              size={13}
              stroke={2.2}
              className="key-preview-icon"
            />
          )}
          {line.sends}
        </>
      )}
    </p>
  );
}
