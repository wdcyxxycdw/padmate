import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import type { ProfileEntry } from "../../../shared/api";
import { RenameProfileDialog } from "./ProfileDialogs";

const photo: ProfileEntry = { id: "photo", name: "Photo" };

/**
 * The first render of the dialog for a profile, before any effect has run:
 * what its first frame shows. An effect that fills the name in only after it
 * would leave 保存 disabled (drawn at 45 % opacity, which axe can measure as
 * too little contrast) and 名称不能为空 on screen for that frame.
 */
function firstFrame(entry: ProfileEntry | null) {
  return renderToStaticMarkup(
    <RenameProfileDialog
      entry={entry}
      onClose={() => {}}
      state={{ language: "zh-CN", profiles: [photo], renameProfile: vi.fn() }}
    />,
  );
}

describe("RenameProfileDialog", () => {
  it("opens with the profile's name and an enabled 保存 in its first frame", () => {
    const html = firstFrame(photo);
    expect(html).toContain('value="Photo"');
    expect(html).not.toContain("名称不能为空");
    const save = html.match(/<button[^>]*type="submit"[^>]*>/)?.[0] ?? "";
    expect(save).not.toBe("");
    expect(save).not.toContain("disabled");
  });
});
