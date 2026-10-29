import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { appIconHandlers } from "./app-icon-handlers";
import { appPathHandlers } from "./app-paths";
import { archiveHandlers } from "./archive-handlers";
import { daemonTopics, rawInputTopic } from "./bridge";
import { configRepairHandlers } from "./config-repair-handlers";
import { deviceHandlers } from "./device-handlers";
import { installedAppHandlers } from "./installed-apps";
import { libraryHandlers } from "./library-handlers";
import { macroHandlers } from "./macro-handlers";
import { startFailureHandlers } from "./start-failure";
import { startupHandlers } from "./startup-handlers";
import { systemSettingsHandlers } from "./system-settings";
import { updateHandlers } from "./update-handlers";

// The wiring between the daemon, the main process and the preload lives in
// files that need Electron or a daemon to run, so these checks read them.
const source = (path: string) =>
  readFileSync(new URL(path, import.meta.url), "utf8");

describe("Electron IPC wiring", () => {
  it("subscribes to every topic the daemon publishes", () => {
    const server = source("../../../daemon/internal/ipc/server.go");
    const list = /var Topics = \[\]string\{([^}]*)\}/.exec(server)?.[1];
    expect(list).toBeDefined();
    const published = [...(list ?? "").matchAll(/"([^"]+)"/g)].map((m) => m[1]);
    // input.raw (IN-17) is the daemon's last topic and the mapping wizard's
    // alone: the default subscription leaves it out (rawInputTopic).
    expect(published.at(-1)).toBe(rawInputTopic);
    expect([...daemonTopics]).toEqual(published.slice(0, -1));
    // subscribe (bridge.test.ts) asks for daemonTopics, hud included while
    // the overlay is up; each connection made subscribes through the one
    // HudSubscription (hud-subscription.test.ts, SCR-22).
    expect(source("./runtime.ts")).toContain(
      "await hud.open((method, params) => bridge.call(method, params));",
    );
    expect(source("./runtime.ts")).not.toContain("subscribe(");
  });

  it("asks for the hud topic only while the overlay is up, from its window being ready until it is given up, on each connection (SCR-22)", () => {
    const main = source("./index.ts");
    // The daemon tells scripts an overlay is there while somebody subscribes
    // to hud: not before the window is ready, not once it will not come back.
    expect(main.match(/new HudSubscription\(/g)).toHaveLength(1);
    expect(main).toContain("overlay.onReady(() => hud.overlayUp(true));");
    expect(main).toContain("overlay.onGaveUp(() => hud.overlayUp(false));");
    // The same object subscribes each connection made, learns when a
    // connection comes and goes, and when the app ends.
    expect(main).toMatch(/\n {6}hud,\n {4}\);/);
    expect(main).toMatch(
      /if \(state\.phase === "connected"\) hud\.connected\(\);\s*else hud\.disconnected\(\);/,
    );
    expect(main).toMatch(/overlay\?\.stop\(\);\s*hud\.stop\(\);/);
    // Its call is the manager's, which rejects with the disconnected error
    // that is not tried again.
    expect(main).toContain("Promise.reject(new Error(managerDisconnected))");
    // And nothing else subscribes on its own, with the topic or without.
    expect(main).not.toMatch(/\bsubscribe\(/);
  });

  it("handles daemon.exiting in Main, before the event buffer (DMN-22)", () => {
    // The overlay's toasts (HUD-04), key hints (HUD-03) and a script's
    // panels (SCR-22) see the same events, after the buffer. A script's
    // toast (the hud topic) is for the overlay alone: the page never gets it.
    expect(source("./index.ts")).toMatch(
      /createDaemonManager\(\s*routeDaemonEvents\(\{\s*deliver: \(event\) => \{(?:\s*\/\/[^\n]*)*\s*if \(event\.topic !== "hud"\) events\.push\(event\);\s*toasts\.handle\(event\);\s*hints\.handle\(event\);\s*panels\.handle\(event\);\s*\},/,
    );
    // And a lost connection ends what no state event will end.
    expect(source("./index.ts")).toMatch(
      /hints\.clear\(\);\s*panels\.clear\(\);/,
    );
  });

  it("saves before the UI quits for the tray, and tells the daemon (DMN-22, G9)", () => {
    const main = source("./index.ts");
    // The exit waits for the renderer through the close guard, answers the
    // daemon with ui.exitReady and quits only after that.
    expect(main).toMatch(/exitAfterSaves\(\{/);
    expect(main).toMatch(/closeGuard\.settle\(/);
    expect(main).toContain('manager?.call("ui.exitReady", {})');
    // A close of the window during the wait does not end it early, but the
    // UI's own quit closes it through the same handler (daemon-exit.test.ts).
    expect(main).toMatch(
      /holdsWindowClose\(\{ trayExiting, quitting \}, \(\) => closeGuard\.hold\(\)\)/,
    );
    // The daemon lets this call through the bridge.
    const bridge = source("../../../daemon/cmd/padmatectl/ui_bridge.go");
    expect(bridge).toContain('"ui.exitReady"');
    expect(bridge).toContain('"config.repairSeen"');
  });

  it("pushes the overlay's settings through the one handler that checks them (HUD-05)", () => {
    // Renderer -> preload -> the handler table, where only the main window's
    // page is trusted; Main -> the overlay's page is a separate, receive-only
    // channel (security.test.ts).
    expect(source("../preload/index.ts")).toContain(
      'setOverlayPrefs: (prefs) => invoke("padmate:overlayPrefs", prefs),',
    );
    expect(source("./index.ts")).toContain(
      "overlayPrefs: { count: 1, run: (prefs) => overlayPreferences.push(prefs) },",
    );
    expect(source("../shared/api.ts")).toContain(
      "setOverlayPrefs(prefs: OverlayPrefs): Promise<void>;",
    );
  });

  it("identifies a controller through one fixed method, from the row's key to devices.identify (IN-13)", () => {
    // Renderer -> preload -> Main's handler table, which checks the key ->
    // the daemon's devices.identify, which the UI bridge lets through.
    expect(source("../shared/api.ts")).toContain(
      "identifyDevice(key: string): Promise<DeviceIdentify>;",
    );
    expect(source("../preload/index.ts")).toContain(
      'identifyDevice: (key) => invoke("padmate:identifyDevice", key),',
    );
    expect(source("./device-handlers.ts")).toContain(
      'call("devices.identify", { device })',
    );
    expect(source("./device-handlers.ts")).toContain("validateDeviceKey(key)");
    expect(source("../../../daemon/cmd/padmatectl/ui_bridge.go")).toContain(
      '"devices.identify"',
    );
    // The daemon has the method the bridge lets through.
    expect(source("../../../daemon/cmd/padmated/ipc.go")).toContain(
      '"devices.identify": r.rpcIdentify',
    );
    // And the UI is not given the fake daemon's methods.
    expect(source("../../../daemon/cmd/padmatectl/ui_bridge.go")).not.toContain(
      "fake.effects",
    );
  });

  it("watches the raw events of one controller and saves its mapping through fixed methods (IN-17, IN-18, IN-19)", () => {
    // Renderer -> preload -> Main's handler table, which checks the selector
    // and the mapping -> the daemon's methods, which the UI bridge lets through.
    const api = source("../shared/api.ts");
    for (const signature of [
      "rawWatchStart(device: string): Promise<RawInfo>;",
      "rawWatchStop(device: string): Promise<void>;",
      "setMapping(device: string, mapping: string): Promise<MappingResult>;",
      "resetMapping(device: string): Promise<MappingResult>;",
    ])
      expect(api).toContain(signature);
    const preload = source("../preload/index.ts");
    for (const channel of [
      'rawWatchStart: (device) => invoke("padmate:rawWatchStart", device),',
      'rawWatchStop: (device) => invoke("padmate:rawWatchStop", device),',
      'invoke("padmate:mappingSet", device, mapping)',
      'resetMapping: (device) => invoke("padmate:mappingReset", device),',
    ])
      expect(preload).toContain(channel);
    const handlers = source("./device-handlers.ts");
    expect(handlers).toContain('"devices.setMapping"');
    expect(handlers).toContain('"devices.resetMapping"');
    expect(handlers).toContain("validateMapping(mapping)");
    expect(handlers).toContain("validateDeviceKey(device)");
    expect(source("./raw-watch.ts")).toContain('"devices.rawInfo"');
    const bridge = source("../../../daemon/cmd/padmatectl/ui_bridge.go");
    expect(bridge).toContain(
      '"devices.rawInfo", "devices.setMapping", "devices.resetMapping"',
    );
    // The daemon has the methods the bridge lets through.
    const daemon = source("../../../daemon/cmd/padmated/ipc_mapping.go");
    for (const method of [
      "devices.rawInfo",
      "devices.setMapping",
      "devices.resetMapping",
    ])
      expect(daemon).toContain(`methods["${method}"]`);
    // The input.raw topic is on the one subscription, wished up for the
    // length of a watch; the watch ends when the page does, whatever the way
    // it ends, and when the connection does.
    const main = source("./index.ts");
    expect(main.match(/new RawWatch\(/g)).toHaveLength(1);
    expect(main).toContain("{ up: (on) => hud.rawUp(on) }");
    expect(main).toMatch(
      /\.\.\.deviceHandlers\(\s*\(method, params\) => manager\?\.call\(method, params\),\s*rawWatch,\s*\),/,
    );
    for (const place of [
      /"render-process-gone", \(\) => \{\s*capture\.stop\("renderer-exit"\);\s*padCapture\.stop\(\);\s*rawWatch\.reset\(\);/,
      // A close that hides the window (ADR-0077) keeps the raw watch of the
      // wizard, which resumes once the window is back.
      /window\.on\("close", \(event\) => \{\s*capture\.stop\("close"\);\s*padCapture\.stop\(\);[\s\S]*?hideOnClose\(\);\s*return;\s*\}\s*rawWatch\.reset\(\);/,
      /"did-start-loading", \(\) => \{\s*capture\.stop\("reload"\);\s*padCapture\.stop\(\);\s*rawWatch\.reset\(\);/,
      /capture\.stop\("disconnected"\);\s*padCapture\.disconnected\(\);\s*rawWatch\.reset\(\);/,
      /padCapture\.disconnected\(\);\s*rawWatch\.reset\(\);\s*void exitAfterSaves/,
    ])
      expect(main).toMatch(place);
  });

  it("imports an AntiMicroX profile as data through one fixed method (PRO-21)", () => {
    // Main reads the chosen file; the daemon is given its bytes, never a path.
    const handlers = source("./archive-handlers.ts");
    expect(handlers).toContain('"profiles.importAmgp"');
    expect(handlers).toMatch(
      /"profiles\.importAmgp", \{\s*data,\s*fileName: \[\.\.\.basename\(path\)\]\.slice\(0, 128\)\.join\(""\),\s*\}/,
    );
    expect(source("../../../daemon/cmd/padmatectl/ui_bridge.go")).toContain(
      '"profiles.importAmgp"',
    );
    expect(source("../../../daemon/cmd/padmated/ipc_amgp.go")).toContain(
      'methods["profiles.importAmgp"]',
    );
    // The dialog offers the extensions the handler routes.
    expect(source("./index.ts")).toContain(
      'extensions: [archiveExtension, "amgp", "xml"]',
    );
    expect(handlers).toContain('new Set([".amgp", ".xml"])');
  });

  it("reads and clears the latency through two fixed methods, from the diagnostics page to latency.get and latency.reset (DMN-15)", () => {
    // Renderer -> preload -> Main's handler table, which checks the selector
    // -> the daemon's methods, which the UI bridge lets through.
    const api = source("../shared/api.ts");
    expect(api).toContain(
      "getLatency(device?: string): Promise<LatencyResult>;",
    );
    expect(api).toContain(
      "resetLatency(device?: string): Promise<{ reset: boolean }>;",
    );
    const preload = source("../preload/index.ts");
    expect(preload).toContain(
      'getLatency: (device) => invoke("padmate:latencyGet", device),',
    );
    expect(preload).toContain(
      'resetLatency: (device) => invoke("padmate:latencyReset", device),',
    );
    const handlers = source("./device-handlers.ts");
    expect(handlers).toContain('"latency.get"');
    expect(handlers).toContain('"latency.reset"');
    expect(handlers).toContain("validateDevice(device)");
    const bridge = source("../../../daemon/cmd/padmatectl/ui_bridge.go");
    expect(bridge).toContain('"latency.get", "latency.reset"');
    // The daemon has the methods the bridge lets through.
    const daemon = source("../../../daemon/cmd/padmated/ipc_latency.go");
    expect(daemon).toContain('methods["latency.get"]');
    expect(daemon).toContain('methods["latency.reset"]');
  });

  it("handles every channel the preload invokes", () => {
    const preload = source("../preload/index.ts");
    const main = source("./index.ts");
    // Handlers built outside index.ts, which spreads them into its table.
    const library = [
      ...Object.keys(libraryHandlers(() => undefined)),
      ...Object.keys(
        macroHandlers({
          call: () => undefined,
          chooseScript: async () => undefined,
          openEditor: async () => false,
        }),
      ),
      ...Object.keys(deviceHandlers(() => undefined)),
      ...Object.keys(startupHandlers(() => undefined)),
      ...Object.keys(
        updateHandlers({
          getState: () => {
            throw new Error("unused");
          },
          check: async () => {
            throw new Error("unused");
          },
          download: async () => {
            throw new Error("unused");
          },
          install: async () => {
            throw new Error("unused");
          },
          dismiss: async () => {
            throw new Error("unused");
          },
          setAutoCheck: async () => {
            throw new Error("unused");
          },
          openReleasePage: async () => {},
        }),
      ),
      ...Object.keys(
        installedAppHandlers({
          list: async () => [],
          chooseExe: async () => undefined,
        }),
      ),
      ...Object.keys(
        configRepairHandlers({ call: () => undefined, show: () => {} }),
      ),
      ...Object.keys(
        startFailureHandlers({
          state: () => undefined,
          open: async () => {},
          retry: () => undefined,
          quit: () => {},
        }),
      ),
      ...Object.keys(
        archiveHandlers({
          call: () => undefined,
          chooseExport: async () => undefined,
          chooseImport: async () => undefined,
          writeFile: async () => {},
        }),
      ),
      ...Object.keys(appPathHandlers(async () => "")),
      ...Object.keys(systemSettingsHandlers(async () => {})),
      ...Object.keys(
        appIconHandlers({
          thumbnail: async () => {
            throw new Error("unused");
          },
          fileIcon: async () => {
            throw new Error("unused");
          },
        }),
      ),
    ];
    const channels = [
      ...preload.matchAll(/invoke\(\s*"padmate:([A-Za-z]+)"/g),
    ].map((m) => m[1]);
    expect(channels.length).toBeGreaterThan(20);
    expect(main).toMatch(/ipcMain\.handle\(`padmate:\$\{name\}`/);
    expect(main).toContain("...libraryHandlers(");
    expect(main).toContain("...macroHandlers(");
    expect(main).toContain("...deviceHandlers(");
    expect(main).toContain("...startupHandlers(");
    expect(main).toContain("...systemSettingsHandlers(");
    expect(main).toContain("...updateHandlers(");
    expect(main).toContain("...startFailureHandlers(");
    expect(main).toContain("...archiveHandlers(");
    expect(main).toContain("...appIconHandlers(");
    expect(main).toContain("...appPathHandlers(");
    expect(main).toContain("...installedAppHandlers(");
    expect(main).toContain("...configRepairHandlers(");
    for (const channel of channels)
      expect(
        library.includes(channel) || new RegExp(`\\b${channel}:`).test(main),
        channel,
      ).toBe(true);
    for (const name of library) expect(channels).toContain(name);
  });

  it("reads a profile with its parents merged through a checked ID, read-only (PRO-19)", () => {
    const main = source("./index.ts");
    // The renderer supplies only the profile ID; the daemon method is Main's.
    expect(main).toMatch(
      /profileResolve: \{\s*count: 1,\s*run: \(id\) =>\s*manager\?\.call\("profiles\.resolve", \{ id: validateProfileID\(id\) \}\),/,
    );
    expect(source("../preload/index.ts")).toContain(
      'resolveProfile: (id) => invoke("padmate:profileResolve", id),',
    );
  });
});
