import type {
  ProfileActivation,
  RecentApp,
  UIErrorData,
  UIResult,
} from "../shared/api";
import {
  validateProfileID,
  validateProfileSelection,
  validateRevision,
} from "./security";

// Sends one allowlisted request through the authenticated CLI bridge;
// undefined while no daemon manager exists.
export type DaemonCall = (
  method: string,
  params: unknown,
) => Promise<unknown> | undefined;

export interface EditorHandler {
  count: number;
  run: (...args: unknown[]) => unknown;
}

function object(value: unknown): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value))
    throw new Error("Invalid daemon response");
  return value as Record<string, unknown>;
}

function recentApp(value: unknown): RecentApp {
  const app = object(value);
  if (
    typeof app.exe !== "string" ||
    typeof app.path !== "string" ||
    typeof app.elevated !== "boolean"
  )
    throw new Error("Invalid daemon response");
  return { exe: app.exe, path: app.path, elevated: app.elevated };
}

// Profile library management (UI-17) and window capture (PRO-16). Renderer
// arguments are validated here before any request reaches the daemon. The
// daemon still keeps saved versions of profile.json (PRO-17); the UI no
// longer lists or restores them (ADR-0054).
export function libraryHandlers(
  call: DaemonCall,
): Record<string, EditorHandler> {
  // A missing manager must not look like a completed delete or copy.
  const request = async (method: string, params: unknown) => {
    const pending = call(method, params);
    if (!pending) throw new Error("Daemon is not connected");
    return pending;
  };
  return {
    profileActivate: {
      count: 1,
      run: async (id): Promise<ProfileActivation> => {
        const result = object(
          await request("profiles.activate", {
            id: validateProfileSelection(id),
          }),
        );
        if (
          typeof result.profileId !== "string" ||
          !Number.isSafeInteger(result.generation)
        )
          throw new Error("Invalid daemon response");
        return {
          profileId: result.profileId,
          generation: result.generation as number,
        };
      },
    },
    profileDelete: {
      count: 2,
      run: async (id, revision): Promise<void> => {
        await request("profiles.delete", {
          id: validateProfileID(id),
          revision: validateRevision(revision),
        });
      },
    },
    profileCopy: {
      count: 2,
      run: async (from, to): Promise<{ revision: string }> => {
        const params = {
          from: validateProfileID(from),
          to: validateProfileID(to),
        };
        if (params.from === params.to)
          throw new Error("Copy needs a different profile ID");
        const result = object(await request("profiles.copy", params));
        if (typeof result.revision !== "string")
          throw new Error("Invalid daemon response");
        return { revision: result.revision };
      },
    },
    appsRecent: {
      count: 0,
      run: async (): Promise<RecentApp[]> => {
        const result = object(await request("apps.recent", {}));
        if (!Array.isArray(result.apps))
          throw new Error("Invalid daemon response");
        return result.apps.map(recentApp);
      },
    },
  };
}

// Editor calls resolve to plain data: contextBridge drops custom Error
// properties, and the renderer needs the daemon's code and data, such as a
// revision conflict or the rules that block a delete.
export async function uiResult<T>(
  run: () => T | Promise<T>,
): Promise<UIResult<T>> {
  try {
    return { ok: true, value: await run() };
  } catch (error) {
    const e = error as { message?: unknown; code?: unknown; data?: unknown };
    const failure: UIErrorData = {
      message: typeof e?.message === "string" ? e.message : "Request failed",
      ...(typeof e?.code === "number" ? { code: e.code } : {}),
      ...(e?.data !== undefined ? { data: e.data } : {}),
    };
    return { ok: false, error: failure };
  }
}
