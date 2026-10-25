import type { RawInfo } from "../shared/api";
import { ask, count, record } from "./ask";
import type { DaemonCall } from "./library-handlers";

/** Subscribes the connection to the daemon's input.raw topic, or lets go of it. */
export interface RawTopic {
  /**
   * Resolves once the daemon has been told; rejects when it cannot be (no
   * connection, or a daemon that does not know the topic).
   */
  up(on: boolean): Promise<void>;
}

/** The daemon's devices.rawInfo answer, checked field by field. */
function rawInfoResult(value: unknown): RawInfo {
  const info = record(value);
  if (
    typeof info.device !== "string" ||
    typeof info.guid !== "string" ||
    typeof info.gamepad !== "boolean" ||
    typeof info.watching !== "boolean" ||
    !Array.isArray(info.axisRest) ||
    info.axisRest.length > 64
  )
    throw new Error("Invalid daemon response");
  return {
    device: info.device,
    id: count(info.id),
    guid: info.guid,
    gamepad: info.gamepad,
    axes: count(info.axes),
    buttons: count(info.buttons),
    hats: count(info.hats),
    axisRest: info.axisRest.map(count),
    watching: info.watching,
  };
}

/**
 * The raw joystick events of the controllers the mapping wizard watches (IN-17,
 * IN-19, ADR-0072). The daemon sends them as input.raw events to the
 * connections that asked for a controller with devices.rawInfo and subscribe
 * to the topic, so a watch is both: the topic first (an event published before
 * the subscription is lost), then the request. The UI's one bridge connection
 * is shared, so the topic stays up while any controller is watched.
 *
 * The daemon ends a watch when the connection ends; reset is for the page that
 * asked going away (a reload, a close) with the connection still there.
 *
 * start, stop and reset run one at a time, in the order they were asked: a stop
 * that is asked while a start is on its way (the wizard closed at once) must
 * not reach the daemon before the start does, or the watch it meant to end
 * would begin after it.
 */
export class RawWatch {
  private readonly watched = new Set<string>();
  private tail: Promise<unknown> = Promise.resolve();

  constructor(
    private readonly call: DaemonCall,
    private readonly topic: RawTopic,
  ) {}

  /** Runs job after the ones asked before it. */
  private enqueue<T>(job: () => Promise<T>): Promise<T> {
    const next = this.tail.then(job, job);
    this.tail = next.catch(() => {});
    return next;
  }

  /** Starts the raw events of the controller device (a connection ID or a key). */
  start(device: string): Promise<RawInfo> {
    return this.enqueue(() => this.begin(device));
  }

  private async begin(device: string): Promise<RawInfo> {
    await this.topic.up(true);
    this.watched.add(device);
    try {
      return await ask(
        this.call,
        "devices.rawInfo",
        { device, watch: true },
        rawInfoResult,
        "Raw events not started",
      );
    } catch (error) {
      this.watched.delete(device);
      await this.release();
      throw error;
    }
  }

  /** Ends the raw events of the controller, and the subscription with the last. */
  stop(device: string): Promise<void> {
    return this.enqueue(() => this.end(device));
  }

  private async end(device: string): Promise<void> {
    this.watched.delete(device);
    try {
      await ask(
        this.call,
        "devices.rawInfo",
        { device, watch: false },
        () => undefined,
        "Raw events not stopped",
      );
    } finally {
      await this.release();
    }
  }

  /**
   * Forgets every watch and ends them, best effort and without waiting: the
   * page that asked is gone, or the connection is.
   */
  reset(): void {
    void this.enqueue(async () => {
      const devices = [...this.watched];
      this.watched.clear();
      for (const device of devices)
        this.call("devices.rawInfo", { device, watch: false })?.catch(() => {});
      if (devices.length) await this.topic.up(false).catch(() => {});
    });
  }

  /** Lets go of the topic when nothing is watched; a failure to is not the caller's. */
  private async release(): Promise<void> {
    if (this.watched.size === 0) await this.topic.up(false).catch(() => {});
  }
}
