// @vitest-environment jsdom
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import type { PadmateAPI, ProfileEntry } from "../shared/api";
import type { Profile } from "../shared/generated/schema";
import { editorAPIFixture } from "./editor-api-fixture";
import { createEditorStore, type EditorStore } from "./editor-store";
import { ProfileSettingsDialog } from "./ProfileSettingsDialog";
import type { Language } from "./store";

afterEach(cleanup);

const base: Profile = { schemaVersion: 1, name: "Paint", layers: { base: {} } };

/** An editor on a profile document, with the dialog open on it. */
async function open(
  profile: Profile = base,
  language: Language = "zh-CN",
): Promise<{ editor: EditorStore; api: ReturnType<typeof editorAPIFixture> }> {
  const api = editorAPIFixture();
  api.getProfile.mockImplementation(async (id: string) => ({
    id,
    text: JSON.stringify(profile),
    revision: "r1",
    diagnostics: [],
  }));
  const editor = createEditorStore(api as unknown as PadmateAPI);
  await editor
    .getState()
    .activate({ id: "paint", device: "pad", connection: "c" });
  await waitFor(() => expect(editor.getState().draft).not.toBeNull());
  render(
    <ProfileSettingsDialog
      open
      editor={editor}
      language={language}
      onClose={() => {}}
    />,
  );
  return { editor, api };
}

const draft = (editor: EditorStore) => editor.getState().draft as Profile;
const saved = (api: ReturnType<typeof editorAPIFixture>): Profile => {
  const call = api.putProfile.mock.calls.at(-1);
  return JSON.parse(call?.[1] ?? "{}");
};

describe("配置设置 holds the name and the timing only (UI-27, ADR-0067)", () => {
  it("has no feedback section and no switch: they are in the global settings", async () => {
    await open();
    expect(
      screen.queryByRole("heading", { name: /^(反馈|Feedback)$/ }),
    ).toBeNull();
    expect(screen.queryAllByRole("switch")).toHaveLength(0);
    for (const name of ["震动", "灯光", "暂停提示", "低电提醒"])
      expect(screen.queryByText(name)).toBeNull();
  });

  it("keeps 名称 and the timing rows as they were", async () => {
    await open();
    const headings = screen
      .getAllByRole("heading")
      .map((heading) => heading.textContent);
    expect(headings).toEqual(["配置设置", "按键判定"]);
    expect(screen.getByRole("textbox", { name: "名称" })).toBeTruthy();
    expect(screen.getByRole("textbox", { name: "触发长按时间" })).toBeTruthy();
    expect(screen.getByRole("textbox", { name: "连击间隔" })).toBeTruthy();
    // 高级 holds the two tap times.
    expect(screen.getByText("高级")).toBeTruthy();
    expect(screen.getByText("点按时长 · 点按间隔")).toBeTruthy();
  });

  it("is in English when the language is", async () => {
    await open(base, "en");
    expect(screen.queryByRole("heading", { name: "Feedback" })).toBeNull();
    expect(screen.queryAllByRole("switch")).toHaveLength(0);
    expect(screen.getByRole("textbox", { name: "Name" })).toBeTruthy();
    expect(
      screen.getByRole("textbox", { name: "Long-press delay" }),
    ).toBeTruthy();
  });

  it("does not write anything by itself, and shows no switch for a file that has them", async () => {
    const { api, editor } = await open({
      ...base,
      feedback: { rumble: false, led: false, strength: 100 },
    });
    expect(api.putProfile).not.toHaveBeenCalled();
    expect(draft(editor).feedback).toEqual({
      rumble: false,
      led: false,
      strength: 100,
    });
    expect(screen.queryAllByRole("switch")).toHaveLength(0);
  });

  it("leaves the retired switches out of the file when an edit saves it, and keeps the other members", async () => {
    const { api } = await open({
      ...base,
      feedback: {
        rumble: false,
        led: true,
        pause: false,
        lowBattery: false,
        strength: 12000,
        pausedColor: "#112233",
      },
    });
    const name = screen.getByRole("textbox", { name: "名称" });
    fireEvent.change(name, { target: { value: "Renamed" } });
    fireEvent.blur(name);
    await waitFor(() => expect(api.putProfile).toHaveBeenCalledTimes(1));
    expect(saved(api)).toMatchObject({
      name: "Renamed",
      feedback: { strength: 12000, pausedColor: "#112233" },
    });
    expect(saved(api).feedback).toEqual({
      strength: 12000,
      pausedColor: "#112233",
    });
  });

  it("keeps the name field's own name while it shows an error (UI-24)", async () => {
    await open(base);
    const name = screen.getByRole("textbox", { name: "名称" });
    fireEvent.change(name, { target: { value: "" } });
    const error = screen.getByText("名称不能为空");
    expect(error.getAttribute("role")).toBe("alert");
    expect(name.getAttribute("aria-describedby")).toBe(error.id);
    expect(error.closest("label")).toBeNull();
    expect(screen.getByRole("textbox", { name: "名称" })).toBe(name);
  });

  it("drops a feedback object that only held the retired switches when an edit saves it", async () => {
    const { api } = await open({
      ...base,
      feedback: { rumble: false, lowBattery: false },
    });
    const field = screen.getByRole("textbox", { name: "触发长按时间" });
    fireEvent.change(field, { target: { value: "300" } });
    fireEvent.blur(field);
    await waitFor(() => expect(api.putProfile).toHaveBeenCalledTimes(1));
    expect(saved(api)).not.toHaveProperty("feedback");
    expect(saved(api).timing).toEqual({ holdMs: 300 });
  });
});

describe("a file that has extends (PRO-19, ADR-0076)", () => {
  const library = (): ProfileEntry[] => [
    { id: "default", name: "default", default: true },
    { id: "paint", name: "Paint" },
    { id: "photo", name: "Photo" },
    { id: "sketch", name: "Sketch", extends: "paint" },
  ];
  async function openWith(
    profile: Profile = base,
    language: Language = "zh-CN",
  ) {
    const api = editorAPIFixture();
    api.listProfiles.mockResolvedValue(library());
    api.getProfile.mockImplementation(async (id: string) => ({
      id,
      text: JSON.stringify(profile),
      revision: "r1",
      diagnostics: [],
    }));
    const editor = createEditorStore(api as unknown as PadmateAPI);
    await editor
      .getState()
      .activate({ id: "paint", device: "pad", connection: "c" });
    await waitFor(() => expect(editor.getState().profiles).toHaveLength(4));
    render(
      <ProfileSettingsDialog
        open
        editor={editor}
        language={language}
        onClose={() => {}}
      />,
    );
    return { editor, api };
  }

  it("has no 基于 choice, whether the profile extends another or not", async () => {
    await openWith();
    expect(screen.queryByRole("combobox")).toBeNull();
    expect(screen.queryByText("基于")).toBeNull();
    cleanup();
    await openWith({ ...base, extends: "photo" });
    expect(screen.queryByRole("combobox")).toBeNull();
    expect(screen.queryByText("基于")).toBeNull();
    expect(screen.queryByText("Photo")).toBeNull();
    expect(
      screen.getAllByRole("heading").map((heading) => heading.textContent),
    ).toEqual(["配置设置", "按键判定"]);
  });

  it("has none in English either", async () => {
    await openWith({ ...base, extends: "photo" }, "en");
    expect(screen.queryByRole("combobox")).toBeNull();
    expect(screen.queryByText("Based on")).toBeNull();
  });

  it("keeps the extends member when an edit saves the file", async () => {
    const { api } = await openWith({
      schemaVersion: 1,
      name: "Paint",
      extends: "photo",
      layers: { base: {} },
    });
    const name = screen.getByRole("textbox", { name: "名称" });
    fireEvent.change(name, { target: { value: "Renamed" } });
    fireEvent.blur(name);
    await waitFor(() => expect(api.putProfile).toHaveBeenCalledTimes(1));
    expect(Object.keys(saved(api))).toEqual([
      "schemaVersion",
      "name",
      "extends",
      "layers",
    ]);
    expect(saved(api).extends).toBe("photo");
  });

  it("shows the timing it takes from the parent in the empty fields, without naming it", async () => {
    const { editor } = await openWith({ ...base, extends: "photo" });
    // The parent sets the long-press delay, not the multi-tap window.
    editor.setState({
      parent: {
        id: "photo",
        profile: { ...base, name: "Photo", timing: { holdMs: 333 } },
      },
    });
    await waitFor(() =>
      expect(
        (
          screen.getByRole("textbox", {
            name: "触发长按时间",
          }) as HTMLInputElement
        ).placeholder,
      ).toBe("333"),
    );
    expect(screen.getByText("留空则使用 333 ms。")).toBeTruthy();
    expect(screen.queryByText(/Photo/)).toBeNull();
    expect(
      (screen.getByRole("textbox", { name: "连击间隔" }) as HTMLInputElement)
        .placeholder,
    ).toBe("220");
    expect(screen.getByText("留空则使用全局设置（220 ms）。")).toBeTruthy();
  });

  it("says the same in English", async () => {
    const { editor } = await openWith({ ...base, extends: "photo" }, "en");
    editor.setState({
      parent: {
        id: "photo",
        profile: { ...base, name: "Photo", timing: { holdMs: 333 } },
      },
    });
    await waitFor(() =>
      expect(screen.getByText("Leave empty to use 333 ms.")).toBeTruthy(),
    );
  });
});
