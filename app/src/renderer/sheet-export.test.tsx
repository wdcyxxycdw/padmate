// @vitest-environment jsdom
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import type { PadmateAPI } from "../shared/api";
import type { Profile } from "../shared/generated/schema";
import { editorAPIFixture } from "./editor-api-fixture";
import { createEditorStore } from "./editor-store";
import { useSheetExport } from "./sheet-export";

afterEach(cleanup);

// The shortcut table of a profile that is based on another lists what it takes
// from that one (PRO-19, OUT-18).

const parent: Profile = {
  schemaVersion: 1,
  name: "Paint",
  layers: {
    base: {
      south: { label: "Undo", tap: { type: "keys", keys: "Ctrl+Z" } },
      east: { label: "Redo", tap: { type: "keys", keys: "Ctrl+Y" } },
    },
  },
};
const child: Profile = {
  schemaVersion: 1,
  name: "Sketch",
  extends: "paint",
  layers: {
    base: { east: { label: "Fill", tap: { type: "keys", keys: "F13" } } },
  },
};

/** The Markdown the child's table export sends, with parentFile as its parent. */
async function exportedTable(parentFile: Profile): Promise<string> {
  const api = editorAPIFixture();
  api.getProfile.mockImplementation(async (id: string) => ({
    id,
    text: JSON.stringify(child),
    revision: "r1",
    diagnostics: [],
  }));
  api.resolveProfile.mockImplementation(async (id: string) => ({
    id,
    text: JSON.stringify(parentFile),
    revision: "r1",
    inherited: [],
    diagnostics: [],
  }));
  const editor = createEditorStore(api as unknown as PadmateAPI);
  await editor
    .getState()
    .activate({ id: "sketch", device: "d", connection: "c" });
  await waitFor(() => expect(editor.getState().parent).not.toBeNull());
  function Probe() {
    const sheet = useSheetExport({
      state: {
        api: api as unknown as PadmateAPI,
        status: null,
        selectedDevice: 0,
      },
      editor,
      language: "en",
      onMessage: () => {},
    });
    return (
      <>
        <button type="button" onClick={() => sheet.setFormat("markdown")}>
          markdown
        </button>
        <button type="button" onClick={() => void sheet.exportSheet()}>
          export
        </button>
      </>
    );
  }
  render(<Probe />);
  fireEvent.click(screen.getByRole("button", { name: "markdown" }));
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "export" }));
  });
  await waitFor(() => expect(api.exportCheatSheet).toHaveBeenCalled());
  const call = (api.exportCheatSheet.mock.calls as unknown[][])[0];
  expect(call?.[0]).toBe("markdown");
  return String(call?.[1] ?? "");
}

it("lists the shortcuts the profile inherits, and its own in place of the parent's", async () => {
  const content = await exportedTable(parent);
  expect(content).toContain("Ctrl+Z");
  // The profile's own east replaces the parent's.
  expect(content).toContain("F13");
  expect(content).not.toContain("Ctrl+Y");
});

it("counts the shared (common) settings the profile inherits among those it does not list", async () => {
  const content = await exportedTable({
    ...parent,
    common: { west: { tap: { type: "keys", keys: "W" } } },
  });
  expect(content).toContain("The profile file has 1 more setting");
});
