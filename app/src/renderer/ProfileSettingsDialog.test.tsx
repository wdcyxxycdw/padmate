// @vitest-environment jsdom
import {
  act,
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

describe("基于: the profile this one is based on (PRO-19)", () => {
  const library = (extra: Partial<ProfileEntry>[] = []): ProfileEntry[] => [
    { id: "default", name: "default", default: true },
    { id: "paint", name: "Paint" },
    { id: "photo", name: "Photo" },
    { id: "sketch", name: "Sketch", extends: "paint" },
    ...extra.map((entry) => ({ id: "x", ...entry })),
  ];
  async function openWith(
    profiles: ProfileEntry[],
    profile: Profile = base,
    language: Language = "zh-CN",
  ) {
    const api = editorAPIFixture();
    api.listProfiles.mockResolvedValue(profiles);
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
  const select = () =>
    screen.getByRole("combobox", { name: "基于" }) as HTMLSelectElement;
  const options = () =>
    [...select().options].map((option) => [option.value, option.textContent]);

  it("offers none and the other profiles, the default first, not itself nor one based on it", async () => {
    await openWith(library());
    // The profile is paint: sketch is based on it, so it cannot be its parent.
    expect(options()).toEqual([
      ["", "无"],
      ["default", "默认"],
      ["photo", "Photo"],
    ]);
    expect(select().value).toBe("");
  });

  it("offers a profile made or renamed after the dialog's editor read the list", async () => {
    const { editor, api } = await openWith(library());
    expect(options().map(([value]) => value)).toEqual(["", "default", "photo"]);
    // A profile is copied on the 配置 page; another is renamed. This one is
    // not touched, so the editor sees only profile.changed.
    api.listProfiles.mockResolvedValue([
      ...library().map((entry) =>
        entry.id === "photo" ? { ...entry, name: "Photo 2" } : entry,
      ),
      { id: "copy", name: "Paint 2" },
    ]);
    await act(async () => {
      await editor.getState().profileChanged();
    });
    await waitFor(() =>
      expect(options()).toEqual([
        ["", "无"],
        ["default", "默认"],
        ["copy", "Paint 2"],
        ["photo", "Photo 2"],
      ]),
    );
  });

  it("is a field of its own, with no hint under it", async () => {
    await openWith(library());
    expect(
      screen.getAllByRole("heading").map((heading) => heading.textContent),
    ).toEqual(["配置设置", "按键判定"]);
    expect(screen.queryByText(/沿用/)).toBeNull();
  });

  it("shows the profile it is based on", async () => {
    await openWith(library(), { ...base, extends: "photo" });
    expect(select().value).toBe("photo");
  });

  it("keeps showing a parent the library does not offer", async () => {
    await openWith(library(), { ...base, extends: "gone" });
    expect(select().value).toBe("gone");
    expect(options()).toContainEqual(["gone", "gone"]);
  });

  it("bases the profile on the profile chosen, and saves it right after the name", async () => {
    const { api, editor } = await openWith(library());
    fireEvent.change(select(), { target: { value: "default" } });
    expect(draft(editor).extends).toBe("default");
    await waitFor(() => expect(api.putProfile).toHaveBeenCalledTimes(1));
    expect(Object.keys(saved(api))).toEqual([
      "schemaVersion",
      "name",
      "extends",
      "layers",
    ]);
    expect(saved(api).extends).toBe("default");
    // It is a step that can be taken back.
    expect(editor.getState().canUndo).toBe(true);
  });

  it("takes the base away with 无", async () => {
    const { api, editor } = await openWith(library(), {
      ...base,
      extends: "photo",
    });
    fireEvent.change(select(), { target: { value: "" } });
    expect(draft(editor)).not.toHaveProperty("extends");
    await waitFor(() => expect(api.putProfile).toHaveBeenCalledTimes(1));
    expect(saved(api)).not.toHaveProperty("extends");
  });

  it("is in English when the language is", async () => {
    await openWith(library(), base, "en");
    const field = screen.getByRole("combobox", { name: "Based on" });
    expect(
      [...(field as HTMLSelectElement).options].map(
        (option) => option.textContent,
      ),
    ).toEqual(["None", "Default", "Photo"]);
    expect(screen.queryByText(/follow the profile/)).toBeNull();
  });

  it("shows the timing it takes from its parent in the empty fields", async () => {
    const { editor } = await openWith(library(), {
      ...base,
      extends: "photo",
    });
    // The parent sets the long-press delay, not the multi-tap window.
    editor.setState({
      parent: {
        id: "photo",
        profile: { ...base, name: "Photo", timing: { holdMs: 333 } },
        origins: [],
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
    expect(screen.getByText("留空则沿用「Photo」（333 ms）。")).toBeTruthy();
    expect(
      (screen.getByRole("textbox", { name: "连击间隔" }) as HTMLInputElement)
        .placeholder,
    ).toBe("220");
    expect(screen.getByText("留空则使用全局设置（220 ms）。")).toBeTruthy();
  });
});
