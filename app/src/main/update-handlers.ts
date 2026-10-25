import type {
  UpdateDismissal,
  UpdateInstallResult,
  UpdateState,
} from "../shared/update";
import type { EditorHandler } from "./library-handlers";
import { validateToggle } from "./security";

/** What the handlers need of the controller. */
export interface UpdateHandlerTarget {
  getState(): UpdateState;
  check(manual: boolean): Promise<UpdateState>;
  download(): Promise<UpdateState>;
  install(): Promise<UpdateInstallResult>;
  dismiss(choice: UpdateDismissal): Promise<UpdateState>;
  setAutoCheck(enabled: boolean): Promise<UpdateState>;
  openReleasePage(): Promise<void>;
}

function validateDismissal(value: unknown): UpdateDismissal {
  if (value !== "later" && value !== "skip")
    throw new Error("Invalid update choice");
  return value;
}

// The window's side of the automatic update (PKG-10). It sends no version, no
// address and no file: the release page is opened with no argument (Main
// builds the address from constants and the version it checked itself), and
// the only things the window chooses are the two buttons and the switch.
export function updateHandlers(
  target: UpdateHandlerTarget,
): Record<string, EditorHandler> {
  return {
    updateGet: { count: 0, run: () => target.getState() },
    // The button 检查更新; the daily check is the controller's own.
    updateCheck: { count: 0, run: () => target.check(true) },
    updateDownload: { count: 0, run: () => target.download() },
    updateInstall: { count: 0, run: () => target.install() },
    updateDismiss: {
      count: 1,
      run: (choice) => target.dismiss(validateDismissal(choice)),
    },
    updateAuto: {
      count: 1,
      run: (enabled) => target.setAutoCheck(validateToggle(enabled)),
    },
    updateReleasePage: { count: 0, run: () => target.openReleasePage() },
  };
}
