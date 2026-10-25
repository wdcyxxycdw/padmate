// The first-run page (UI-38, ADR-0053) without React: when it opens by
// itself, what it says is connected and how long it waits before going on.
import { isDefaultProfile } from "./library-model";
import { activeProfileId, type PadmateState } from "./store";

/**
 * How long the page shows a connected controller before it goes to the
 * main page by itself: long enough to read the name.
 */
export const ADVANCE_MS = 1500;

/**
 * The page opens by itself only for someone new: a library session that
 * has answered, with no profiles besides the fixed default (PRO-28), none
 * other in use, and the page not done. The library is never empty: the
 * daemon keeps the default in it and uses it at first.
 */
export function shouldOffer(
  state: Pick<
    PadmateState,
    | "connection"
    | "status"
    | "profiles"
    | "profilesLoaded"
    | "profilesError"
    | "onboardingDone"
  >,
): boolean {
  return (
    !state.onboardingDone &&
    state.connection.phase === "connected" &&
    !!state.status &&
    state.profilesLoaded &&
    !state.profilesError &&
    state.profiles.every((entry) => isDefaultProfile(entry.id)) &&
    (!activeProfileId(state) || isDefaultProfile(activeProfileId(state)))
  );
}

/** The connected controllers' names, in the daemon's order. */
export function padNames(status: PadmateState["status"]): string[] {
  return (status?.devices ?? []).map((entry) => entry.info.name);
}
