// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { StrictMode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { PadmateAPI } from "../shared/api";
import { CardProfileSettings } from "./CardProfileSettings";
import { editorAPIFixture } from "./editor-api-fixture";
import { createEditorStore, type EditorStore } from "./editor-store";

afterEach(cleanup);

/** The card dialog for profile "sketch" while the mapping page holds "paint". */
function setup(strict: boolean) {
  const api = editorAPIFixture();
  api.getProfile.mockImplementation(async (id: string) => ({
    id,
    text: JSON.stringify({
      schemaVersion: 1,
      name: id === "sketch" ? "Sketch" : "Paint",
      layers: { base: {} },
    }),
    revision: `${id}-r1`,
    diagnostics: [],
  }));
  const mapping = createEditorStore(api as unknown as PadmateAPI);
  const editors: (EditorStore | null)[] = [];
  const onEditor = vi.fn((editor: EditorStore | null) => {
    editors.push(editor);
  });
  const dialog = (
    <CardProfileSettings
      id="sketch"
      mappingEditor={mapping}
      activeId="paint"
      api={api as unknown as PadmateAPI}
      globalTiming={{}}
      profileChanges={0}
      language="zh-CN"
      onEditor={onEditor}
      onClose={() => {}}
    />
  );
  render(strict ? <StrictMode>{dialog}</StrictMode> : dialog);
  return { api, editors, onEditor };
}

describe("CardProfileSettings with an editor of its own", () => {
  it("loads the profile and hands its editor to the shell", async () => {
    const { api, editors } = setup(false);
    const name = (await screen.findByRole("textbox", {
      name: "名称",
    })) as HTMLInputElement;
    await waitFor(() => expect(name.value).toBe("Sketch"));
    expect(api.getProfile).toHaveBeenCalledWith("sketch");
    expect(editors.at(-1)).not.toBeNull();
  });

  it("keeps the profile on screen when React replays the effects (StrictMode)", async () => {
    const { api, editors } = setup(true);
    const name = (await screen.findByRole("textbox", {
      name: "名称",
    })) as HTMLInputElement;
    await waitFor(() => expect(name.value).toBe("Sketch"));
    // The replay's cleanup lets go of its profile once the save settles; a
    // dialog that was activated only once would be emptied by it.
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(screen.getByRole("textbox", { name: "名称" })).toBeTruthy();
    const shown = editors.at(-1);
    expect(shown?.getState().draft).not.toBeNull();
    expect(api.getProfile).toHaveBeenCalledWith("sketch");
  });

  it("lets go of the profile when the dialog closes", async () => {
    const { editors } = setup(false);
    await screen.findByRole("textbox", { name: "名称" });
    const shown = editors.at(-1) as EditorStore;
    cleanup();
    await waitFor(() => expect(shown.getState().draft).toBeNull());
    expect(editors.at(-1)).toBeNull();
  });
});
