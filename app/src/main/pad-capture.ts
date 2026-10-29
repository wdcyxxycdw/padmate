// Recording a new emergency combo (UI-19) and the mapping wizard (IN-19).
// capture.start, through the UI's own bridge connection, pauses the mapping
// while the controller's input events keep flowing, so pressing the new combo
// triggers no shortcut. The capture ends when the renderer stops it, and Main
// stops it when the window closes or reloads, and when the window loses focus
// or the session locks or sleeps, except for a capture that was asked to
// stay: the wizard's, which keeps the mapping paused while the user is in
// another program, and after an unlock or a wake (the daemon's own system
// pause covers the lock itself), until the wizard is closed (ADR-0076). When
// the bridge connection ends, the daemon ends the capture itself.
//
// The bridge connection lives as long as the UI, so a lost capture.stop
// would leave the mapping paused. A capture.start that failed or timed out
// may still reach the daemon, and a capture.stop may be rejected before it
// is sent (too many pending requests). So once a start was sent, a stop is
// sent, and sent again after a failure, until one succeeds after the last
// start or the connection ends. capture.stop is idempotent in the daemon.
export interface PadCaptureOptions {
  /** Sends one bridge request; undefined while no daemon is connected. */
  call: (method: string) => Promise<unknown> | undefined;
  focused: () => boolean;
  /** How long to wait before sending a failed capture.stop again. */
  retryMs?: number;
}

export class PadCapture {
  private active = false;
  // The capture was asked to stay when the window loses the front.
  private stays = false;
  // A capture.start was sent, and no capture.stop sent after it succeeded.
  private mayBeOn = false;
  // Counts the starts and the disconnections: a request's result counts
  // only while none came after it was sent.
  private generation = 0;
  // A capture.stop of this generation is in flight.
  private stopping = false;
  private retry: ReturnType<typeof setTimeout> | undefined;
  constructor(private options: PadCaptureOptions) {}

  /**
   * Starts (or, when it is on, confirms) the capture, from the front window.
   * stays: the window losing the front does not end it; only stop(), the
   * window going away or the connection ending do.
   */
  async start(stays = false): Promise<void> {
    if (!this.options.focused())
      throw new Error("Controller capture requires the focused padmate window");
    const pending = this.options.call("capture.start");
    if (!pending) throw new Error("Daemon is not connected");
    // Set first: a blur while the request runs must still stop it, unless
    // the capture stays.
    this.active = true;
    this.stays = stays;
    this.mayBeOn = true;
    this.stopping = false;
    const generation = this.next();
    try {
      await pending;
    } catch (error) {
      // The request may still reach the daemon, or have reached it. A later
      // start, or the end of the connection, decides instead.
      if (generation === this.generation) this.stop();
      throw error;
    }
    if (!this.active) {
      // Stopped while starting; the bridge keeps request order, so the
      // stop sent then arrives after this start.
      throw new Error("Controller capture stopped");
    }
    if (!this.stays && !this.options.focused()) {
      this.stop();
      throw new Error("Controller capture requires the focused padmate window");
    }
  }

  /** Ends a capture this window started; a no-op otherwise. */
  stop(): void {
    this.active = false;
    this.stays = false;
    if (this.mayBeOn && !this.stopping) this.sendStop();
  }

  /**
   * The window lost the front, or the session locks or sleeps: ends the
   * capture unless it stays.
   */
  windowAway(): void {
    if (!this.stays) this.stop();
  }

  /** The bridge is gone, and the daemon ended its capture with it. */
  disconnected(): void {
    this.active = false;
    this.stays = false;
    this.mayBeOn = false;
    this.stopping = false;
    this.next();
  }

  get capturing(): boolean {
    return this.active;
  }

  /** Starts a new generation, which drops a pending retry. */
  private next(): number {
    clearTimeout(this.retry);
    this.retry = undefined;
    return ++this.generation;
  }

  private sendStop(): void {
    clearTimeout(this.retry);
    this.retry = undefined;
    const generation = this.generation;
    const pending = this.options.call("capture.stop");
    if (!pending) {
      this.retryStop();
      return;
    }
    this.stopping = true;
    pending.then(
      () => {
        if (generation !== this.generation) return;
        this.stopping = false;
        this.mayBeOn = false;
      },
      () => {
        if (generation !== this.generation) return;
        this.stopping = false;
        this.retryStop();
      },
    );
  }

  private retryStop(): void {
    if (this.active || !this.mayBeOn) return;
    this.retry = setTimeout(() => {
      this.retry = undefined;
      if (!this.active && this.mayBeOn && !this.stopping) this.sendStop();
    }, this.options.retryMs ?? 1000);
  }
}
