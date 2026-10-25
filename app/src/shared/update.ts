// What the window and Main say to each other about updating padmate (PKG-10,
// ADR-0073). The state is plain data: Main keeps the truth (update-controller.ts)
// and pushes every change on padmate:updateState; the window only shows it and
// sends back what the person chose.

/** Where the update is: nothing, a check, a version found, its download. */
export type UpdatePhase =
  | "idle"
  | "checking"
  | "available"
  | "downloading"
  | "downloaded"
  | "installing";

/**
 * The blocking dialog the window shows, "none" for no dialog. The first four
 * follow the phase; the last four tell how something that the person asked for
 * ended (a check they pressed, a download they started, an install).
 */
export type UpdateDialogKind =
  | "none"
  | "available"
  | "downloading"
  | "downloaded"
  | "installing"
  | "upToDate"
  | "checkFailed"
  | "downloadFailed"
  | "installFailed";

export interface UpdateState {
  /**
   * False in a development build and in an application that was not
   * installed: nothing is checked, and the settings only say so.
   */
  supported: boolean;
  /** 自动检查更新: the daily check while the window is open. */
  autoCheck: boolean;
  phase: UpdatePhase;
  /** The version found while phase is available to installing, else "". */
  version: string;
  /** The download so far, 0 to 100, while phase is downloading. */
  percent: number;
  dialog: UpdateDialogKind;
  /**
   * A short code for the last failure (electron-updater's, such as
   * ERR_UPDATER_INVALID_RELEASE_FEED), "" when there is none. Never shown
   * as text; tests and the log read it.
   */
  errorCode: string;
}

export const initialUpdateState: UpdateState = {
  supported: false,
  autoCheck: true,
  phase: "idle",
  version: "",
  percent: 0,
  dialog: "none",
  errorCode: "",
};

/** What the person chose in the dialog that offers a version. */
export type UpdateDismissal = "later" | "skip";

/**
 * The answer of updateInstall. dryRun is true only in the installed
 * application's test mode, which records the call instead of making it.
 */
export interface UpdateInstallResult {
  dryRun: boolean;
}
