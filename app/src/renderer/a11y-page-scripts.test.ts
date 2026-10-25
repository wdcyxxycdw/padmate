// @vitest-environment jsdom
// UI-24: the functions the Electron pass of tests/e2e/a11y.spec.ts runs inside
// the page (tests/a11y/page-scripts.ts). That pass is not part of `task ci`, so
// what they decide is proved here, in jsdom, on small documents.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  computeSignature,
  placeFocusStart,
  signatureProperties,
} from "../../tests/a11y/page-scripts";

beforeEach(() => {
  window.scrollTo = vi.fn() as unknown as typeof window.scrollTo;
});
afterEach(() => {
  document.body.innerHTML = "";
  document.getSelection()?.removeAllRanges();
});

describe("placeFocusStart", () => {
  it("leaves nothing focused and the starting point at the start of the document", () => {
    document.body.innerHTML = `<button id="a">a</button><button id="b">b</button><button id="c">c</button>`;
    const middle = document.getElementById("b") as HTMLButtonElement;
    middle.focus();
    // The selection was left where the person clicked.
    document.getSelection()?.collapse(middle, 0);
    expect(document.activeElement).toBe(middle);

    placeFocusStart();

    expect(document.activeElement).toBe(document.body);
    const selection = document.getSelection();
    expect(selection?.rangeCount).toBe(1);
    expect(selection?.isCollapsed).toBe(true);
    expect(selection?.anchorNode).toBe(document.body);
    expect(selection?.anchorOffset).toBe(0);
    expect(window.scrollTo).toHaveBeenCalledWith(0, 0);
  });

  it("does not fail on a page with nothing focused or no selection", () => {
    document.body.innerHTML = "<p>nothing to focus</p>";
    expect(() => placeFocusStart()).not.toThrow();
    expect(document.getSelection()?.anchorNode).toBe(document.body);
  });
});

describe("computeSignature", () => {
  const sign = (element: Element) =>
    computeSignature(element, [signatureProperties, ""]);
  const page = (
    parentStyle: string,
    ownStyle: string,
  ): { parent: HTMLElement; own: HTMLElement } => {
    document.body.innerHTML = `<div id="parent" style="${parentStyle}"><button id="own" style="${ownStyle}">x</button></div>`;
    return {
      parent: document.getElementById("parent") as HTMLElement,
      own: document.getElementById("own") as HTMLElement,
    };
  };

  it("counts a fill that is not the backdrop's", () => {
    const { own } = page("background: rgb(10, 10, 10)", "background: #00f");
    expect(sign(own)).toContain("fill=rgb(0, 0, 255)");
  });

  it("does not count a fill that is the color behind it", () => {
    // Under a forced scheme both come out as Canvas: nothing can be seen.
    const { own } = page(
      "background: rgb(10, 10, 10)",
      "background: rgb(10, 10, 10)",
    );
    expect(sign(own)).toContain("fill=none");
  });

  it("does not count a transparent one", () => {
    const { own } = page(
      "background: rgb(10, 10, 10)",
      "background: transparent",
    );
    expect(sign(own)).toContain("fill=none");
  });

  it("finds the backdrop through ancestors that paint nothing", () => {
    document.body.innerHTML = `<section style="background: rgb(1, 2, 3)"><div><button id="own" style="background: rgb(1, 2, 3)">x</button></div></section>`;
    expect(sign(document.getElementById("own") as HTMLElement)).toContain(
      "fill=none",
    );
  });

  it("tells a filled state from an unfilled one when nothing else differs", () => {
    const { own } = page("background: rgb(10, 10, 10)", "background: #0af");
    const filled = sign(own);
    own.style.background = "transparent";
    expect(sign(own)).not.toBe(filled);
  });

  it("does not tell two states apart by their font weight alone", () => {
    const { own } = page("background: rgb(10, 10, 10)", "font-weight: 400");
    const light = sign(own);
    own.style.fontWeight = "600";
    expect(sign(own)).toBe(light);
    expect(signatureProperties).not.toContain("fontWeight");
  });

  it("does not tell two states apart by a fill that is the backdrop's color", () => {
    // The check that a state differs must not pass on the numbers of a color
    // that looks the same: a transparent option against one that fills with
    // the color of its parent.
    const a = page("background: rgb(9, 9, 9)", "background: transparent");
    const transparent = sign(a.own);
    a.own.style.background = "rgb(9, 9, 9)";
    expect(sign(a.own)).toBe(transparent);
  });

  it("tells states apart by an edge or an outline", () => {
    const { own } = page("background: rgb(9, 9, 9)", "");
    const plain = sign(own);
    own.style.border = "2px solid rgb(255, 255, 255)";
    const edged = sign(own);
    expect(edged).not.toBe(plain);
    own.style.border = "";
    // (jsdom does not expand the outline shorthand.)
    own.style.outlineStyle = "solid";
    own.style.outlineWidth = "2px";
    own.style.outlineColor = "rgb(0, 0, 255)";
    expect(sign(own)).not.toBe(plain);
  });

  it("works when nothing behind the element paints", () => {
    document.body.innerHTML = `<button id="own" style="background: rgb(5, 6, 7)">x</button>`;
    expect(() =>
      sign(document.getElementById("own") as HTMLElement),
    ).not.toThrow();
  });
});
