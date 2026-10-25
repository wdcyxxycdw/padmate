import type { DaemonCall } from "./library-handlers";

/** A daemon answer that is a plain object, or an error. */
export function record(value: unknown): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value))
    throw new Error("Invalid daemon response");
  return value as Record<string, unknown>;
}

/** A daemon answer that is a finite number, or an error. */
export function count(value: unknown): number {
  if (typeof value !== "number" || !Number.isFinite(value))
    throw new Error("Invalid daemon response");
  return value;
}

/**
 * Runs a daemon call whose failure the renderer only needs the code of (the
 * message may carry the daemon's own text, for its log), and checks its answer.
 */
export async function ask<T>(
  call: DaemonCall,
  method: string,
  params: unknown,
  check: (value: unknown) => T,
  failure: string,
): Promise<T> {
  const pending = call(method, params);
  if (!pending) throw new Error("Daemon is not connected");
  try {
    return check(await pending);
  } catch (error) {
    const e = error as { code?: unknown };
    throw Object.assign(new Error(failure), {
      ...(typeof e?.code === "number" ? { code: e.code } : {}),
    });
  }
}
