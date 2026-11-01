// @vitest-environment jsdom
import { render } from "@testing-library/react";
import { useLayoutEffect } from "react";
import { describe, expect, it } from "vitest";
import { NameDialog } from "./ScriptsPage";

/**
 * Reads the dialog right after a commit, before any effect of it runs: what
 * the frame that opens it shows. A layout effect of a sibling rendered after
 * the dialog runs at that point.
 */
function FirstFrame({ open, seen }: { open: boolean; seen: string[] }) {
  useLayoutEffect(() => {
    if (!open) return;
    const dialog = document.querySelector("dialog");
    const submit = dialog?.querySelector<HTMLButtonElement>(
      'button[type="submit"]',
    );
    const field = dialog?.querySelector("input");
    seen.push(`${field?.value}|${submit?.disabled ? "disabled" : "enabled"}`);
  }, [open, seen]);
  return null;
}

function pane(open: boolean, seen: string[]) {
  return (
    <>
      <NameDialog
        open={open}
        title="新建脚本"
        confirm="新建"
        initial={open ? "宏 1" : ""}
        macros={[]}
        failure=""
        busy={false}
        language="zh-CN"
        onCancel={() => {}}
        onConfirm={() => {}}
      />
      <FirstFrame open={open} seen={seen} />
    </>
  );
}

describe("NameDialog", () => {
  it("opens with its name and an enabled confirm button in the first frame", () => {
    const seen: string[] = [];
    const view = render(pane(false, seen));
    view.rerender(pane(true, seen));
    expect(seen).toEqual(["宏 1|enabled"]);
  });
});
