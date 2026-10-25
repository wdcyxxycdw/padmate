import { useCallback, useEffect, useRef, useState } from "react";
import type { PadmateAPI } from "../shared/api";
import { initialUpdateState, type UpdateState } from "../shared/update";

/** The part of the window's API that the automatic update (PKG-10) uses. */
export type UpdateApi = Pick<
  PadmateAPI,
  | "getUpdateState"
  | "onUpdateState"
  | "checkForUpdate"
  | "downloadUpdate"
  | "installUpdate"
  | "dismissUpdate"
  | "setAutoUpdate"
  | "openReleasePage"
>;

/**
 * Main's update state, as it is now and as it changes: read once when the
 * component mounts, then every push of padmate:updateState. A push that comes
 * before the read has answered is newer than the read, so the read is
 * dropped. run starts a call and leaves its answer to the push it causes:
 * Main pushes every change of the state, so the answer says nothing new, and
 * a failure leaves the state as it was.
 */
export function useUpdateState(api: UpdateApi): {
  state: UpdateState;
  run: (call: Promise<unknown>) => void;
} {
  const [state, setState] = useState<UpdateState>(initialUpdateState);
  const pushed = useRef(false);
  useEffect(() => {
    let alive = true;
    pushed.current = false;
    const stop = api.onUpdateState((next) => {
      if (!alive) return;
      pushed.current = true;
      setState(next);
    });
    api
      .getUpdateState()
      .then((read) => {
        if (alive && !pushed.current) setState(read);
      })
      .catch(() => {});
    return () => {
      alive = false;
      stop();
    };
  }, [api]);
  const run = useCallback((call: Promise<unknown>) => {
    call.catch(() => {});
  }, []);
  return { state, run };
}
