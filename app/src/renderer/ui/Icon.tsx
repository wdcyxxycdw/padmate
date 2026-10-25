const paths = {
  pad: "M7 7h10c2 0 3 2 4 7l1 4c0 2-2 3-3 1l-3-3H8l-3 3c-1 2-3 1-3-1l1-4c1-5 2-7 4-7ZM7 10v4M5 12h4",
  code: "m8 7-5 5 5 5M16 7l5 5-5 5",
  log: "M6 3h12v18H6zM9 8h6M9 12h6M9 16h4",
  settings: "M4 7h10M18 7h2M4 17h4M12 17h8M16 5v4M10 15v4",
  pause: "M9 5v14M15 5v14",
  play: "m8 5 11 7-11 7Z",
  chevronDown: "m6 9 6 6 6-6",
  chevronUp: "m6 15 6-6 6 6",
  chevronRight: "m9 6 6 6-6 6",
  plus: "M12 5v14M5 12h14",
  close: "M6 6l12 12M18 6 6 18",
  check: "m5 12 4.5 4.5L19 7",
  search: "M16 16l4 4M11 17.5a6.5 6.5 0 1 0 0-13 6.5 6.5 0 0 0 0 13Z",
  warn: "M12 3 2 21h20L12 3ZM12 10v4M12 17.5v.01",
  /** A circle with a cross: an error, distinct in shape from `warn`. */
  error: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18ZM9 9l6 6M15 9l-6 6",
  info: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18ZM12 11v6M12 7.5v.01",
  refresh: "M20 8a8 8 0 1 0 .9 6M20 3v5h-5",
  /** Out of the tray: a file saved elsewhere (PRO-20). */
  export: "M12 15V4M7 9l5-5 5 5M5 20h14",
  /** Into the tray: a file brought in (PRO-20). */
  import: "M12 4v11M7 10l5 5 5-5M5 20h14",
  folder: "M3 6h6l2 2h10v11H3z",
  file: "M6 3h8l5 5v13H6zM14 3v5h5",
  list: "M9 6h11M9 12h11M9 18h11M4 6h.01M4 12h.01M4 18h.01",
  image: "M3 5h18v14H3zM3 16l5-5 4 4 3-3 6 6",
  doc: "M6 3h12v18H6zM9 8h6M9 12h6",
  release: "M12 3v10M7 8l5 5 5-5M5 20h14",
  copy: "M8 8h12v12H8zM16 8V4H4v12h4",
  paste: "M9 4h6v3H9zM7 5H5v16h14V5h-2M9 12h6M9 16h4",
  edit: "M4 20h4L19 9l-4-4L4 16v4Z",
  trash: "M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13",
  lock: "M5 11h14v9H5zM8 11V8a4 4 0 0 1 8 0v3",
  sliders: "M4 7h10M18 7h2M4 17h4M12 17h8M16 5v4M10 15v4",
  script: "m8 7-5 5 5 5M16 7l5 5-5 5",
  /** A mouse with its wheel: the mouse keys of a shortcut (ADR-0062). */
  mouse: "M12 3a6 6 0 0 1 6 6v6a6 6 0 0 1-12 0V9a6 6 0 0 1 6-6ZM12 7v3",
  /** A program's window: stands in for an app without an icon of its own (UI-42). */
  app: "M3 5h18v14H3zM3 9.5h18M6.5 7.25h.01M9.5 7.25h.01",
  more: "",
} as const;

export type IconName = keyof typeof paths;

export function Icon({
  name,
  size = 16,
  stroke = 1.8,
  className = "",
}: {
  name: IconName;
  size?: number;
  stroke?: number;
  className?: string;
}) {
  if (name === "more")
    return (
      <svg
        className={`ui-icon ${className}`}
        width={size}
        height={size}
        viewBox="0 0 24 24"
        fill="currentColor"
        aria-hidden="true"
      >
        <circle cx="5" cy="12" r="1.6" />
        <circle cx="12" cy="12" r="1.6" />
        <circle cx="19" cy="12" r="1.6" />
      </svg>
    );
  return (
    <svg
      className={`ui-icon ${className}`}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={stroke}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d={paths[name]} />
    </svg>
  );
}
