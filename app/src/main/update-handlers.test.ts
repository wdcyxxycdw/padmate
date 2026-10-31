import { describe, expect, it, vi } from "vitest";
import { initialUpdateState } from "../shared/update";
import { type UpdateHandlerTarget, updateHandlers } from "./update-handlers";

function fixture() {
  const state = { ...initialUpdateState, supported: true };
  const target: UpdateHandlerTarget = {
    getState: vi.fn(() => state),
    check: vi.fn(async () => state),
    download: vi.fn(async () => state),
    install: vi.fn(async () => ({ dryRun: false })),
    dismiss: vi.fn(async () => state),
    setAutoCheck: vi.fn(async () => state),
    openManualDownload: vi.fn(async () => {}),
  };
  return { state, target, handlers: updateHandlers(target) };
}

describe("update handlers (PKG-10)", () => {
  it("declares the renderer argument counts", () => {
    const { handlers } = fixture();
    expect(
      Object.fromEntries(
        Object.entries(handlers).map(([name, item]) => [name, item.count]),
      ),
    ).toEqual({
      updateGet: 0,
      updateCheck: 0,
      updateDownload: 0,
      updateInstall: 0,
      updateDismiss: 1,
      updateAuto: 1,
      updateManualDownload: 0,
    });
  });

  it("reads and acts through the controller", async () => {
    const { state, target, handlers } = fixture();
    expect(handlers.updateGet.run()).toBe(state);
    await handlers.updateCheck.run();
    await handlers.updateDownload.run();
    await expect(handlers.updateInstall.run()).resolves.toEqual({
      dryRun: false,
    });
    await handlers.updateManualDownload.run();
    // A press of the button is a manual check.
    expect(target.check).toHaveBeenCalledExactlyOnceWith(true);
    expect(target.download).toHaveBeenCalledOnce();
    expect(target.install).toHaveBeenCalledOnce();
    expect(target.openManualDownload).toHaveBeenCalledWith();
  });

  it("passes the choice of the dialog and the switch, checked", async () => {
    const { target, handlers } = fixture();
    await handlers.updateDismiss.run("later");
    await handlers.updateDismiss.run("skip");
    await handlers.updateAuto.run(false);
    await handlers.updateAuto.run(true);
    expect(vi.mocked(target.dismiss).mock.calls).toEqual([["later"], ["skip"]]);
    expect(vi.mocked(target.setAutoCheck).mock.calls).toEqual([
      [false],
      [true],
    ]);
  });

  it.each(["", "Later", "never", 1, null, undefined, { choice: "later" }])(
    "refuses %j as a choice",
    (value) => {
      const { target, handlers } = fixture();
      expect(() => handlers.updateDismiss.run(value)).toThrow("choice");
      expect(target.dismiss).not.toHaveBeenCalled();
    },
  );

  it.each(["yes", 1, 0, null, undefined, "true"])(
    "refuses %j as the switch",
    (value) => {
      const { target, handlers } = fixture();
      expect(() => handlers.updateAuto.run(value)).toThrow("boolean");
      expect(target.setAutoCheck).not.toHaveBeenCalled();
    },
  );

  it("takes no address: the download site is opened without arguments", () => {
    const { handlers } = fixture();
    expect(handlers.updateManualDownload.count).toBe(0);
    expect(handlers.updateManualDownload.run.length).toBe(0);
  });
});
