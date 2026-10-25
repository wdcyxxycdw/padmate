import type { ChildProcessWithoutNullStreams } from "node:child_process";
import type { DaemonEvent } from "../shared/api";

export type BridgeProcess = Pick<
  ChildProcessWithoutNullStreams,
  "stdin" | "stdout" | "stderr" | "on" | "kill"
>;
export interface BridgeInfo {
  pid: number;
  protocol: number;
}
interface Pending {
  resolve: (value: unknown) => void;
  reject: (error: Error) => void;
  timer: ReturnType<typeof setTimeout>;
}

// Every topic the daemon publishes (ipc.Topics in daemon/internal/ipc), all
// of which the UI subscribes to.
export const daemonTopics = [
  "input",
  "engine.activity",
  "log",
  "script.status",
  "profile.changed",
  "output.preview",
  "app.foreground",
  "daemon.exiting",
  "hud",
] as const satisfies readonly DaemonEvent["topic"][];
/**
 * The raw joystick events of the controllers the mapping wizard asked for with
 * devices.rawInfo (IN-17, ADR-0072). The one topic of ipc.Topics that is not in
 * daemonTopics: the wizard subscribes to it while it is open (a new subscribe
 * call replaces the list), and the default subscription, which a daemon from
 * before this topic must accept, leaves it out.
 */
export const rawInputTopic = "input.raw" satisfies DaemonEvent["topic"];
// "dropped" reports events the daemon or the bridge could not deliver.
const topics = new Set<string>([...daemonTopics, rawInputTopic, "dropped"]);
const maxFrame = 8 * 1024 * 1024;
const invalidParams = -32602;

/** What a call to a bridge that has ended says (the bridge's own, and the one it rejects its pending calls with). */
export const bridgeDisconnected = "Daemon bridge disconnected";
/** What a call to the manager says when it has no connection (daemon-manager.ts). */
export const managerDisconnected = "Daemon is disconnected";

/**
 * Whether a failed call failed for want of a connection, the two messages
 * above: there is nothing to try again on. Any other error is a daemon that is
 * there and did not answer (a timeout, too many calls pending).
 */
export function isDisconnectedError(error: unknown): boolean {
  return (
    error instanceof Error &&
    (error.message === bridgeDisconnected ||
      error.message === managerDisconnected)
  );
}

/**
 * Subscribes to daemonTopics; a new list replaces the one before. A daemon
 * from before SCR-22 refuses a list with hud, which it does not know: the UI
 * then goes without it and a script's toasts do not show. One from before
 * DMN-22 refuses daemon.exiting as well: without that too, and the tray's 退出
 * shows as a lost connection.
 *
 * With `hud: false` the list leaves out hud from the start. The daemon tells
 * a script that an overlay is there (host.has("hud")) while somebody
 * subscribes to hud, so the UI asks for it only while its overlay is up
 * (hud-subscription.ts).
 *
 * With `raw: true` every list ends with input.raw (IN-17): the mapping wizard
 * is open. A daemon that does not know it refuses all of them, and the wizard
 * cannot work with it anyway.
 */
export async function subscribe(
  call: (method: string, params: unknown) => Promise<unknown>,
  options: { hud?: boolean; raw?: boolean } = {},
): Promise<void> {
  const withHud = options.hud ?? true;
  const extra = options.raw ? [rawInputTopic] : [];
  const lists: string[][] = [];
  if (withHud) lists.push([...daemonTopics, ...extra]);
  const withoutHud = daemonTopics.filter((topic) => topic !== "hud");
  lists.push([...withoutHud, ...extra]);
  lists.push([
    ...withoutHud.filter((topic) => topic !== "daemon.exiting"),
    ...extra,
  ]);
  for (const [index, topics] of lists.entries()) {
    try {
      await call("subscribe", { topics });
      return;
    } catch (error) {
      if (
        (error as { code?: unknown }).code !== invalidParams ||
        index === lists.length - 1
      )
        throw error;
    }
  }
}

export class BridgeClient {
  readonly ready: Promise<BridgeInfo>;
  private readyResolve!: (info: BridgeInfo) => void;
  private readyReject!: (error: Error) => void;
  private readyTimer: ReturnType<typeof setTimeout>;
  private authenticated = false;
  private closed = false;
  private buffer = "";
  private nextID = 0;
  private pending = new Map<number, Pending>();
  private closeListeners = new Set<() => void>();

  constructor(
    private child: BridgeProcess,
    private notify: (event: DaemonEvent) => void,
    timeout = 1000,
    exitGrace = 1000,
  ) {
    this.ready = new Promise((resolve, reject) => {
      this.readyResolve = resolve;
      this.readyReject = reject;
    });
    this.readyTimer = setTimeout(
      () => this.fail(new Error("Daemon connection timed out")),
      timeout,
    );
    child.stdout.setEncoding("utf8");
    child.stdout.on("data", (chunk: string) => this.read(chunk));
    // stderr must be drained, but neither discovery tokens nor private details
    // from a subprocess should be sent into the renderer or app logs.
    child.stderr.resume();
    child.on("error", () =>
      this.fail(new Error("Could not start the authenticated daemon bridge")),
    );
    // The connection ends once the output is read: Node may report the
    // exit before the last lines, such as daemon.exiting (DMN-22). Output
    // that stays open after the exit ends it a moment later.
    child.on("close", () => this.fail(new Error(bridgeDisconnected)));
    child.on("exit", () => {
      setTimeout(() => this.fail(new Error(bridgeDisconnected)), exitGrace);
    });
    child.stdin.on("error", () => this.fail(new Error(bridgeDisconnected)));
  }

  onClose(listener: () => void): void {
    this.closeListeners.add(listener);
  }

  async call<T = unknown>(method: string, params: unknown = {}): Promise<T> {
    await this.ready;
    if (this.closed) throw new Error(bridgeDisconnected);
    if (this.pending.size >= 16)
      throw new Error("Too many pending daemon requests");
    const id = ++this.nextID;
    return new Promise<T>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error("Daemon request timed out"));
      }, 10000);
      this.pending.set(id, {
        resolve: (value) => resolve(value as T),
        reject,
        timer,
      });
      const frame = `${JSON.stringify({ id, method, params })}\n`;
      if (Buffer.byteLength(frame, "utf8") > maxFrame) {
        this.pending.delete(id);
        clearTimeout(timer);
        reject(new Error("Daemon request is too large"));
        return;
      }
      this.child.stdin.write(frame, (error) => {
        if (error) this.fail(new Error(bridgeDisconnected));
      });
    });
  }

  close(): void {
    this.fail(new Error(bridgeDisconnected));
  }

  private read(chunk: string): void {
    if (this.closed) return;
    this.buffer += chunk;
    let newline = this.buffer.indexOf("\n");
    while (newline >= 0) {
      const line = this.buffer.slice(0, newline);
      this.buffer = this.buffer.slice(newline + 1);
      if (Buffer.byteLength(line, "utf8") > maxFrame) {
        this.fail(new Error("Daemon bridge frame exceeded limits"));
        return;
      }
      try {
        this.frame(JSON.parse(line) as Record<string, unknown>);
      } catch {
        this.fail(new Error("Daemon bridge sent an invalid frame"));
        return;
      }
      if (this.closed) return;
      newline = this.buffer.indexOf("\n");
    }
    if (Buffer.byteLength(this.buffer, "utf8") > maxFrame)
      this.fail(new Error("Daemon bridge frame exceeded limits"));
  }

  private frame(frame: Record<string, unknown>): void {
    if (frame === null || Array.isArray(frame) || typeof frame !== "object")
      throw new Error("invalid frame");
    if (!this.authenticated) {
      if (
        frame.ready !== true ||
        !Number.isSafeInteger(frame.pid) ||
        (frame.pid as number) <= 0 ||
        frame.protocol !== 1
      )
        throw new Error("invalid ready");
      this.authenticated = true;
      clearTimeout(this.readyTimer);
      this.readyResolve({ pid: frame.pid as number, protocol: 1 });
      return;
    }
    if (typeof frame.topic === "string" && topics.has(frame.topic)) {
      this.notify({
        topic: frame.topic as DaemonEvent["topic"],
        data: frame.data,
      });
      return;
    }
    if (!Number.isSafeInteger(frame.id)) throw new Error("invalid response");
    const pending = this.pending.get(frame.id as number);
    // A timed-out request may still finish; ignore its late reply.
    if (!pending) return;
    this.pending.delete(frame.id as number);
    clearTimeout(pending.timer);
    if (frame.error && typeof frame.error === "object") {
      const rpcError = frame.error as {
        message?: unknown;
        code?: unknown;
        data?: unknown;
      };
      pending.reject(
        Object.assign(
          new Error(
            typeof rpcError.message === "string"
              ? rpcError.message
              : "Daemon request failed",
          ),
          { code: rpcError.code, data: rpcError.data },
        ),
      );
    } else {
      pending.resolve(frame.result);
    }
  }

  private fail(error: Error): void {
    if (this.closed) return;
    this.closed = true;
    clearTimeout(this.readyTimer);
    this.readyReject(error);
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timer);
      pending.reject(error);
    }
    this.pending.clear();
    // Only the CLI bridge belongs to this UI. The detached daemon is never killed.
    this.child.kill();
    for (const listener of this.closeListeners) listener();
  }
}
