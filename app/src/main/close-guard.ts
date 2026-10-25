/** How long closing waits for the renderer's saves at most. */
export const CLOSE_WAIT_MS = 3000;
/**
 * How long the daemon's tray exit waits for them (DMN-22): inside the 2 s
 * the daemon itself waits for ui.exitReady, so the answer still reaches it.
 */
export const EXIT_WAIT_MS = 1800;

/**
 * Closing the window waits for the mapping page's saves (UI-43), without
 * asking the user anything: the first close is held while the renderer is
 * asked to finish, and its answer, or the time limit, closes the window.
 * The daemon's tray exit asks the same way (settle) and quits by itself.
 */
export class CloseGuard {
  private state: "open" | "asking" | "closing" = "open";
  private timer: ReturnType<typeof setTimeout> | undefined;
  // A window close is held and goes ahead once the renderer is done; false
  // while only settle waits.
  private closeHeld = false;
  private waiters: Array<() => void> = [];
  // The guard was given up for good (abandon): the application is quitting.
  private abandoned = false;

  constructor(
    private readonly options: {
      /** Asks the renderer to finish; false when there is none to ask. */
      ask: () => boolean;
      /** Closes the window again, for real. */
      close: () => void;
      waitMs?: number;
    },
  ) {}

  /** From the window's close event: true holds this close. */
  hold(): boolean {
    if (this.state === "closing") return false;
    if (this.state === "asking") {
      this.closeHeld = true;
      return true;
    }
    if (!this.options.ask()) return false;
    this.closeHeld = true;
    this.begin();
    return true;
  }

  /**
   * The daemon is exiting for good (DMN-22, the tray's 退出 or the
   * installer's exit, PKG-04): has the
   * renderer finish its saves like a held close, without closing the
   * window. Resolves once it has answered, or after waitMs, or at once when
   * there is none to ask or the guard has let go. The caller then quits.
   */
  async settle(waitMs: number = EXIT_WAIT_MS): Promise<void> {
    if (this.state === "closing") return;
    if (this.state === "open") {
      if (!this.options.ask()) return;
      this.begin();
    }
    await new Promise<void>((resolve) => {
      const done = () => {
        clearTimeout(timer);
        resolve();
      };
      const timer = setTimeout(done, waitMs);
      this.waiters.push(done);
    });
  }

  /**
   * Closing no longer waits: the daemon, which writes the saves, is
   * stopping (DMN-22), or there is nothing to save (UI-45). A held close
   * goes ahead.
   */
  abandon(): void {
    this.abandoned = true;
    if (this.state === "asking") this.release();
    else this.state = "closing";
  }

  /**
   * Waits for the saves again. settle ends in the state that lets every close
   * through; an install that then does not start (PKG-10) leaves the
   * application running, and closing it must wait for the renderer's saves
   * like before (UI-43). Not after abandon: the application is quitting then.
   */
  rearm(): void {
    if (this.state === "closing" && !this.abandoned) this.state = "open";
  }

  /**
   * The renderer is done, the time is up, or the renderer went away or
   * reloaded: a held close goes ahead, and settle goes on.
   */
  release(): void {
    if (this.state !== "asking") return;
    clearTimeout(this.timer);
    this.state = "closing";
    const held = this.closeHeld;
    this.closeHeld = false;
    for (const done of this.waiters.splice(0)) done();
    if (held) this.options.close();
  }

  private begin(): void {
    this.state = "asking";
    this.timer = setTimeout(
      () => this.release(),
      this.options.waitMs ?? CLOSE_WAIT_MS,
    );
  }
}
