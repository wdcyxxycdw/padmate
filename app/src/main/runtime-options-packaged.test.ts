import { describe, expect, it } from "vitest";
import { resolveRuntimeOptions } from "./runtime-options";

const context = {
  root: "D:\\Projects\\padmate workspace",
  resources: "C:\\Installed\\padmate\\resources",
  appData: "C:\\RealUser\\AppData\\Roaming",
  env: { LOCALAPPDATA: "C:\\RealUser\\AppData\\Local" },
};
const report = "D:\\reports\\daemon-1-1.json";

// Set by a test that launches the installed application: a private data
// folder, discovery folder and browser data, the bundled daemon, no real keys.
const test = {
  PADMATE_UI_TEST_PACKAGED: "1",
  PADMATE_UI_DATA_DIR: "D:\\test space\\data",
  PADMATE_UI_IPC_DIR: "D:\\test space\\run",
  PADMATE_UI_USER_DATA: "D:\\test space\\browser",
};
const packaged = (env: NodeJS.ProcessEnv) =>
  resolveRuntimeOptions({
    ...context,
    packaged: true,
    env: { ...context.env, ...env },
  });

describe("packaged application in test mode (PKG-03)", () => {
  it("uses the private folders, a private pipe and a dry-run daemon", () => {
    const options = packaged(test);
    expect(options.packagedTest).toBe(true);
    expect(options.dataDir).toBe("D:\\test space\\data");
    expect(options.ipcDir).toBe("D:\\test space\\run");
    expect(options.userData).toBe("D:\\test space\\browser");
    expect(options.startupReports).toBe(
      "D:\\test space\\browser\\startup-reports",
    );
    // The bundled daemon and CLI, not the ones a variable names.
    expect(options.daemon).toBe(
      "C:\\Installed\\padmate\\resources\\bin\\padmated.exe",
    );
    expect(options.ctl).toBe(
      "C:\\Installed\\padmate\\resources\\bin\\padmatectl.exe",
    );
    const launch = options.daemonLaunch(report);
    expect(launch.args).toEqual([
      "--startup-report",
      report,
      "--dry-run",
      "--data-dir",
      "D:\\test space\\data",
    ]);
    expect(launch.args).not.toContain("--inject");
    // A release daemon takes its isolation from the environment: its own
    // pipe and discovery folder, and user folders of its own for the log.
    expect(launch.env.PADMATE_PIPE).toBe(options.pipe);
    expect(options.pipe).toMatch(/^\\\\\.\\pipe\\padmate-ui-dev-[0-9a-f]{16}$/);
    expect(launch.env.PADMATE_IPC_DIR).toBe("D:\\test space\\run");
    expect(launch.env.LOCALAPPDATA).toBe(
      "D:\\test space\\browser\\daemon-home\\Local",
    );
    expect(launch.env.APPDATA).toBe(
      "D:\\test space\\browser\\daemon-home\\Roaming",
    );
    expect(JSON.stringify(launch)).not.toContain("RealUser");
    expect(options.childEnv).toBe(launch.env);
  });

  it("takes fake input over IPC, still a dry run, when the test asks for one controller", () => {
    const args = packaged({
      ...test,
      PADMATE_UI_TEST_FAKE_INPUT: "1",
    }).daemonLaunch(report).args;
    expect(args).toEqual([
      "--startup-report",
      report,
      "--dry-run",
      "--fake-input=ipc",
      "--data-dir",
      "D:\\test space\\data",
    ]);
    // Only in the test mode, and only when it is asked for.
    expect(packaged(test).daemonLaunch(report).args).not.toContain(
      "--fake-input=ipc",
    );
    const real = packaged({ PADMATE_UI_TEST_FAKE_INPUT: "1" });
    expect(real.daemonLaunch(report).args).toContain("--inject");
    expect(real.daemonLaunch(report).args).not.toContain("--fake-input=ipc");
  });

  it("is off unless the flag and all three folders are set", () => {
    for (const missing of Object.keys(test)) {
      const partial: Record<string, string | undefined> = { ...test };
      delete partial[missing];
      const options = packaged(partial);
      expect(options.packagedTest, missing).toBe(false);
      // The real application: real folders, real keys.
      expect(options.dataDir).toBe("C:\\RealUser\\AppData\\Roaming\\padmate");
      expect(options.ipcDir).toBe("C:\\RealUser\\AppData\\Local\\padmate\\run");
      expect(options.daemonLaunch(report).args).toContain("--inject");
      expect(options.childEnv).not.toHaveProperty("PADMATE_PIPE");
    }
    for (const flag of ["0", "true", ""]) {
      expect(
        packaged({ ...test, PADMATE_UI_TEST_PACKAGED: flag }).packagedTest,
        flag,
      ).toBe(false);
    }
  });

  it("does not honor the other development overrides", () => {
    const options = packaged({
      ...test,
      PADMATE_UI_DAEMON: "D:\\untrusted\\padmated.exe",
      PADMATE_UI_CTL: "D:\\untrusted\\padmatectl.exe",
      PADMATE_UI_TEST_HARNESS: "1",
      PADMATE_UI_EDITOR_LOG: "D:\\editor.log",
      PADMATE_UI_APPS_FILE: "D:\\apps.json",
    });
    expect(options.daemon).toContain("Installed");
    expect(options.ctl).toContain("Installed");
    expect(options.daemonLaunch(report).args).not.toContain(
      "-test.run=^TestM10UIHarness$",
    );
    expect(options.editorLog).toBe("");
    expect(options.appsFile).toBe("");
  });

  it("is not a mode of a development run: the flag does nothing outside a packaged app", () => {
    const options = resolveRuntimeOptions({
      ...context,
      packaged: false,
      env: { ...context.env, ...test },
    });
    expect(options.packagedTest).toBe(false);
    expect(options.daemonLaunch(report).env).not.toHaveProperty(
      "PADMATE_IPC_DIR",
    );
  });
});

describe("the update feed of the packaged test mode (PKG-10)", () => {
  const feed = (url: string | undefined, env: NodeJS.ProcessEnv = test) =>
    packaged({
      ...env,
      ...(url === undefined ? {} : { PADMATE_UI_TEST_UPDATE_URL: url }),
    }).updateFeed;

  it("is the loopback address a test names, with a path of its own allowed", () => {
    expect(feed("http://127.0.0.1:53211/")).toBe("http://127.0.0.1:53211/");
    expect(feed("http://localhost:8080/")).toBe("http://localhost:8080/");
    expect(feed("http://[::1]:9000/")).toBe("http://[::1]:9000/");
    expect(feed("http://127.0.0.1:53211/updates/")).toBe(
      "http://127.0.0.1:53211/updates/",
    );
    // A trailing slash is added: the updater resolves file names against it.
    expect(feed("http://127.0.0.1:53211")).toBe("http://127.0.0.1:53211/");
  });

  it("is none for the real application, whatever the variable says", () => {
    const real = { PADMATE_UI_TEST_UPDATE_URL: "http://127.0.0.1:53211/" };
    expect(packaged(real).updateFeed).toBeUndefined();
    expect(packaged(real).packagedTest).toBe(false);
  });

  it("is none when one of the three folders or the flag is missing", () => {
    for (const missing of Object.keys(test)) {
      const partial: Record<string, string | undefined> = { ...test };
      delete partial[missing];
      expect(feed("http://127.0.0.1:53211/", partial), missing).toBeUndefined();
    }
  });

  it("is none for an address that is not a plain http one on this machine", () => {
    for (const url of [
      "",
      "not a url",
      "https://127.0.0.1:53211/",
      "http://example.com/",
      "http://127.0.0.2:53211/",
      "http://192.168.0.5:80/",
      "http://0.0.0.0:80/",
      "http://localhost.evil.test:80/",
      "http://127.0.0.1.evil.test/",
      "http://user:pass@127.0.0.1:53211/",
      "http://127.0.0.1:53211/?token=1",
      "http://127.0.0.1:53211/#x",
      "ftp://127.0.0.1/",
      "file:///C:/updates/",
      "http://[::2]:80/",
      "http://127.0.0.1:0/",
    ])
      expect(feed(url), url).toBeUndefined();
  });

  it("is none in a development run", () => {
    const options = resolveRuntimeOptions({
      ...context,
      packaged: false,
      env: {
        ...context.env,
        ...test,
        PADMATE_UI_TEST_UPDATE_URL: "http://127.0.0.1:53211/",
      },
    });
    expect(options.updateFeed).toBeUndefined();
  });
});
