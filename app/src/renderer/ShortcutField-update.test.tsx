// @vitest-environment jsdom
// PKG-10 with UI-24: Main opens the update dialog by itself (the automatic
// check, 30 seconds after the window starts). A shortcut capture is running
// at that moment? The dialog takes the focus, the field loses it, and a field
// that loses focus commits the chord it heard so far: a half-pressed chord
// would become the key's shortcut without the person doing anything. So an
// update dialog waits for the capture to end.
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it } from "vitest";
import type { ProfileBinding } from "../shared/generated/schema";
import { BindingFields } from "./BindingEditor";
import { fakeCapture } from "./capture-fixture";
import { editorAPIFixture } from "./editor-api-fixture";
import { MacroContext } from "./macro-context";
import { UpdateDialogs } from "./UpdateDialogs";
import { supportedUpdateState, updateApi } from "./update-api-fixture";

afterEach(cleanup);

function Window({ onUpdate }: { onUpdate: ReturnType<typeof updateApi> }) {
  const [value, setValue] = useState<ProfileBinding>({
    press: { type: "keys", keys: "F13" },
  });
  return (
    <MacroContext value={{ list: null, failing: new Set() }}>
      <BindingFields
        value={value}
        onChange={setValue}
        language="zh-CN"
        actions={[{ name: "cycleBrush" }]}
      />
      <output data-testid="draft">{JSON.stringify(value)}</output>
      <UpdateDialogs api={onUpdate.api} language="zh-CN" />
    </MacroContext>
  );
}
const draft = (): ProfileBinding =>
  JSON.parse(screen.getByTestId("draft").textContent ?? "{}");

const offer = {
  phase: "available",
  version: "1.0.1",
  dialog: "available",
} as const;

async function startCapture() {
  const api = editorAPIFixture();
  const capture = fakeCapture(api);
  const update = updateApi(supportedUpdateState);
  render(<Window onUpdate={update} />);
  await waitFor(() => expect(update.api.getUpdateState).toHaveBeenCalled());
  fireEvent.click(screen.getByRole("button", { name: "录入快捷键" }));
  await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(1));
  return { api, capture, update };
}

describe("an update dialog and a running shortcut capture", () => {
  it("do not meet: the dialog waits, and the half-pressed chord is not committed", async () => {
    const { capture, update } = await startCapture();
    // The person holds Ctrl, K is yet to come.
    await capture.emit({ active: true, chord: "Ctrl" });

    update.push(offer);
    // Give the dialog every chance to open.
    await new Promise((done) => setTimeout(done, 20));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(
      screen.getByRole("button", { name: "停止捕获" }),
      "the capture goes on",
    ).toBeTruthy();
    expect(draft().press).toEqual({ type: "keys", keys: "F13" });
  });

  it("opens when the capture has ended, with the chord the person made", async () => {
    const { capture, update } = await startCapture();
    update.push(offer);
    await capture.emit({ active: true, chord: "Ctrl+K" });
    expect(screen.queryByRole("dialog")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "停止捕获" }));
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "有新版本" })).toBeTruthy(),
    );
    expect(draft().press).toEqual({ type: "keys", keys: "Ctrl+K" });
  });

  it("opens when the capture ends by itself", async () => {
    const { capture, update } = await startCapture();
    update.push(offer);
    expect(screen.queryByRole("dialog")).toBeNull();
    await capture.emit({ active: false, reason: "timeout" });
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "有新版本" })).toBeTruthy(),
    );
  });

  it("opens at once when no capture runs", async () => {
    const update = updateApi({ ...supportedUpdateState, ...offer });
    render(<Window onUpdate={update} />);
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "有新版本" })).toBeTruthy(),
    );
  });
});
