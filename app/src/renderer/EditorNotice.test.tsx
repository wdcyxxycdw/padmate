// @vitest-environment jsdom
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { PadmateAPI, ProfileDiagnostic } from "../shared/api";
import type { Profile } from "../shared/generated/schema";
import { EditorNotice } from "./EditorNotice";
import { editorAPIFixture } from "./editor-api-fixture";
import { createEditorStore, type EditorStore } from "./editor-store";

afterEach(cleanup);

const paint: Profile = {
  schemaVersion: 1,
  name: "绘画",
  layers: {
    base: {
      south: { press: { type: "keys", keys: "Ctrl+Z" } },
      east: { press: { type: "keys", keys: "Space" } },
    },
  },
};

async function setup(profile: Profile = paint) {
  const api = editorAPIFixture();
  api.getProfile = vi.fn(async (id: string) => ({
    id,
    text: JSON.stringify(profile),
    revision: "r1",
    diagnostics: [],
  }));
  const editor = createEditorStore(api as unknown as PadmateAPI);
  await editor
    .getState()
    .activate({ id: "paint", device: "d", connection: "c" });
  return { api, editor };
}

function draft(editor: EditorStore) {
  const value = editor.getState().draft;
  if (!value) throw new Error("Expected a loaded draft");
  return value;
}

function notice(editor: EditorStore, onLocate = vi.fn()) {
  return render(
    <div className="controller-workspace">
      <button type="button" data-callout="north" aria-pressed="false">
        Y
      </button>
      <button type="button" data-callout="south" aria-pressed="true">
        A
      </button>
      <EditorNotice
        editor={editor}
        language="zh-CN"
        onLocate={onLocate}
        describe={(diagnostic) => `位置 ${diagnostic.pointer}`}
      />
    </div>,
  );
}

const nextFrame = () =>
  act(
    () =>
      new Promise<void>((resolve) => requestAnimationFrame(() => resolve())),
  );

async function rename(editor: EditorStore) {
  await act(async () => {
    editor.getState().edit({ ...draft(editor), name: "改名" });
    await editor.getState().flush();
  });
}

const diagnostics: ProfileDiagnostic[] = [
  {
    severity: "error",
    pointer: "/layers/base/south",
    message: "unknown key",
    line: 1,
    col: 1,
    offset: 1,
  },
  {
    severity: "warning",
    pointer: "/layers/base/east",
    message: "binding has no action",
    line: 2,
    col: 1,
    offset: 2,
  },
];

// The fixture's mocks are typed from their default values.
const rejected = { valid: false, diagnostics } as unknown as {
  valid: boolean;
  diagnostics: never[];
};

it("says nothing while edits save and apply", async () => {
  const { api, editor } = await setup();
  const { container } = notice(editor);
  await rename(editor);
  expect(api.putProfile).toHaveBeenCalledOnce();
  expect(container.querySelector(".editor-notice")).toBeNull();
  expect(screen.queryByRole("button", { name: /保存/ })).toBeNull();
});

it("shows a failed save with 重试", async () => {
  const { api, editor } = await setup();
  notice(editor);
  api.putProfile.mockRejectedValueOnce({ message: "write denied" });
  await rename(editor);
  const alert = screen.getByRole("alert");
  expect(alert.textContent).toBe("保存失败：write denied");
  expect(screen.getByTestId("editor-notice").dataset.state).toBe("error");
  const retry = screen.getByRole("button", { name: "重试" });
  retry.focus();
  fireEvent.click(retry);
  await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
  expect(api.putProfile).toHaveBeenCalledTimes(2);
  // The retry button went away: focus moves to the selected chip.
  await nextFrame();
  expect(document.activeElement?.textContent).toBe("A");
});

it("says why a saved profile is not in effect", async () => {
  const { api, editor } = await setup();
  notice(editor);
  api.putProfile.mockResolvedValueOnce({
    revision: "r2",
    applied: false,
    problem: "daemon reload failed",
  });
  await rename(editor);
  expect(screen.getByRole("status").textContent).toBe(
    "未生效：daemon reload failed",
  );
});

it("shows a load failure and loads again on 重试", async () => {
  const api = editorAPIFixture();
  api.getProfile = vi.fn(async () => {
    throw new Error("profile not found");
  });
  const editor = createEditorStore(api as unknown as PadmateAPI);
  await editor
    .getState()
    .activate({ id: "paint", device: "d", connection: "c" });
  notice(editor);
  expect(screen.getByRole("alert").textContent).toBe(
    "配置载入失败：profile not found",
  );
  api.getProfile = vi.fn(async (id: string) => ({
    id,
    text: JSON.stringify(paint),
    revision: "r1",
    diagnostics: [],
  }));
  fireEvent.click(screen.getByRole("button", { name: "重试" }));
  await waitFor(() => expect(editor.getState().draft?.name).toBe("绘画"));
  expect(screen.queryByRole("alert")).toBeNull();
});

it("reports a profile without a base layer", async () => {
  const api = editorAPIFixture();
  api.getProfile = vi.fn(async (id: string) => ({
    id,
    text: JSON.stringify({ schemaVersion: 0, name: "x", layers: {} }),
    revision: "r1",
    diagnostics: [],
  }));
  const editor = createEditorStore(api as unknown as PadmateAPI);
  await editor
    .getState()
    .activate({ id: "paint", device: "d", connection: "c" });
  notice(editor);
  expect(screen.getByRole("alert").textContent).toContain(
    "does not contain a base layer",
  );
});

it("lists why a change was not saved and tells errors from warnings", async () => {
  const { api, editor } = await setup();
  const onLocate = vi.fn();
  notice(editor, onLocate);
  api.validateProfile.mockResolvedValueOnce(rejected);
  await rename(editor);
  expect(api.putProfile).not.toHaveBeenCalled();
  expect(screen.getByRole("alert").textContent).toBe("修改未保存");
  // A non-modal list, opened from its toggle: an edit in progress is often
  // refused for a moment, and the drawer shows the key's own problems.
  expect(screen.queryByRole("region", { name: "校验结果" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "2 个问题" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  const list = screen.getByRole("region", { name: "校验结果" });
  const rows = screen.getAllByTestId("profile-diagnostic");
  expect(rows[0].textContent).toContain("错误：位置 /layers/base/south");
  expect(rows[0].dataset.severity).toBe("error");
  expect(rows[1].textContent).toContain("警告：位置 /layers/base/east");
  expect(rows[1].dataset.severity).toBe("warning");
  // The two kinds also differ in shape, not only in color.
  const shape = (row: HTMLElement) =>
    row.querySelector("svg path")?.getAttribute("d");
  expect(shape(rows[0])).not.toBe(shape(rows[1]));
  // Escape closes the list and returns to its toggle.
  fireEvent.keyDown(list, { key: "Escape" });
  expect(screen.queryByRole("region", { name: "校验结果" })).toBeNull();
  const toggle = screen.getByRole("button", { name: "2 个问题" });
  expect(document.activeElement).toBe(toggle);
  expect(toggle.getAttribute("aria-expanded")).toBe("false");
  fireEvent.click(toggle);
  fireEvent.click(screen.getAllByTestId("profile-diagnostic")[1]);
  expect(onLocate).toHaveBeenCalledWith(diagnostics[1]);
});

it("keeps focus on the toggle after locating a problem the editor cannot open", async () => {
  const { api, editor } = await setup();
  notice(editor);
  api.validateProfile.mockResolvedValueOnce(rejected);
  await rename(editor);
  fireEvent.click(screen.getByRole("button", { name: "2 个问题" }));
  const [row] = screen.getAllByTestId("profile-diagnostic");
  row.focus();
  fireEvent.click(row);
  expect(screen.queryByTestId("profile-diagnostic")).toBeNull();
  await nextFrame();
  expect(document.activeElement).toBe(
    screen.getByRole("button", { name: "2 个问题" }),
  );
});

it("lists a loaded file's warnings without opening the list", async () => {
  const api = editorAPIFixture();
  api.getProfile = vi.fn(async (id: string) => ({
    id,
    text: JSON.stringify(paint),
    revision: "r1",
    diagnostics: [diagnostics[1] as ProfileDiagnostic],
  }));
  const editor = createEditorStore(api as unknown as PadmateAPI);
  await editor
    .getState()
    .activate({ id: "paint", device: "d", connection: "c" });
  notice(editor);
  expect(screen.queryByRole("region", { name: "校验结果" })).toBeNull();
  // Saved and in effect: the line says what the toggle opens.
  expect(screen.getByRole("status").textContent).toBe("配置有 1 个警告");
  expect(
    screen
      .getByRole("button", { name: "1 个问题" })
      .getAttribute("aria-expanded"),
  ).toBe("false");
});

it("says once that an edit was lost when the profile changed", async () => {
  const { api, editor } = await setup();
  notice(editor);
  api.validateProfile.mockResolvedValueOnce(rejected);
  await rename(editor);
  await act(() =>
    editor.getState().activate({ id: "sketch", device: "d", connection: "c" }),
  );
  expect(screen.getByRole("status").textContent).toBe(
    "切换配置前的修改未能保存。",
  );
  fireEvent.click(screen.getByRole("button", { name: "关闭" }));
  expect(screen.queryByTestId("editor-notice")).toBeNull();
});

it("says once that the file changed elsewhere and was reloaded", async () => {
  const { api, editor } = await setup();
  notice(editor);
  api.putProfile.mockRejectedValueOnce({
    message: "revision conflict",
    code: -32009,
    data: { revision: "r9" },
  });
  api.getProfile.mockResolvedValueOnce({
    id: "paint",
    text: JSON.stringify({ ...paint, name: "别处" }),
    revision: "r9",
    diagnostics: [],
  });
  await rename(editor);
  expect(editor.getState().draft?.name).toBe("别处");
  expect(screen.getByRole("status").textContent).toBe(
    "配置文件已在别处修改，已重新载入。",
  );
  fireEvent.click(screen.getByRole("button", { name: "关闭" }));
  expect(screen.queryByTestId("editor-notice")).toBeNull();
});
