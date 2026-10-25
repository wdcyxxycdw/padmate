import type { KeyboardCaptureEvent } from "../shared/api";
import type { BridgeProcess } from "./bridge";

interface CaptureOptions {
  spawn: () => CaptureProcess;
  focused: () => boolean;
  notify: (event: KeyboardCaptureEvent) => void;
}
type CaptureProcess = Pick<
  BridgeProcess,
  "stdin" | "stdout" | "stderr" | "kill"
> & { on: (event: string, listener: (...args: unknown[]) => void) => unknown };
interface CaptureSession {
  id: number;
  child: CaptureProcess;
  reject: (error: Error) => void;
  timer: ReturnType<typeof setTimeout>;
  ready: boolean;
}

// A dedicated helper owns its daemon capture lease. Terminating an old
// helper can never release the lease of a newer one or change manual pause.
export class NativeCapture {
  private current?: CaptureSession;
  private nextSession = 0;
  constructor(private options: CaptureOptions) {}
  async start(): Promise<number> {
    this.stop("replaced");
    if (!this.options.focused())
      throw new Error("Keyboard capture requires the focused padmate window");
    const child = this.options.spawn();
    const id = ++this.nextSession;
    await new Promise<void>((resolve, reject) => {
      const session: CaptureSession = {
        id,
        child,
        reject,
        ready: false,
        timer: setTimeout(() => this.end(session, "timeout"), 2000),
      };
      this.current = session;
      let buffer = "";
      child.stdout.setEncoding("utf8");
      child.stderr.resume();
      child.stdout.on("data", (chunk: string) => {
        if (this.current !== session) return;
        if (!this.options.focused()) {
          this.end(session, "blur");
          return;
        }
        buffer += chunk;
        if (buffer.length > 8192) {
          this.end(session, "invalid");
          return;
        }
        let newline = buffer.indexOf("\n");
        while (newline >= 0) {
          const line = buffer.slice(0, newline);
          buffer = buffer.slice(newline + 1);
          try {
            const event = JSON.parse(line) as KeyboardCaptureEvent;
            if (
              event?.active === false &&
              typeof event.reason === "string" &&
              /^[a-z-]{1,64}$/.test(event.reason)
            ) {
              this.end(session, event.reason);
              return;
            }
            if (
              event?.active !== true ||
              (event.chord !== undefined &&
                (typeof event.chord !== "string" || event.chord.length > 200))
            )
              throw new Error("invalid");
            if (!session.ready) {
              session.ready = true;
              clearTimeout(session.timer);
              session.timer = setTimeout(
                () => this.end(session, "timeout"),
                30000,
              );
              resolve();
            }
            this.options.notify({ ...event, session: id });
          } catch {
            this.end(session, "invalid");
            return;
          }
          newline = buffer.indexOf("\n");
        }
      });
      child.on("exit", () => this.end(session, "ended"));
      child.on("error", () => this.end(session, "unavailable"));
      child.stdin.on("error", () => this.end(session, "ended"));
    });
    return id;
  }
  stopSession(id: number): void {
    if (!Number.isSafeInteger(id) || id < 1)
      throw new Error("Invalid capture session");
    if (this.current?.id === id) this.end(this.current, "stopped");
  }
  stop(reason = "stopped"): void {
    if (this.current) this.end(this.current, reason);
  }
  private end(session: CaptureSession, reason: string): void {
    if (this.current !== session) return;
    this.current = undefined;
    clearTimeout(session.timer);
    session.child.stdin.end();
    session.child.kill();
    if (!session.ready) session.reject(new Error(`Keyboard capture ${reason}`));
    this.options.notify({ active: false, reason, session: session.id });
  }
}
