import { describe, expect, it } from "vitest";
import { closeHides, quitsWhileHidden } from "./window-close";

const open = {
  quitting: false,
  leaving: false,
  phase: "connected" as const,
  pageReady: true,
};

describe("closing the main window (ADR-0077)", () => {
  it("hides the window while the daemon is connected, so the page and its model stay loaded", () => {
    expect(closeHides(open)).toBe(true);
  });

  it("closes it for real while the application quits or the daemon's exit waits for the saves", () => {
    expect(closeHides({ ...open, quitting: true })).toBe(false);
    // A quit held for the saves, the tray's 退出 or the installer's exit:
    // the window they close must close, or the quit is cancelled.
    expect(closeHides({ ...open, leaving: true })).toBe(false);
  });

  it("closes it for real with nothing to come back to: no connection, or no page", () => {
    for (const phase of ["connecting", "disconnected", "failed"] as const)
      expect(closeHides({ ...open, phase })).toBe(false);
    expect(closeHides({ ...open, phase: undefined })).toBe(false);
    expect(closeHides({ ...open, pageReady: false })).toBe(false);
  });
});

describe("a hidden window when the daemon goes away (ADR-0077)", () => {
  it("quits the application once the connection has ended", () => {
    expect(quitsWhileHidden(true, "disconnected")).toBe(true);
    expect(quitsWhileHidden(true, "failed")).toBe(true);
  });

  it("stays while connected or connecting, and a shown window never quits by itself", () => {
    expect(quitsWhileHidden(true, "connected")).toBe(false);
    expect(quitsWhileHidden(true, "connecting")).toBe(false);
    for (const phase of [
      "connecting",
      "connected",
      "disconnected",
      "failed",
    ] as const)
      expect(quitsWhileHidden(false, phase)).toBe(false);
  });
});
