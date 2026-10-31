import { type ElectronApplication, expect, test } from "@playwright/test";
import { cleanup, fixture, launch, rpc, setWindowSize } from "./helpers";

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("按键预览 says what a pad press sends while padmate is in front, then fades", async ({}, info) => {
  test.setTimeout(60000);
  const f = await fixture();
  let app: ElectronApplication | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    const page = opened.page;
    const line = page.getByTestId("key-preview");
    const hint = page.locator(".controller-hint");
    await expect(page.locator('button[data-callout="south"]')).toContainText(
      "撤销",
    );
    await expect(line).toHaveText("");
    await expect(line).toHaveAttribute("aria-live", "polite");
    await expect(hint).toHaveCount(1);
    // The line shows only while padmate's window is in front; launch()
    // brings it there, as the native capture test needs too.
    await expect
      .poll(() => page.evaluate(() => document.hasFocus()))
      .toBe(true);
    const focused = () =>
      page.evaluate(() => document.activeElement?.outerHTML.slice(0, 60));
    const before = await focused();

    // The fake pad is a standard one: south is printed S. The fixture's
    // south taps Ctrl+Z; padmate's own window only previews it.
    await rpc(f, "fake.press", { control: "south" });
    await rpc(f, "fake.release", { control: "south" });
    await expect(line).toHaveText("S → Ctrl+Z");
    // The control is drawn, not written (G6): a standard pad's face button
    // is its place among the four (ADR-0079 §7); S is read out only.
    await expect(line.locator(".control-icon")).toHaveAttribute(
      "data-kind",
      "art",
    );
    await expect(
      line.locator(
        '.control-icon svg[data-pad-icon="kenney/switch_buttons_down"]',
      ),
    ).toHaveCount(1);
    await expect(line.locator(".visually-hidden")).toHaveText("S");
    await expect(hint).toHaveCount(0);
    expect(await focused()).toBe(before);
    await page.screenshot({ path: info.outputPath("key-preview.png") });
    // The line sits where the hint was and covers no control label.
    const covered = () =>
      page.evaluate(() => {
        const box = document
          .querySelector('[data-testid="key-preview"]')
          ?.getBoundingClientRect();
        if (!box) return ["no line"];
        return [...document.querySelectorAll("button[data-callout]")]
          .filter((chip) => {
            const c = chip.getBoundingClientRect();
            return (
              c.left < box.right &&
              c.right > box.left &&
              c.top < box.bottom &&
              c.bottom > box.top
            );
          })
          .map((chip) => chip.getAttribute("data-callout"));
      });
    expect(await covered()).toEqual([]);

    // About 3 s later it fades and the hint comes back.
    await expect(line).toHaveText("", { timeout: 6000 });
    await expect(hint).toHaveCount(1);

    // In the smallest window, above the editor notice (a label-only key in
    // another key group gives the profile a warning; one in the edited
    // group is a key not set yet), it still covers no label.
    await setWindowSize(app, 850, 620);
    const current = (await rpc(f, "profiles.get", { id: "paint" })) as {
      text: string;
      revision: string;
    };
    const profile = JSON.parse(current.text);
    profile.layers.alt = { east: { label: "仅名称" } };
    await rpc(f, "profiles.put", {
      id: "paint",
      text: JSON.stringify(profile),
      revision: current.revision,
    });
    await expect(page.getByTestId("editor-notice")).toContainText(
      "配置有 1 个警告",
    );
    await rpc(f, "fake.press", { control: "south" });
    await rpc(f, "fake.release", { control: "south" });
    await expect(line).toHaveText("S → Ctrl+Z");
    await page.screenshot({ path: info.outputPath("key-preview-850.png") });
    expect(await covered()).toEqual([]);
    await expect(line).toHaveText("", { timeout: 6000 });

    // An unbound control sends nothing, so nothing shows.
    await rpc(f, "fake.press", { control: "north" });
    await rpc(f, "fake.release", { control: "north" });
    await page.waitForTimeout(500);
    await expect(line).toHaveText("");
  } finally {
    await cleanup(f, app);
  }
});
