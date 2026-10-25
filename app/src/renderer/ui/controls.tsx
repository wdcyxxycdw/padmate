import {
  type ReactNode,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { Icon, type IconName } from "./Icon";

export interface SegmentOption<T extends string> {
  value: T;
  label: ReactNode;
  disabled?: boolean;
  title?: string;
}

/** A radiogroup of buttons. Arrow keys move the selection like native radios. */
export function Segmented<T extends string>({
  label,
  options,
  value,
  onChange,
  small = false,
  disabled = false,
  testId,
  className = "",
}: {
  label: string;
  options: SegmentOption<T>[];
  value: T;
  onChange: (value: T) => void;
  small?: boolean;
  disabled?: boolean;
  testId?: string;
  className?: string;
}) {
  const enabled = options.filter((option) => !option.disabled && !disabled);
  // The selected option is the tab stop; with none selected (a value the
  // options do not have) the first one is, or the group could not be reached.
  const stop = (enabled.find((option) => option.value === value) ?? enabled[0])
    ?.value;
  return (
    <div
      role="radiogroup"
      aria-label={label}
      data-testid={testId}
      className={`segmented${small ? " small" : ""} ${className}`}
      onKeyDown={(event) => {
        const step =
          event.key === "ArrowRight" || event.key === "ArrowDown"
            ? 1
            : event.key === "ArrowLeft" || event.key === "ArrowUp"
              ? -1
              : 0;
        if (!step || !enabled.length) return;
        event.preventDefault();
        const index = enabled.findIndex((option) => option.value === value);
        // Nothing selected: the arrows enter from the end they point away from.
        const next =
          index < 0
            ? enabled[step > 0 ? 0 : enabled.length - 1]
            : (enabled[(index + step + enabled.length) % enabled.length] ??
              enabled[0]);
        onChange(next.value);
        const target = event.currentTarget.querySelector<HTMLButtonElement>(
          `[data-value="${CSS.escape(next.value)}"]`,
        );
        target?.focus();
      }}
    >
      {options.map((option) => (
        // biome-ignore lint/a11y/useSemanticElements: Styled segment buttons; the group implements radio keyboard behavior.
        <button
          type="button"
          role="radio"
          key={option.value}
          data-value={option.value}
          aria-checked={option.value === value}
          tabIndex={option.value === stop ? 0 : -1}
          disabled={disabled || option.disabled}
          title={option.title}
          onClick={() => onChange(option.value)}
        >
          {option.label}
        </button>
      ))}
    </div>
  );
}

export function Switch({
  checked,
  onChange,
  label,
  disabled = false,
  id,
}: {
  checked: boolean;
  onChange: (checked: boolean) => void;
  label: string;
  disabled?: boolean;
  id?: string;
}) {
  return (
    <button
      type="button"
      role="switch"
      id={id}
      className="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
    />
  );
}

export type MenuEntry =
  | {
      kind?: "item";
      label: string;
      icon?: IconName;
      hint?: string;
      danger?: boolean;
      disabled?: boolean;
      testId?: string;
      onSelect: () => void;
    }
  | {
      kind: "radio";
      label: string;
      hint?: string;
      checked: boolean;
      onSelect: () => void;
    }
  | { kind: "separator" }
  | { kind: "section"; label: string };

/** Where an open menu sits relative to its button, and how tall it may be. */
interface MenuPlacement {
  side: "bottom" | "top";
  align: "start" | "end";
  maxHeight: number;
}
const MENU_GAP = 6;
/** Room kept between a menu and the edge that clips it. */
const MENU_MARGIN = 4;

/**
 * The nearest ancestor that clips its content on one axis (a scroll
 * container or overflow: hidden), within the window.
 */
function clipRect(node: Element, axis: "x" | "y" = "y"): DOMRect {
  for (let el = node.parentElement; el; el = el.parentElement) {
    const style = getComputedStyle(el);
    const overflow = axis === "x" ? style.overflowX : style.overflowY;
    if (overflow === "auto" || overflow === "scroll" || overflow === "hidden")
      return el.getBoundingClientRect();
  }
  return new DOMRect(0, 0, window.innerWidth, window.innerHeight);
}

/**
 * Which edge of its button a menu of width lines up with: align, unless the
 * menu would cross the clipping edge there and fits the other way; when
 * neither fits, the side with more room.
 */
export function menuAlign(
  align: "start" | "end",
  button: { left: number; right: number },
  clip: { left: number; right: number },
  width: number,
): "start" | "end" {
  if (width <= 0) return align;
  const right = Math.min(clip.right, window.innerWidth);
  const left = Math.max(clip.left, 0);
  // "start" grows rightwards from the button's left edge, "end" leftwards.
  const room = {
    start: right - button.left - MENU_MARGIN,
    end: button.right - left - MENU_MARGIN,
  };
  if (room[align] >= width) return align;
  const other = align === "start" ? "end" : "start";
  if (room[other] >= width) return other;
  return room.start >= room.end ? "start" : "end";
}

const menuTrigger = (anchor: HTMLElement | null) =>
  anchor?.querySelector<HTMLButtonElement>(":scope > button");

/**
 * A button that opens a popover menu. Escape, Tab out or an outside click
 * closes it. `openRequest` opens it from outside (a right click elsewhere):
 * every new value above 0 opens the menu, including the value it mounts with,
 * so the caller resets it to 0 when an old request must not reopen the menu.
 */
export function MenuButton({
  label,
  icon = "more",
  children,
  items,
  align = "end",
  className = "icon-btn",
  disabled = false,
  testId,
  openRequest = 0,
}: {
  label: string;
  icon?: IconName;
  children?: ReactNode;
  items: MenuEntry[];
  align?: "start" | "end";
  className?: string;
  disabled?: boolean;
  testId?: string;
  openRequest?: number;
}) {
  const [open, setOpen] = useState(false);
  const [placement, setPlacement] = useState<MenuPlacement>({
    side: "bottom",
    align,
    maxHeight: 0,
  });
  const root = useRef<HTMLDivElement>(null);
  const handledRequest = useRef(0);
  const id = useId();
  // Focus goes back to the button before the chosen item unmounts, so it does
  // not fall to <body>; an action that moves focus itself (a dialog) still wins.
  const choose = (onSelect: () => void) => {
    menuTrigger(root.current)?.focus();
    setOpen(false);
    onSelect();
  };
  // biome-ignore lint/correctness/useExhaustiveDependencies: Only a new request opens the menu.
  useEffect(() => {
    if (openRequest <= 0 || openRequest === handledRequest.current) return;
    handledRequest.current = openRequest;
    if (!disabled) setOpen(true);
  }, [openRequest]);
  // Open upward when the space below the button (inside the nearest scroll
  // container, e.g. the drawer body) is too small, and cap the height when
  // neither side fits. Likewise line the menu up with the button's other
  // edge when it would be cut off sideways (a button near a card's right
  // edge). Runs before paint and before the first item takes focus.
  useLayoutEffect(() => {
    if (!open) return;
    const anchor = root.current;
    const menu = anchor?.querySelector<HTMLElement>('[role="menu"]');
    if (!anchor || !menu) return;
    const clip = clipRect(anchor);
    const box = anchor.getBoundingClientRect();
    const horizontal = menuAlign(
      align,
      box,
      clipRect(anchor, "x"),
      menu.offsetWidth,
    );
    // scrollHeight ignores an earlier max-height; add the borders back.
    const height =
      menu.scrollHeight + (menu.offsetHeight - menu.clientHeight) + MENU_GAP;
    const below = clip.bottom - box.bottom;
    const above = box.top - clip.top;
    const side = height > below && above > below ? "top" : "bottom";
    // Room for the menu box itself, keeping a small margin to the clip edge.
    const room = (side === "top" ? above : below) - MENU_GAP - 4;
    const maxHeight = height - MENU_GAP > room && room >= 40 ? room : 0;
    setPlacement((current) =>
      current.side === side &&
      current.align === horizontal &&
      current.maxHeight === maxHeight
        ? current
        : { side, align: horizontal, maxHeight },
    );
  }, [open, align]);
  useEffect(() => {
    if (!open) return;
    const close = (event: MouseEvent | KeyboardEvent) => {
      if (event instanceof KeyboardEvent) {
        if (event.key !== "Escape") return;
        setOpen(false);
        menuTrigger(root.current)?.focus();
        return;
      }
      if (!root.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", close);
    document.addEventListener("keydown", close);
    root.current
      ?.querySelector<HTMLButtonElement>('[role="menu"] button:not(:disabled)')
      ?.focus();
    return () => {
      document.removeEventListener("mousedown", close);
      document.removeEventListener("keydown", close);
    };
  }, [open]);
  const hintId = (index: number) => `${id}-hint-${index}`;
  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: The blur handler only closes the menu when focus leaves it.
    <div
      className="menu-anchor"
      ref={root}
      onBlur={(event) => {
        // Tab or Shift+Tab out of the menu closes it. A null relatedTarget (a
        // click on a non-focusable part, or the window losing focus) keeps it
        // open; the outside-mousedown listener handles real outside clicks.
        const next = event.relatedTarget as Node | null;
        if (open && next && !event.currentTarget.contains(next)) setOpen(false);
      }}
    >
      <button
        type="button"
        className={className}
        aria-label={children ? undefined : label}
        title={label}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        disabled={disabled}
        data-testid={testId}
        onClick={() => setOpen(!open)}
      >
        {children ?? <Icon name={icon} />}
      </button>
      {open && (
        <div
          role="menu"
          id={id}
          aria-label={label}
          className="menu"
          data-align={placement.align}
          data-side={placement.side}
          style={
            placement.maxHeight
              ? { maxHeight: placement.maxHeight, overflowY: "auto" }
              : undefined
          }
          onKeyDown={(event) => {
            const buttons = [
              ...event.currentTarget.querySelectorAll<HTMLButtonElement>(
                "button:not(:disabled)",
              ),
            ];
            if (!buttons.length) return;
            let next: HTMLButtonElement | undefined;
            if (event.key === "Home") next = buttons[0];
            else if (event.key === "End") next = buttons[buttons.length - 1];
            else if (event.key === "ArrowDown" || event.key === "ArrowUp") {
              const index = buttons.indexOf(
                document.activeElement as HTMLButtonElement,
              );
              next =
                buttons[
                  (index +
                    (event.key === "ArrowDown" ? 1 : -1) +
                    buttons.length) %
                    buttons.length
                ];
            } else return;
            event.preventDefault();
            next?.focus();
          }}
        >
          {items.map((item, index) => {
            if (item.kind === "separator")
              // biome-ignore lint/suspicious/noArrayIndexKey: Separators have no identity of their own.
              return <div key={index} className="menu-separator" />;
            if (item.kind === "section")
              return (
                <div key={item.label} className="menu-section">
                  {item.label}
                </div>
              );
            // The hint is a description, not part of the item's name.
            const hint = item.hint && (
              <span className="menu-hint" id={hintId(index)} aria-hidden="true">
                {item.hint}
              </span>
            );
            if (item.kind === "radio")
              return (
                <button
                  type="button"
                  role="menuitemradio"
                  aria-checked={item.checked}
                  aria-describedby={item.hint ? hintId(index) : undefined}
                  key={item.label}
                  className="menu-item"
                  onClick={() => choose(item.onSelect)}
                >
                  <span className="menu-radio" />
                  {item.label}
                  {hint}
                </button>
              );
            return (
              <button
                type="button"
                role="menuitem"
                key={item.label}
                className={`menu-item${item.danger ? " danger" : ""}`}
                aria-describedby={item.hint ? hintId(index) : undefined}
                disabled={item.disabled}
                data-testid={item.testId}
                onClick={() => choose(item.onSelect)}
              >
                {item.icon && <Icon name={item.icon} size={15} />}
                {item.label}
                {hint}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

/** Moves focus to `target` unless something other than <body> already has it. */
function restoreFocus(target: Element | null | undefined, force: boolean) {
  if (!(target instanceof HTMLElement) || !target.isConnected) return;
  const active = document.activeElement;
  if (active === target) return;
  if (force || !active || active === document.body) target.focus();
}

/**
 * A modal dialog built on the native <dialog> element. Closing it returns
 * focus to the element that had it when the dialog opened, or to
 * `returnFocus()` when the caller knows a better place (the opener may be gone).
 */
export function Dialog({
  open,
  title,
  subtitle,
  closeLabel,
  closeDisabled = false,
  onClose,
  children,
  footer,
  className = "",
  returnFocus,
}: {
  open: boolean;
  title: string;
  subtitle?: string;
  closeLabel: string;
  /** Esc and the close button do nothing, for a step that must finish first. */
  closeDisabled?: boolean;
  onClose: () => void;
  children: ReactNode;
  footer?: ReactNode;
  className?: string;
  returnFocus?: () => HTMLElement | null | undefined;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const opener = useRef<Element | null>(null);
  const returnTo = useRef(returnFocus);
  returnTo.current = returnFocus;
  const heading = useId();
  useEffect(() => {
    const node = dialog.current;
    if (!node) return;
    if (open && !node.open) {
      opener.current = document.activeElement;
      if (typeof node.showModal === "function") node.showModal();
      else node.setAttribute("open", "");
    } else if (!open && node.open) {
      if (typeof node.close === "function") node.close();
      else node.removeAttribute("open");
      const target = returnTo.current?.() ?? opener.current;
      opener.current = null;
      // close() may already have refocused the opener; a caller's target wins.
      restoreFocus(target, true);
    }
  }, [open]);
  // Unmounted while open (its owner went away): the element is gone, so put
  // focus back once the rest of the tree has settled.
  useEffect(() => {
    const node = dialog.current;
    return () => {
      if (!node || node.isConnected || !node.hasAttribute("open")) return;
      const target = returnTo.current?.() ?? opener.current;
      const schedule =
        typeof requestAnimationFrame === "function"
          ? requestAnimationFrame
          : (callback: () => void) => setTimeout(callback, 0);
      schedule(() => restoreFocus(target, false));
    };
  }, []);
  // The element stays mounted so closing can call close() and restore focus;
  // `dialog:not([open])` keeps it hidden. Its content renders only while open.
  return (
    <dialog
      ref={dialog}
      className={`dialog ${className}`}
      aria-labelledby={heading}
      onCancel={(event) => {
        event.preventDefault();
        if (!closeDisabled) onClose();
      }}
    >
      {open && (
        <div className="dialog-frame">
          <header className="dialog-header">
            <div className="dialog-title">
              <h2 id={heading}>{title}</h2>
              {subtitle && <p>{subtitle}</p>}
            </div>
            <button
              type="button"
              className="icon-btn bare"
              aria-label={closeLabel}
              disabled={closeDisabled}
              onClick={onClose}
            >
              <Icon name="close" />
            </button>
          </header>
          <div className="dialog-body">{children}</div>
          {footer && <footer className="dialog-footer">{footer}</footer>}
        </div>
      )}
    </dialog>
  );
}

/**
 * Collapsible "高级" section. `defaultOpen` opens it on mount and whenever it
 * becomes true later (a new target inside it); it never closes what the user
 * opened.
 */
export function Disclosure({
  title,
  summary,
  children,
  defaultOpen = false,
  testId,
}: {
  title: string;
  summary?: string;
  children: ReactNode;
  defaultOpen?: boolean;
  testId?: string;
}) {
  const details = useRef<HTMLDetailsElement>(null);
  useLayoutEffect(() => {
    if (defaultOpen && details.current) details.current.open = true;
  }, [defaultOpen]);
  return (
    <details ref={details} className="disclosure" data-testid={testId}>
      <summary>
        <Icon
          name="chevronRight"
          size={14}
          stroke={2}
          className="disclosure-icon"
        />
        {title}
        {summary && <span className="disclosure-summary">{summary}</span>}
      </summary>
      <div className="disclosure-body">{children}</div>
    </details>
  );
}

export function Notice({
  kind = "info",
  title,
  children,
  icon,
  actions,
  role,
  testId,
}: {
  kind?: "info" | "warning" | "error" | "script";
  title?: ReactNode;
  children?: ReactNode;
  icon?: IconName | null;
  actions?: ReactNode;
  role?: "status" | "alert";
  testId?: string;
}) {
  const symbol =
    icon === null
      ? null
      : (icon ?? (kind === "warning" || kind === "error" ? "warn" : "info"));
  return (
    <div className={`notice ${kind}`} role={role} data-testid={testId}>
      {symbol && <Icon name={symbol} size={15} stroke={2} />}
      <div className="notice-body">
        {title && <span className="notice-title">{title}</span>}
        {children}
      </div>
      {actions}
    </div>
  );
}
