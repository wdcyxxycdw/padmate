// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import {
  PAD_QUIET_MS,
  padChanged,
  padHoldsUndo,
  padSendsHere,
  padSettingKnown,
  typingTarget,
  undoCommand,
} from "./undo-shortcuts";

const key = (init: Partial<KeyboardEventInit> & { key: string }) =>
  new KeyboardEvent("keydown", init);

describe("undo shortcuts", () => {
  it("reads Ctrl+Z as undo and Ctrl+Y or Ctrl+Shift+Z as redo", () => {
    expect(undoCommand(key({ key: "z", ctrlKey: true }))).toBe("undo");
    expect(undoCommand(key({ key: "Z", ctrlKey: true, shiftKey: true }))).toBe(
      "redo",
    );
    expect(undoCommand(key({ key: "y", ctrlKey: true }))).toBe("redo");
    expect(undoCommand(key({ key: "z" }))).toBeNull();
    expect(undoCommand(key({ key: "z", ctrlKey: true, altKey: true }))).toBe(
      null,
    );
    expect(undoCommand(key({ key: "z", ctrlKey: true, metaKey: true }))).toBe(
      null,
    );
    expect(undoCommand(key({ key: "y", ctrlKey: true, shiftKey: true }))).toBe(
      null,
    );
    // An input method reports "Process": the key position decides.
    expect(
      undoCommand(key({ key: "Process", code: "KeyZ", ctrlKey: true })),
    ).toBe("undo");
    expect(
      undoCommand(key({ key: "Process", code: "KeyY", ctrlKey: true })),
    ).toBe("redo");
  });

  it("leaves the keys to fields that edit text", () => {
    const input = (type: string) => {
      const element = document.createElement("input");
      element.type = type;
      return element;
    };
    expect(typingTarget(input("text"))).toBe(true);
    expect(typingTarget(input("number"))).toBe(true);
    expect(typingTarget(document.createElement("textarea"))).toBe(true);
    const editable = document.createElement("div");
    editable.contentEditable = "true";
    // jsdom does not compute isContentEditable.
    Object.defineProperty(editable, "isContentEditable", { value: true });
    expect(typingTarget(editable)).toBe(true);
    expect(typingTarget(input("range"))).toBe(false);
    expect(typingTarget(input("checkbox"))).toBe(false);
    expect(typingTarget(document.createElement("button"))).toBe(false);
    expect(typingTarget(document.createElement("select"))).toBe(false);
    expect(typingTarget(null)).toBe(false);
  });

  it("knows when a pad may send keys to padmate's own window", () => {
    const pad = [{ info: { id: 1 } }];
    const state = (sendToOwnWindow: boolean, devices: unknown[], text = "") =>
      ({
        status: { sendToOwnWindow, devices },
        config: text ? { text, revision: "r1" } : null,
      }) as never;
    expect(padSendsHere(state(true, pad))).toBe(true);
    expect(padSendsHere(state(true, []))).toBe(false);
    expect(padSendsHere(state(false, pad))).toBe(false);
    // Just switched on: the setting is saved before the daemon says so.
    expect(
      padSendsHere(
        state(
          false,
          pad,
          '{"schemaVersion":0,"output":{"sendToOwnWindow":true}}',
        ),
      ),
    ).toBe(true);
    expect(padSendsHere({ status: null, config: null })).toBe(false);
  });

  it("notices pad buttons and pushed axes, not a resting stick", () => {
    const rest = { buttons: { south: false }, axes: { leftx: 600 } };
    expect(padChanged({ 1: rest }, { 1: rest })).toBe(false);
    expect(
      padChanged({ 1: rest }, { 1: { ...rest, buttons: { south: true } } }),
    ).toBe(true);
    // A release after a merged press is a change too.
    expect(
      padChanged({ 1: rest }, { 1: { ...rest, buttons: { south: false } } }),
    ).toBe(true);
    expect(
      padChanged({ 1: rest }, { 1: { ...rest, axes: { leftx: 1600 } } }),
    ).toBe(false);
    expect(
      padChanged({ 1: rest }, { 1: { ...rest, axes: { lefttrigger: 20000 } } }),
    ).toBe(true);
    expect(padChanged({}, { 2: rest })).toBe(true);
  });

  it("holds undo back for a pad only while its keys can reach this window", () => {
    const pad = [{ info: { id: 1 } }];
    const pushedPad = {
      1: { buttons: { east: true }, axes: { leftx: 20000 } },
    };
    const state = (
      sendToOwnWindow: boolean | undefined,
      config: string | null,
      inputs: Record<number, unknown> = {},
    ) =>
      ({
        status: { sendToOwnWindow, devices: pad },
        config: config === null ? null : { text: config, revision: "r1" },
        inputs,
      }) as never;
    const off = '{"schemaVersion":0}';
    const on = '{"schemaVersion":0,"output":{"sendToOwnWindow":true}}';
    // On: nothing undoes, whatever the pad did.
    expect(padHoldsUndo(state(true, off), -Infinity, 1e6)).toBe(true);
    expect(padHoldsUndo(state(false, on), -Infinity, 1e6)).toBe(true);
    // Known to be off: a pushed button, a stick off centre and a press a
    // moment ago hold nothing back.
    expect(padSettingKnown(state(false, off))).toBe(true);
    expect(padHoldsUndo(state(false, off, pushedPad), 1000, 1001)).toBe(false);
    // The daemon's answer alone is enough, and so is config.json's.
    expect(padHoldsUndo(state(false, null, pushedPad), 1000, 1001)).toBe(false);
    expect(padHoldsUndo(state(undefined, off, pushedPad), 1000, 1001)).toBe(
      false,
    );
    // Not known yet: a recent press or a pushed pad holds it back.
    expect(padSettingKnown(state(undefined, null))).toBe(false);
    expect(
      padHoldsUndo(state(undefined, null), 1000, 1000 + PAD_QUIET_MS - 1),
    ).toBe(true);
    expect(
      padHoldsUndo(state(undefined, null), 1000, 1000 + PAD_QUIET_MS),
    ).toBe(false);
    expect(padHoldsUndo(state(undefined, null, pushedPad), 0, 1e6)).toBe(true);
    expect(
      padHoldsUndo(
        state(undefined, null, { 1: { buttons: { east: false }, axes: {} } }),
        0,
        1e6,
      ),
    ).toBe(false);
  });
});
