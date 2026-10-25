import type { ConnectionState, StartFailure } from "../shared/api";
import { type BridgeInfo, managerDisconnected } from "./bridge";
import { type DaemonExit, exitFailure, failedStart } from "./start-failure";

export interface DaemonConnection {
  info: BridgeInfo;
  call<T = unknown>(method: string, params?: unknown): Promise<T>;
  close(): void;
  onClose(listener: () => void): void;
}

/** A daemon this UI started. */
export interface LaunchedDaemon {
  /** Settles when it exits, with its report; never while it runs. */
  exited: Promise<DaemonExit>;
}

interface ManagerOptions {
  connect: () => Promise<DaemonConnection>;
  /** Starts the daemon; throws when it cannot be run at all. */
  launch: () => LaunchedDaemon;
  wait: () => Promise<void>;
  attempts: number;
  changed?: (state: ConnectionState) => void;
}

/**
 * The UI's connection to the daemon. connect tries the running daemon
 * first and starts one when there is none. A start that does not lead to
 * a connection ends "failed" (UI-45): the daemon exited, with its report
 * or without, could not be run, or never answered. A connection that ends
 * later is "disconnected", unless the daemon said it is exiting (DMN-22).
 */
export class DaemonManager {
  state: ConnectionState = { phase: "connecting" };
  private connection?: DaemonConnection;
  private inFlight?: Promise<void>;
  private closed = false;
  // The daemon is stopping for good (DMN-22): calls still go through
  // until the connection ends, so the UI can save its last edits.
  private leaving = false;
  private launched = false;
  private connectedOnce = false;
  // Counts the attempts: a daemon's late exit only describes the attempt
  // that started it, while nothing came after it.
  private generation = 0;

  constructor(private options: ManagerOptions) {}

  connect(): Promise<void> {
    if (this.closed || this.connection) return Promise.resolve();
    if (this.inFlight) return this.inFlight;
    this.inFlight = this.attempt().finally(() => {
      this.inFlight = undefined;
    });
    return this.inFlight;
  }

  async call<T>(method: string, params?: unknown): Promise<T> {
    if (!this.connection || (this.closed && !this.leaving))
      throw new Error(managerDisconnected);
    return this.connection.call<T>(method, params);
  }

  close(): void {
    this.closed = true;
    this.leaving = false;
    this.connection?.close();
    this.connection = undefined;
    this.update({ phase: "disconnected" });
  }

  /**
   * The daemon is stopping for good (DMN-22, the tray's 退出 or the
   * installer's exit, PKG-04): the end of its connection is no loss to
   * show, and nothing connects again. Calls still go through while the
   * connection lasts, for the last saves and the answer ui.exitReady.
   */
  exiting(): void {
    this.closed = true;
    this.leaving = true;
  }

  private update(state: ConnectionState): void {
    this.state = state;
    this.options.changed?.(state);
  }

  private fail(failure: StartFailure): void {
    this.update({ phase: "failed", failure, startedByUI: this.launched });
  }

  private async attempt(): Promise<void> {
    const generation = ++this.generation;
    // A retry keeps showing why the last start failed until it is decided.
    const failure =
      this.state.phase === "failed" ? this.state.failure : undefined;
    this.update({ phase: "connecting", ...(failure ? { failure } : {}) });
    let daemon: LaunchedDaemon | undefined;
    let exit: DaemonExit | undefined;
    let message = "Daemon is unavailable";
    for (let i = 0; i < this.options.attempts && !this.closed; i++) {
      try {
        const connection = await this.options.connect();
        if (this.closed) {
          connection.close();
          return;
        }
        this.connection = connection;
        this.connectedOnce = true;
        connection.onClose(() => {
          if (this.connection !== connection) return;
          this.connection = undefined;
          if (!this.closed)
            this.update({
              phase: "disconnected",
              message: "Daemon connection was lost",
              startedByUI: this.launched,
            });
        });
        this.update({
          phase: "connected",
          ...connection.info,
          startedByUI: this.launched,
        });
        return;
      } catch (error) {
        message =
          error instanceof Error ? error.message : "Daemon is unavailable";
        if (this.closed) return;
        if (!daemon) {
          this.launched = true;
          try {
            daemon = this.options.launch();
          } catch {
            exit = { spawned: false };
            break;
          }
        }
        if (exit && failedStart(exit)) break;
        if (i + 1 < this.options.attempts) {
          // A daemon that exits ends the wait: why it did not start needs
          // no further attempts. One that handed the request to the
          // running instance (exit 0) leaves the plain wait.
          const waited = this.options.wait().then(() => undefined);
          const ended = await (exit
            ? waited
            : Promise.race([waited, daemon.exited]));
          exit ??= ended;
          if (exit && failedStart(exit)) break;
        }
      }
    }
    if (this.closed) return;
    if (exit && failedStart(exit)) {
      this.fail(exitFailure(exit));
      return;
    }
    if (this.connectedOnce)
      this.update({
        phase: "disconnected",
        message,
        startedByUI: this.launched,
      });
    else this.fail({ reason: "timeout" });
    // A daemon still starting may say later why it did not start, e.g.
    // after the running instance did not answer its request.
    if (daemon && !exit)
      void daemon.exited.then((late) => {
        if (
          generation === this.generation &&
          !this.connection &&
          !this.closed &&
          failedStart(late)
        )
          this.fail(exitFailure(late));
      });
  }
}
