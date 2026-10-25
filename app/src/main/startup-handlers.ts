import { LibraryErrorCode, type StartupState } from "../shared/api";
import type { DaemonCall, EditorHandler } from "./library-handlers";
import { validateToggle } from "./security";

/** The daemon's startup.get/set result, checked field by field. */
function startupState(value: unknown): StartupState {
  if (value === null || typeof value !== "object" || Array.isArray(value))
    throw new Error("Invalid daemon response");
  const state = value as Record<string, unknown>;
  if (
    typeof state.available !== "boolean" ||
    typeof state.enabled !== "boolean" ||
    typeof state.current !== "boolean" ||
    (state.mode !== "inject" && state.mode !== "dry-run") ||
    (state.reason !== undefined &&
      state.reason !== "development" &&
      state.reason !== "unsupported")
  )
    throw new Error("Invalid daemon response");
  return {
    available: state.available,
    enabled: state.enabled,
    current: state.current,
    mode: state.mode,
    ...(state.reason ? { reason: state.reason } : {}),
  };
}

// Start at sign-in (UI-19, ADR-0052). The renderer passes only a boolean:
// the daemon registers its own executable and startup arguments, and a
// development or test daemon refuses (-32017). locked is a packaged
// application started by a test (PKG-03): it never asks, whatever its daemon
// would answer, as the entry is the user's real Run value.
export function startupHandlers(
  call: DaemonCall,
  locked = false,
): Record<string, EditorHandler> {
  const request = async (method: string, params: unknown) => {
    const pending = call(method, params);
    if (!pending) throw new Error("Daemon is not connected");
    let reply: unknown;
    try {
      reply = await pending;
    } catch (error) {
      const e = error as { code?: unknown; data?: unknown };
      // Only "unavailable" keeps its data. A system error's message can
      // name the startup folder under the user's profile; the daemon log
      // has it, the renderer gets a fixed message and the code.
      if (e?.code === LibraryErrorCode.startupUnavailable) throw error;
      throw Object.assign(new Error("Startup entry not changed"), {
        ...(typeof e?.code === "number" ? { code: e.code } : {}),
      });
    }
    return startupState(reply);
  };
  return {
    startupGet: { count: 0, run: () => request("startup.get", {}) },
    startupSet: {
      count: 1,
      run: (enabled) => {
        const toggle = validateToggle(enabled);
        if (locked)
          throw Object.assign(new Error("Login startup is unavailable"), {
            code: LibraryErrorCode.startupUnavailable,
          });
        return request("startup.set", { enabled: toggle });
      },
    },
  };
}
