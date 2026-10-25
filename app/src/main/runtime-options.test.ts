import { describe, expect, it } from "vitest";
import {
  developmentLinkerFlags,
  resolveRuntimeOptions,
} from "./runtime-options";

const context = {
  packaged: false,
  root: "D:\\Projects\\padmate workspace",
  resources: "C:\\Installed\\padmate\\resources",
  appData: "C:\\RealUser\\AppData\\Roaming",
  env: { LOCALAPPDATA: "C:\\RealUser\\AppData\\Local" },
};
const report = "D:\\reports\\daemon-1-1.json";

describe("isolated development runtime", () => {
  it("isolates discovery, daemon data, browser data, pipe and binaries", () => {
    const options = resolveRuntimeOptions(context);
    expect(options.ipcDir).toContain("padmate workspace\\.cache\\ui-run");
    expect(options.dataDir).toContain("padmate workspace\\.cache\\ui-data");
    expect(options.userData).toContain("padmate workspace\\.cache\\ui-browser");
    expect(options.daemon).toContain(".cache\\ui-bin\\padmated.exe");
    expect(options.childEnv.LOCALAPPDATA).toContain(".cache\\ui-local");
    expect(options.childEnv.PADMATE_PIPE).toBe(options.pipe);
    // UI-45: the report comes first, so a usage error after it is reported.
    expect(options.daemonLaunch(report).args).toEqual([
      "--startup-report",
      report,
      "--dry-run",
      "--data-dir",
      options.dataDir,
    ]);
    expect(options.daemonLaunch(report).env).toBe(options.childEnv);
    expect(options.startupReports).toBe(`${options.userData}\\startup-reports`);
    expect(JSON.stringify(options)).not.toContain("RealUser");
    // The scripts page starts a real editor unless a run records it.
    expect(options.editorLog).toBe("");
    // The 添加软件 list is the real Start Menu unless a run names a file.
    expect(options.appsFile).toBe("");
  });
  it("lists the programs of a file only in a development run that asks (UI-46)", () => {
    const env = { PADMATE_UI_APPS_FILE: "D:\\test space\\apps.json" };
    expect(resolveRuntimeOptions({ ...context, env }).appsFile).toBe(
      "D:\\test space\\apps.json",
    );
    expect(
      resolveRuntimeOptions({ ...context, packaged: true, env }).appsFile,
    ).toBe("");
  });
  it("puts the overlay on the secondary display only in an isolated run that asks (ADR-0063)", () => {
    const env = { PADMATE_E2E_DISPLAY: "secondary" };
    expect(resolveRuntimeOptions(context).e2eDisplay).toBe("primary");
    expect(resolveRuntimeOptions({ ...context, env }).e2eDisplay).toBe(
      "secondary",
    );
    // Any other value is the primary display.
    expect(
      resolveRuntimeOptions({
        ...context,
        env: { PADMATE_E2E_DISPLAY: "other" },
      }).e2eDisplay,
    ).toBe("primary");
    // The installed application ignores it, unless it is a test run with
    // private folders (the packaged spec moves its window too).
    expect(
      resolveRuntimeOptions({ ...context, packaged: true, env }).e2eDisplay,
    ).toBe("primary");
    expect(
      resolveRuntimeOptions({
        ...context,
        packaged: true,
        env: {
          ...env,
          PADMATE_UI_TEST_PACKAGED: "1",
          PADMATE_UI_DATA_DIR: "D:\\t\\data",
          PADMATE_UI_IPC_DIR: "D:\\t\\run",
          PADMATE_UI_USER_DATA: "D:\\t\\browser",
        },
      }).e2eDisplay,
    ).toBe("secondary");
  });
  it("records opened editors only in a development run that asks", () => {
    const env = { PADMATE_UI_EDITOR_LOG: "D:\\test space\\editor.log" };
    expect(resolveRuntimeOptions({ ...context, env }).editorLog).toBe(
      "D:\\test space\\editor.log",
    );
    expect(
      resolveRuntimeOptions({ ...context, packaged: true, env }).editorLog,
    ).toBe("");
  });
  it("keeps the dry run for the test harness, which reads the report from its environment", () => {
    const options = resolveRuntimeOptions({
      ...context,
      env: { PADMATE_UI_TEST_HARNESS: "1" },
    });
    const launch = options.daemonLaunch(report);
    // A Go test executable refuses padmated's flags.
    expect(launch.args).toEqual(["-test.run=^TestM10UIHarness$", "-test.v"]);
    expect(launch.env.PADMATE_M10_STARTUP_REPORT).toBe(report);
    expect(launch.env.PADMATE_PIPE).toBe(options.pipe);
    expect(options.childEnv).not.toHaveProperty("PADMATE_M10_STARTUP_REPORT");
  });
  it("passes explicit development endpoint and data consistently", () => {
    const options = resolveRuntimeOptions({
      ...context,
      env: {
        PADMATE_UI_IPC_DIR: "D:\\test space\\run",
        PADMATE_UI_DATA_DIR: "D:\\test space\\data",
        PADMATE_UI_USER_DATA: "D:\\test space\\browser",
      },
    });
    expect(options.ipcDir).toBe("D:\\test space\\run");
    expect(options.daemonLaunch(report).args).toContain("D:\\test space\\data");
    expect(options.userData).toBe("D:\\test space\\browser");
    expect(options.startupReports).toBe(
      "D:\\test space\\browser\\startup-reports",
    );
    expect(developmentLinkerFlags(options)).toContain(
      "-X 'main.ipcDir=D:\\test space\\run'",
    );
  });
  it("ignores development overrides in a packaged application", () => {
    const options = resolveRuntimeOptions({
      ...context,
      packaged: true,
      env: {
        ...context.env,
        PADMATE_UI_DAEMON: "D:\\untrusted.exe",
        PADMATE_UI_IPC_DIR: "D:\\fake",
        PADMATE_UI_TEST_HARNESS: "1",
      },
    });
    expect(options.daemon).toBe(
      "C:\\Installed\\padmate\\resources\\bin\\padmated.exe",
    );
    expect(options.ipcDir).toBe("C:\\RealUser\\AppData\\Local\\padmate\\run");
    // A packaged UI's daemon sends real keys (ADR-0052); the harness
    // variable does not turn it into a test binary.
    const launch = options.daemonLaunch(report);
    expect(launch.args).toEqual([
      "--startup-report",
      report,
      "--inject",
      "--data-dir",
      "C:\\RealUser\\AppData\\Roaming\\padmate",
    ]);
    expect(launch.env).not.toHaveProperty("PADMATE_M10_STARTUP_REPORT");
    expect(options.userData).toBe(
      "C:\\RealUser\\AppData\\Roaming\\padmate\\ui",
    );
    expect(options.startupReports).toBe(
      "C:\\RealUser\\AppData\\Roaming\\padmate\\ui\\startup-reports",
    );
  });
});
