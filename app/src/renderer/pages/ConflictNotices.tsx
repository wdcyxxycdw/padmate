import { useRef } from "react";
import type { StatusResult } from "../../shared/api";
import type { Language } from "../store";
import { Notice } from "../ui/controls";
import { Icon } from "../ui/Icon";
import "./pages.css";

export type Conflict = NonNullable<StatusResult["conflicts"]>[number];
const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/** Software that padmated reports as possibly holding the controller. */
export function conflictName(process: string): string {
  const name = process.toLowerCase().replace(/\.exe$/, "");
  if (name === "steam") return "Steam";
  if (name === "ds4windows") return "DS4Windows";
  if (name.startsWith("hidhide")) return "HidHide";
  return process;
}

/** The kinds of controller Steam Input has a setting for (IN-24), in plain words. */
function steamClassName(kind: string, language: Language): string {
  switch (kind) {
    case "xbox":
      return tx(language, "Xbox 类", "Xbox-type");
    case "playstation":
      return tx(language, "PlayStation 类", "PlayStation-type");
    case "switch":
      return tx(language, "Switch 类", "Switch-type");
    case "steamHardware":
      return tx(language, "Steam 自家", "Steam's own");
  }
  return kind;
}

/**
 * The classes of controller that a "steamInput" entry (ADR-0071) lists for
 * the software called name, each once, in the order the daemon gave.
 */
export function conflictClasses(
  conflicts: readonly Conflict[] | undefined,
  name: string,
): string[] {
  const kinds = (conflicts ?? [])
    .filter((entry) => conflictName(entry.process) === name)
    .flatMap((entry) => entry.classes ?? []);
  return [...new Set(kinds)];
}

/**
 * What to do about running software. Steam with the classes of controller it
 * may manage (IN-24) says which controllers and where to turn Steam Input off;
 * padmate cannot see Steam's virtual controller, so it only says "may".
 */
export function conflictAdvice(
  name: string,
  language: Language,
  classes: readonly string[] = [],
): string {
  switch (name) {
    case "Steam":
      if (classes.length) {
        const kinds = classes.map((kind) => steamClassName(kind, language));
        const last = kinds.pop();
        const list = kinds.length
          ? tx(
              language,
              `${[...kinds, last].join("、")}`,
              `${kinds.join(", ")} and ${last}`,
            )
          : (last ?? "");
        return tx(
          language,
          `它可能接管 ${list}手柄：请在 Steam 的设置 → 控制器里关掉这类手柄的 Steam Input，或在不用 Steam 时退出它。`,
          `It may take over ${list} controllers: turn Steam Input off for them in Steam's Settings → Controller, or quit Steam when you are not using it.`,
        );
      }
      return tx(
        language,
        "Steam 输入可能抢占手柄或重复发送按键。如有异常，请完全退出 Steam。",
        "Steam Input may take over the controller or send keys twice. If something is wrong, quit Steam completely.",
      );
    case "DS4Windows":
      return tx(
        language,
        "DS4Windows 可能另外创建一个虚拟手柄。如出现重复的手柄，请退出该程序。",
        "DS4Windows may add a virtual controller. If controllers appear twice, quit it.",
      );
    case "HidHide":
      return tx(
        language,
        "HidHide 可能对 padmate 隐藏手柄。请将 padmated.exe 加入其允许列表。",
        "HidHide may hide the controller from padmate. Allow padmated.exe in it.",
      );
    default:
      return tx(
        language,
        `${name} 可能拦截手柄输入。如有异常，请先退出该程序。`,
        `${name} may intercept controller input. If something is wrong, quit it.`,
      );
  }
}

/** Unique display names of the reported software that is not dismissed. */
export function conflictNames(
  conflicts: readonly Conflict[] | undefined,
  dismissed: readonly string[] = [],
): string[] {
  const names = (conflicts ?? []).map((entry) => conflictName(entry.process));
  return [...new Set(names)].filter((name) => !dismissed.includes(name));
}

/**
 * The first enabled control after `root` inside its dialog (the first-run
 * page), else inside its parent, if any.
 */
function focusableAfter(root: HTMLElement): HTMLElement | undefined {
  const scope = root.closest("dialog") ?? root.parentElement;
  const candidates = scope?.querySelectorAll<HTMLElement>(
    "button, select, input, textarea, a[href]",
  );
  return [...(candidates ?? [])].find(
    (element) =>
      !root.contains(element) &&
      !!(
        root.compareDocumentPosition(element) & Node.DOCUMENT_POSITION_FOLLOWING
      ) &&
      !element.matches(":disabled"),
  );
}

/**
 * Compact warnings about running software that may hold the controller
 * (Steam, DS4Windows, HidHide). Without `onDismiss` they cannot be closed.
 */
export function ConflictNotices({
  conflicts,
  language,
  dismissed,
  onDismiss,
}: {
  conflicts: readonly Conflict[] | undefined;
  language: Language;
  dismissed?: readonly string[];
  onDismiss?: (name: string) => void;
}) {
  const list = useRef<HTMLDivElement>(null);
  const names = conflictNames(conflicts, dismissed);
  if (!names.length) return null;
  // The closed notice takes its focused button with it; keep the keyboard
  // nearby: the next notice, else the previous one, else the next control.
  const dismiss = (name: string, index: number) => {
    const root = list.current;
    const buttons = [
      ...(root?.querySelectorAll<HTMLButtonElement>(".page-conflict-close") ??
        []),
    ];
    const target =
      buttons[index + 1] ??
      buttons[index - 1] ??
      (root ? focusableAfter(root) : undefined);
    onDismiss?.(name);
    target?.focus();
  };
  // The name and the advice share one paragraph, so a space separates them
  // for screen readers and copied text.
  return (
    <div className="page-conflicts" ref={list}>
      {names.map((name, index) => (
        <Notice
          key={name}
          kind="warning"
          role="status"
          title={tx(language, `${name} 正在运行`, `${name} is running`)}
          actions={
            onDismiss && (
              <button
                type="button"
                className="icon-btn bare page-conflict-close"
                aria-label={tx(language, "关闭提示", "Dismiss")}
                title={tx(language, "关闭提示", "Dismiss")}
                onClick={() => dismiss(name, index)}
              >
                <Icon name="close" size={14} stroke={2} />
              </button>
            )
          }
        >
          {" "}
          <span>
            {conflictAdvice(name, language, conflictClasses(conflicts, name))}
          </span>
        </Notice>
      ))}
    </div>
  );
}
