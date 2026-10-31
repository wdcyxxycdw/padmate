import { act } from "@testing-library/react";
import { vi } from "vitest";
import { initialUpdateState, type UpdateState } from "../shared/update";
import type { UpdateApi } from "./update-model";

/** A build that can update, with nothing on offer. */
export const supportedUpdateState: UpdateState = {
  ...initialUpdateState,
  supported: true,
};

/**
 * An update API for the tests of the window's side (PKG-10): the state is
 * the test's to push, as Main pushes it on padmate:updateState.
 */
export function updateApi(initial: UpdateState = supportedUpdateState) {
  let state = initial;
  let listener: ((state: UpdateState) => void) | undefined;
  const api: UpdateApi = {
    getUpdateState: vi.fn(async () => state),
    onUpdateState: vi.fn((next: (state: UpdateState) => void) => {
      listener = next;
      return () => {
        listener = undefined;
      };
    }),
    checkForUpdate: vi.fn(async () => state),
    downloadUpdate: vi.fn(async () => state),
    installUpdate: vi.fn(async () => ({ dryRun: false })),
    dismissUpdate: vi.fn(async () => state),
    setAutoUpdate: vi.fn(async () => state),
    openManualDownload: vi.fn(async () => {}),
  };
  return {
    api,
    push: (next: Partial<UpdateState>) => {
      state = { ...state, ...next };
      act(() => listener?.(state));
    },
  };
}
