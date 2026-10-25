import { describe, expect, it, vi } from "vitest";
import { configRepairHandlers } from "./config-repair-handlers";
import type { DaemonCall } from "./library-handlers";
import { validateConfigBackup } from "./security";

describe("backup names of a repaired config.json (PRO-29)", () => {
  it.each([
    "config.broken-20261015-191600.json",
    "config.broken-20261015-191600-2.json",
    "config.broken-20261231-235959-12.json",
  ])("accepts %s", (name) => {
    expect(validateConfigBackup(name)).toBe(name);
  });

  it.each([
    "",
    "config.json",
    "config.broken-20261015-191600",
    "config.broken-2026101-191600.json",
    "config.broken-20261015-191600-.json",
    "config.broken-20261015-191600-2-3.json",
    "config.broken-20261015-191600.json.exe",
    "Config.broken-20261015-191600.json",
    "x\\config.broken-20261015-191600.json",
    "../config.broken-20261015-191600.json",
    "C:\\data\\config.broken-20261015-191600.json",
    "config.broken-20261015-191600.json\n",
    "config.broken-20261015-191600.json ",
    "config.broken-20261015-191600.json:stream",
    undefined,
    null,
    7,
    ["config.broken-20261015-191600.json"],
  ])("refuses %j: only a file name of that form, never a path", (value) => {
    expect(() => validateConfigBackup(value)).toThrow();
  });
});

describe("config repair handlers", () => {
  const backup = "config.broken-20261015-191600.json";
  function fixture(reply: unknown = { cleared: true }) {
    const call = vi.fn<DaemonCall>(async () => reply);
    const show = vi.fn<(name: string) => void>();
    return { call, show, handlers: configRepairHandlers({ call, show }) };
  }

  it("declares one argument each", () => {
    const { handlers } = fixture();
    expect(
      Object.fromEntries(
        Object.entries(handlers).map(([name, item]) => [name, item.count]),
      ),
    ).toEqual({ configRepairSeen: 1, configBackupShow: 1 });
  });

  it("tells the daemon the repair was shown, with only the backup's name", async () => {
    const { call, handlers } = fixture();
    await handlers.configRepairSeen.run(backup);
    expect(call).toHaveBeenCalledWith("config.repairSeen", { backup });
  });

  it("selects the backup by name, without asking the daemon", async () => {
    const { call, show, handlers } = fixture();
    await handlers.configBackupShow.run(backup);
    expect(show).toHaveBeenCalledWith(backup);
    expect(call).not.toHaveBeenCalled();
  });

  it("refuses a name that is not a backup before anything happens", async () => {
    const { call, show, handlers } = fixture();
    await expect(handlers.configRepairSeen.run("..\\x")).rejects.toThrow();
    await expect(
      handlers.configBackupShow.run("C:\\Windows\\a.json"),
    ).rejects.toThrow();
    expect(call).not.toHaveBeenCalled();
    expect(show).not.toHaveBeenCalled();
  });

  it("does not look like a success without a daemon", async () => {
    const handlers = configRepairHandlers({
      call: () => undefined,
      show: () => {},
    });
    await expect(handlers.configRepairSeen.run(backup)).rejects.toThrow(
      "Daemon is not connected",
    );
  });
});
