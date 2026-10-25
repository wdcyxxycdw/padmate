import { isDisconnectedError, managerDisconnected, subscribe } from "./bridge";

// The daemon's hud topic (SCR-22, ADR-0063). The daemon tells a script that an
// overlay is there (host.has("hud")) while some client subscribes to hud, and
// a new subscribe call replaces the client's whole list of topics. So the UI
// asks for hud only while its overlay is up: from the moment the overlay's
// window is ready (a window that is only being made, or whose page failed to
// load, shows nothing) until the overlay is given up. A renderer that died and
// is made again is a short gap, not an end, and keeps hud.
//
// What the daemon was last told is kept per connection. A change of the
// wish is sent as soon as there is a connection; one at a time, the latest
// wish wins; a call that fails while the daemon is still there is tried
// again, and one that fails for want of a connection is not, because the
// next connection is made as the overlay is then.
//
// The same object holds the one other topic the UI asks for only at times:
// input.raw, while the mapping wizard is open (IN-17, ADR-0072). A change of
// that wish is waited for (rawUp): the wizard asks the daemon for a
// controller's raw events only once the daemon has the subscription. A
// connection that ends takes the wizard's watch with it, so the wish does not
// outlive the connection.

type Call = (method: string, params: unknown) => Promise<unknown>;

/** How long after a failed call the next try is, in ms. */
export const hudRetryMs = 1000;

export class HudSubscription {
  /** Whether the overlay is up: what the topic list says. */
  private up = false;
  /** Whether the mapping wizard is open: input.raw is on the list. */
  private raw = false;
  /**
   * What the daemon was last told on this connection, or undefined while there
   * is none. A daemon that does not know hud is told too: subscribe goes
   * without it for such a daemon, and there is nothing more to try.
   */
  private told: { hud: boolean; raw: boolean } | undefined;
  /** Calls to rawUp that wait for the daemon to be told. */
  private waiters: Array<{
    resolve: () => void;
    reject: (error: unknown) => void;
  }> = [];
  private linked = false;
  private busy = false;
  private stopped = false;
  // Counts the connections and their ends: a call that comes back after the
  // connection it was made on is gone settles nothing.
  private generation = 0;
  private retry: ReturnType<typeof setTimeout> | undefined;

  constructor(
    /** The call of the current connection: rejects when there is none. */
    private readonly call: Call,
    private readonly retryMs = hudRetryMs,
  ) {}

  /** The overlay's window is ready (true), or it will not come back (false). */
  overlayUp(up: boolean): void {
    this.up = up;
    this.sync();
  }

  /**
   * The mapping wizard needs input.raw (true) or is done with it (false).
   * Resolves once the daemon has been told; rejects when it cannot be: there is
   * no connection (nothing to tell when lowering it), or the daemon did not
   * take the list. The wish stays as it was asked, so the caller that gives up
   * lowers it.
   */
  rawUp(on: boolean): Promise<void> {
    this.raw = on;
    if (this.stopped || !this.linked)
      return on
        ? Promise.reject(new Error(managerDisconnected))
        : Promise.resolve();
    if (this.matches()) return Promise.resolve();
    return new Promise((resolve, reject) => {
      this.waiters.push({ resolve, reject });
      this.sync();
    });
  }

  /**
   * A connection that has just been made subscribes with what the overlay is
   * now. `call` is that connection's own: the manager does not have it yet.
   * It rejects when the subscription does, and the connection is then dropped.
   */
  async open(call: Call): Promise<void> {
    const hud = this.up;
    const raw = this.raw;
    await subscribe(call, { hud, raw });
    this.generation++;
    this.told = { hud, raw };
  }

  /** The connection is in the manager's hands: calls go through it. */
  connected(): void {
    this.linked = true;
    // The overlay may have changed while the connection was being made.
    this.sync();
  }

  /**
   * The connection is gone, or there is none yet. The wizard's watch went with
   * it, so the next connection does not subscribe to input.raw.
   */
  disconnected(): void {
    this.linked = false;
    this.told = undefined;
    this.raw = false;
    this.generation++;
    this.cancelRetry();
    this.settle(new Error(managerDisconnected));
  }

  /** The app is ending: nothing more is sent. */
  stop(): void {
    this.stopped = true;
    this.cancelRetry();
    this.settle(new Error(managerDisconnected));
  }

  /** Whether the daemon was told what the overlay and the wizard are. */
  private matches(): boolean {
    return (
      this.told !== undefined &&
      this.told.hud === this.up &&
      this.told.raw === this.raw
    );
  }

  /** Ends the calls to rawUp that wait: with error, or when undefined, as done. */
  private settle(error?: unknown): void {
    const waiters = this.waiters;
    this.waiters = [];
    for (const waiter of waiters)
      if (error === undefined) waiter.resolve();
      else waiter.reject(error);
  }

  private sync(): void {
    if (this.stopped || !this.linked || this.busy || this.matches()) return;
    this.cancelRetry();
    this.busy = true;
    void this.run();
  }

  private async run(): Promise<void> {
    try {
      // Read again after each call: what the overlay and the wizard did
      // meanwhile is next.
      while (!this.stopped && this.linked && !this.matches()) {
        const generation = this.generation;
        const hud = this.up;
        const raw = this.raw;
        try {
          await subscribe(this.call, { hud, raw });
        } catch (error) {
          // The connection ended meanwhile: the one after it starts afresh.
          if (generation !== this.generation) continue;
          // No connection: the next one subscribes as the overlay is then.
          // Otherwise the daemon is there and did not take it; try again.
          if (!isDisconnectedError(error)) this.retryLater();
          this.settle(error);
          return;
        }
        if (generation === this.generation) this.told = { hud, raw };
      }
      if (this.matches()) this.settle();
    } finally {
      this.busy = false;
    }
  }

  private retryLater(): void {
    this.cancelRetry();
    this.retry = setTimeout(() => {
      this.retry = undefined;
      this.sync();
    }, this.retryMs);
  }

  private cancelRetry(): void {
    if (this.retry) clearTimeout(this.retry);
    this.retry = undefined;
  }
}
