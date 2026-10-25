import { createHash } from "node:crypto";
import { join, resolve } from "node:path";

export interface RuntimeContext {
  packaged: boolean;
  root: string;
  resources: string;
  appData: string;
  env: NodeJS.ProcessEnv;
}

/**
 * The address an installed application in test mode (PKG-03) may take its
 * updates from (PKG-10): plain http on this machine, no credentials, query or
 * fragment. The test serves latest.yml and a stand-in installer there. Any
 * other text is no feed.
 */
export function loopbackFeed(text: string | undefined): string | undefined {
  if (!text || text.includes("?") || text.includes("#")) return undefined;
  let url: URL;
  try {
    url = new URL(text);
  } catch {
    return undefined;
  }
  if (
    url.protocol !== "http:" ||
    url.username ||
    url.password ||
    url.port === "0" ||
    !["127.0.0.1", "localhost", "[::1]"].includes(url.hostname)
  )
    return undefined;
  return url.href;
}

export function resolveRuntimeOptions(context: RuntimeContext) {
  const { packaged, root, resources, appData, env } = context;
  const cache = join(root, ".cache");
  // A test that launches the installed application (the Playwright spec
  // app/tests/e2e/packaged.spec.ts, PKG-03) names three private folders and
  // the flag; the application then keeps to them like a development run does,
  // with its bundled daemon and CLI, and starts that daemon with --dry-run
  // and never --inject. Any one of the four missing, it is the real application.
  const packagedTest =
    packaged &&
    env.PADMATE_UI_TEST_PACKAGED === "1" &&
    !!env.PADMATE_UI_DATA_DIR &&
    !!env.PADMATE_UI_IPC_DIR &&
    !!env.PADMATE_UI_USER_DATA;
  const isolated = !packaged || packagedTest;
  // The daemon of a packaged test may take its input over IPC, with one fake
  // controller, instead of SDL's: a test that looks at the controller view
  // needs a controller, whatever is plugged in.
  const fakeInput = packagedTest && env.PADMATE_UI_TEST_FAKE_INPUT === "1";
  const bin = packaged ? join(resources, "bin") : join(cache, "ui-bin");
  const developmentValue = (key: string, fallback: string): string =>
    !packaged && env[key] ? resolve(env[key] as string) : fallback;
  const isolatedValue = (key: string, fallback: string): string =>
    isolated && env[key] ? resolve(env[key] as string) : fallback;
  const ipcDir = isolatedValue(
    "PADMATE_UI_IPC_DIR",
    packaged
      ? join(env.LOCALAPPDATA || appData, "padmate", "run")
      : join(cache, "ui-run"),
  );
  const dataDir = isolatedValue(
    "PADMATE_UI_DATA_DIR",
    packaged ? join(appData, "padmate") : join(cache, "ui-data"),
  );
  const userData = isolatedValue(
    "PADMATE_UI_USER_DATA",
    packaged ? join(appData, "padmate", "ui") : join(cache, "ui-browser"),
  );
  // A packaged daemon in a test also gets user folders of its own, below the
  // browser data, so that nothing it writes (its log) reaches the real ones.
  const daemonHome = join(userData, "daemon-home");
  const localAppData = packagedTest
    ? join(daemonHome, "Local")
    : packaged
      ? env.LOCALAPPDATA
      : join(cache, "ui-local");
  const identity = createHash("sha256")
    .update(ipcDir.toLowerCase())
    .digest("hex")
    .slice(0, 16);
  const pipe = `\\\\.\\pipe\\padmate-ui-dev-${identity}`;
  const mutex = `Local\\padmate-ui-dev-${identity}`;
  const daemon = developmentValue(
    "PADMATE_UI_DAEMON",
    join(bin, "padmated.exe"),
  );
  const ctl = developmentValue("PADMATE_UI_CTL", join(bin, "padmatectl.exe"));
  // A release daemon has no linker flags (developmentLinkerFlags): the pipe,
  // and for a packaged test the discovery folder, come in the environment
  // (daemon/cmd/padmated/isolation.go).
  const childEnv = {
    ...env,
    ...(isolated ? { LOCALAPPDATA: localAppData, PADMATE_PIPE: pipe } : {}),
    ...(packagedTest
      ? { APPDATA: join(daemonHome, "Roaming"), PADMATE_IPC_DIR: ipcDir }
      : {}),
  };
  // Where a daemon this UI starts says why it did not start (UI-45): one
  // file per start, in the UI's own browser data, which tests isolate.
  const startupReports = join(userData, "startup-reports");
  const harness = !packaged && env.PADMATE_UI_TEST_HARNESS === "1";
  // The command of one start, whose report goes to report. A packaged UI
  // starts the daemon like a normal app, sending real keys (ADR-0052);
  // padmate's own window in front still only previews them (OUT-25).
  // Development builds and tests keep the dry run. --startup-report comes
  // first, so a usage error after it is reported too; the test harness, a
  // Go test executable that takes no padmated flags, reads it from its
  // environment.
  const daemonLaunch = (
    report: string,
  ): { args: string[]; env: NodeJS.ProcessEnv } => ({
    args: harness
      ? ["-test.run=^TestM10UIHarness$", "-test.v"]
      : [
          "--startup-report",
          report,
          packaged && !packagedTest ? "--inject" : "--dry-run",
          ...(fakeInput ? ["--fake-input=ipc"] : []),
          "--data-dir",
          dataDir,
        ],
    env: harness
      ? { ...childEnv, PADMATE_M10_STARTUP_REPORT: report }
      : childEnv,
  });
  // E2E runs record the files the scripts page opens instead of starting
  // an editor; a packaged UI always starts one.
  const editorLog = developmentValue("PADMATE_UI_EDITOR_LOG", "");
  // E2E runs list the programs of this JSON file in the 添加软件 dialog
  // (UI-46) instead of reading the real Start Menu; a packaged UI never.
  const appsFile = developmentValue("PADMATE_UI_APPS_FILE", "");
  // E2E runs that moved their window to the secondary display
  // (PADMATE_E2E_DISPLAY=secondary, tests/e2e/helpers.ts placeWindow) put
  // the overlay there too, so that no test covers the person's primary
  // display (ADR-0063). Only isolated runs: the installed application
  // ignores the variable.
  const e2eDisplay: "primary" | "secondary" =
    isolated && env.PADMATE_E2E_DISPLAY === "secondary"
      ? "secondary"
      : "primary";
  // The update feed is the test mode's alone: the real application reads
  // its updates from the repository of update-config.ts and nothing else.
  const updateFeed = packagedTest
    ? loopbackFeed(env.PADMATE_UI_TEST_UPDATE_URL)
    : undefined;
  return {
    packagedTest,
    updateFeed,
    bin,
    ipcDir,
    dataDir,
    userData,
    localAppData,
    pipe,
    mutex,
    daemon,
    ctl,
    daemonLaunch,
    startupReports,
    childEnv,
    editorLog,
    appsFile,
    e2eDisplay,
  };
}

// cmd/link parses quoted flag values itself after receiving the one argv
// element from go build; do not use a shell to assemble these strings.
export function developmentLinkerFlags(
  options: ReturnType<typeof resolveRuntimeOptions>,
): string {
  return [
    ["main.instanceName", options.mutex],
    ["main.ipcDir", options.ipcDir],
    ["main.ipcPipe", options.pipe],
  ]
    .map(([key, value]) => {
      if (value.includes("'"))
        throw new Error("Development path cannot contain a single quote");
      return `-X '${key}=${value}'`;
    })
    .join(" ");
}
