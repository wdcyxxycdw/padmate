// Code that runs inside the page of a Playwright test (UI-24): the helpers of
// tests/e2e/a11y-helpers.ts hand these to `evaluate`, which serializes the
// function, so each one is plain and reaches for nothing outside itself. They
// live here, away from Playwright, so that src/renderer/a11y-page-scripts.test.ts
// can run them in jsdom: the Electron pass is not part of `task ci`.

/**
 * The properties of a style signature besides the fill. Not in it: the
 * font weight (a bold label stays distinguishable in a forced scheme, but it
 * says nothing about the forced-colors block) and the background color, which
 * the signature reduces to the one thing a person sees: a fill that is not the
 * backdrop's.
 */
export const signatureProperties = [
  "color",
  "borderTopColor",
  "borderRightColor",
  "borderBottomColor",
  "borderLeftColor",
  "borderTopWidth",
  "borderRightWidth",
  "borderBottomWidth",
  "borderLeftWidth",
  "borderTopStyle",
  "outlineStyle",
  "outlineWidth",
  "outlineColor",
  "textDecorationLine",
] as const;

/**
 * What an element looks like once the browser has computed it: the properties
 * that stay under a forced color scheme, as one string, and its fill. A fill
 * counts only when it is painted and is not the color of what is behind it:
 * under forced colors an author color and a transparent one both come out as
 * the page's Canvas or ButtonFace, and they look the same although the
 * numbers differ. `pseudo` reads the element's ::before or ::after, whose
 * backdrop is the element itself.
 */
export function computeSignature(
  element: Element,
  args: readonly [readonly string[], string],
): string {
  const [properties, pseudo] = args;
  const style = getComputedStyle(element, pseudo || null) as unknown as Record<
    string,
    string
  >;
  const alphaOf = (color: string): number => {
    if (color === "transparent") return 0;
    const inner = /\(([^)]*)\)/.exec(color)?.[1];
    if (!inner) return 1;
    const alpha = inner.split(/[\s,/]+/).filter(Boolean)[3];
    if (alpha === undefined) return 1;
    const value = alpha.endsWith("%")
      ? Number.parseFloat(alpha) / 100
      : Number(alpha);
    return Number.isNaN(value) ? 1 : value;
  };
  let backdrop = "";
  for (
    let node: Element | null = pseudo ? element : element.parentElement;
    node && !backdrop;
    node = node.parentElement
  ) {
    const color = getComputedStyle(node).backgroundColor;
    if (alphaOf(color) > 0) backdrop = color;
  }
  if (!backdrop) {
    // Nothing behind it paints: the page is Canvas.
    const probe = document.createElement("div");
    probe.style.backgroundColor = "Canvas";
    document.body.append(probe);
    backdrop = getComputedStyle(probe).backgroundColor;
    probe.remove();
  }
  const fill = style.backgroundColor;
  const painted = alphaOf(fill) > 0 && fill !== backdrop;
  return [
    ...properties.map((name) => `${name}=${style[name]}`),
    `fill=${painted ? fill : "none"}`,
  ].join(";");
}

/**
 * Puts the starting point of the keyboard at the beginning of the document and
 * leaves nothing focused. Chromium keeps the sequential focus navigation
 * starting point on the element that last had the focus (or was clicked),
 * also after blur(): the next Tab would go to the control after it, not to
 * the first one. A selection collapsed at the start of the body moves it.
 */
export function placeFocusStart(): void {
  const active = document.activeElement as HTMLElement | null;
  if (active && active !== document.body) active.blur();
  const selection = document.getSelection();
  if (selection) {
    const range = document.createRange();
    range.setStart(document.body, 0);
    range.collapse(true);
    selection.removeAllRanges();
    selection.addRange(range);
  }
  window.scrollTo(0, 0);
}
