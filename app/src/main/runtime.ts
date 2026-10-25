import { spawn } from "node:child_process";
import { rmSync } from "node:fs";
import { mkdir } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { app } from "electron";
import type { ConnectionState, DaemonEvent } from "../shared/api";
import { BridgeClient } from "./bridge";
import { DaemonManager } from "./daemon-manager";
import type { HudSubscription } from "./hud-subscription";
import { resolveRuntimeOptions } from "./runtime-options";
import { type DaemonExit, readStartupReport } from "./start-failure";

export function runtimeOptions() {
  return resolveRuntimeOptions({
    packaged: app.isPackaged,
    root: resolve(__dirname, "../../.."),
    resources: process.resourcesPath,
    appData: app.getPath("appData"),
    env: process.env,
  });
}

let launches = 0;

/**
 * hud subscribes each connection made, so a daemon that is started again is
 * subscribed as the UI wants it then (SCR-22: with the hud topic while the
 * overlay is up).
 */
export async function createDaemonManager(
  event: (event: DaemonEvent) => void,
  changed: (state: ConnectionState) => void,
  hud: HudSubscription,
): Promise<DaemonManager> {
  const options = runtimeOptions();
  // A data folder that cannot be made is the daemon's to report (UI-45).
  await mkdir(options.dataDir, { recursive: true }).catch(() => {});
  return new DaemonManager({
    attempts: 6,
    wait: () => new Promise((done) => setTimeout(done, 250)),
    changed,
    connect: async () => {
      const child = spawn(
        options.ctl,
        [
          "ui-bridge",
          "--ipc-dir",
          options.ipcDir,
          "--ui-pid",
          String(process.pid),
          "--timeout",
          "1s",
        ],
        {
          windowsHide: true,
          shell: false,
          stdio: "pipe",
          env: options.childEnv,
        },
      );
      const bridge = new BridgeClient(child, event, 1000);
      try {
        const info = await bridge.ready;
        await hud.open((method, params) => bridge.call(method, params));
        return {
          info,
          call: bridge.call.bind(bridge),
          close: bridge.close.bind(bridge),
          onClose: bridge.onClose.bind(bridge),
        };
      } catch (error) {
        bridge.close();
        throw error;
      }
    },
    launch: () => {
      // UI-45: a report of its own for each start, read once the daemon
      // has exited; padmated also removes it first, and writes it only
      // when it does not start.
      const report = join(
        options.startupReports,
        `daemon-${process.pid}-${++launches}.json`,
      );
      rmSync(report, { force: true });
      const command = options.daemonLaunch(report);
      const child = spawn(options.daemon, command.args, {
        cwd: dirname(options.daemon),
        detached: true,
        windowsHide: true,
        shell: false,
        stdio: "ignore",
        env: command.env,
      });
      const exited = new Promise<DaemonExit>((done) => {
        child.once("error", () => done({ spawned: false }));
        child.once("exit", (code) => {
          if (code === 0) {
            done({ spawned: true, code });
            return;
          }
          void readStartupReport(report).then((parsed) =>
            done({
              spawned: true,
              code,
              ...(parsed ? { report: parsed } : {}),
            }),
          );
        });
      });
      // The daemon outlives this UI; its exit is only watched.
      child.unref();
      return { exited };
    },
  });
}
